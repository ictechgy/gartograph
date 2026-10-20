// schema 사실 symbol.usr 귀속 테스트 — usr가 impact와 같은 심볼 그래프의 정점 ID이고,
// 수확이 간선 출발점으로 쓰는 선언에 귀속되는지 fixture 모듈로 확인한다.
package source

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
	"golang.org/x/tools/go/packages"
)

// usrFixture는 귀속 규칙의 경우를 모두 담은 모듈이다.
func usrFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"store/store.go": `package store

import "database/sql"

var db *sql.DB

type Repo struct{}

// 포인터 리시버 메서드 — ID는 리시버 포인터를 벗긴 (Repo)다.
func (r *Repo) List() { db.Query("SELECT * FROM repo_t") }

// 클로저 안의 사실은 감싸는 함수에 귀속한다.
func Each() {
	f := func() { db.Exec("DELETE FROM closure_t") }
	f()
}

// 패키지 변수 초기화식 — 그 변수 정점이다.
var countQuery = "SELECT count(*) FROM var_t"

// 이름과 값이 짝지어진 다중 선언 — 값마다 그 이름이다.
var qa, qb = "SELECT * FROM qa_t", "SELECT * FROM qb_t"

// 모듈 심볼을 쓰는 빈 선언 — 패키지 초기화 루트(pkg._)다.
var _ = register("SELECT * FROM blank_t")

func register(q string) string { return q }

// 태그는 타입 정점, TableName 리터럴은 그 메서드 정점이다.
type User struct {
	Email string ` + "`db:\"email\"`" + `
}

func (User) TableName() string { return "users" }

// 제네릭 리시버 — ID는 타입 파라미터 없는 (Box)다.
type Box[T any] struct{}

func (Box[T]) Get() { db.Query("SELECT * FROM box_t") }

func init() { db.Exec("CREATE TABLE IF NOT EXISTS init_t (id int)") }

// 빈 함수는 정점이 없다 — usr 없이 센다.
func _() { db.Query("SELECT * FROM blankfn_t") }

// 패키지 경로 store.v와 겹치는 심볼 — 충돌 접미사가 붙은 ID다.
func v() { db.Query("SELECT * FROM coll_t") }

func use() { _ = countQuery; _ = qa; _ = qb; v() }
`,
		// 심볼 ID example.com/fixture/store.v와 겹치는 패키지 경로.
		"store.v/p.go": "package storev\n\nfunc P() {}\n",
		// 모듈 심볼을 쓰지 않는 빈 선언만 있는 패키지 — pkg._ 정점이 없다.
		"other/other.go": `package other

import "strings"

var _ = strings.ToUpper("select * from nomod_t")
`,
		// exclude로 뺀 패키지 — 정점이 없어 usr를 싣지 않는다.
		"excl/excl.go": `package excl

import "database/sql"

func Run(db *sql.DB) { db.Query("SELECT * FROM excl_t") }
`,
	})
}

// channelUsrs는 비동적 사실의 channel → usr(없으면 빈 문자열) 표다.
func channelUsrs(t *testing.T, doc *BridgeFactsDocument) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range doc.Facts {
		fact := f.(RelationFact)
		key := fact.Channel
		if fact.Method != "" {
			key += "|" + fact.Method
		}
		usr := ""
		if fact.Symbol != nil {
			usr = fact.Symbol.Usr
			if fact.Symbol.QualifiedName == "" {
				t.Fatalf("symbol without qualifiedName: %+v", fact)
			}
		}
		if prev, ok := out[key]; ok && prev != usr {
			t.Fatalf("channel %q has two owners %q and %q", key, prev, usr)
		}
		out[key] = usr
	}
	return out
}

// TestSchemaUsrAttribution은 선언 종류별 귀속과 빠진 신원의 계수를 확인한다.
func TestSchemaUsrAttribution(t *testing.T) {
	dir := usrFixture(t)
	doc, err := SchemaFacts(Options{Dir: dir, Exclude: []string{"excl"}}, "test")
	if err != nil {
		t.Fatalf("SchemaFacts: %v", err)
	}
	const s = "example.com/fixture/store"
	want := map[string]string{
		"repo_t":      s + ".(Repo).List",
		"closure_t":   s + ".Each",
		"var_t":       s + ".countQuery",
		"qa_t":        s + ".qa",
		"qb_t":        s + ".qb",
		"blank_t":     s + "._",
		"users|email": s + ".User",
		"users":       s + ".(User).TableName",
		"box_t":       s + ".(Box).Get",
		"init_t":      s + ".init",
		"coll_t":      s + ".v" + graph.CollisionSuffix,
		"blankfn_t":   "",
		"nomod_t":     "",
		"excl_t":      "",
	}
	got := channelUsrs(t, doc)
	for channel, usr := range want {
		if g, ok := got[channel]; !ok || g != usr {
			t.Errorf("channel %q: usr = %q (present %v), want %q", channel, g, ok, usr)
		}
	}
	if got := missingUsrCount(doc); got != 3 {
		t.Fatalf("missing-relation-usrs = %d, want 3: %v", got, doc.Limitations)
	}
}

// TestSchemaUsrsAreGraphVertices는 모든 usr가 같은 옵션으로 수확한 impact 그래프의
// 심볼 정점인지 확인한다 — trace가 문자열 일치로 잇는 계약의 핵심이다.
func TestSchemaUsrsAreGraphVertices(t *testing.T) {
	dir := usrFixture(t)
	for _, exclude := range [][]string{nil, {"excl"}} {
		doc, err := SchemaFacts(Options{Dir: dir, Exclude: exclude}, "test")
		if err != nil {
			t.Fatalf("SchemaFacts: %v", err)
		}
		g, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Exclude: exclude})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		kinds := map[string]graph.VertexKind{}
		for _, v := range g.Vertices {
			kinds[v.ID] = v.Kind
		}
		withUsr := 0
		for _, f := range doc.Facts {
			fact := f.(RelationFact)
			if fact.Symbol == nil {
				continue
			}
			withUsr++
			kind, ok := kinds[fact.Symbol.Usr]
			if !ok || kind == graph.KindPackage || kind == graph.KindModule {
				t.Errorf("usr %q is not a symbol vertex of the impact graph (kind %q)", fact.Symbol.Usr, kind)
			}
		}
		if withUsr == 0 {
			t.Fatal("fixture must yield facts with usr")
		}
		// exclude가 없으면 excl 패키지 사실도 신원을 가진다.
		if exclude == nil && channelUsrs(t, doc)["excl_t"] != "example.com/fixture/excl.Run" {
			t.Fatalf("excl_t without exclude must carry excl.Run: %v", doc.Facts)
		}
	}
}

// missingUsrCount는 missing-relation-usrs limitation의 수를 읽는다(없으면 0).
func missingUsrCount(doc *BridgeFactsDocument) int {
	for _, lim := range doc.Limitations {
		if rest, ok := strings.CutPrefix(lim, "missing-relation-usrs: "); ok {
			n := 0
			for _, c := range rest {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int(c-'0')
			}
			return n
		}
	}
	return 0
}

// TestUsrDocumentKeepsTheVertexSet은 schema·routes의 usr 확인 문서(usrDocument)가 같은 적재·옵션의 전체 symbol
// 문서(documentFrom)와 같은 정점 ID 집합을 내는지 본다 — usr는 impact 그래프의 정점이어야 한다(유령 ID 금지).
// 사례: init, 빈 식별자·호출 초기화식(pkg._), 패키지 ID 충돌 접미사(store.v), exclude, --pattern 축소, go.work.
// want는 사례가 실제로 그 정점을 만드는지 확인하는 표본이다 — 없으면 비교가 그 경우를 보지 못한다.
func TestUsrDocumentKeepsTheVertexSet(t *testing.T) {
	dir := usrFixture(t)
	_, workspaceModule := writeWorkspace(t)
	const store = "example.com/fixture/store"
	for _, tc := range []struct {
		name string
		opts Options
		want []string
	}{
		{"init, initializer roots and the collision suffix", Options{Dir: dir},
			[]string{store + ".init", store + "._", store + ".v#symbol", store + ".(Box).Get"}},
		{"exclude", Options{Dir: dir, Exclude: []string{"excl"}}, []string{store + "._", store + ".v#symbol"}},
		{"pattern", Options{Dir: dir, Patterns: []string{"./store/..."}}, []string{store + ".init", store + "._"}},
		// 심볼은 패턴에 걸린 패키지에서만 수확한다 — 형제 모듈 b도 패턴으로 고른다.
		{"go.work", Options{Dir: workspaceModule, Patterns: []string{"./...", "example.com/b/..."}},
			[]string{"example.com/a.A", "example.com/b.B"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts
			opts.Level = graph.LevelSymbol
			pkgs, err := load(opts)
			if err != nil {
				t.Fatal(err)
			}
			full, err := documentFrom(pkgs, opts)
			if err != nil {
				t.Fatal(err)
			}
			usr, err := usrDocument(pkgs, opts)
			if err != nil {
				t.Fatal(err)
			}
			want, got := newSymbolIDs(full), newSymbolIDs(usr)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("the usr document must keep the vertex set of the full harvest\n got: %v\nwant: %v", got, want)
			}
			for _, id := range tc.want {
				if !got.symbols[id] {
					t.Errorf("fixture symbol %s is missing — the case is not exercised: %v", id, got.symbols)
				}
			}
		})
	}
}

// TestUsrDocumentSkipsEdgeStages는 usr 확인 문서가 간선 전용 단계(구조 간선·외부 디스패치·CHA 팬아웃)를 건너뛰고
// 심볼 간선을 기록하지 않는지 본다. schema·routes는 이 문서에서 정점 ID·종류(newSymbolIDs)만 읽는데, 그 단계와
// 간선 기록이 수확 시간·메모리의 큰 몫이고 구조 간선 판정은 구체 타입 × 인터페이스로 는다. 같은 fixture의 전체
// 문서에는 implements·call 간선과 satisfies 사실이 있어야 비교가 뜻이 있다. 패키지 import 간선은 정점 단계
// (buildDocument)가 만들어 남는다.
func TestUsrDocumentSkipsEdgeStages(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import (
	"fmt"

	"example.com/fixture/shape"
)

func total(shapes []shape.Shape) (n int) {
	for _, s := range shapes {
		n += s.Area()
	}
	return n
}

func main() { fmt.Println(total([]shape.Shape{shape.Square{Side: 2}})) }
`,
		"shape/shape.go": `package shape

import "fmt"

type Shape interface{ Area() int }

type Square struct{ Side int }

func (s Square) Area() int { return s.Side * s.Side }

func (s Square) String() string { return fmt.Sprint(s.Side) }
`,
	})
	opts := Options{Dir: dir, Level: graph.LevelSymbol}
	pkgs, err := load(opts)
	if err != nil {
		t.Fatal(err)
	}
	full, err := documentFrom(pkgs, opts)
	if err != nil {
		t.Fatal(err)
	}
	usr, err := usrDocument(pkgs, opts)
	if err != nil {
		t.Fatal(err)
	}
	kinds := func(doc *graph.Document) map[graph.EdgeKind]bool {
		out := map[graph.EdgeKind]bool{}
		for _, e := range doc.Edges {
			out[e.Kind] = true
		}
		return out
	}
	satisfies := func(doc *graph.Document) []string {
		var out []string
		for _, v := range doc.Vertices {
			if len(v.Satisfies) > 0 {
				out = append(out, v.ID)
			}
		}
		return out
	}
	if k := kinds(full); !k[graph.EdgeImplements] || !k[graph.EdgeCall] || !k[graph.EdgeImport] || len(satisfies(full)) == 0 {
		t.Fatalf("the full harvest must hold implements, call and import edges and satisfies facts: %v %v",
			k, satisfies(full))
	}
	for kind := range kinds(usr) {
		if kind != graph.EdgeImport {
			t.Errorf("the usr document must record no symbol edges, found %q", kind)
		}
	}
	if !kinds(usr)[graph.EdgeImport] {
		t.Errorf("package import edges come from the vertex stage and must stay")
	}
	if got := satisfies(usr); len(got) > 0 {
		t.Errorf("the usr document must skip external-dispatch facts, found satisfies on %v", got)
	}
}

// denseInterfaces는 타입 n개와 인터페이스 n개가 모두 같은 메서드 M을 갖는 패키지 소스다 — 모든 (타입, 인터페이스)
// 쌍이 만족해 이름 사전 필터도 거르지 못하므로, 전체 수확의 implements 판정·간선은 n×n으로 는다.
func denseInterfaces(n int) string {
	var src strings.Builder
	src.WriteString("package x\n")
	for k := range n {
		fmt.Fprintf(&src, "\ntype I%[1]d interface{ M() }\n\ntype T%[1]d struct{}\n\nfunc (T%[1]d) M() {}\n", k)
	}
	return src.String()
}

// TestUsrDocumentGrowsLinearly는 usr 확인 문서의 할당이 선언 수에 선형인지 본다. 모든 쌍이 만족하는 패키지에서
// 전체 수확은 implements 판정과 간선을 쌍마다 만들어, 타입·인터페이스를 네 배로 늘리면 할당이 열 배 넘게 는다.
// usr 문서는 정점만 만들어 네 배 안팎이어야 한다. 간선을 기록하지 않아도 구조 간선 단계가 돌면 쌍마다 판정하므로
// 비율이 전체 수확 쪽으로 돌아간다 — 문서에 흔적이 남지 않는 그 회귀를 이 비율이 잡는다. 시간 대신 할당 횟수를
// 세므로 바쁜 머신에서도 흔들리지 않는다.
func TestUsrDocumentGrowsLinearly(t *testing.T) {
	allocations := func(n int, build func([]*packages.Package, Options) (*graph.Document, error)) float64 {
		p := checkedPackage(t, "example.com/x", denseInterfaces(n))
		p.ID, p.Name = p.PkgPath, "x"
		p.Module = &packages.Module{Path: p.PkgPath, Main: true}
		opts := Options{Dir: t.TempDir(), Level: graph.LevelSymbol}
		var buildErr error
		allocs := testing.AllocsPerRun(3, func() { _, buildErr = build([]*packages.Package{p}, opts) })
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		return allocs
	}
	const small, large = 20, 80
	full := allocations(large, documentFrom) / allocations(small, documentFrom)
	usr := allocations(large, usrDocument) / allocations(small, usrDocument)
	t.Logf("allocation growth for %dx types and interfaces: full harvest %.1fx, usr document %.1fx", large/small, full, usr)
	if full < 8 {
		t.Fatalf("the fixture must make the full harvest grow with type×interface pairs, got %.1fx", full)
	}
	if usr > 6 {
		t.Fatalf("the usr document must grow linearly with declarations: %.1fx allocations for %dx types and "+
			"interfaces (the full harvest grows %.1fx)", usr, large/small, full)
	}
}
