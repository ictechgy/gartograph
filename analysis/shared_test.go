package analysis

import (
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// TestShared는 두 루트의 공통 도달 집합과 고유분을 확인한다 —
// goda shared의 계약이다. 교집합과 only는 서로 다른 사실이라
// 둘 다 검증한다.
func TestShared(t *testing.T) {
	doc := &graph.Document{
		Vertices: []graph.Vertex{
			{ID: "m/a", Kind: graph.KindPackage},
			{ID: "m/b", Kind: graph.KindPackage},
			{ID: "m/common", Kind: graph.KindPackage},
			{ID: "m/onlya", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			{From: "m/a", To: "m/common", Kind: graph.EdgeImport},
			{From: "m/a", To: "m/onlya", Kind: graph.EdgeImport},
			{From: "m/b", To: "m/common", Kind: graph.EdgeImport},
		},
	}
	res, err := Shared(doc, []string{"m/a", "m/b"})
	if err != nil {
		t.Fatalf("Shared: %v", err)
	}
	// 교집합은 common 하나다 — 루트는 서로를 도달하지 못한다.
	if len(res.Shared) != 1 || res.Shared[0] != "m/common" {
		t.Fatalf("shared must be the intersection only: %v", res.Shared)
	}
	// only는 루트 자신도 담는다 — 루트는 자기만 도달하는 정점이다.
	if len(res.Only["m/a"]) != 2 ||
		res.Only["m/a"][0] != "m/a" || res.Only["m/a"][1] != "m/onlya" {
		t.Fatalf("only[a] should be a and onlya: %v", res.Only)
	}
	if len(res.Only["m/b"]) != 1 || res.Only["m/b"][0] != "m/b" {
		t.Fatalf("only[b] should be just b: %v", res.Only)
	}
}

// TestSharedNotFound는 없는 루트가 ErrNotFound인지 확인한다 —
// "교집합이 비었다"와 "루트가 없다"를 구분해야 한다.
func TestSharedNotFound(t *testing.T) {
	doc := &graph.Document{
		Vertices: []graph.Vertex{{ID: "m/a", Kind: graph.KindPackage}},
	}
	if _, err := Shared(doc, []string{"m/a", "m/ghost"}); err == nil {
		t.Fatal("missing root must be an error")
	}
}

// TestSharedThreeRoots는 3개 이상 루트의 교집합 의미론을 확인한다 —
// 세 루트 모두가 도달하는 것만 shared다.
func TestSharedThreeRoots(t *testing.T) {
	doc := &graph.Document{
		Vertices: []graph.Vertex{
			{ID: "a", Kind: graph.KindPackage},
			{ID: "b", Kind: graph.KindPackage},
			{ID: "c", Kind: graph.KindPackage},
			{ID: "all", Kind: graph.KindPackage},
			{ID: "two", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			{From: "a", To: "all", Kind: graph.EdgeImport},
			{From: "b", To: "all", Kind: graph.EdgeImport},
			{From: "c", To: "all", Kind: graph.EdgeImport},
			{From: "a", To: "two", Kind: graph.EdgeImport},
			{From: "b", To: "two", Kind: graph.EdgeImport},
		},
	}
	res, err := Shared(doc, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Shared: %v", err)
	}
	// "two"는 두 루트만 도달 — shared가 아니라 a·b의 only다.
	for _, id := range res.Shared {
		if id == "two" {
			t.Fatal("a vertex reachable from a subset is not shared")
		}
	}
	if len(res.Only["a"]) != 2 ||
		res.Only["a"][0] != "a" || res.Only["a"][1] != "two" {
		t.Fatalf("two belongs to a and b's only sets: %v", res.Only)
	}
	if len(res.Only["c"]) != 1 || res.Only["c"][0] != "c" {
		t.Fatalf("c reaches everything shared: %v", res.Only)
	}
}
