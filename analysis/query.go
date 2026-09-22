package analysis

import (
	"errors"
	"fmt"
	"sort"

	"github.com/ictechgy/gartograph/graph"
)

// ErrNotFound는 질의한 정점이 그래프에 없다는 사실이다.
// 소비자가 "없는 것"과 "도구가 못 본 것"을 구분할 수 있게 별도 오류로 둔다.
var ErrNotFound = errors.New("vertex not found")

// NeighborEdge는 이웃과의 관계다.
// 두 정점 사이의 관계 종류는 전부 싣는다 — 하나만 고르면 나머지 사실이 사라진다.
type NeighborEdge struct {
	ID    string           `json:"id"`
	Kinds []graph.EdgeKind `json:"edges"`
}

// Neighbors는 한 정점의 양방향 이웃이다.
// DependsOn은 이 정점이 의존하는 쪽, DependedBy는 이 정점을 의존하는 쪽이다.
type Neighbors struct {
	ID         string           `json:"id"`
	Kind       graph.VertexKind `json:"kind"`
	Depth      int              `json:"depth"`
	DependsOn  []NeighborEdge   `json:"dependsOn"`
	DependedBy []NeighborEdge   `json:"dependedBy"`
	Truncated  bool             `json:"truncated,omitempty"`
}

// Query는 정점 하나에 대해 depth까지 이웃을 되묻는다.
// 에이전트 소비용 JSON 계약: 잘렸으면 truncated, 깊이는 depth를 명시한다.
// depth 1은 직접 이웃, 0 이하는 1로 정규화한다.
func Query(d *graph.Document, id string, depth int, maxNeighbors int) (*Neighbors, error) {
	vtx, ok := d.VertexByID(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if depth < 1 {
		depth = 1
	}
	out := graph.Adjacency(d)
	in := graph.Incoming(d)

	dependsOn, truncOut := walk(d, out, id, depth, maxNeighbors, false)
	dependedBy, truncIn := walk(d, in, id, depth, maxNeighbors, true)

	return &Neighbors{
		ID:         id,
		Kind:       vtx.Kind,
		Depth:      depth,
		DependsOn:  dependsOn,
		DependedBy: dependedBy,
		Truncated:  truncOut || truncIn,
	}, nil
}

// walk는 한 방향의 인접 맵을 BFS로 넓혀 이웃 목록을 만든다.
// reverse가 참이면 인접 맵이 들어오는 방향이라 간선 조회도 (to→cur)로 뒤집는다.
// maxNeighbors를 넘기면 잘랐다는 사실을 함께 돌려준다 —
// 잘린 이웃을 조용히 버리면 소비자가 "전부"로 오독한다.
func walk(d *graph.Document, adj map[string][]string, root string,
	depth, maxNeighbors int, reverse bool) ([]NeighborEdge, bool) {
	seen := map[string]bool{root: true}
	frontier := []string{root}
	var out []NeighborEdge
	truncated := false

	for step := 0; step < depth; step++ {
		var next []string
		for _, cur := range frontier {
			for _, to := range adj[cur] {
				if seen[to] {
					continue
				}
				seen[to] = true
				next = append(next, to)
				if maxNeighbors > 0 && len(out) >= maxNeighbors {
					truncated = true
					continue
				}
				out = append(out, NeighborEdge{
					ID:    to,
					Kinds: dependencyKinds(d, cur, to, reverse),
				})
			}
		}
		frontier = next
	}
	return out, truncated
}

// dependencyKinds는 두 정점 사이의 의존 관계 종류를 모두 돌려준다.
// contains는 소유 관계라 제외한다 — 소유를 의존으로 섞으면
// dependsOn이 "아무것도 의존하지 않는다"가 아니라 "잘못 채워진다"가 된다.
// reverse는 들어오는 방향 질의에서 간선의 실제 방향을 맞춘다.
func dependencyKinds(d *graph.Document, cur, to string, reverse bool) []graph.EdgeKind {
	from, target := cur, to
	if reverse {
		from, target = to, cur
	}
	var kinds []graph.EdgeKind
	for _, k := range graph.EdgeKinds(d, from, target) {
		if k != graph.EdgeContains {
			kinds = append(kinds, k)
		}
	}
	return kinds
}

// mergeKinds는 두 간선 종류 목록의 합집합을 정렬해 돌려준다.
// 멀티 루트 영향 분석에서 같은 의존자가 여러 루트에 다른 종류로 닿을 때
// 사실을 유실하지 않기 위한 병합이다.
func mergeKinds(a, b []graph.EdgeKind) []graph.EdgeKind {
	set := map[graph.EdgeKind]bool{}
	for _, k := range a {
		set[k] = true
	}
	for _, k := range b {
		set[k] = true
	}
	out := make([]graph.EdgeKind, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
