package source

import (
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// fixture는 a→b import와 b의 stdlib import를 가진 모듈이다.
// stdlib은 모듈이 없어 생략되고 limitation으로 남아야 한다.
func fixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

import _ "example.com/fixture/b"
`,
		"b/b.go": `package b

import "fmt"

var _ = fmt.Sprint
`,
	})
}

// TestLoadPackageGraph는 패키지 그래프 수확의 기본 계약을 확인한다:
// 모듈 내부 정점, import 간선, 외부 생략 limitation.
func TestLoadPackageGraph(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Level != graph.LevelPackage {
		t.Fatalf("expected package level, got %s", doc.Level)
	}
	if !doc.HasVertex("example.com/fixture/a") || !doc.HasVertex("example.com/fixture/b") {
		t.Fatalf("missing package vertices: %+v", doc.Vertices)
	}
	kinds := graph.EdgeKinds(doc, "example.com/fixture/a", "example.com/fixture/b")
	if len(kinds) != 1 || kinds[0] != graph.EdgeImport {
		t.Fatalf("missing import edge a→b: %+v", doc.Edges)
	}
	// fmt는 모듈 밖이라 정점이 되지 않고 limitation으로 남는다.
	if doc.HasVertex("fmt") {
		t.Fatal("stdlib package leaked as vertex")
	}
	if len(doc.Limitations) == 0 ||
		!strings.Contains(doc.Limitations[0], "outside the module") {
		t.Fatalf("expected external-import limitation: %v", doc.Limitations)
	}
}

// TestImportEdgePositions는 import 간선이 선언 위치를 싣는지 확인한다.
// v2 스키마에서 "의존이 어디서 일어나는가"는 간선이 직접 담는 사실이다.
func TestImportEdgePositions(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	var e *graph.Edge
	for i := range doc.Edges {
		if doc.Edges[i].Kind == graph.EdgeImport &&
			doc.Edges[i].From == "example.com/fixture/a" {
			e = &doc.Edges[i]
		}
	}
	if e == nil || len(e.Positions) != 1 {
		t.Fatalf("import edge must carry its decl site: %+v", doc.Edges)
	}
	pos := e.Positions[0]
	if !strings.HasSuffix(pos.File, "a/a.go") || pos.Line != 3 {
		t.Fatalf("position must point at the import spec in a/a.go: %+v", pos)
	}
}

// TestTestVariantMerge는 --tests 수확에서 _test.go의 import가 원 패키지의
// 간선으로 합쳐지는지 확인한다 — 변형을 통째로 버리면 테스트만의 내부
// 의존이 그래프에서 사라진다.
func TestTestVariantMerge(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"lib/lib.go": `package lib

func Add() {}
`,
		"lib/lib_test.go": `package lib

import (
	"example.com/fixture/util"
	"testing"
)

func TestAdd(t *testing.T) { util.Shout(); Add() }
`,
		"util/util.go": `package util

func Shout() {}
`,
	})
	doc, err := LoadPackageGraph(Options{Dir: dir, Tests: true})
	if err != nil {
		t.Fatal(err)
	}
	// 변형 정점은 생기지 않는다 — 정점은 PkgPath 하나다.
	if doc.HasVertex("example.com/fixture/lib [example.com/fixture/lib.test]") {
		t.Fatal("test variant leaked as a vertex")
	}
	kinds := graph.EdgeKinds(doc, "example.com/fixture/lib", "example.com/fixture/util")
	if len(kinds) != 1 || kinds[0] != graph.EdgeImport {
		t.Fatalf("_test.go import must merge into the base package: %+v", doc.Edges)
	}
	// 사용 지점은 _test.go 파일을 가리켜야 한다 — 어느 파일이 이
	// 의존을 만드는지가 파일 스코프 판단의 재료다.
	var merged bool
	for _, e := range doc.Edges {
		if e.From == "example.com/fixture/lib" && e.To == "example.com/fixture/util" {
			for _, p := range e.Positions {
				if strings.HasSuffix(p.File, "lib_test.go") {
					merged = true
				}
			}
		}
	}
	if !merged {
		t.Fatal("merged edge must carry the _test.go site")
	}
	var noted bool
	for _, l := range doc.Limitations {
		if strings.Contains(l, "test-variant") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("variant merge must be noted: %v", doc.Limitations)
	}
}

// TestLoadPackageGraphDeps는 --deps가 외부 패키지를 정점으로 담는지 확인한다.
func TestLoadPackageGraphDeps(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t), IncludeDeps: true})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.HasVertex("fmt") {
		t.Fatal("expected stdlib vertex with --deps")
	}
	kinds := graph.EdgeKinds(doc, "example.com/fixture/b", "fmt")
	if len(kinds) != 1 {
		t.Fatalf("missing edge b→fmt: %+v", doc.Edges)
	}
}

// TestPlatformSelection은 GOOS=linux 수확이 linux 전용 파일을 싣고
// 다른 타깃 파일을 싣지 않는지 확인한다 — 조건부 파일이 다른 그래프를
// 만드는 게 이 옵션의 존재 이유다.
func TestPlatformSelection(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"lib/lib_linux.go": `package lib

func LinuxOnly() {}
`,
		"lib/lib_windows.go": `package lib

func WindowsOnly() {}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol", GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.HasVertex("example.com/fixture/lib.LinuxOnly") {
		t.Fatalf("linux file must be harvested: %+v", doc.Vertices)
	}
	if doc.HasVertex("example.com/fixture/lib.WindowsOnly") {
		t.Fatal("windows file must not be harvested under GOOS=linux")
	}
	var noted bool
	for _, l := range doc.Limitations {
		if strings.Contains(l, "GOOS=linux") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("platform selection must be a limitation: %v", doc.Limitations)
	}
}

// TestPlatformDefault는 플랫폼 옵션이 없으면 limitation을 싣지 않는지
// 확인한다 — 기본 호스트 수확에 노이즈를 얹으면 안 된다.
func TestPlatformDefault(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range doc.Limitations {
		if strings.Contains(l, "GOOS") {
			t.Fatalf("default harvest must not carry a platform note: %v", l)
		}
	}
}

// TestExclude는 exclude 패턴에 맞는 패키지가 정점도 도달 집합도 아니며,
// 그쪽으로의 import가 limitation으로 남는지 확인한다 —
// "제외됐다"와 "모듈 밖이다"는 다른 사실이다.
func TestExclude(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

import _ "example.com/fixture/gen"
`,
		"gen/gen.go": `package gen
`,
	})
	doc, err := LoadPackageGraph(Options{Dir: dir, Exclude: []string{"gen/**", "gen"}})
	if err != nil {
		t.Fatal(err)
	}
	if doc.HasVertex("example.com/fixture/gen") {
		t.Fatal("excluded package leaked as a vertex")
	}
	for _, e := range doc.Edges {
		if e.To == "example.com/fixture/gen" {
			t.Fatalf("import to excluded package must not be an edge: %+v", e)
		}
	}
	var matched, imports bool
	for _, l := range doc.Limitations {
		if strings.Contains(l, "matched exclude patterns") {
			matched = true
		}
		if strings.Contains(l, "imports to excluded packages") {
			imports = true
		}
	}
	if !matched || !imports {
		t.Fatalf("exclusion must be reported as limitations: %v", doc.Limitations)
	}
}

// TestConstValueHarvest는 const 정점이 값을 싣는지 확인한다 —
// diff의 breaking 판정 재료다.
func TestConstValueHarvest(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"lib/lib.go": `package lib

const Version = "1.0"
const count = 42
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "type"})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range doc.Vertices {
		if v.ID == "example.com/fixture/lib.Version" && v.Value != `"1.0"` {
			t.Fatalf("const value must be harvested: %+v", v)
		}
	}
}

// TestFieldVertices는 struct 필드가 심볼 레벨 정점과 참조 간선을 갖는지 확인한다.
// 선택(x.F)·키 리터럴(T{F: v})·위치 리터럴(T{v}) 세 경로가 모두
// 필드 도달성의 사실이어야 한다 — 하나라도 빠지면 dead가 쓰이는 필드를
// unreachable로 오보한다.
func TestFieldVertices(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Use() }
`,
		"lib/lib.go": `package lib

type Point struct {
	X     int
	Y     int
	label string
}

func Use() int {
	p := Point{1, 2}
	q := Point{X: 3}
	return p.X + q.Y
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"example.com/fixture/lib.(Point).X",
		"example.com/fixture/lib.(Point).Y",
		"example.com/fixture/lib.(Point).label",
	} {
		v, ok := doc.VertexByID(id)
		if !ok || v.Kind != graph.KindField {
			t.Fatalf("missing field vertex %s", id)
		}
	}
	// 위치 리터럴 p := Point{1,2}는 모든 필드를 쓰고, 선택 p.X는 X를 쓴다.
	refs := map[string]bool{}
	for _, e := range doc.Edges {
		if e.Kind == graph.EdgeReferences && e.From == "example.com/fixture/lib.Use" {
			refs[e.To] = true
		}
	}
	for _, f := range []string{"X", "Y", "label"} {
		id := "example.com/fixture/lib.(Point)." + f
		if !refs[id] {
			t.Fatalf("field %s must be referenced by Use (positional literal): %v", id, refs)
		}
	}
	// 타입 레벨 문서에는 필드 정점이 없다 — 멤버 단위는 심볼 레벨의 분해다.
	tdoc, err := Load(Options{Dir: dir, Level: "type"})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range tdoc.Vertices {
		if v.Kind == graph.KindField {
			t.Fatalf("type-level doc must not carry field vertices: %+v", v)
		}
	}
}

// TestFieldSelectorRef는 셀렉터 접근만 있는 필드가 참조되는지 확인한다 —
// 리터럴을 쓰지 않는 필드(외부에서 만들어진 값을 읽기만 하는 경우)도
// 사실로 남아야 한다.
func TestFieldSelectorRef(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Serve() }
`,
		"lib/lib.go": `package lib

type Cfg struct {
	Addr string
	dead int
}

func Serve() int {
	var c Cfg
	_ = c.Addr
	return 0
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	var addrRef, deadRef bool
	for _, e := range doc.Edges {
		if e.Kind != graph.EdgeReferences {
			continue
		}
		if e.To == "example.com/fixture/lib.(Cfg).Addr" {
			addrRef = true
		}
		if e.To == "example.com/fixture/lib.(Cfg).dead" {
			deadRef = true
		}
	}
	if !addrRef {
		t.Fatal("selector c.Addr must produce a references edge")
	}
	if deadRef {
		t.Fatal("unreferenced field must not gain a references edge")
	}
}

// TestKeepAnnotation은 //deadcode:keep·//gartograph:keep 표지가 붙은
// 선언이 보존 루트로 수확되는지 확인한다 — 의도적 보존이 선언 옆의
// 사실로 문서에 남아야 dead가 살린다.
func TestKeepAnnotation(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() {}
`,
		"lib/lib.go": `package lib

//deadcode:keep
func PluginEntry() {}

// gartograph:keep for the reflection registry
var Registry = map[string]int{}

//deadcode:keep
const Magic = 7

//deadcode:keep
type marker struct{}

type Meta struct {
	//deadcode:keep — written by reflection
	tag    string
	Unused int
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	roots := map[string]bool{}
	for _, r := range doc.Roots {
		roots[r] = true
	}
	for _, id := range []string{
		"example.com/fixture/lib.PluginEntry",
		"example.com/fixture/lib.Registry",
		"example.com/fixture/lib.Magic",
		"example.com/fixture/lib.marker",
		"example.com/fixture/lib.(Meta).tag",
	} {
		if !roots[id] {
			t.Fatalf("annotated symbol %s must be a retention root: %v", id, doc.Roots)
		}
	}
	if roots["example.com/fixture/lib.(Meta).Unused"] {
		t.Fatal("unannotated field must not become a root")
	}
}

// TestFieldPromotion은 승격 선택이 경유하는 임베드 필드를 참조로 긋는지
// 확인한다 — a.Name에서 Name이 임베드 Base의 필드면 Base 자체도 밟힌다.
// 경유 필드를 빼먹으면 승격으로만 쓰이는 임베드가 unreachable로 오보된다.
func TestFieldPromotion(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Use() }
`,
		"lib/lib.go": `package lib

type Base struct{ Name string }

type Wrap struct {
	Base
	extra int
}

func Use() string {
	var w Wrap
	return w.Name
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]bool{}
	for _, e := range doc.Edges {
		if e.Kind == graph.EdgeReferences && e.From == "example.com/fixture/lib.Use" {
			refs[e.To] = true
		}
	}
	for _, id := range []string{
		"example.com/fixture/lib.(Base).Name", // 도착 필드
		"example.com/fixture/lib.(Wrap).Base", // 경유 임베드 필드
	} {
		if !refs[id] {
			t.Fatalf("promoted selection must reference %s: %v", id, refs)
		}
	}
	if refs["example.com/fixture/lib.(Wrap).extra"] {
		t.Fatal("untouched field must not gain a reference")
	}
}
