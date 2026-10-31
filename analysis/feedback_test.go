package analysis

import (
	"reflect"
	"slices"
	"testing"

	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/graph"
)

// 후보 디스패치는 도달성을 넓혀도 확정 순환의 증거가 될 수 없다.
func TestFeedbackCandidateCallsDoNotProveCycles(t *testing.T) {
	d := &graph.Document{
		Vertices: []graph.Vertex{{ID: "a"}, {ID: "b"}},
		Edges: []graph.Edge{
			{From: "a", To: "b", Kind: graph.EdgeCall, Candidate: true},
			{From: "b", To: "a", Kind: graph.EdgeCall},
		},
	}
	if cycles := Cycles(d); len(cycles) != 0 {
		t.Fatalf("candidate dispatch proved a cycle: %+v", cycles)
	}
	if !reflect.DeepEqual(graph.Adjacency(d)["a"], []string{"b"}) {
		t.Fatal("candidate reachability must remain available")
	}
	d.Edges = append(d.Edges, graph.Edge{From: "a", To: "b", Kind: graph.EdgeCall})
	d.Sort()
	if cycles := Cycles(d); len(cycles) != 1 || len(cycles[0].Edges) != 2 {
		t.Fatalf("confirmed calls must still prove the cycle: %+v", cycles)
	}
}

// 같은 from/to를 가진 여러 위치·규칙의 출력은 입력 순서에 좌우되지 않는다.
func TestFeedbackRuleTieOrderIsDeterministic(t *testing.T) {
	positions := make([]graph.Position, 16)
	for i := range positions {
		positions[i] = graph.Position{File: "/m/web/web.go", Line: i + 1, Column: 2}
	}
	d := &graph.Document{
		Module:   "example.com/m",
		Vertices: []graph.Vertex{{ID: "example.com/m/web", Kind: graph.KindPackage}, {ID: "example.com/m/db", Kind: graph.KindPackage}},
		Edges:    []graph.Edge{{From: "example.com/m/web", To: "example.com/m/db", Kind: graph.EdgeImport, Positions: positions}},
	}
	cfg := &config.File{
		Components: map[string][]string{"web": {"web"}, "db": {"db"}},
		Deps:       map[string][]string{"web": {"db"}},
		FileRules:  []config.FileRule{{Name: "z", From: "*", To: "db"}, {Name: "a", From: "*", To: "db"}},
	}
	first := CheckRules(d, cfg)
	slices.Reverse(d.Edges[0].Positions)
	slices.Reverse(cfg.FileRules)
	second := CheckRules(d, cfg)
	if len(first.Violations) < 16 || !reflect.DeepEqual(first, second) {
		t.Fatalf("rule order depends on input: first=%+v, second=%+v", first.Violations, second.Violations)
	}
}
