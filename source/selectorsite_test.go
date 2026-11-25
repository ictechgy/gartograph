package source

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// selectorSiteMain은 선택자 참조 지점을 고정하는 fixture의 main 패키지다. 한 줄에 한 종류의 선택자를
// 두어 기대 위치를 줄·열로 계산할 수 있게 한다.
const selectorSiteMain = `package main

import (
	"fmt"

	"example.com/fixture/lib"
)

var cfg lib.Doc

func use(d *lib.Doc, w lib.Wrapper) {
	d.Sort()
	fmt.Println(d.Name)
	_ = cfg.Name
	_ = lib.Doc{Name: "k"}
	_ = w.Inner.Field
}

func main() { use(&lib.Doc{}, lib.Wrapper{}) }
`

// selectorSiteLib는 fixture의 lib 패키지다 — 메서드·필드·중첩 필드 선택의 목표다.
const selectorSiteLib = `package lib

type Doc struct{ Name string }

func (d *Doc) Sort() {}

type Inner struct{ Field int }

type Wrapper struct{ Inner Inner }
`

// TestSelectorIsOneReferenceSite는 선택자 x.Sel 하나가 참조 지점 하나로 기록되는지 확인한다. selectorEdge가
// 선택자 위치로 기록한 뒤 ast.Inspect가 Sel 식별자로 내려가 identEdge가 같은 객체를 다시 기록해, 간선
// 위치가 둘(선택자 시작·Sel)이 되고 signature 간선의 한정 이름(lib.Wrapper)도 두 위치였다. 선택자의 X 쪽
// 식별자(cfg.Name의 cfg)는 다른 참조라 그대로 남아야 하고, 건너뛰기는 식별자 노드 단위라 선택자로 쓴 객체를
// 선택자 밖에서 다시 쓴 지점(키 리터럴 lib.Doc{Name: …}의 Name)도 남아야 한다 — 객체로 거르면 이 지점을 잃는다.
func TestSelectorIsOneReferenceSite(t *testing.T) {
	doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{
		"main.go":    selectorSiteMain,
		"lib/lib.go": selectorSiteLib,
	}))
	const m, lib = "example.com/fixture", "example.com/fixture/lib"
	cases := []struct {
		to     string
		kind   graph.EdgeKind
		needle string // 기대 위치(선택자 시작)를 찾을 원문 조각 — 그 줄의 첫 등장 열이다
	}{
		{lib + ".(Doc).Sort", graph.EdgeReferences, "d.Sort()"},
		{lib + ".(Wrapper).Inner", graph.EdgeReferences, "w.Inner.Field"},
		// 중첩 선택자 w.Inner.Field의 바깥 Sel도 한 번만이다 — 마지막 선택자 하나만 기억하면 안쪽
		// 선택자를 지나며 바깥 Sel 표시를 잃는다.
		{lib + ".(Inner).Field", graph.EdgeReferences, "w.Inner.Field"},
		{lib + ".Wrapper", graph.EdgeSignature, "lib.Wrapper)"},
		{lib + ".Wrapper", graph.EdgeReferences, "lib.Wrapper)"},
	}
	for _, c := range cases {
		e, ok := findEdge(doc, m+".use", c.to, c.kind)
		if !ok {
			t.Fatalf("missing %s edge use -> %s: %+v", c.kind, c.to, doc.Edges)
		}
		line, col := sourceSite(t, selectorSiteMain, c.needle)
		if len(e.Positions) != 1 || e.Positions[0].Line != line || e.Positions[0].Column != col {
			t.Errorf("%s use -> %s: want one site at %d:%d (the selector's start), got %+v",
				c.kind, c.to, line, col, e.Positions)
		}
	}
	// d.Name·cfg.Name·lib.Doc{Name: …}은 (Doc).Name의 세 지점이다 — 선택자는 그 시작, 키는 키 식별자 한 위치다.
	name, ok := findEdge(doc, m+".use", lib+".(Doc).Name", graph.EdgeReferences)
	if !ok || len(name.Positions) == 0 {
		t.Fatalf("missing references edge use -> (Doc).Name with positions: %+v", name)
	}
	file := name.Positions[0].File
	dLine, dCol := sourceSite(t, selectorSiteMain, "d.Name")
	cLine, cCol := sourceSite(t, selectorSiteMain, "cfg.Name")
	kLine, kCol := sourceSite(t, selectorSiteMain, "Name: \"k\"")
	want := []graph.Position{{File: file, Line: dLine, Column: dCol}, {File: file, Line: cLine, Column: cCol},
		{File: file, Line: kLine, Column: kCol}}
	if !slices.Equal(name.Positions, want) {
		t.Errorf("d.Name, cfg.Name and the key Name are three sites, one position each: want %+v, got %+v",
			want, name.Positions)
	}
	// X 쪽 패키지 변수 cfg는 자기 참조로 남는다.
	if !hasEdge(doc, m+".use", m+".cfg", graph.EdgeReferences) {
		t.Error("the X side of a selector (cfg in cfg.Name) is a reference of its own and must stay")
	}
}

// TestSelectorExternalReferenceCountedOnce는 모듈 밖 참조 수가 같은 호출을 철자와 무관하게 같게 세는지
// 확인한다. 한정 호출 fmt.Println("x")는 call과 references(선택자) 두 사실이라 2를 더한다 — 점 import로 쓴
// 같은 호출 Println("x")와 같다. Sel 식별자를 다시 세면 3이었다.
func TestSelectorExternalReferenceCountedOnce(t *testing.T) {
	extRefs := func(imp, call string) int {
		doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{
			"main.go": "package main\n\nimport " + imp + "\n\nfunc main() {\n\t" + call + "\n}\n",
		}))
		for _, l := range doc.Limitations {
			var n int
			if _, err := fmt.Sscanf(l, "%d references to symbols outside the module were omitted", &n); err == nil {
				return n
			}
		}
		t.Fatalf("expected an external-references limitation: %v", doc.Limitations)
		return 0
	}
	qualified := extRefs(`"fmt"`, `fmt.Println("x")`)
	dotImport := extRefs(`. "fmt"`, `Println("x")`)
	if qualified != 2 || dotImport != 2 {
		t.Fatalf("one call to an external function is a call and a reference, however it is spelled: "+
			"fmt.Println counted %d, dot-imported Println counted %d, want 2 and 2", qualified, dotImport)
	}
}

// sourceSite는 원문에서 needle이 처음 나오는 줄·열(1부터, 바이트 기준 — go/token과 같다)이다.
func sourceSite(t *testing.T, src, needle string) (int, int) {
	t.Helper()
	i := strings.Index(src, needle)
	if i < 0 {
		t.Fatalf("needle %q not in fixture", needle)
	}
	line := strings.Count(src[:i], "\n") + 1
	return line, i - strings.LastIndex(src[:i], "\n")
}
