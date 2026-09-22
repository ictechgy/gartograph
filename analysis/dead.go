// 도달성 질의 — 보존 루트에서 출발해 도달 불가능한 심볼을 찾는다.
//
// "unreachable"은 그래프 사실이지 삭제 판정이 아니다. Go에는 main이 없는
// 라이브러리와 공개 API가 있으므로, 루트 집합이 결과를 좌우한다 —
// 루트가 무엇이었는지를 항상 결과에 함께 싣는다.
package analysis

import (
	"fmt"
	"sort"

	"github.com/ictechgy/gartograph/graph"
)

// Finding은 도달 불가능한 심볼 하나의 보고다.
// State는 그래프 사실("unreachable")이고 Reason은 그 이유다 —
// "지워도 된다"는 판정은 어디에도 없다.
type Finding struct {
	ID       string           `json:"id"`
	Kind     graph.VertexKind `json:"kind"`
	Package  string           `json:"package,omitempty"`
	Position *graph.Position  `json:"position,omitempty"`
	State    string           `json:"state"`
	Reason   string           `json:"reason"`
}

// StateUnreachable은 보존 루트에서 도달할 수 없다는 그래프 사실이다.
const StateUnreachable = "unreachable"

// ReasonUnreachable는 Finding의 기본 사유다.
const ReasonUnreachable = "not reachable from retention roots"

// RetentionRoots는 루트 집합을 만든다.
// 문서가 기록한 루트(main·init) + retainPublic이면 공개 심볼 전부 + 추가 지정.
// 지정했는데 문서에 없는 루트는 unknown으로 돌려준다 — 조용히 무시하면
// 소비자가 "루트로 썼다"고 오해한다.
func RetentionRoots(d *graph.Document, retainPublic bool,
	extra []string) (roots []string, unknown []string) {
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			roots = append(roots, id)
		}
	}
	for _, r := range d.RootIDs() {
		add(r)
	}
	if retainPublic {
		for _, v := range d.Symbols() {
			if v.Exported {
				add(v.ID)
			}
		}
	}
	for _, e := range extra {
		if d.HasVertex(e) {
			add(e)
		} else {
			unknown = append(unknown, e)
		}
	}
	sort.Strings(roots)
	sort.Strings(unknown)
	return roots, unknown
}

// Reachable은 루트들에서 의존 간선을 따라 도달 가능한 정점 집합을 BFS로 만든다.
// contains는 의존이 아니라 제외된다(Adjacency가 걸러 준다).
func Reachable(d *graph.Document, roots []string) map[string]bool {
	adj := graph.Adjacency(d)
	seen := make(map[string]bool)
	queue := append([]string(nil), roots...)
	for _, r := range roots {
		seen[r] = true
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, to := range adj[cur] {
			if !seen[to] {
				seen[to] = true
				queue = append(queue, to)
			}
		}
	}
	return seen
}

// Dead는 도달 불가능한 심볼 레벨 정점을 Finding으로 돌려준다.
// 패키지·모듈 정점은 심볼이 아니라 대상이 아니다.
func Dead(d *graph.Document, reachable map[string]bool) []Finding {
	var out []Finding
	for _, v := range d.Symbols() {
		if reachable[v.ID] {
			continue
		}
		out = append(out, Finding{
			ID:       v.ID,
			Kind:     v.Kind,
			Package:  v.Package,
			Position: v.Position,
			State:    StateUnreachable,
			Reason:   ReasonUnreachable,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Explain은 정점까지의 도달 경로 하나를 돌려준다.
// 두 번째 반환값이 false면 루트에서 이 정점으로 가는 경로가 없다 —
// "왜 살아 있나"와 "왜 못 찾았나"를 소비자가 구분할 수 있어야 한다.
func Explain(d *graph.Document, id string, roots []string) ([]string, bool, error) {
	if !d.HasVertex(id) {
		return nil, false, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	adj := graph.Adjacency(d)
	parent := map[string]string{}
	seen := map[string]bool{}
	queue := append([]string(nil), roots...)
	for _, r := range roots {
		seen[r] = true
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, to := range adj[cur] {
			if !seen[to] {
				seen[to] = true
				parent[to] = cur
				queue = append(queue, to)
			}
		}
	}
	if !seen[id] {
		return nil, false, nil
	}
	var path []string
	for cur := id; ; cur = parent[cur] {
		path = append([]string{cur}, path...)
		if _, hasParent := parent[cur]; !hasParent {
			break
		}
	}
	return path, true, nil
}
