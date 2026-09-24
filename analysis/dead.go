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
// Exported는 심볼의 공개 여부 사실이다 — 비공개 unreachable이 공개
// unreachable보다 triage 우선도가 높다는 분류(go-fynx의 HIGH/MEDIUM)의
// 재료다. 확신도를 판정해 적지 않는다 — 공개 심볼은 모듈 밖 호출자·
// reflection·플러그인이 쓸 수 있어 "낮은 확신"도 사실이 아니라 해석이다.
type Finding struct {
	ID       string           `json:"id"`
	Kind     graph.VertexKind `json:"kind"`
	Package  string           `json:"package,omitempty"`
	Position *graph.Position  `json:"position,omitempty"`
	Exported bool             `json:"exported"`
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

// ReasonRTA는 RTA 모드의 사유다 — CHA 그래프와 다른 분석의 말이므로
// 사유로 구분해 소비자가 알고리즘을 오독하지 않게 한다.
const ReasonRTA = "not reachable under rapid type analysis"

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
			Exported: v.Exported,
			State:    StateUnreachable,
			Reason:   ReasonUnreachable,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// DeadRTA는 RTA 도달 집합으로 호출 가능 심볼을 판정한다.
// func·method는 RTA로, var·const·type은 그래프 도달성으로 본다 —
// RTA의 호출 그래프는 비호출 심볼을 담지 않으므로 두 사실을 섞어
// 말할 수 없다. RTA는 과소 근사라 "unreachable"의 의미가 CHA보다
// 넓어진다는 점이 사유와 limitation으로 구분되어야 한다.
func DeadRTA(d *graph.Document, graphReach, rtaReach map[string]bool) []Finding {
	var out []Finding
	for _, v := range d.Symbols() {
		callable := v.Kind == graph.KindFunc || v.Kind == graph.KindMethod
		var dead bool
		reason := ReasonUnreachable
		if callable {
			dead, reason = !rtaReach[v.ID], ReasonRTA
		} else {
			dead = !graphReach[v.ID]
		}
		if dead {
			out = append(out, Finding{
				ID:       v.ID,
				Kind:     v.Kind,
				Package:  v.Package,
				Position: v.Position,
				Exported: v.Exported,
				State:    StateUnreachable,
				Reason:   reason,
			})
		}
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
	path, found := ExplainAdjacency(graph.Adjacency(d), id, roots)
	return path, found, nil
}

// ExplainAdjacency는 주어진 인접 맵 위에서 루트→정점 최단 경로를 BFS로 찾는다.
// 문서의 간선 맵이 아닌 다른 사실(RTA 콜그래프) 위에서 같은 "왜 도달했나"
// 질의를 하기 위한 장치다 — 경로 알고리즘은 하나여야 한다.
func ExplainAdjacency(adj map[string][]string, id string, roots []string) ([]string, bool) {
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
		return nil, false
	}
	var path []string
	for cur := id; ; cur = parent[cur] {
		path = append([]string{cur}, path...)
		if _, hasParent := parent[cur]; !hasParent {
			break
		}
	}
	return path, true
}
