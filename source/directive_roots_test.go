package source

import (
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestWasmExportRoots는 //go:wasmexport 함수가 보존 루트인지 본다. wasm 호스트가 부르는 진입점이라
// 그래프에 호출자가 없다 — cgo //export와 같은 종류다. 내보내는 이름은 함수 이름과 달라도 된다.
// 인자 없는 지시자·비슷한 접두사·wasmimport(호스트 함수 가져오기)는 루트가 아니다.
func TestWasmExportRoots(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
		"gen/gen.go": `package gen

//go:wasmexport add
func add(x, y int32) int32 { return helper(x) + y }

func helper(x int32) int32 { return x }

// 내보내는 이름이 함수 이름과 다르다.
//
//go:wasmexport renamed_export
func renamed() {}

//go:wasmexport
func missingName() {}

//go:wasmexportx other
func lookalike() {}

//go:wasmimport env host
func host() int32
`,
	})
	doc := loadSymbol(t, dir)
	const m = "example.com/fixture/gen"
	for _, id := range []string{m + ".add", m + ".renamed"} {
		if !slices.Contains(doc.Roots, id) {
			t.Errorf("wasm export %s must be a retention root: %v", id, doc.Roots)
		}
	}
	for _, id := range []string{m + ".missingName", m + ".lookalike", m + ".host", m + ".helper"} {
		if slices.Contains(doc.Roots, id) {
			t.Errorf("%s must not become a root", id)
		}
	}
}

// TestLinknameTiesModuleSymbols는 //go:linkname 양쪽이 모두 모듈 심볼이면 서로를 후보 간선으로
// 잇고 본문 있는 쪽(당겨 온 구현 hidden)을 루트로 두는지 본다 — RTA는 본문 없는 선언(local)에서
// SSA가 끝나 간선만으로는 구현에 닿지 못한다. 본문 없는 쪽은 루트가 아니다. 본문이 있는 함수를
// 모듈 밖으로 밀어 넣거나(push) 인자 하나로 내보내는 지시문도 밖에서 불릴 수 있어 루트다.
// 해석한 지시문은 limitation으로 세지 않고, 메서드 표기처럼 해석하지 못한 지시문만 센다.
func TestLinknameTiesModuleSymbols(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/b"

func main() { b.Local() }
`,
		"a/a.go": `package a

import _ "unsafe"

func Used() {}

func hidden() int { return 42 }

//go:linkname pushed
func pushed() {}

//go:linkname provided example.com/elsewhere.name
func provided() {}

type T struct{}

func (T) m() {}

//go:linkname viaMethod example.com/fixture/a.T.m
func viaMethod()
`,
		"b/b.go": `package b

import (
	_ "unsafe"

	_ "example.com/fixture/a"
)

//go:linkname local example.com/fixture/a.hidden
func local() int

func Local() { _ = local() }
`,
	})
	doc := loadSymbol(t, dir)
	const a, b = "example.com/fixture/a", "example.com/fixture/b"
	// 실제 의존은 본문 없는 선언(b.local) → 본문(a.hidden) 한 방향이다. 역방향 간선은 rules·query·explain에
	// 없는 의존을 만든다(검토에서 forbidden low→high가 거짓 위반을 냈다).
	if e, ok := findEdge(doc, b+".local", a+".hidden", graph.EdgeReferences); !ok || !e.Candidate {
		t.Errorf("linkname must tie %s -> %s with a candidate references edge (found=%v)", b+".local", a+".hidden", ok)
	}
	if _, ok := findEdge(doc, a+".hidden", b+".local", graph.EdgeReferences); ok {
		t.Errorf("the body %s does not depend on the bodyless declaration %s; no reverse edge may exist", a+".hidden", b+".local")
	}
	for _, id := range []string{a + ".pushed", a + ".provided", a + ".hidden"} {
		if !slices.Contains(doc.Roots, id) {
			t.Errorf("%s is callable from outside through //go:linkname and must be a root: %v", id, doc.Roots)
		}
	}
	for _, id := range []string{b + ".local", a + ".viaMethod"} {
		if slices.Contains(doc.Roots, id) {
			t.Errorf("bodyless declaration %s must not become a root", id)
		}
	}
	var notes []string
	for _, l := range doc.Limitations {
		if strings.Contains(l, "//go:linkname") {
			notes = append(notes, l)
		}
	}
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "1 //go:linkname directives") {
		t.Errorf("only the unresolved method-form directive must be counted: %v", notes)
	}
}

// TestBuildConstraintHintNamesPlatformFlags는 빌드 제약으로 빠진 파일의 limitation이 --tags만이 아니라
// --goos/--goarch도 안내하는지 본다 — _windows.go 같은 플랫폼 파일은 태그가 아니라 대상 플랫폼으로만
// 들어온다. 검토에서 GOOS 파일 때문에 unreachable이 된 함수에 --tags만 안내됐다.
func TestBuildConstraintHintNamesPlatformFlags(t *testing.T) {
	other := "windows"
	if runtime.GOOS == "windows" {
		other = "linux"
	}
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":               "package main\n\nfunc main() {}\n",
		"main_" + other + ".go": "package main\n\nfunc init() { onlyThere() }\n",
		"helper.go":             "package main\n\nfunc onlyThere() {}\n",
		"tagged.go":             "//go:build custom\n\npackage main\n",
	})
	doc := loadSymbol(t, dir)
	for _, l := range doc.Limitations {
		if strings.Contains(l, "excluded by build constraints") {
			if !strings.Contains(l, "--tags") || !strings.Contains(l, "--goos") {
				t.Fatalf("the hint must name both --tags and --goos/--goarch: %q", l)
			}
			return
		}
	}
	t.Fatalf("excluded files must be counted: %v", doc.Limitations)
}

// TestLinknameBodyInSiblingFile은 //go:linkname의 local 이름을 지시문이 없는 같은 패키지의 다른 파일에서
// 찾는지 본다. gc는 local 이름을 패키지 스코프에서 찾으므로 선언이 다른 파일에 있어도 유효하다. 검토에서
// 지시문 파일만 보느라 (a) 모듈 밖으로 밀어 넣은 구현이 루트가 되지 못하고 limitation도 사라졌고,
// (b) 모듈 안 본문 없는 선언으로 밀어 넣은 구현 대신 본문 없는 선언이 루트가 됐다.
func TestLinknameBodyInSiblingFile(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/b"

func main() { b.Local() }
`,
		"a/link.go": `package a

import _ "unsafe"

//go:linkname impl example.com/elsewhere.hook
`,
		"a/impl.go": `package a

func impl() int { return 1 }
`,
		"a/push.go": `package a

import _ "unsafe"

//go:linkname pushedImpl example.com/fixture/b.hook
`,
		"a/pushimpl.go": `package a

func pushedImpl() int { return 2 }
`,
		"b/b.go": `package b

import (
	_ "unsafe"

	_ "example.com/fixture/a"
)

func hook() int

func Local() { _ = hook() }
`,
		"b/empty.s": "",
	})
	doc := loadSymbol(t, dir)
	const a, b = "example.com/fixture/a", "example.com/fixture/b"
	for _, id := range []string{a + ".impl", a + ".pushedImpl"} {
		if !slices.Contains(doc.Roots, id) {
			t.Errorf("%s is the body of a //go:linkname push and must be a root: %v", id, doc.Roots)
		}
	}
	if slices.Contains(doc.Roots, b+".hook") {
		t.Errorf("the bodyless declaration %s must not become a root", b+".hook")
	}
	if e, ok := findEdge(doc, b+".hook", a+".pushedImpl", graph.EdgeReferences); !ok || !e.Candidate {
		t.Errorf("linkname must tie %s -> %s (found=%v)", b+".hook", a+".pushedImpl", ok)
	}
	for _, l := range doc.Limitations {
		if strings.Contains(l, "//go:linkname") {
			t.Errorf("both directives resolve to module bodies; no linkname limitation may remain: %q", l)
		}
	}
}

// TestLinknameLimitationAtTypeLevel은 심볼이 아닌 레벨의 linkname limitation이 원인을 사실대로 적는지
// 본다. type 레벨에는 함수·변수 정점이 없어 지시문을 이을 수 없는데, 검토에서 해석 가능한 지시문까지
// "could not be tied … (method targets, unknown names)"로 세어 원인을 잘못 설명했다.
func TestLinknameLimitationAtTypeLevel(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/high"

func main() { high.Run() }
`,
		"high/high.go": `package high

import (
	_ "unsafe"

	_ "example.com/fixture/low"
)

//go:linkname pulled example.com/fixture/low.hidden
func pulled() int

func Run() { _ = pulled() }
`,
		"high/empty.s": "",
		"low/low.go": `package low

func hidden() int { return 42 }
`,
	})
	doc, err := Load(Options{Dir: dir, Level: graph.LevelType})
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	for _, l := range doc.Limitations {
		if strings.Contains(l, "//go:linkname") {
			notes = append(notes, l)
		}
	}
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "1 //go:linkname directives") ||
		!strings.Contains(notes[0], "not vertices at type level") || strings.Contains(notes[0], "could not be tied") {
		t.Errorf("type-level linkname limitation must say functions are not vertices at this level: %v", notes)
	}
}
