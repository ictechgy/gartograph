package analysis

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
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

// TestSharedMatchesPerRootReach는 무작위 문서에서 Shared가 루트마다 DependencyReachable로 도달 집합을
// 구하던 구현(perRootShared)과 같은 결과를 내는지 확인한다. 겹친 루트, 서로 닿는 루트, 자기 루프,
// contains, 정점 없는 끝을 섞는다 — 인접 맵을 한 번만 만드는 것은 비용만 줄여야 한다.
func TestSharedMatchesPerRootReach(t *testing.T) {
	r := rand.New(rand.NewSource(29))
	sawShared, sawOnly := false, false
	for trial := 0; trial < 500; trial++ {
		d := randomDependencyDocument(r)
		roots := make([]string, 2+r.Intn(3))
		for i := range roots {
			roots[i] = d.Vertices[r.Intn(len(d.Vertices))].ID
		}
		got, err := Shared(d, roots)
		if err != nil {
			t.Fatal(err)
		}
		if want := perRootShared(d, roots); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d roots %v diverged:\ngot  %+v\nwant %+v\nedges %+v", trial, roots, got, want, d.Edges)
		}
		sawShared = sawShared || len(got.Shared) > 0
		sawOnly = sawOnly || len(got.Only) > 0
	}
	if !sawShared || !sawOnly {
		t.Fatalf("random documents must exercise shared (%v) and only (%v) sets", sawShared, sawOnly)
	}
}

// perRootShared는 인접 맵을 한 번만 만들기 전의 Shared다 — 루트마다 DependencyReachable이 인접 맵을
// 새로 만든다. 느려도 출력 대조 기준으로 그대로 둔다.
func perRootShared(d *graph.Document, roots []string) *SharedResult {
	reach := make(map[string]map[string]bool, len(roots))
	count := map[string]int{}
	for _, r := range roots {
		reach[r] = DependencyReachable(d, []string{r})
		for id := range reach[r] {
			count[id]++
		}
	}
	res := &SharedResult{Roots: roots, Limitations: d.Limitations}
	for id, n := range count {
		if n == len(roots) {
			res.Shared = append(res.Shared, id)
		}
	}
	sort.Strings(res.Shared)
	for _, r := range roots {
		var only []string
		for id := range reach[r] {
			if count[id] < len(roots) {
				only = append(only, id)
			}
		}
		if len(only) > 0 {
			sort.Strings(only)
			if res.Only == nil {
				res.Only = map[string][]string{}
			}
			res.Only[r] = only
		}
	}
	return res
}

// TestSharedBuildsAdjacencyOnce는 루트가 늘어도 Shared가 의존 인접 맵을 다시 만들지 않는지 할당 횟수로
// 본다. 루트마다 인접 맵을 새로 만들면 할당이 루트 수 × 간선 출발 정점 수만큼 늘어난다. 시간 대신
// 할당 횟수를 세므로 바쁜 머신에서도 결과가 흔들리지 않는다. 루트는 이웃이 없는 정점이라 루트 하나의
// BFS는 할당 몇 번으로 끝난다.
func TestSharedBuildsAdjacencyOnce(t *testing.T) {
	const ring = 2000
	d := &graph.Document{}
	for i := 0; i < ring; i++ {
		id := fmt.Sprintf("m.f%04d", i)
		d.Vertices = append(d.Vertices, graph.Vertex{ID: id, Kind: graph.KindFunc})
		d.Edges = append(d.Edges, graph.Edge{From: id, To: fmt.Sprintf("m.f%04d", (i+1)%ring), Kind: graph.EdgeCall})
	}
	var roots []string
	for i := 0; i < 20; i++ {
		roots = append(roots, fmt.Sprintf("m.root%02d", i))
		d.Vertices = append(d.Vertices, graph.Vertex{ID: roots[i], Kind: graph.KindFunc})
	}
	var err error
	allocations := func(n int) float64 {
		return testing.AllocsPerRun(3, func() {
			if _, e := Shared(d, roots[:n]); e != nil {
				err = e
			}
		})
	}
	two, twenty := allocations(2), allocations(20)
	if err != nil {
		t.Fatal(err)
	}
	if twenty > 1.5*two {
		t.Fatalf("Shared must build the dependency adjacency once, not per root: "+
			"%.0f allocations for 2 roots, %.0f for 20", two, twenty)
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
