package analysis

import (
	"errors"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// doc은 테스트용 그래프다: a→b→c→a 순환, d→a 비순환, e 단독.
func doc() *graph.Document {
	return &graph.Document{
		Vertices: []graph.Vertex{
			{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"},
		},
		Edges: []graph.Edge{
			{From: "a", To: "b", Kind: graph.EdgeImport},
			{From: "b", To: "c", Kind: graph.EdgeImport},
			{From: "c", To: "a", Kind: graph.EdgeImport},
			{From: "d", To: "a", Kind: graph.EdgeImport},
			{From: "e", To: "e", Kind: graph.EdgeImport},
		},
	}
}

// TestCycles는 SCC 순환과 자기루프를 찾고 비순환 정점을 제외하는지 확인한다.
// 자기루프 없는 단일 정점을 순환으로 보고하는 것은 오탐이라 회귀를 막는다.
func TestCycles(t *testing.T) {
	cycles := Cycles(doc())
	if len(cycles) != 2 {
		t.Fatalf("expected 2 cycles, got %d: %+v", len(cycles), cycles)
	}
	found := map[string]bool{}
	for _, c := range cycles {
		for _, m := range c.Members {
			found[m] = true
		}
	}
	for _, want := range []string{"a", "b", "c", "e"} {
		if !found[want] {
			t.Fatalf("expected %s in a cycle: %+v", want, cycles)
		}
	}
	if found["d"] {
		t.Fatalf("d is not in a cycle: %+v", cycles)
	}
	// evidence 간선이 실제로 실렸는지 확인한다.
	if len(cycles[0].Edges) == 0 || len(cycles[1].Edges) == 0 {
		t.Fatalf("cycle without evidence edges: %+v", cycles)
	}
}

// TestQueryNotFound는 없는 정점 질의가 ErrNotFound로 구분되는지 확인한다.
func TestQueryNotFound(t *testing.T) {
	_, err := Query(doc(), "missing", 1, 0)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestQueryDepth는 depth별 이웃 범위를 확인한다.
func TestQueryDepth(t *testing.T) {
	res, err := Query(doc(), "a", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DependsOn) != 1 || res.DependsOn[0].ID != "b" {
		t.Fatalf("depth1 dependsOn: %+v", res.DependsOn)
	}
	if len(res.DependedBy) != 2 { // c, d
		t.Fatalf("depth1 dependedBy: %+v", res.DependedBy)
	}
	res2, err := Query(doc(), "d", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	// d→a→b: depth2면 b까지 도달한다.
	ids := map[string]bool{}
	for _, n := range res2.DependsOn {
		ids[n.ID] = true
	}
	if !ids["a"] || !ids["b"] {
		t.Fatalf("depth2 dependsOn missing: %+v", res2.DependsOn)
	}
}

// TestQueryTruncated는 잘림이 조용히 버려지지 않고 표시되는지 확인한다.
func TestQueryTruncated(t *testing.T) {
	res, err := Query(doc(), "a", 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated result")
	}
}

// TestNeighborKinds는 이웃 간선 종류가 모두 실리는지 확인한다.
// 관계를 하나만 고르면 나머지 사실이 소비자에게 사라진다.
func TestNeighborKinds(t *testing.T) {
	d := &graph.Document{
		Vertices: []graph.Vertex{{ID: "a"}, {ID: "b"}},
		Edges: []graph.Edge{
			{From: "a", To: "b", Kind: graph.EdgeImport},
			{From: "a", To: "b", Kind: graph.EdgeCall},
		},
	}
	res, err := Query(d, "a", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DependsOn[0].Kinds) != 2 {
		t.Fatalf("expected both kinds reported, got %+v", res.DependsOn[0].Kinds)
	}
}
