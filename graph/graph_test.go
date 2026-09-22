package graph

import (
	"testing"
)

// TestSort는 정렬이 결정적이고 중복 간선을 제거하는지 확인한다.
// 출력 결정성은 리포트 diff와 캐시의 기반이라 회귀를 막아야 한다.
func TestSort(t *testing.T) {
	doc := &Document{
		Vertices: []Vertex{{ID: "b"}, {ID: "a"}},
		Edges: []Edge{
			{From: "b", To: "a", Kind: EdgeImport},
			{From: "a", To: "b", Kind: EdgeImport},
			{From: "a", To: "b", Kind: EdgeImport},
		},
	}
	doc.Sort()
	if doc.Vertices[0].ID != "a" {
		t.Fatalf("vertices not sorted: %+v", doc.Vertices)
	}
	if len(doc.Edges) != 2 {
		t.Fatalf("expected 2 deduped edges, got %d", len(doc.Edges))
	}
}

// TestAdjacencyExcludesContains는 contains 간선이 의존 질의에서
// 빠지는지 확인한다 — 소유 관계를 의존으로 세면 가짜 순환이 나온다.
func TestAdjacencyExcludesContains(t *testing.T) {
	doc := &Document{
		Vertices: []Vertex{{ID: "pkg"}, {ID: "sym"}, {ID: "dep"}},
		Edges: []Edge{
			{From: "pkg", To: "sym", Kind: EdgeContains},
			{From: "pkg", To: "dep", Kind: EdgeImport},
		},
	}
	adj := Adjacency(doc)
	if len(adj["pkg"]) != 1 || adj["pkg"][0] != "dep" {
		t.Fatalf("contains edge leaked into adjacency: %v", adj["pkg"])
	}
	inc := Incoming(doc)
	if len(inc["sym"]) != 0 {
		t.Fatalf("contains edge leaked into incoming: %v", inc["sym"])
	}
}

// TestEdgeKinds는 한 쌍의 모든 관계 종류가 돌아오는지 확인한다.
func TestEdgeKinds(t *testing.T) {
	doc := &Document{
		Edges: []Edge{
			{From: "a", To: "b", Kind: EdgeImport},
			{From: "a", To: "b", Kind: EdgeCall},
			{From: "a", To: "c", Kind: EdgeImport},
		},
	}
	kinds := EdgeKinds(doc, "a", "b")
	if len(kinds) != 2 || kinds[0] != EdgeCall || kinds[1] != EdgeImport {
		t.Fatalf("expected [call import], got %v", kinds)
	}
}

// TestLimitation은 limitation이 문서에 순서대로 기록되는지 확인한다 —
// "알릴 것이 없으면 조용하다"의 대칭으로, 알릴 것은 실제로 남아야 한다.
func TestLimitation(t *testing.T) {
	d := &Document{}
	if len(d.Limitations) != 0 {
		t.Fatal("fresh document must have no limitations")
	}
	d.Limitation("a")
	d.Limitation("b")
	if len(d.Limitations) != 2 || d.Limitations[0] != "a" || d.Limitations[1] != "b" {
		t.Fatalf("limitations must record in order: %v", d.Limitations)
	}
}

// TestVertexByID는 정점 조회와 미존재 구분을 확인한다.
func TestVertexByID(t *testing.T) {
	doc := &Document{Vertices: []Vertex{{ID: "x", Kind: KindFunc}}}
	if _, ok := doc.VertexByID("x"); !ok {
		t.Fatal("expected vertex x")
	}
	if doc.HasVertex("y") {
		t.Fatal("unexpected vertex y")
	}
}
