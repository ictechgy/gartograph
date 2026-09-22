// 정점 간 경로 질의 — "왜 A가 B를 아는가"의 답이다.
// query가 한 정점의 이웃을 넓히는 것과 달리, path는 두 정점을 잇는
// 최단 의존 사슬 하나를 보여준다.
package analysis

import (
	"fmt"

	"github.com/ictechgy/gartograph/graph"
)

// PathHop은 경로 위의 정점 하나다.
// Kinds는 바로 앞 정점에서 이 정점으로 이어지는 의존 간선 종류다 —
// 첫 정점(from)에는 앞 정점이 없어 비어 있다.
type PathHop struct {
	ID    string           `json:"id"`
	Kinds []graph.EdgeKind `json:"edges,omitempty"`
}

// PathResult는 from→to 최단 의존 경로 질의의 결과다.
// Found가 false면 두 정점 사이에 의존 경로가 없다는 그래프 사실이다 —
// 정점이 문서에 없는 것(ErrNotFound)과 구분해야 소비자가 "없는 정점"과
// "끊긴 의존"을 혼동하지 않는다.
type PathResult struct {
	From  string    `json:"from"`
	To    string    `json:"to"`
	Found bool      `json:"found"`
	Hops  []PathHop `json:"hops,omitempty"`
	// Limitations은 수확이 보지 못한 영역이다 — found:false가
	// 부분 수확의 산물일 수 있음을 소비자에게 남긴다.
	Limitations []string `json:"limitations,omitempty"`
}

// Path는 from에서 to로 가는 최단 의존 경로를 BFS로 찾는다.
// contains는 소유 관계라 경로에 쓰지 않는다(Adjacency가 걸러 준다).
func Path(d *graph.Document, from, to string) (*PathResult, error) {
	for _, id := range []string{from, to} {
		if !d.HasVertex(id) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
	}
	adj := graph.Adjacency(d)
	parent := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			break
		}
		for _, next := range adj[cur] {
			if _, seen := parent[next]; seen {
				continue
			}
			parent[next] = cur
			queue = append(queue, next)
		}
	}
	res := &PathResult{From: from, To: to, Limitations: d.Limitations}
	if _, ok := parent[to]; !ok {
		return res, nil
	}
	var ids []string
	for cur := to; ; cur = parent[cur] {
		ids = append([]string{cur}, ids...)
		if cur == from {
			break
		}
	}
	res.Found = true
	res.Hops = make([]PathHop, len(ids))
	res.Hops[0] = PathHop{ID: ids[0]}
	for i := 1; i < len(ids); i++ {
		res.Hops[i] = PathHop{
			ID:    ids[i],
			Kinds: dependencyKinds(d, ids[i-1], ids[i], false),
		}
	}
	return res, nil
}
