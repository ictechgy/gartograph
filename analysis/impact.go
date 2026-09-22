package analysis

import (
	"fmt"
	"sort"

	"github.com/ictechgy/gartograph/graph"
)

// ImpactEntry는 영향을 받는 정점 하나다.
// Depth는 대상 정점에서 몇 홉 떨어져 의존하는지다 — 1이 직접 의존자다.
type ImpactEntry struct {
	ID    string           `json:"id"`
	Kind  graph.VertexKind `json:"kind"`
	Depth int              `json:"depth"`
	Kinds []graph.EdgeKind `json:"edges"`
}

// Impact는 한 정점의 역방향 전이 클로저다 — 이 정점을 바꾸면 무엇이 깨지는가.
// query의 dependedBy와 달리 한 방향만 보고, 각 항목에 거리를 싣는다.
// contains는 소유 관계라 의존 전이에 섞지 않는다.
type Impact struct {
	ID        string           `json:"id"`
	Kind      graph.VertexKind `json:"kind"`
	Depth     int              `json:"depth"`
	Dependers []ImpactEntry    `json:"dependers"`
	Truncated bool             `json:"truncated,omitempty"`
}

// FindImpact는 id를 의존하는 정점들을 depth까지 역방향 BFS로 모은다.
// depth 0 이하는 전이 끝까지(사실상 무제한) 본다 — 깊이 제한은
// 소비자가 고르는 것이지 도구가 정할 기본값이 없기 때문이다.
func FindImpact(d *graph.Document, id string, depth, maxEntries int) (*Impact, error) {
	vtx, ok := d.VertexByID(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	out, truncated := impactFrom(d, []string{id}, depth, maxEntries)
	return &Impact{ID: id, Kind: vtx.Kind, Depth: depth, Dependers: out, Truncated: truncated}, nil
}

// impactFrom은 여러 루트의 합집합을 역방향 BFS로 모은다.
// Depth는 가장 가까운 루트에서의 거리다 — 파일 집합 영향 분석처럼
// 루트가 여럿일 때 "몇 홉이나 떨어져 있나"는 최단 거리여야 의미가 있다.
func impactFrom(d *graph.Document, roots []string, depth, maxEntries int) ([]ImpactEntry, bool) {
	in := graph.Incoming(d)
	seen := map[string]bool{}
	for _, r := range roots {
		seen[r] = true
	}
	frontier := append([]string(nil), roots...)
	var out []ImpactEntry
	truncated := false

	for step := 1; len(frontier) > 0 && (depth <= 0 || step <= depth); step++ {
		var next []string
		for _, cur := range frontier {
			for _, from := range in[cur] {
				if seen[from] {
					continue
				}
				seen[from] = true
				next = append(next, from)
				if maxEntries > 0 && len(out) >= maxEntries {
					truncated = true
					continue
				}
				kinds := dependencyKinds(d, from, cur, false)
				dep := ImpactEntry{ID: from, Depth: step, Kinds: kinds}
				if v, ok := d.VertexByID(from); ok {
					dep.Kind = v.Kind
				}
				out = append(out, dep)
			}
		}
		frontier = next
	}
	// 발견 순서는 BFS라 실행마다 같지만, 파일에 남길 결과는
	// (depth, id) 순으로 정규화한다.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].ID < out[j].ID
	})
	return out, truncated
}
