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
	// Satisfies는 메서드가 구현하는 모듈 밖 인터페이스다(정점 사실 그대로).
	// 리시버 타입까지 도달하지 못해 보고된 메서드라도 외부 코드가 이
	// 인터페이스로 부를 수 있다는 triage 재료다 — 확신도로 번역하지 않는다.
	Satisfies []string `json:"satisfies,omitempty"`
	State     string   `json:"state"`
	Reason    string   `json:"reason"`
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

// ReachAdjacency는 도달성 전용 인접 맵이다 — 의존 간선에 외부 디스패치를 더한다.
// 모듈 밖 인터페이스를 구현한 메서드(Satisfies)는 리시버 타입이 도달하면
// 함께 도달한다고 본다. 그 인터페이스의 호출 지점(fmt의 Error 호출, flag의
// Value.Set 호출)은 그래프 밖이라, 이 규칙이 없으면 살아 있는 메서드가
// unreachable로 나온다 — 모듈 안 인터페이스의 CHA 팬아웃과 같은 "살아
// 있다" 쪽 과대 근사다. 문서 간선으로 긋지 않는 이유: 메서드→리시버
// references와 맞물려 모든 해당 메서드가 cycles에 2-순환으로 새어 나간다.
func ReachAdjacency(d *graph.Document) map[string][]string {
	adj := graph.Adjacency(d)
	exists := make(map[string]bool, len(d.Vertices))
	for _, v := range d.Vertices {
		exists[v.ID] = true
	}
	touched := map[string]bool{}
	for _, v := range d.Vertices {
		if v.Kind != graph.KindMethod || len(v.Satisfies) == 0 || !exists[v.Receiver] {
			continue
		}
		adj[v.Receiver] = append(adj[v.Receiver], v.ID)
		touched[v.Receiver] = true
	}
	// 결정성 — 덧붙인 목록도 Adjacency와 같은 정렬·중복 없는 형태로 맞춘다.
	for from := range touched {
		adj[from] = sortedUnique(adj[from])
	}
	return adj
}

// sortedUnique는 문자열 목록을 정렬하고 중복을 없앤다.
func sortedUnique(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// IsExternalDispatch는 경로의 한 걸음(from→to)이 문서 간선이 아니라
// 외부 디스패치 규칙(리시버 타입 → Satisfies 메서드)으로 이어졌는지 본다.
// explain 출력이 합성 걸음을 표시해, 소비자가 없는 간선을 찾지 않게 한다.
// 돌려주는 목록은 그 메서드의 Satisfies다.
func IsExternalDispatch(d *graph.Document, from, to string) ([]string, bool) {
	v, ok := d.VertexByID(to)
	if !ok || v.Kind != graph.KindMethod || len(v.Satisfies) == 0 || v.Receiver != from {
		return nil, false
	}
	for _, e := range d.Edges {
		if e.From == from && e.To == to && e.Kind != graph.EdgeContains {
			return nil, false
		}
	}
	return v.Satisfies, true
}

// Reachable은 dead·explain의 도달 집합이다 — 의존 간선에 외부 디스패치를
// 더한 ReachAdjacency 위의 BFS다. contains는 의존이 아니라 제외된다.
func Reachable(d *graph.Document, roots []string) map[string]bool {
	return reachFrom(ReachAdjacency(d), roots)
}

// DependencyReachable은 문서의 의존 간선만 따르는 도달 집합이다.
// shared처럼 "무엇에 의존하나"를 묻는 질의용이다 — 외부 디스패치는
// 의존이 아니라 도달 가능성 규칙이라 path·impact와 답이 갈라지면 안 된다.
func DependencyReachable(d *graph.Document, roots []string) map[string]bool {
	return reachFrom(graph.Adjacency(d), roots)
}

// reachFrom은 인접 맵 위에서 루트들의 BFS 도달 집합을 만든다.
func reachFrom(adj map[string][]string, roots []string) map[string]bool {
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
			ID:        v.ID,
			Kind:      v.Kind,
			Package:   v.Package,
			Position:  v.Position,
			Exported:  v.Exported,
			Satisfies: v.Satisfies,
			State:     StateUnreachable,
			Reason:    ReasonUnreachable,
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
				ID:        v.ID,
				Kind:      v.Kind,
				Package:   v.Package,
				Position:  v.Position,
				Exported:  v.Exported,
				Satisfies: v.Satisfies,
				State:     StateUnreachable,
				Reason:    reason,
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
	path, found := ExplainAdjacency(ReachAdjacency(d), id, roots)
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
