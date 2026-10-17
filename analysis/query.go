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
	// Limitations은 이웃이 비어 있어도 수확의 관측 공백을 숨기지 않는다.
	Limitations []string `json:"limitations,omitempty"`
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
	out := graph.KindedAdjacency(d)
	in := graph.KindedIncoming(d)

	dependsOn, truncOut := walk(out, id, depth, maxNeighbors)
	dependedBy, truncIn := walk(in, id, depth, maxNeighbors)

	return &Neighbors{
		ID:          id,
		Kind:        vtx.Kind,
		Depth:       depth,
		DependsOn:   dependsOn,
		DependedBy:  dependedBy,
		Truncated:   truncOut || truncIn,
		Limitations: d.Limitations,
	}, nil
}

// walk는 한 방향의 종류 포함 이웃 목록을 BFS로 넓혀 이웃 목록을 만든다.
// 관계 종류는 이웃 목록에서 그 이웃의 구간을 읽는다 — 들어오는 방향 목록도 간선의
// 실제 종류를 담고 있어 방향을 뒤집어 다시 찾지 않는다.
// maxNeighbors를 넘기면 잘랐다는 사실을 함께 돌려준다 —
// 잘린 이웃을 조용히 버리면 소비자가 "전부"로 오독한다.
func walk(adj map[string][]graph.Neighbor, root string,
	depth, maxNeighbors int) ([]NeighborEdge, bool) {
	seen := map[string]bool{root: true}
	frontier := []string{root}
	var out []NeighborEdge
	truncated := false

	for step := 0; step < depth && len(frontier) > 0; step++ {
		var next []string
		for _, cur := range frontier {
			ns := adj[cur]
			for i, end := 0, 0; i < len(ns); i = end {
				end = neighborRunEnd(ns, i)
				to := ns[i].ID
				if seen[to] {
					continue
				}
				seen[to] = true
				next = append(next, to)
				if maxNeighbors > 0 && len(out) >= maxNeighbors {
					truncated = true
					continue
				}
				out = append(out, NeighborEdge{ID: to, Kinds: runKinds(ns[i:end])})
			}
		}
		frontier = next
	}
	return out, truncated
}

// neighborRunEnd는 정렬된 이웃 목록에서 i번째와 같은 이웃의 구간이 끝나는 위치다.
// 목록이 (ID, Kind) 순이라 한 이웃과의 관계 종류는 [i, end)에 모여 있다.
func neighborRunEnd(ns []graph.Neighbor, i int) int {
	end := i + 1
	for end < len(ns) && ns[end].ID == ns[i].ID {
		end++
	}
	return end
}

// runKinds는 한 이웃 구간의 관계 종류를 정렬 순서대로 돌려준다.
// 같은 관계가 중복된 문서에서는 EdgeKinds처럼 그 종류가 거듭 실린다.
func runKinds(run []graph.Neighbor) []graph.EdgeKind {
	kinds := make([]graph.EdgeKind, len(run))
	for i, n := range run {
		kinds[i] = n.Kind
	}
	return kinds
}

// distinctRunKinds는 한 이웃 구간의 관계 종류를 중복 없이 돌려준다 — 영향 항목의
// edges는 관계 집합이라 같은 관계가 중복된 문서에서도 종류를 한 번만 싣는다.
func distinctRunKinds(run []graph.Neighbor) []graph.EdgeKind {
	kinds := make([]graph.EdgeKind, 0, len(run))
	for i, n := range run {
		if i == 0 || n.Kind != run[i-1].Kind {
			kinds = append(kinds, n.Kind)
		}
	}
	return kinds
}

// dependencyKinds는 from→to의 의존 관계 종류를 모두 돌려준다.
// contains는 소유 관계라 제외한다 — 소유를 의존으로 섞으면
// dependsOn이 "아무것도 의존하지 않는다"가 아니라 "잘못 채워진다"가 된다.
// 문서 전체 간선을 훑으므로 경로의 홉처럼 몇 번만 묻는 곳에서만 쓴다 — 이웃마다
// 묻는 질의는 종류 포함 이웃 목록(graph.KindedAdjacency)을 쓴다.
func dependencyKinds(d *graph.Document, from, to string) []graph.EdgeKind {
	var kinds []graph.EdgeKind
	for _, k := range graph.EdgeKinds(d, from, to) {
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
