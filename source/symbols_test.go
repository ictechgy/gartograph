package source

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
	"golang.org/x/tools/go/packages"
)

// symbolFixture는 인터페이스 구현·임베딩·호출·init·미사용 심볼이 있는 모듈이다.
// 도달성·디스패치·구조 간선을 한 fixture에서 다 본다.
func symbolFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/symfix\n\ngo 1.27\n",
		"main.go": `package main

import "example.com/symfix/impl"

func main() {
	run()
}

func run() {
	var d impl.Doer = impl.Worker{}
	d.Do()
}
`,
		"impl/impl.go": `package impl

type Doer interface {
	Do() string
}

type Base struct{}

func (Base) Name() string { return "base" }

type Worker struct {
	Base
}

func (w Worker) Do() string {
	return w.Name()
}

var Default = Worker{}

const Kind = "worker"

func init() { _ = Default }

func Unused() {}

func Spin() { Spin() }
`,
	})
}

// loadSymbol은 심볼 레벨 문서를 수확한다.
func loadSymbol(t *testing.T, dir string) *graph.Document {
	t.Helper()
	doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// hasEdge는 (from,to,kind) 간선의 존재를 확인한다.
func hasEdge(d *graph.Document, from, to string, kind graph.EdgeKind) bool {
	for _, e := range d.Edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return true
		}
	}
	return false
}

// TestSymbolVertices는 심볼 레벨이 모든 선언 종류를 정점으로 담는지 확인한다.
func TestSymbolVertices(t *testing.T) {
	doc := loadSymbol(t, symbolFixture(t))
	if doc.Level != graph.LevelSymbol {
		t.Fatalf("expected symbol level, got %s", doc.Level)
	}
	want := map[string]graph.VertexKind{
		"example.com/symfix":                  graph.KindPackage,
		"example.com/symfix.main":             graph.KindFunc,
		"example.com/symfix.run":              graph.KindFunc,
		"example.com/symfix/impl":             graph.KindPackage,
		"example.com/symfix/impl.init":        graph.KindFunc,
		"example.com/symfix/impl.Unused":      graph.KindFunc,
		"example.com/symfix/impl.Spin":        graph.KindFunc,
		"example.com/symfix/impl.Doer":        graph.KindType,
		"example.com/symfix/impl.Base":        graph.KindType,
		"example.com/symfix/impl.Worker":      graph.KindType,
		"example.com/symfix/impl.Default":     graph.KindVar,
		"example.com/symfix/impl.Kind":        graph.KindConst,
		"example.com/symfix/impl.(Doer).Do":   graph.KindMethod,
		"example.com/symfix/impl.(Base).Name": graph.KindMethod,
		"example.com/symfix/impl.(Worker).Do": graph.KindMethod,
	}
	for id, kind := range want {
		v, ok := doc.VertexByID(id)
		if !ok {
			t.Fatalf("missing vertex %s", id)
		}
		if v.Kind != kind {
			t.Fatalf("vertex %s: expected kind %s, got %s", id, kind, v.Kind)
		}
		// 패키지 정점은 한 지점이 없어 위치가 없다 — 심볼만 위치를 요구한다.
		if kind != graph.KindPackage && v.Position == nil {
			t.Fatalf("vertex %s has no position", id)
		}
	}
	// 메서드 승격: Worker의 메서드 집합에 Name이 있어도 정점은 선언 타입 아래 하나뿐이다.
	if doc.HasVertex("example.com/symfix/impl.(Worker).Name") {
		t.Fatal("promoted method must not get a vertex under the embedding type")
	}
}

// TestSymbolStructuralEdges는 contains·embeds·implements 간선을 확인한다.
func TestSymbolStructuralEdges(t *testing.T) {
	doc := loadSymbol(t, symbolFixture(t))
	const impl = "example.com/symfix/impl"
	if !hasEdge(doc, impl, impl+".Worker", graph.EdgeContains) {
		t.Fatal("missing contains edge pkg→Worker")
	}
	if !hasEdge(doc, impl+".Worker", impl+".Base", graph.EdgeEmbeds) {
		t.Fatal("missing embeds edge Worker→Base")
	}
	if !hasEdge(doc, impl+".Worker", impl+".Doer", graph.EdgeImplements) {
		t.Fatal("missing implements edge Worker→Doer")
	}
}

// TestSymbolCallEdges는 직접 호출과 인터페이스 디스패치의 CHA 팬아웃을 확인한다.
// 인터페이스 메서드 호출은 인터페이스 메서드와 모든 구현 메서드로 간선이 가야
// 도달성 분석이 구현체를 죽은 코드로 오판하지 않는다.
func TestSymbolCallEdges(t *testing.T) {
	doc := loadSymbol(t, symbolFixture(t))
	const (
		mainID = "example.com/symfix.main"
		runID  = "example.com/symfix.run"
		doerDo = "example.com/symfix/impl.(Doer).Do"
		workDo = "example.com/symfix/impl.(Worker).Do"
		name   = "example.com/symfix/impl.(Base).Name"
	)
	for _, to := range [][2]string{
		{mainID, runID},
		{runID, doerDo}, // d.Do()의 정적 대상
		{runID, workDo}, // CHA 팬아웃 — 구현체 호출
		{workDo, name},  // 승격 메서드 호출은 선언 타입의 메서드로 간다
	} {
		if !hasEdge(doc, to[0], to[1], graph.EdgeCall) {
			t.Fatalf("missing call edge %s → %s", to[0], to[1])
		}
	}
	// 자기 호출도 간선으로 남아야 한다 — 자기루프는 실제 순환이다.
	if !hasEdge(doc, "example.com/symfix/impl.Spin",
		"example.com/symfix/impl.Spin", graph.EdgeCall) {
		t.Fatal("missing self-call edge Spin→Spin")
	}
}

// TestSymbolReferenceEdges는 타입·값 참조가 references 간선으로 남는지 확인한다.
func TestSymbolReferenceEdges(t *testing.T) {
	doc := loadSymbol(t, symbolFixture(t))
	const impl = "example.com/symfix/impl"
	for _, e := range [][2]string{
		{"example.com/symfix.run", impl + ".Doer"},   // var d impl.Doer
		{"example.com/symfix.run", impl + ".Worker"}, // impl.Worker{}
		{impl + ".init", impl + ".Default"},          // _ = Default
	} {
		if !hasEdge(doc, e[0], e[1], graph.EdgeReferences) {
			t.Fatalf("missing references edge %s → %s", e[0], e[1])
		}
	}
}

// TestSymbolRoots는 main과 init이 보존 루트로 기록되는지 확인한다.
func TestSymbolRoots(t *testing.T) {
	doc := loadSymbol(t, symbolFixture(t))
	var hasMain, hasInit bool
	for _, r := range doc.Roots {
		hasMain = hasMain || r == "example.com/symfix.main"
		hasInit = hasInit || r == "example.com/symfix/impl.init"
	}
	if !hasMain || !hasInit {
		t.Fatalf("expected main and init roots, got %v", doc.Roots)
	}
}

// TestTestsHarvest는 --tests 수확을 검증한다. 두 가지 회귀를 묶는다:
// err.Error() 같은 universe 스코프 피호출자는 Pkg()가 nil이라 옛 코드는
// 여기서 패닉했고, go test 진입점은 호출자가 없어 루트로 잡아야 한다.
func TestTestsHarvest(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() { run() }
func run() int { return 1 }
`,
		"main_test.go": `package main

import (
	"errors"
	"testing"
)

func TestRun(t *testing.T) {
	if err := errors.New("x"); err.Error() == "" {
		t.Fatal("empty")
	}
	_ = run()
}

func BenchmarkRun(b *testing.B) { _ = run() }
`,
	})
	doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Tests: true})
	if err != nil {
		t.Fatal(err)
	}
	var hasTest, hasBench bool
	for _, r := range doc.Roots {
		hasTest = hasTest || r == "example.com/fixture.TestRun"
		hasBench = hasBench || r == "example.com/fixture.BenchmarkRun"
	}
	if !hasTest || !hasBench {
		t.Fatalf("test entry points should be roots, got %v", doc.Roots)
	}
}

// TestTagsAndIgnored는 빌드 태그로 제외된 파일이 limitation에 잡히고
// --tags로 포함되는지 확인한다. 제약에 빠진 파일을 조용히 버리면
// "분석했다"와 "못 본 것"을 소비자가 구분할 수 없다.
func TestTagsAndIgnored(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() {}
`,
		"tagged.go": `//go:build special

package main

func Tagged() {}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol})
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, l := range doc.Limitations {
		saw = saw || strings.Contains(l, "build constraints")
	}
	if !saw {
		t.Fatalf("ignored file must appear in limitations: %v", doc.Limitations)
	}
	if doc.HasVertex("example.com/fixture.Tagged") {
		t.Fatal("tagged file must not be harvested without the tag")
	}
	// 태그를 켜면 파일이 수확되고 limitation도 사라져야 한다.
	doc, err = Load(Options{Dir: dir, Level: graph.LevelSymbol, Tags: "special"})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.HasVertex("example.com/fixture.Tagged") {
		t.Fatal("--tags must include the tagged file")
	}
	for _, l := range doc.Limitations {
		if strings.Contains(l, "build constraints") {
			t.Fatalf("no files should be ignored with the tag set: %v", doc.Limitations)
		}
	}
}

// TestGeneratedMark는 생성 표지 파일 출신 정점에 generated가 붙는지 확인한다.
// 숨기지 않고 표시하는 이유는 Vertex.Generated의 주석에 있다.
func TestGeneratedMark(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() { Generated() }
`,
		"gen.go": `// Code generated by fixture. DO NOT EDIT.

package main

func Generated() {}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range doc.Vertices {
		want := v.ID == "example.com/fixture.Generated"
		if v.Generated != want {
			t.Fatalf("vertex %s generated=%v, want %v", v.ID, v.Generated, want)
		}
	}
}

// TestTypeLevel은 type 레벨이 타입과 구조 간선만 담는지 확인한다.
// 함수 정점과 call 간선이 새어 들어오면 레벨 구분이 깨진 것이다.
func TestTypeLevel(t *testing.T) {
	doc, err := Load(Options{Dir: symbolFixture(t), Level: graph.LevelType})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Level != graph.LevelType {
		t.Fatalf("expected type level, got %s", doc.Level)
	}
	if !doc.HasVertex("example.com/symfix/impl.Worker") {
		t.Fatal("missing type vertex at type level")
	}
	if doc.HasVertex("example.com/symfix.main") {
		t.Fatal("func vertex leaked into type level")
	}
	for _, e := range doc.Edges {
		if e.Kind == graph.EdgeCall {
			t.Fatalf("call edge leaked into type level: %+v", e)
		}
	}
}

// TestSymbolEdgeCases는 제네릭 인스턴스화·괄호 감싸기·포인터 임베드·
// linkname 지시문의 수확 경로를 확인한다.
func TestSymbolEdgeCases(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

import _ "unsafe"

//go:linkname hidden
func hidden() {}

func Generic[T any](v T) T { return v }

func Caller() {
	_ = Generic[int](1)
	((hidden))()
}
`,
		"b/b.go": `package b

type Inner struct{}

type Outer struct {
	*Inner
}
`,
	})
	doc := loadSymbol(t, dir)
	const a = "example.com/fixture/a"
	if !hasEdge(doc, a+".Caller", a+".Generic", graph.EdgeCall) {
		t.Fatal("generic call Generic[int]() must unwrap to a call edge")
	}
	if !hasEdge(doc, a+".Caller", a+".hidden", graph.EdgeCall) {
		t.Fatal("parenthesized call must unwrap to a call edge")
	}
	if !hasEdge(doc, "example.com/fixture/b.Outer",
		"example.com/fixture/b.Inner", graph.EdgeEmbeds) {
		t.Fatal("embedded *Inner must produce an embeds edge")
	}
	// linkname은 그래프 밖에서 심볼을 살리므로 limitation으로 남아야 한다.
	var linknameNoted bool
	for _, l := range doc.Limitations {
		if strings.Contains(l, "linkname") {
			linknameNoted = true
		}
	}
	if !linknameNoted {
		t.Fatalf("expected linkname limitation: %v", doc.Limitations)
	}
}

// TestEdgePositions는 사용 지점이 간선에 실리고 같은 관계의 반복 호출이
// 지점으로 합쳐지는지 확인한다 — 위치가 diff의 노이즈가 아니라
// "어디서"의 사실이 되는 v2 계약이다.
func TestEdgePositions(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/posfix\n\ngo 1.27\n",
		"main.go": `package main

func main() {
	help()
	help()
}

func help() {}
`,
	})
	doc := loadSymbol(t, dir)
	var call *graph.Edge
	for i := range doc.Edges {
		e := &doc.Edges[i]
		if e.Kind == graph.EdgeCall && e.To == "example.com/posfix.help" {
			call = e
		}
	}
	if call == nil || len(call.Positions) != 2 {
		t.Fatalf("two call sites must merge into one edge with two positions: %+v",
			doc.Edges)
	}
	if call.Positions[0].Line >= call.Positions[1].Line {
		t.Fatalf("positions must be sorted: %+v", call.Positions)
	}
	if !strings.HasSuffix(call.Positions[0].File, "main.go") {
		t.Fatalf("position must point at the caller file: %+v", call.Positions)
	}
}

// TestSortedPackagePaths는 디버깅용 정렬 헬퍼를 확인한다.
func TestSortedPackagePaths(t *testing.T) {
	pkgs, err := load(Options{Dir: fixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	paths := SortedPackagePaths(pkgs)
	if len(paths) != 2 || paths[0] != "example.com/fixture/a" {
		t.Fatalf("unexpected paths: %v", paths)
	}
}

// TestSymbolExternalRefs는 모듈 밖 참조가 유령 정점이 아니라
// limitation 개수로 남는지 확인한다.
func TestSymbolExternalRefs(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

import "fmt"

func F() { fmt.Println() }
`,
	})
	doc := loadSymbol(t, dir)
	if doc.HasVertex("fmt.Println") {
		t.Fatal("external symbol leaked as vertex")
	}
	var found bool
	for _, l := range doc.Limitations {
		if strings.Contains(l, "outside the module") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected external-references limitation: %v", doc.Limitations)
	}
}

// TestAliasReceiverMethodID는 별칭 리시버 메서드가 실체 타입 아래 ID를
// 갖는지 확인한다 — 별칭을 벗기지 않으면 "pkg.(pkg.A).M" 같은 깨진 ID가 된다.
func TestAliasReceiverMethodID(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/aliasfix\n\ngo 1.27\n",
		"lib/lib.go": `package lib

type Real struct{}

type Al = Real

func (Al) Name() string { return "al" }
`,
	})
	doc := loadSymbol(t, dir)
	if !doc.HasVertex("example.com/aliasfix/lib.(Real).Name") {
		var ids []string
		for _, v := range doc.Vertices {
			ids = append(ids, v.ID)
		}
		t.Fatalf("alias receiver method must live under the real type, got %v", ids)
	}
}

// idCollisionFixture는 점이 든 패키지 경로(example.com/m/x.y)와 패키지 x의 심볼 y가
// 같은 ID가 되는 모듈이다. 빈 식별자 변수 초기화식(var _ = first())과 빈 함수
// (func _())도 담는다.
func idCollisionFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.27\n",
		"main.go": `package main

import (
	"example.com/m/x"
	xy "example.com/m/x.y"
)

func main() { x.Use(); xy.Other() }
`,
		"x/x.go": `package x

func y() {}

func Use() { y() }

type I interface{ M() }
type T struct{}

func (T) M() {}

var _ I = T{}
var _ = first()

func first() int { return 1 }

func _() { onlyBlank() }

func onlyBlank() {}
`,
		"x/x2.go": `package x

var _ = second()

func second() int { return 2 }
`,
		"x.y/xy.go": `package xy

func Other() {}
`,
		"x/z.go": `package x

// Z의 타입 ID는 패키지 example.com/m/x.Z와 같다.
type Z struct{}

func (*Z) Error() string { return "z" }

func MakeErr() error { return &Z{} }
`,
		"x.Z/z.go": `package xz

func Zed() {}
`,
	})
}

// TestSymbolIDCollidingWithPackage는 패키지 ID와 겹치는 심볼이 접미사 붙은 ID로
// 제 정점·간선을 갖고, 패키지 정점에 간선을 얹지 않는지 확인한다. 함수가 패키지를
// 호출하는 간선은 거짓 사실이고, 심볼을 빼면 그 피호출자가 dead가 된다.
func TestSymbolIDCollidingWithPackage(t *testing.T) {
	doc := loadSymbol(t, idCollisionFixture(t))
	const pkg = "example.com/m/x.y"
	for _, e := range doc.Edges {
		touches := e.From == pkg || e.To == pkg
		legit := e.Kind == graph.EdgeImport || (e.Kind == graph.EdgeContains && e.From == pkg)
		if touches && !legit {
			t.Fatalf("symbol edge landed on package vertex %s: %+v", pkg, e)
		}
	}
	sym := pkg + graph.CollisionSuffix
	if v, ok := doc.VertexByID(sym); !ok || v.Kind != graph.KindFunc {
		t.Fatalf("colliding symbol must keep a vertex under %s, got %+v", sym, v)
	}
	if !hasEdge(doc, "example.com/m/x.Use", sym, graph.EdgeCall) ||
		!hasEdge(doc, "example.com/m/x", sym, graph.EdgeContains) {
		t.Fatal("colliding symbol must keep its edges under the suffixed ID")
	}
	// 리시버 타입도 같은 규칙 — 외부 디스패치 Receiver가 타입 정점을 가리킨다.
	zerr, _ := doc.VertexByID("example.com/m/x.(Z).Error")
	if zerr.Receiver != "example.com/m/x.Z"+graph.CollisionSuffix || !slices.Contains(zerr.Satisfies, "error") {
		t.Fatalf("receiver must point at the suffixed type vertex: %+v", zerr)
	}
}

// TestSymbolIDCollidingWithTestMain은 --tests의 테스트 main 패키지(x.test)와
// 패키지 x의 함수 test가 겹쳐도 그 함수와 피호출자가 그래프에 남는지 확인한다 —
// 점 경로를 직접 만들지 않아도 흔한 이름만으로 생기는 충돌이다.
func TestSymbolIDCollidingWithTestMain(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.27\n",
		"main.go": `package main

import "example.com/m/x"

func main() { x.Run() }
`,
		"x/x.go":      "package x\n\nfunc Run() { test() }\n\nfunc test() { helper() }\n\nfunc helper() {}\n",
		"x/x_test.go": "package x\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) { Run() }\n",
	})
	doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Tests: true})
	if err != nil {
		t.Fatal(err)
	}
	sym := "example.com/m/x.test" + graph.CollisionSuffix
	if !hasEdge(doc, "example.com/m/x.Run", sym, graph.EdgeCall) ||
		!hasEdge(doc, sym, "example.com/m/x.helper", graph.EdgeCall) {
		t.Fatalf("function test must keep its call edges beside the test main package")
	}
}

// TestCollidingConstAtTypeLevel은 type 레벨(const 정점 없음)에서도 충돌 ID의
// 참조가 패키지 정점에 얹히지 않는지 확인한다.
func TestCollidingConstAtTypeLevel(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.27\n",
		"main.go": `package main

import (
	"example.com/m/x"
	_ "example.com/m/x.y"
)

func main() { _ = x.T{} }
`,
		"x/x.go":    "package x\n\nconst y = 2\n\ntype T [y]int\n",
		"x.y/xy.go": "package xy\n",
	})
	doc, err := Load(Options{Dir: dir, Level: graph.LevelType})
	if err != nil {
		t.Fatal(err)
	}
	if hasEdgeTo(doc, "example.com/m/x.T", "example.com/m/x.y") {
		t.Fatal("a type-level reference to a colliding const must not land on the package vertex")
	}
}

// TestBlankInitializersAreRoots는 빈 식별자 변수 초기화식이 보존 루트로 수확되는지
// 확인한다 — var _ = f()는 프로그램 초기화 때 실행되고, var _ I = T{}는 T·I를
// 쓴다. 빈 함수(func _())의 본문은 실행되지 않으므로 루트가 아니다.
func TestBlankInitializersAreRoots(t *testing.T) {
	doc := loadSymbol(t, idCollisionFixture(t))
	const blank = "example.com/m/x._"
	if _, ok := doc.VertexByID(blank); !ok || !slices.Contains(doc.Roots, blank) {
		t.Fatalf("blank declarations must share one root vertex, roots=%v", doc.Roots)
	}
	for _, to := range []string{"example.com/m/x.first", "example.com/m/x.second",
		"example.com/m/x.T", "example.com/m/x.I"} {
		if !hasEdgeTo(doc, blank, to) {
			t.Fatalf("blank initializer must reference %s", to)
		}
	}
	if hasEdgeTo(doc, blank, "example.com/m/x.onlyBlank") {
		t.Fatal("a blank function body never runs and must not become a root's edge")
	}
}

// hasEdgeTo는 종류와 상관없이 from→to 간선이 있는지 본다.
func hasEdgeTo(doc *graph.Document, from, to string) bool {
	return slices.ContainsFunc(doc.Edges, func(e graph.Edge) bool { return e.From == from && e.To == to })
}

// TestSideEffectInitializersAreRoots는 이름 있는 패키지 변수라도 초기화식이 실제로
// 호출을 실행하면 그 호출이 패키지 초기화 루트(pkg._)에서 닿는지 확인한다 —
// var registered = register()는 registered를 아무도 읽지 않아도 초기화 때 실행된다.
// 호출이 없는 초기화식(함수 값 표·형 변환·함수 리터럴 본문)은 변수를 거쳐서만 닿는다.
func TestSideEffectInitializersAreRoots(t *testing.T) {
	doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{
		"main.go": `package main

type Duration int

var registered = register()
var table = map[string]func(){"a": handleA}
var lazy = func() { inner() }
var d = Duration(3)
var n = len("abc")

func register() int { return 1 }
func handleA()      {}
func inner()        {}

func main() {}
`,
	}))
	const m = "example.com/fixture"
	if !slices.Contains(doc.Roots, m+"._") || !hasEdgeTo(doc, m+"._", m+".register") {
		t.Fatalf("a side-effecting initializer must be reached from the package init root, roots=%v", doc.Roots)
	}
	for _, to := range []string{m + ".handleA", m + ".inner", m + ".Duration"} {
		if hasEdgeTo(doc, m+"._", to) {
			t.Fatalf("%s is not executed at init and must stay behind its variable", to)
		}
	}
	// 모듈 심볼을 참조하지 않는 초기화식(var ErrX = errors.New(...))은 간선 없는 루트만
	// 만든다 — 흔한 관용구라 정점·루트 노이즈가 된다.
	lone := loadSymbol(t, testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport \"errors\"\n\nvar ErrX = errors.New(\"x\")\n\nfunc main() { _ = ErrX }\n",
	}))
	if _, ok := lone.VertexByID(m + "._"); ok {
		t.Fatal("an initializer that references no module symbol must not create an init root")
	}
}

// TestExplainRoots는 RTA explain 출발점이 문서 루트에 패키지별 합성 init 순회 ID를
// 더하는지 확인한다 — 합성 init은 RTA 루트지만 문서 정점이 아니다.
func TestExplainRoots(t *testing.T) {
	doc := &graph.Document{Vertices: []graph.Vertex{
		{ID: "m/a", Kind: graph.KindPackage}, {ID: "m/a.F", Kind: graph.KindFunc},
	}}
	got := ExplainRoots(doc, []string{"m/a.F"})
	if !slices.Equal(got, []string{"m/a.F", "m/a" + PackageInitSuffix}) {
		t.Fatalf("explain roots must add one synthetic initializer per package, got %v", got)
	}
}

// TestInitializerRootCountsExternalRefsOnce는 초기화 루트 순회가 모듈 밖 참조를 다시
// 세지 않는지 확인한다 — 같은 외부 참조를 가진 두 초기화식(루트를 만드는 것과 만들지
// 않는 것)의 limitation 문장이 같아야 한다. 지역 변수가 패키지 변수와 이름이 같아도
// 모듈 참조로 세지 않아 빈 루트를 만들지 않는다.
func TestInitializerRootCountsExternalRefsOnce(t *testing.T) {
	extRefs := func(src string) (string, *graph.Document) {
		doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{"main.go": src}))
		for _, l := range doc.Limitations {
			if strings.Contains(l, "references to symbols outside the module") {
				return l, doc
			}
		}
		return "", doc
	}
	const head = "package main\n\nimport \"sort\"\n\nconst three = 3\n\nvar y = 3\n\n"
	withRoot, doc := extRefs(head + "var z = sort.SearchInts(nil, three)\n\nfunc main() {}\n")
	if _, ok := doc.VertexByID("example.com/fixture._"); !ok {
		t.Fatal("an initializer calling out with a module reference must create the init root")
	}
	withoutRoot, _ := extRefs(head + "var z = sort.SearchInts(nil, 3)\n\nfunc main() {}\n")
	if withRoot == "" || withRoot != withoutRoot {
		t.Fatalf("the init-root pass must not recount external refs:\n%q\n%q", withRoot, withoutRoot)
	}
	_, shadow := extRefs(head + "var z = sort.SearchInts(nil, func() int { y := 2; return y }())\n\nfunc main() {}\n")
	if _, ok := shadow.VertexByID("example.com/fixture._"); ok {
		t.Fatal("a local variable shadowing a package variable is not a module reference")
	}
}

// TestInitRootCleanup은 초기화 루트(pkg._)의 노이즈와 사실을 고정한다 — 열거형의
// const ( _ = iota )는 모듈 심볼을 참조하지 않아 간선 없는 루트를 만들지 않는다.
// var _ I = (*T)(nil)은 선언 타입까지 보고 루트를 만든다. 생성 파일과 손으로 쓴
// 파일의 선언이 함께 기여하면 위치는 손으로 쓴 쪽이라 generated가 아니다.
func TestInitRootCleanup(t *testing.T) {
	const m = "example.com/fixture"
	iota := loadSymbol(t, testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nconst (\n\t_ = iota\n\tA\n\tB\n)\n\nfunc main() { _ = A + B }\n",
	}))
	if _, ok := iota.VertexByID(m + "._"); ok {
		t.Fatal("an iota-skipping blank const references nothing and must not create an init root")
	}
	doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{
		"a_gen.go": "// Code generated by hand-rolled test. DO NOT EDIT.\n\npackage main\n\nvar _ = f()\n\nfunc f() int { return 1 }\n",
		"main.go":  "package main\n\ntype I interface{ M() }\ntype T struct{}\n\nfunc (*T) M() {}\n\nvar _ I = (*T)(nil)\n\nfunc main() {}\n",
	}))
	blank, ok := doc.VertexByID(m + "._")
	if !ok || !hasEdgeTo(doc, blank.ID, m+".I") || !hasEdgeTo(doc, blank.ID, m+".f") {
		t.Fatalf("blank declarations must still share the init root, got %+v", blank)
	}
	if blank.Generated || !strings.HasSuffix(blank.Position.File, "main.go") {
		t.Fatalf("a hand-written contribution must win the position: %+v %+v", blank, blank.Position)
	}
}

// lineDirectiveDecls는 첫 빈 선언의 위치를 //line으로 target에 옮긴 선언부다. 같은 패키지의
// 다음 빈 선언이 초기화 루트(pkg._)에 합쳐질 때 수확은 위치 파일의 생성 여부를 판정한다
// (preferHandWritten) — 그 파일 이름은 //line이 정한다.
func lineDirectiveDecls(target string) string {
	return "package main\n\ntype I interface{ M() }\n\ntype T struct{}\n\nfunc (*T) M() {}\n\n" +
		"//line " + target + ":1\nvar _ I = (*T)(nil)\n"
}

// TestInitRootLineDirectiveFIFO는 //line이 FIFO를 가리켜도 심볼 수확이 끝나는지 확인한다.
// 생성 여부를 보려고 위치 파일을 그대로 열면 쓰는 쪽이 없는 FIFO의 open에서 수확이
// 영원히 멈췄다 — 분석 대상 저장소 하나로 dead·query·mcp를 세울 수 있었다.
func TestInitRootLineDirectiveFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	testutil.MakeFIFO(t, fifo)
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": lineDirectiveDecls(fifo) + "\nvar _ I = (*T)(nil)\n\nfunc main() {}\n",
	})
	var doc *graph.Document
	var err error
	testutil.FinishWithin(t, testutil.BlockDeadline, func() {
		doc, err = Load(Options{Dir: dir, Level: graph.LevelSymbol})
	})
	if err != nil {
		t.Fatal(err)
	}
	if blank, ok := doc.VertexByID("example.com/fixture._"); !ok || blank.Generated {
		t.Fatalf("both blank declarations must still form a hand-written init root: %+v", blank)
	}
}

// TestInitRootJudgesOnlyLoadedFiles는 초기화 루트의 위치 선택이 로드한 파일의 생성 여부만
// 보는지 확인한다. //line이 모듈 밖의 생성 표지 파일을 가리켜도 그 파일은 열지 않는다 —
// markGenerated도 로드한 파일만 판정해 그 위치는 generated가 아니므로, 손으로 쓴 b.go로
// 위치를 옮길 이유가 없다. 열면 분석 대상이 루트 밖 아무 파일이나 읽게 할 수 있다.
func TestInitRootJudgesOnlyLoadedFiles(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "gen.go")
	if err := os.WriteFile(outside,
		[]byte("// Code generated by fixture. DO NOT EDIT.\n\npackage main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{
		"a.go": lineDirectiveDecls(outside),
		"b.go": "package main\n\nvar _ I = (*T)(nil)\n\nfunc main() {}\n",
	}))
	blank, ok := doc.VertexByID("example.com/fixture._")
	if !ok || blank.Generated || blank.Position == nil || blank.Position.File != outside {
		t.Fatalf("a //line target outside the loaded files must not be judged generated: %+v %+v",
			blank, blank.Position)
	}
}

// TestBlankExternalRefsCountedRegardlessOfOrder는 모듈 심볼을 참조하지 않는 빈 선언의
// 외부 참조도 세는지, 그 수가 같은 패키지 다른 빈 선언의 순서에 흔들리지 않는지 확인한다.
func TestBlankExternalRefsCountedRegardlessOfOrder(t *testing.T) {
	count := func(body string) string {
		doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{
			"main.go": "package main\n\nimport \"errors\"\n\nfunc f() int { return 1 }\n\n" + body + "\nfunc main() {}\n",
		}))
		for _, l := range doc.Limitations {
			if strings.Contains(l, "references to symbols outside the module") {
				return l
			}
		}
		return ""
	}
	externalFirst := count("var _ = errors.New(\"x\")\nvar _ = f()\n")
	moduleFirst := count("var _ = f()\nvar _ = errors.New(\"x\")\n")
	if externalFirst == "" || externalFirst != moduleFirst {
		t.Fatalf("external refs of blank declarations must not depend on order:\n%q\n%q", externalFirst, moduleFirst)
	}
}

// TestUniverseEmbedsDoNotCrash는 universe 타입(error·comparable)을 임베드한 인터페이스·
// struct를 수확해도 패닉하지 않는지 확인한다 — universe 객체는 패키지가 없어 정점 ID를
// 만들 수 없고, 임베드 간선의 대상이 되면 nil 역참조로 죽었다(type·symbol 레벨 모두).
func TestUniverseEmbedsDoNotCrash(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

type E interface {
	error
	Code() int
}

type K interface{ comparable }

type Wrapped struct{ error }

// myErr는 error를 임베드한 모듈 인터페이스 E를 구현하고, Wrapped는 승격된 universe
// Error로 Errorer를 구현한다 — implements 계산이 universe 메서드의 ID를 만들다 죽었다.
type myErr struct{}

func (myErr) Error() string { return "x" }
func (myErr) Code() int     { return 1 }

type Errorer interface{ Error() string }

func Eq[T K](a, b T) bool { return a == b }

func main() {
	_ = Eq(1, 2)
	var e E = myErr{}
	_ = e.Code()
	var r Errorer = Wrapped{}
	_ = r
}
`,
	})
	for _, level := range []graph.Level{graph.LevelType, graph.LevelSymbol} {
		doc, err := Load(Options{Dir: dir, Level: level})
		if err != nil {
			t.Fatalf("%s: %v", level, err)
		}
		if _, ok := doc.VertexByID("example.com/fixture.E"); !ok {
			t.Fatalf("%s: interface embedding error must still be a vertex", level)
		}
	}
}

// depImporter는 테스트가 먼저 검사한 패키지만 내어 주는 importer다 — go/packages 없이 여러
// 패키지 fixture를 go/types로 검사할 때 쓴다.
type depImporter map[string]*types.Package

// Import는 먼저 검사한 패키지를 돌려준다. 없는 경로는 fixture 구성 오류다.
func (d depImporter) Import(path string) (*types.Package, error) {
	if p, ok := d[path]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("fixture imports %q before checking it: pass it in deps", path)
}

// checkSource는 소스 한 파일을 go/types로 직접 검사한 패키지와 타입 오류를 돌려준다. implements
// 판정만 보는 테스트가 패키지 로드(go list) 없이 수확과 같은 go/types 객체를 얻는다. deps는
// import로 내어 줄, 먼저 검사한 패키지다.
func checkSource(t testing.TB, path, src string, deps ...*packages.Package) (*packages.Package, []error) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path+"/x.go", src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	imports := depImporter{}
	for _, d := range deps {
		imports[d.PkgPath] = d.Types
	}
	var typeErrs []error
	conf := types.Config{Importer: imports, Error: func(err error) { typeErrs = append(typeErrs, err) }}
	pkg, err := conf.Check(path, fset, []*ast.File{file}, nil)
	if err != nil && len(typeErrs) == 0 {
		t.Fatalf("check %s: %v", path, err)
	}
	return &packages.Package{PkgPath: path, Types: pkg, Fset: fset}, typeErrs
}

// checkedPackage는 타입 오류가 없어야 하는 fixture를 검사한다.
func checkedPackage(t testing.TB, path, src string, deps ...*packages.Package) *packages.Package {
	t.Helper()
	p, typeErrs := checkSource(t, path, src, deps...)
	if len(typeErrs) > 0 {
		t.Fatalf("fixture %s must type-check: %v", path, typeErrs)
	}
	return p
}

// structuralHarvester는 구조 간선 단계(addStructuralEdges)만 돌릴 harvester다. 패키지 스코프 타입을
// 정점으로 등록해 implements가 실제 수확처럼 문서에 그어진다.
func structuralHarvester(pkgs ...*packages.Package) *harvester {
	h := &harvester{
		doc:           &graph.Document{},
		vertices:      map[string]bool{},
		edgeIdx:       map[string]int{},
		impls:         map[string][]string{},
		instanceImpls: map[string][]string{},
	}
	for _, p := range pkgs {
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			if tn, ok := scope.Lookup(name).(*types.TypeName); ok {
				h.vertices[objectID(tn)] = true
			}
		}
	}
	return h
}

// implementsPairs는 문서의 implements 간선을 (from, to) 집합으로 모은다.
func implementsPairs(doc *graph.Document) map[[2]string]bool {
	out := map[[2]string]bool{}
	for _, e := range doc.Edges {
		if e.Kind == graph.EdgeImplements {
			out[[2]string{e.From, e.To}] = true
		}
	}
	return out
}

// sparseInterfaces는 타입 n개와 인터페이스 n개의 패키지 소스다. 타입 Tk는 메서드 Mk 하나만, 인터페이스
// Ik는 Mk 하나만 가져 만족 쌍은 대각선 n개뿐이다 — 나머지 n×(n-1) 쌍은 메서드 이름부터 맞지 않는다.
func sparseInterfaces(n int) string {
	var src strings.Builder
	src.WriteString("package x\n")
	for k := range n {
		fmt.Fprintf(&src, "\ntype I%[1]d interface{ M%[1]d() }\n\ntype T%[1]d struct{}\n\nfunc (T%[1]d) M%[1]d() {}\n", k)
	}
	return src.String()
}

// TestImplementsEdgesSkipsPairsWithoutMethodNames는 메서드 이름이 모자란 (타입, 인터페이스) 쌍에
// go/types 판정을 부르지 않는지 할당 횟수로 본다. 이름 보유는 엄격·느슨 판정 모두의 필요조건인데,
// 모든 쌍을 판정하면 비용이 타입 수 × 인터페이스 수로 커져 큰 모노레포에서는 이 단계만 1분을 넘겼다.
// 판정은 쌍마다 포인터 타입(*T)을 할당하므로, 타입과 인터페이스를 두 배로 늘려 쌍이 네 배가 될 때
// 할당도 네 배 가까이 늘면 전수 대조다(수정 전 100→200에서 약 3.6배). 시간 대신 할당 횟수를 세므로
// 바쁜 머신에서도 흔들리지 않는다.
func TestImplementsEdgesSkipsPairsWithoutMethodNames(t *testing.T) {
	allocations := func(n int) float64 {
		p := checkedPackage(t, "example.com/x", sparseInterfaces(n))
		var h *harvester
		allocs := testing.AllocsPerRun(3, func() {
			h = structuralHarvester(p)
			h.addStructuralEdges([]*packages.Package{p})
		})
		if got := len(implementsPairs(h.doc)); got != n {
			t.Fatalf("each Tk must implement exactly its own Ik: %d implements edges for n=%d", got, n)
		}
		return allocs
	}
	small, large := allocations(100), allocations(200)
	if large > 3*small {
		t.Fatalf("implements harvest must skip type×interface pairs whose method names do not match: "+
			"%.0f allocations for 100 types and interfaces, %.0f for 200", small, large)
	}
}

// implementsCasesDep와 implementsCasesMain은 이름 사전 필터가 바꾸면 안 되는 판정 사례다.
const implementsCasesDep = `package dep

type Base struct{}

func (Base) Exported() {}
func (Base) hidden()   {}

// DI는 dep의 비공개 메서드를 요구한다 — dep.Base를 임베드한 모듈 타입이 승격으로 만족한다.
type DI interface{ hidden() }
`

const implementsCasesMain = `package m

import "example.com/m/dep"

type I interface{ M() }

// J는 I를 임베드하고, 포인터 리시버 메서드 N을 더 요구한다.
type J interface {
	I
	N()
}

// K의 hidden은 이 패키지의 비공개 메서드다 — dep.Base의 hidden과 이름만 같다.
type K interface{ hidden() }

type E interface{ Exported() }

// Gen은 제네릭 원형이라 느슨한 판정(이름·모양)을 쓴다.
type Gen[X any] interface{ Get() X }

type Pair interface{ Pair() (int, int) }

type T struct{}

func (T) M()  {}
func (*T) N() {}

// Promo는 dep.Base의 Exported·hidden을 값 임베드로, PromoPtr은 포인터 임베드로 승격한다.
type Promo struct{ dep.Base }
type PromoPtr struct{ *dep.Base }

type Own struct{}

func (Own) hidden() {}

type Box[X any] struct{ x X }

func (b Box[X]) Get() X { return b.x }

type IntGet struct{}

func (IntGet) Get() int { return 0 }

// Shape의 Pair는 이름은 같고 모양(결과 개수)이 다르다.
type Shape[X any] struct{}

func (Shape[X]) Pair() int { return 0 }

// NamedPtr는 명명 포인터라 메서드 집합이 비었다.
type NamedPtr *T

// Bad·PBad의 밑 타입은 타입 오류로 invalid다 — go/types는 이런 타입이 모든 인터페이스를
// 만족한다고 답한다.
type Bad undefinedType
type PBad *undefinedType
`

// referenceImplements는 이름 사전 필터 없이 모든 (구체 타입, 인터페이스) 쌍을 판정한 implements
// 간선 집합과 (인터페이스 메서드, 구현 메서드) 집합이다 — 필터가 바꿔서는 안 되는 기준이다.
func referenceImplements(h *harvester, pkgs []*packages.Package) (edges, impls map[[2]string]bool) {
	edges, impls = map[[2]string]bool{}, map[[2]string]bool{}
	var concrete, ifaces []*types.Named
	for _, p := range pkgs {
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			if named, ok := tn.Type().(*types.Named); ok && isInterface(named) {
				ifaces = append(ifaces, named)
			} else if ok {
				concrete = append(concrete, named)
			}
		}
	}
	for _, t := range concrete {
		tset := types.NewMethodSet(types.NewPointer(t))
		for _, i := range ifaces {
			it := i.Underlying().(*types.Interface)
			if it.NumMethods() == 0 || !implementsLoosely(t, tset, it, i) {
				continue
			}
			edges[[2]string{h.id(t.Obj()), h.id(i.Obj())}] = true
			for j := range it.NumMethods() {
				m := it.Method(j)
				if m.Pkg() == nil {
					continue
				}
				if sel := tset.Lookup(m.Pkg(), m.Name()); sel != nil {
					if fn, ok := sel.Obj().(*types.Func); ok && fn.Pkg() != nil {
						impls[[2]string{h.id(m), h.id(fn)}] = true
					}
				}
			}
		}
	}
	return edges, impls
}

// TestImplementsNameFilterKeepsEveryMatch는 이름 사전 필터가 판정이 받아들이는 쌍을 하나도 빼지
// 않는지, 필터 없이 모든 쌍을 판정한 결과와 implements·impls가 같은지 확인한다. 사례: 승격 메서드
// (값·포인터 임베드), 다른 패키지의 비공개 메서드(이름만 같은 것과 실제로 승격된 것), 임베드
// 인터페이스, 포인터 리시버, 제네릭 원형 인터페이스·구체 타입(느슨한 판정), 명명 포인터, invalid
// 타입(go/types가 이름과 무관하게 참을 준다 — 필터로 거르면 지금까지 긋던 간선이 사라진다).
func TestImplementsNameFilterKeepsEveryMatch(t *testing.T) {
	dep := checkedPackage(t, "example.com/m/dep", implementsCasesDep)
	mainPkg, typeErrs := checkSource(t, "example.com/m", implementsCasesMain, dep)
	if len(typeErrs) != 2 {
		t.Fatalf("the fixture's only type errors must be Bad's and PBad's undefined type: %v", typeErrs)
	}
	pkgs := []*packages.Package{dep, mainPkg}
	h := structuralHarvester(pkgs...)
	h.addStructuralEdges(pkgs)
	wantEdges, wantImpls := referenceImplements(h, pkgs)
	if got := implementsPairs(h.doc); !maps.Equal(got, wantEdges) {
		t.Fatalf("implements edges differ from checking every pair:\ngot  %v\nwant %v", got, wantEdges)
	}
	gotImpls := map[[2]string]bool{}
	for iface, list := range h.impls {
		for _, impl := range list {
			gotImpls[[2]string{iface, impl}] = true
		}
	}
	if !maps.Equal(gotImpls, wantImpls) {
		t.Fatalf("impls differ from checking every pair:\ngot  %v\nwant %v", gotImpls, wantImpls)
	}
	const m, d = "example.com/m.", "example.com/m/dep."
	for _, c := range []struct {
		from, to string
		want     bool
	}{
		{m + "T", m + "I", true}, {m + "T", m + "J", true}, {d + "Base", d + "DI", true},
		{m + "Promo", d + "DI", true}, {m + "PromoPtr", m + "E", true}, {m + "Own", m + "K", true},
		{m + "Box", m + "Gen", true}, {m + "IntGet", m + "Gen", true}, {m + "Bad", m + "I", true},
		{m + "PBad", m + "J", true}, {m + "Promo", m + "K", false}, {m + "Own", d + "DI", false},
		{m + "Shape", m + "Pair", false}, {m + "NamedPtr", m + "I", false}, {m + "Bad", m + "Gen", false},
	} {
		if wantEdges[[2]string{c.from, c.to}] != c.want {
			t.Errorf("%s implements %s = %v, want %v", c.from, c.to, !c.want, c.want)
		}
	}
}

// TestImplsListEachImplementationOnce는 impls가 인터페이스 메서드마다 구현 메서드를 한 번만 담는지
// 확인한다. 같은 구현이 여러 경로로 다시 잡힌다 — J를 임베드한 I와 J를 함께 만족하는 T, 같은 Base를
// 임베드한 여러 타입(승격 메서드). 중복은 호출 지점마다 같은 팬아웃을 되풀이하고, 모듈 밖 구현이면
// 외부 참조 수(limitation)를 부풀린다.
func TestImplsListEachImplementationOnce(t *testing.T) {
	p := checkedPackage(t, "example.com/x", `package x

type J interface{ M() }
type I interface{ J }

type T struct{}

func (T) M() {}

type Lener interface{ Len() int }

type Base struct{}

func (Base) Len() int { return 0 }

type A struct{ Base }
type B struct{ *Base }
`)
	h := structuralHarvester(p)
	h.addStructuralEdges([]*packages.Package{p})
	for iface, want := range map[string][]string{
		"example.com/x.(J).M":       {"example.com/x.(T).M"},
		"example.com/x.(Lener).Len": {"example.com/x.(Base).Len"},
	} {
		if got := h.impls[iface]; !slices.Equal(got, want) {
			t.Errorf("impls[%s] = %v, want %v", iface, got, want)
		}
	}
}

// TestSharedPromotedImplementationCountedOnce는 여러 모듈 타입이 같은 모듈 밖 구현(bytes.Buffer의
// Len)을 승격해 인터페이스를 만족해도, 인터페이스 호출 하나가 그 외부 참조를 한 번만 세는지 확인한다.
// 호출식은 call(callEdge)과 references(selectorEdge) 두 팬아웃을 내므로 호출 하나가 2를 더해야 한다 —
// 구현 목록이 타입 수만큼 중복되면 6을 더해 limitation이 실제 참조 수를 부풀렸다.
func TestSharedPromotedImplementationCountedOnce(t *testing.T) {
	extRefs := func(body string) int {
		doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{"main.go": `package main

import "bytes"

type Sizer interface{ Len() int }

type A struct{ bytes.Buffer }
type B struct{ bytes.Buffer }
type C struct{ bytes.Buffer }

func size(s Sizer) int { ` + body + ` }

func main() {
	_ = size(&A{})
	_ = size(&B{})
	_ = size(&C{})
}
`}))
		for _, l := range doc.Limitations {
			var n int
			if _, err := fmt.Sscanf(l, "%d references to symbols outside the module were omitted", &n); err == nil {
				return n
			}
		}
		t.Fatalf("expected an external-references limitation: %v", doc.Limitations)
		return 0
	}
	if added := extRefs("return s.Len()") - extRefs("_ = s; return 0"); added != 2 {
		t.Fatalf("one interface call must count its external implementation once per fan-out: +%d, want +2", added)
	}
}

// BenchmarkImplementsEdges는 희소 합성 패키지(타입 2,000·인터페이스 200, 인터페이스마다 고유 메서드
// 어휘라 만족이 드묾)의 구조 간선 단계를 잰다. 모든 쌍을 판정하던 구현은 쌍 수(40만)에 비례했다.
func BenchmarkImplementsEdges(b *testing.B) {
	var src strings.Builder
	src.WriteString("package x\n")
	for g := range 200 {
		fmt.Fprintf(&src, "\ntype I%[1]d interface {\n\tG%[1]dM0()\n\tG%[1]dM1()\n}\n", g)
		for k := range 10 {
			fmt.Fprintf(&src, "\ntype T%d_%d struct{}\n", g, k)
			for j := range 3 {
				fmt.Fprintf(&src, "\nfunc (T%d_%d) G%dM%d() {}\n", g, k, g, (k+j)%8)
			}
		}
	}
	p := checkedPackage(b, "example.com/x", src.String())
	for b.Loop() {
		h := structuralHarvester(p)
		h.addStructuralEdges([]*packages.Package{p})
		if len(h.doc.Edges) == 0 {
			b.Fatal("the synthetic package must have implements edges")
		}
	}
}

// unionChain은 유니언 사슬 U0 = interface{ int | string }, Ui = interface{ U(i-1) | U(i-1) }을 go/types
// API로 직접 만들고 마지막 인터페이스를 돌려준다. 타입 검사기는 이 사슬의 검증에서 깊이마다 두 배로
// 느려져(깊이 22에서 약 2초) 소스 fixture로는 수확기 비용만 잴 수 없다. 인터페이스 항을 만날 때마다
// 다시 펼치면 항이 깊이마다 두 배가 된다.
func unionChain(depth int) *types.Interface {
	pkg := types.NewPackage("example.com/chain", "chain")
	union := func(a, b types.Type) types.Type {
		return types.NewUnion([]*types.Term{types.NewTerm(false, a), types.NewTerm(false, b)})
	}
	iface := types.NewInterfaceType(nil, []types.Type{union(types.Typ[types.Int], types.Typ[types.String])})
	for i := 1; i <= depth; i++ {
		prev := types.NewNamed(types.NewTypeName(token.NoPos, pkg, fmt.Sprintf("U%d", i-1), nil), iface, nil)
		iface = types.NewInterfaceType(nil, []types.Type{union(prev, prev)})
	}
	return iface
}

// TestUnionTypeSetChainIsLinear는 유니언의 인터페이스 항을 한 번만 펼치는지 확인한다 — 출력은 정렬·
// 중복 제거된 항 집합이라 그대로이고(깊이 24도 "int | string"), 비용은 깊이에 선형이다(n→2n에서
// 할당이 두 배 남짓 — 다시 펼치면 2^n배). 실제 수확(fillTypeShape)에서도 사슬과 마름모(같은 제약에 두
// 길로 닿음)의 typeSet이 그대로인지 본다.
func TestUnionTypeSetChainIsLinear(t *testing.T) {
	allocs := func(depth int) float64 {
		iface := unionChain(depth)
		return testing.AllocsPerRun(3, func() { newTypeSpeller().interfaceTypeSet(iface) })
	}
	if small, large := allocs(10), allocs(20); large > 3*small {
		t.Fatalf("union expansion must be linear in the chain depth: depth 10 %.0f allocs, depth 20 %.0f", small, large)
	}
	if got, n := newTypeSpeller().interfaceTypeSet(unionChain(24)); !slices.Equal(got, []string{"int | string"}) || n != 0 {
		t.Fatalf("depth-24 union chain typeSet %q (%d abbreviated), want [\"int | string\"]", got, n)
	}
	var src strings.Builder
	src.WriteString("package a\n\ntype U0 interface{ int | string }\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&src, "type U%d interface{ U%d | U%d }\n", i, i-1, i-1)
	}
	src.WriteString("\ntype z interface{ ~float64 }\ntype x interface{ int | z }\ntype y interface{ string | z }\n" +
		"type Diamond interface{ x | y }\n")
	doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{"a/a.go": src.String()}))
	for id, want := range map[string][]string{
		"example.com/fixture/a.U12":     {"int | string"},
		"example.com/fixture/a.Diamond": {"int | string | ~float64"},
	} {
		if v, _ := doc.VertexByID(id); v == nil || !slices.Equal(v.TypeSet, want) {
			t.Fatalf("%s: vertex %+v, want typeSet %q", id, v, want)
		}
	}
}
