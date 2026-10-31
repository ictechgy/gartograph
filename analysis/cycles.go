// Package analysis는 graph.Document 위의 질의다.
// 수확(source)이 사실을 모으면, 여기서 의미를 묻는다 —
// 순환·도달성·이웃처럼 그래프만으로 답할 수 있는 것들이다.
package analysis

import (
	"slices"
	"sort"
	"strings"

	"github.com/ictechgy/gartograph/graph"
)

// Cycle은 하나의 강연결 요소다.
// Members는 정렬된 정점 ID이고, Edges는 그 안에서 실제로 존재하는 간선이다 —
// 순환 "가능성"이 아니라 관측된 간선을 evidence로 남긴다. 증거 간선은 관계
// (from,to,kind)마다 하나이고 위치 없이 관계만 담는다.
type Cycle struct {
	Members []string     `json:"members"`
	Edges   []graph.Edge `json:"edges"`
	// Position은 증거 간선을 (from,to,kind) 순으로 볼 때 처음으로 위치가 있는 간선의
	// 첫 사용 지점이다 — SARIF 물리 위치의 재료다. 위치는 절대 경로라 cycles JSON·
	// baseline·MCP 출력에 새로 실으면 공개 출력이 바뀌므로 직렬화하지 않는다.
	Position *graph.Position `json:"-"`
}

// Cycles는 Document의 의존 간선 위에서 Tarjan SCC로 순환을 찾는다.
// 크기 2 이상의 SCC와 자기루프를 순환으로 본다.
// contains는 소유 관계이고 CHA 후보는 확정 호출이 아니므로 순환 증거에서 제외한다.
func Cycles(d *graph.Document) []Cycle {
	adj := make(map[string][]string, len(d.Vertices))
	for _, edge := range d.Edges {
		if edge.Kind != graph.EdgeContains && !edge.Candidate {
			adj[edge.From] = append(adj[edge.From], edge.To)
		}
	}
	for from := range adj {
		slices.Sort(adj[from])
		adj[from] = slices.Compact(adj[from])
	}
	index := make(map[string]int)
	low := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var counter int
	var cycles []Cycle

	var strongConnect func(v string)
	strongConnect = func(v string) {
		index[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if _, seen := index[w]; !seen {
				strongConnect(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			if c, ok := popSCC(&stack, onStack, v, adj); ok {
				cycles = append(cycles, c)
			}
		}
	}

	for _, vtx := range d.Vertices {
		if _, seen := index[vtx.ID]; !seen {
			strongConnect(vtx.ID)
		}
	}
	sort.Slice(cycles, func(i, j int) bool { return cycles[i].Members[0] < cycles[j].Members[0] })
	attachEvidence(cycles, d)
	return cycles
}

// popSCC는 스택에서 v까지 꺼내 SCC 하나를 Cycle로 만든다(증거는 attachEvidence가 싣는다).
// 단일 정점은 자기루프가 있을 때만 순환으로 인정한다 — Tarjan은
// 순환 없는 정점도 SCC로 완결하므로 여기서 걸러야 오탐이 없다.
func popSCC(stack *[]string, onStack map[string]bool, v string,
	adj map[string][]string) (Cycle, bool) {
	var members []string
	for {
		w := (*stack)[len(*stack)-1]
		*stack = (*stack)[:len(*stack)-1]
		onStack[w] = false
		members = append(members, w)
		if w == v {
			break
		}
	}
	if len(members) == 1 && !hasSelfLoop(adj, members[0]) {
		return Cycle{}, false
	}
	sort.Strings(members)
	return Cycle{Members: members}, true
}

// hasSelfLoop는 정점이 자기 자신을 가리키는지 확인한다.
func hasSelfLoop(adj map[string][]string, id string) bool {
	for _, to := range adj[id] {
		if to == id {
			return true
		}
	}
	return false
}

// attachEvidence는 문서 간선을 한 번 훑어 양 끝이 같은 순환에 속한 의존 간선을 그
// 순환의 증거로 싣는다. 관측 evidence 없이 "순환"이라고만 말하면 소비자가 검증할 수 없다.
// 멤버→순환 색인으로 고르므로 관계마다 한 번만 실린다 — 멤버의 이웃 목록(같은 쌍의
// 종류마다 중복)을 따라 종류를 다시 붙이면 증거가 종류 수만큼 거듭 실리고, 순환마다
// 문서 전체 간선을 다시 훑게 된다. contains는 Adjacency처럼 의존이 아니라 뺀다.
func attachEvidence(cycles []Cycle, d *graph.Document) {
	owner := make(map[string]int)
	for i, c := range cycles {
		for _, m := range c.Members {
			owner[m] = i
		}
	}
	for _, e := range d.Edges {
		if e.Kind == graph.EdgeContains || e.Candidate {
			continue
		}
		fromCycle, ok := owner[e.From]
		if toCycle, same := owner[e.To]; !ok || !same || fromCycle != toCycle {
			continue
		}
		cycles[fromCycle].Edges = append(cycles[fromCycle].Edges, e)
	}
	for i := range cycles {
		cycles[i].Edges, cycles[i].Position = distinctEvidence(cycles[i].Edges)
	}
}

// distinctEvidence는 증거 간선을 (from,to,kind) 순으로 두고 같은 관계를 하나로 모아
// 위치 없는 관계로 돌려준다. 처음으로 위치가 있는 간선의 첫 지점은 SARIF용으로 따로 준다.
// View 문서는 이미 정렬·중복 제거돼 있다 — 직접 만든 문서도 결정적이도록 그때만 정렬한다.
func distinctEvidence(edges []graph.Edge) ([]graph.Edge, *graph.Position) {
	if !slices.IsSortedFunc(edges, compareRelations) {
		slices.SortStableFunc(edges, compareRelations)
	}
	var first *graph.Position
	out := edges[:0]
	for _, e := range edges {
		if first == nil && len(e.Positions) > 0 {
			p := e.Positions[0]
			first = &p
		}
		if n := len(out); n > 0 && compareRelations(out[n-1], e) == 0 {
			continue
		}
		out = append(out, graph.Edge{From: e.From, To: e.To, Kind: e.Kind})
	}
	return out, first
}

// compareRelations는 간선의 관계 순서다 — Document.Sort와 같은 (From, To, Kind) 순.
func compareRelations(a, b graph.Edge) int {
	if c := strings.Compare(a.From, b.From); c != 0 {
		return c
	}
	if c := strings.Compare(a.To, b.To); c != 0 {
		return c
	}
	return strings.Compare(string(a.Kind), string(b.Kind))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
