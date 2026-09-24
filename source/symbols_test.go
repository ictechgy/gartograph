package source

import (
	"slices"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
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
		// 충돌 심볼을 참조하지 않는 테스트 — --tests에서 x를 두 변형으로 돌게만 한다.
		"x/x_test.go": `package x

import "testing"

func TestNothing(t *testing.T) {}
`,
	})
}

// TestSymbolIDCollidingWithPackage는 패키지 ID와 겹치는 심볼이 패키지 정점에
// 간선을 덮어쓰지 않는지 확인한다. 겹친 심볼은 정점이 없고 그 사실이 limitation으로
// 세어진다 — 함수가 패키지를 호출하는 간선은 거짓 사실이다.
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
	if !hasEdge(doc, pkg, pkg+".Other", graph.EdgeContains) {
		t.Fatal("the dotted package must still contain its own symbols")
	}
	// 리시버 타입이 패키지 ID와 겹치면 외부 디스패치 Receiver가 패키지를 가리키게
	// 된다 — "패키지가 도달하면 메서드도 도달"은 거짓 규칙이다.
	// 대신 메서드는 satisfies를 유지한 채 보존 루트가 된다 — 리시버 규칙을 못 거는
	// 메서드를 죽었다고 하면 과소 근사다.
	zerr, _ := doc.VertexByID("example.com/m/x.(Z).Error")
	if zerr.Receiver == "example.com/m/x.Z" {
		t.Fatalf("receiver must not point at a colliding package vertex: %+v", zerr)
	}
	if !slices.Contains(zerr.Satisfies, "error") || !slices.Contains(doc.Roots, zerr.ID) {
		t.Fatalf("method of a colliding type must keep satisfies and become a root: %+v roots=%v",
			zerr, doc.Roots)
	}
	if !containsLimitation(doc, "share their vertex ID with a package") {
		t.Fatalf("colliding symbols must be counted, got %v", doc.Limitations)
	}
}

// TestCollisionLimitationCountsDistinctEdges는 충돌 limitation이 호출 횟수가 아니라
// 서로 다른 간선 수를 세는지 확인한다 — --tests는 같은 패키지를 두 변형으로
// 돌지만 간선은 같으므로 문장도 같아야 한다.
func TestCollisionLimitationCountsDistinctEdges(t *testing.T) {
	dir := idCollisionFixture(t)
	plain := loadSymbol(t, dir)
	tests, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Tests: true})
	if err != nil {
		t.Fatal(err)
	}
	pick := func(d *graph.Document) string {
		for _, l := range d.Limitations {
			if strings.Contains(l, "share their vertex ID") {
				return l
			}
		}
		return ""
	}
	if pick(plain) == "" || pick(plain) != pick(tests) {
		t.Fatalf("collision count must not depend on harvest passes:\n%q\n%q", pick(plain), pick(tests))
	}
}

// TestCollisionLimitationAtTypeLevel은 정점을 시도하지 않는 type 레벨에서도
// 충돌로 버린 간선을 limitation으로 세는지 확인한다.
func TestCollisionLimitationAtTypeLevel(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.27\n",
		"main.go": `package main

import (
	"example.com/m/x"
	_ "example.com/m/x.y"
)

func main() { _ = x.T{} }
`,
		"x/x.go": `package x

const y = 2

type T [y]int
`,
		"x.y/xy.go": "package xy\n",
	})
	doc, err := Load(Options{Dir: dir, Level: graph.LevelType})
	if err != nil {
		t.Fatal(err)
	}
	if !containsLimitation(doc, "package path containing a dot") {
		t.Fatalf("edges dropped by an ID collision must be counted, got %v", doc.Limitations)
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

// containsLimitation은 문서 limitation 중 부분 문자열을 담은 것이 있는지 본다.
func containsLimitation(doc *graph.Document, part string) bool {
	return slices.ContainsFunc(doc.Limitations, func(l string) bool { return strings.Contains(l, part) })
}

// hasEdgeTo는 종류와 상관없이 from→to 간선이 있는지 본다.
func hasEdgeTo(doc *graph.Document, from, to string) bool {
	return slices.ContainsFunc(doc.Edges, func(e graph.Edge) bool { return e.From == from && e.To == to })
}
