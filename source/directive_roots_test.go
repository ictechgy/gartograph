package source

import (
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
	for _, pair := range [][2]string{{b + ".local", a + ".hidden"}, {a + ".hidden", b + ".local"}} {
		e, ok := findEdge(doc, pair[0], pair[1], graph.EdgeReferences)
		if !ok || !e.Candidate {
			t.Errorf("linkname must tie %s -> %s with a candidate references edge (found=%v)", pair[0], pair[1], ok)
		}
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
