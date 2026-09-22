// Package analysis는 graph.Document 위의 질의다.
// 수확(source)이 사실을 모으면, 여기서 의미를 묻는다 —
// 순환·도달성·이웃처럼 그래프만으로 답할 수 있는 것들이다.
package analysis

import (
	"sort"

	"github.com/ictechgy/gartograph/graph"
)

// Cycle은 하나의 강연결 요소다.
// Members는 정렬된 정점 ID이고, Edges는 그 안에서 실제로 존재하는 간선이다 —
// 순환 "가능성"이 아니라 관측된 간선을 evidence로 남긴다.
type Cycle struct {
	Members []string     `json:"members"`
	Edges   []graph.Edge `json:"edges"`
}

// Cycles는 Document의 의존 간선 위에서 Tarjan SCC로 순환을 찾는다.
// 크기 2 이상의 SCC와 자기루프를 순환으로 본다.
// contains 간선은 소유 관계라 대상이 아니다(Adjacency가 이미 걸러 준다).
func Cycles(d *graph.Document) []Cycle {
	adj := graph.Adjacency(d)
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
			if c, ok := popSCC(&stack, onStack, v, adj, d); ok {
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
	return cycles
}

// popSCC는 스택에서 v까지 꺼내 SCC 하나를 Cycle로 만든다.
// 단일 정점은 자기루프가 있을 때만 순환으로 인정한다 — Tarjan은
// 순환 없는 정점도 SCC로 완결하므로 여기서 걸러야 오탐이 없다.
func popSCC(stack *[]string, onStack map[string]bool, v string,
	adj map[string][]string, d *graph.Document) (Cycle, bool) {
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
	return Cycle{Members: members, Edges: cycleEdges(members, adj, d)}, true
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

// cycleEdges는 SCC 안에서 실제로 존재하는 간선만 모은다.
// 관측 evidence 없이 "순환"이라고만 말하면 소비자가 검증할 수 없다.
func cycleEdges(members []string, adj map[string][]string, d *graph.Document) []graph.Edge {
	in := make(map[string]bool, len(members))
	for _, m := range members {
		in[m] = true
	}
	var edges []graph.Edge
	for _, m := range members {
		for _, to := range adj[m] {
			if in[to] {
				for _, k := range graph.EdgeKinds(d, m, to) {
					edges = append(edges, graph.Edge{From: m, To: to, Kind: k})
				}
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Kind < edges[j].Kind
	})
	return edges
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
