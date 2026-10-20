package graph

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// viewDoc은 세 종류 정점과 구조/소유 간선이 섞인 문서다.
func viewDoc() *Document {
	return &Document{
		Module: "m",
		Roots:  []string{"p.main", "ghost"},
		Vertices: []Vertex{
			{ID: "p", Kind: KindPackage},
			{ID: "p.T", Kind: KindType},
			{ID: "p.f", Kind: KindFunc, Exported: true},
			{ID: "p.main", Kind: KindFunc},
		},
		Edges: []Edge{
			{From: "p", To: "p.T", Kind: EdgeContains},
			{From: "p.f", To: "p.T", Kind: EdgeReferences},
			{From: "p", To: "p.f", Kind: EdgeContains},
		},
	}
}

// TestView는 레벨별 투영이 정점·간선 종류를 정확히 거르는지 확인한다.
// contains는 어느 레벨에서도 의존 간선이 아니라 View에 남지 않는다.
func TestView(t *testing.T) {
	d := viewDoc()
	pkg, err := d.View(LevelPackage)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Vertices) != 1 || len(pkg.Edges) != 0 {
		t.Fatalf("package view: %+v", pkg)
	}
	// 투영은 Module을 잃으면 안 된다 — 규칙 매칭이 모듈 경로에 기댄다.
	if pkg.Module != "m" {
		t.Fatal("view lost Module")
	}
	// 모듈 위치 사실(ModuleDir·WorkspaceModules·정점 Module)도 그대로다 — 패턴·파일 해석의 기준이다.
	ws := viewDoc()
	ws.ModuleDir = "/w/m"
	ws.WorkspaceModules = []WorkspaceModule{{Path: "m", Dir: "/w/m"}, {Path: "n", Dir: "/w/n"}}
	ws.Vertices = append(ws.Vertices, Vertex{ID: "n/q", Kind: KindPackage, Module: "n"})
	wsView, err := ws.View(LevelPackage)
	if err != nil {
		t.Fatal(err)
	}
	if wsView.ModuleDir != ws.ModuleDir || !reflect.DeepEqual(wsView.WorkspaceModules, ws.WorkspaceModules) ||
		len(wsView.Vertices) != 2 || wsView.Vertices[0].Module != "n" {
		t.Fatalf("view lost module location facts: %+v", wsView)
	}

	typ, err := d.View(LevelType)
	if err != nil {
		t.Fatal(err)
	}
	if len(typ.Vertices) != 1 || typ.Vertices[0].ID != "p.T" {
		t.Fatalf("type view must hold types only: %+v", typ.Vertices)
	}
	// references 간선의 from(f)이 레벨에서 빠지면 간선도 빠져야 한다 —
	// 끝이 없는 간선은 유령이다.
	if len(typ.Edges) != 0 {
		t.Fatalf("type view kept an edge with a dead endpoint: %+v", typ.Edges)
	}

	sym, err := d.View(LevelSymbol)
	if err != nil {
		t.Fatal(err)
	}
	if len(sym.Vertices) != 3 || len(sym.Edges) != 1 {
		t.Fatalf("symbol view: %+v", sym)
	}
}

// TestViewUnknownLevel은 미지원 레벨 투영이 에러인지 확인한다.
func TestViewUnknownLevel(t *testing.T) {
	if _, err := viewDoc().View(Level("bogus")); err == nil {
		t.Fatal("unknown level view must be an error")
	}
}

// TestSymbols는 심볼 정점만 골라내는지 확인한다.
func TestSymbols(t *testing.T) {
	syms := viewDoc().Symbols()
	if len(syms) != 3 {
		t.Fatalf("expected 3 symbols, got %+v", syms)
	}
}

// TestRootIDs는 문서에 실제 정점이 있는 루트만 돌아오는지 확인한다.
// 잘린 문서의 루트는 질의 쪽에서 매번 검사하지 않게 여기서 걸러진다.
func TestRootIDs(t *testing.T) {
	roots := viewDoc().RootIDs()
	if len(roots) != 1 || roots[0] != "p.main" {
		t.Fatalf("expected only live root p.main, got %v", roots)
	}
}

// TestRootIDsMatchesScan은 무작위 문서에서 RootIDs가 루트마다 정점 목록을 훑던 구현(scanRootIDs)과
// 같은 목록을 내는지 확인한다. 문서에 없는 루트, 겹친 루트, 겹친 정점 ID를 섞는다 — 존재 확인을
// 색인으로 바꾼 것은 비용만 줄여야 하고 걸러지는 루트와 순서는 그대로여야 한다.
func TestRootIDsMatchesScan(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	sawGhost, sawDuplicate := false, false
	for trial := 0; trial < 500; trial++ {
		d := randomRootedDocument(r)
		got, want := d.RootIDs(), scanRootIDs(d)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d roots %v over %d vertices: got %v, want %v",
				trial, d.Roots, len(d.Vertices), got, want)
		}
		sawGhost = sawGhost || len(want) < len(d.Roots)
		for i := 1; i < len(want); i++ {
			sawDuplicate = sawDuplicate || want[i] == want[i-1]
		}
	}
	if !sawGhost || !sawDuplicate {
		t.Fatalf("random documents must exercise missing (%v) and repeated (%v) roots", sawGhost, sawDuplicate)
	}
}

// randomRootedDocument는 RootIDs 대조용 무작위 문서다. 루트는 정점 ID 범위를 넘겨 뽑아 문서에
// 없는 루트와 겹친 루트가 생기고, 정점 ID도 가끔 겹친다(손으로 합친 저장 문서).
func randomRootedDocument(r *rand.Rand) *Document {
	d := &Document{}
	n := 1 + r.Intn(20)
	for i := 0; i < n; i++ {
		d.Vertices = append(d.Vertices, Vertex{ID: fmt.Sprintf("m.v%02d", i), Kind: KindFunc})
	}
	if r.Intn(3) == 0 {
		d.Vertices = append(d.Vertices, Vertex{ID: d.Vertices[r.Intn(n)].ID, Kind: KindVar})
	}
	r.Shuffle(len(d.Vertices), func(i, j int) { d.Vertices[i], d.Vertices[j] = d.Vertices[j], d.Vertices[i] })
	for k := r.Intn(2*n + 2); k > 0; k-- {
		d.Roots = append(d.Roots, fmt.Sprintf("m.v%02d", r.Intn(n+n/2+2)))
	}
	return d
}

// scanRootIDs는 색인을 쓰기 전의 RootIDs다 — 루트마다 HasVertex로 정점 목록을 훑는다.
// 느려도 출력 대조 기준으로 그대로 둔다.
func scanRootIDs(d *Document) []string {
	var out []string
	for _, r := range d.Roots {
		if d.HasVertex(r) {
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}

// TestParseLevel은 레벨 문자열 해석과 에러 경로를 확인한다.
func TestParseLevel(t *testing.T) {
	for _, s := range []string{"module", "package", "type", "symbol"} {
		l, err := ParseLevel(s)
		if err != nil || string(l) != s {
			t.Fatalf("ParseLevel(%q): %v", s, err)
		}
	}
	l, err := ParseLevel("bogus")
	if err == nil || l != "" {
		t.Fatal("unknown level must be an error")
	}
	var ule *UnknownLevelError
	if !errors.As(err, &ule) {
		t.Fatalf("expected UnknownLevelError, got %T", err)
	}
	// 에러 메시지는 사용자가 고칠 수 있는 형태여야 한다.
	if got := err.Error(); got == "" {
		t.Fatal("empty error message")
	}
}

// TestRank는 레벨의 세밀함 순서를 확인한다 — View·requireLevel이 기댄다.
func TestRank(t *testing.T) {
	if !(LevelPackage.Rank() < LevelType.Rank() &&
		LevelType.Rank() < LevelSymbol.Rank()) {
		t.Fatal("rank order broken")
	}
	if Level("bogus").Rank() != -1 {
		t.Fatal("unknown level must rank -1")
	}
}
