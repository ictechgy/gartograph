package source

import (
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestCandidateEdges는 CHA 팬아웃 간선만 Candidate로 표시되고, 같은 관계가 확정
// 지점(구체 타입 호출)에서도 그어지면 확정으로 남는지 확인한다. 순회 문서의 근거
// 등급이 이 표시로 정해지므로, 확정 호출을 candidate로 적으면 등급이 약해지고
// 팬아웃을 확정으로 적으면 등급을 부풀린다.
func TestCandidateEdges(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

type I interface{ M() }

type A struct{}

func (A) M() {}

type B struct{}

func (B) M() {}

// 인터페이스 호출만 — A.M·B.M은 팬아웃 후보다.
func viaInterface(i I) { i.M() }

// 인터페이스 호출과 구체 호출이 함께 — A.M은 확정, B.M은 후보다.
func mixed(i I, a A) { i.M(); a.M() }

// 인터페이스 메서드 값 — 구현으로 references 팬아웃한다.
func methodValue(i I) func() { return i.M }

func main() {
	viaInterface(A{})
	mixed(B{}, A{})
	_ = methodValue(A{})
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !doc.DispatchEvidence {
		t.Fatal("symbol documents must carry the dispatchEvidence marker")
	}
	const m = "example.com/fixture"
	cases := []struct {
		from, to  string
		kind      graph.EdgeKind
		candidate bool
	}{
		{m + ".viaInterface", m + ".(I).M", graph.EdgeCall, false},
		{m + ".viaInterface", m + ".(A).M", graph.EdgeCall, true},
		{m + ".viaInterface", m + ".(B).M", graph.EdgeCall, true},
		{m + ".mixed", m + ".(A).M", graph.EdgeCall, false},
		{m + ".mixed", m + ".(B).M", graph.EdgeCall, true},
		{m + ".methodValue", m + ".(A).M", graph.EdgeReferences, true},
		{m + ".main", m + ".viaInterface", graph.EdgeCall, false},
	}
	for _, c := range cases {
		e, ok := findEdge(doc, c.from, c.to, c.kind)
		if !ok {
			t.Errorf("missing edge %s -%s-> %s", c.from, c.kind, c.to)
			continue
		}
		if e.Candidate != c.candidate {
			t.Errorf("edge %s -%s-> %s candidate = %v, want %v", c.from, c.kind, c.to, e.Candidate, c.candidate)
		}
	}
	typeDoc, err := Load(Options{Dir: dir, Level: graph.LevelType})
	if err != nil {
		t.Fatalf("Load type: %v", err)
	}
	if typeDoc.DispatchEvidence {
		t.Fatal("type documents have no call fan-out and must not claim dispatch evidence")
	}
}

// findEdge는 (from, to, kind) 간선을 찾는다.
func findEdge(doc *graph.Document, from, to string, kind graph.EdgeKind) (graph.Edge, bool) {
	for _, e := range doc.Edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return e, true
		}
	}
	return graph.Edge{}, false
}
