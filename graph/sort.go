package graph

import (
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Sort는 Document를 정규화한다 — 정점은 ID, 간선은 (From,To,Kind) 순으로
// 정렬하고 중복 간선을 제거한다.
// 같은 입력이 매번 다른 파일이 되면 리포트 diff와 캐시가 무의미해지므로
// 모든 출력 경로는 이 함수를 거친다.
func (d *Document) Sort() {
	sort.Slice(d.Vertices, func(i, j int) bool {
		return d.Vertices[i].ID < d.Vertices[j].ID
	})
	sort.Slice(d.Edges, func(i, j int) bool {
		a, b := d.Edges[i], d.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	d.Edges = dedupeEdges(d.Edges)
	sort.Strings(d.Limitations)
	sort.Strings(d.Roots)
	sort.Slice(d.WorkspaceModules, func(i, j int) bool {
		return d.WorkspaceModules[i].Path < d.WorkspaceModules[j].Path
	})
}

// positionLess는 사용 지점의 결정적 순서다 — 파일, 줄, 열 순.
func positionLess(a, b Position) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column < b.Column
}

// sameEdge는 간선의 집합 동일성이다 — 위치가 아니라 관계가 단위다.
// 같은 관계가 여러 지점에서 성립하면 Positions이 합쳐진다.
func sameEdge(a, b Edge) bool {
	return a.From == b.From && a.To == b.To && a.Kind == b.Kind
}

// dedupeEdges는 정렬된 간선 목록에서 같은 관계를 하나로 합친다.
// Positions는 모아서 정렬한다 — 사용 지점은 순서가 아니라 집합이다.
// Sort 전용이며 정렬되지 않은 입력에는 쓰지 않는다.
func dedupeEdges(edges []Edge) []Edge {
	out := edges[:0]
	for i, e := range edges {
		if i > 0 && sameEdge(e, edges[i-1]) {
			out[len(out)-1].Positions = append(out[len(out)-1].Positions, e.Positions...)
			// 한 지점이라도 확정 관계면 합친 관계도 확정이다.
			out[len(out)-1].Candidate = out[len(out)-1].Candidate && e.Candidate
			continue
		}
		out = append(out, e)
	}
	for i := range out {
		sort.Slice(out[i].Positions, func(a, b int) bool {
			return positionLess(out[i].Positions[a], out[i].Positions[b])
		})
		out[i].Positions = dedupePositions(out[i].Positions)
	}
	return out
}

// dedupePositions는 정렬된 위치 목록에서 연속 중복을 제거한다.
func dedupePositions(ps []Position) []Position {
	out := ps[:0]
	for i, p := range ps {
		if i > 0 && p == ps[i-1] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Adjacency는 의존 방향 간선의 나가는 이웃 맵을 만든다.
// contains는 소유 관계라 의존 질의에서 제외한다 — 소유를 의존으로 세면
// 순환 검사가 패키지→심볼→패키지를 가짜 순환으로 잡는다.
func Adjacency(d *Document) map[string][]string {
	adj := make(map[string][]string, len(d.Vertices))
	for _, e := range d.Edges {
		if e.Kind == EdgeContains {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
	}
	for k := range adj {
		sort.Strings(adj[k])
	}
	return adj
}

// Incoming은 의존 방향 간선의 들어오는 이웃 맵을 만든다.
// "누가 나를 쓰나" 질의 전용으로, 역방향 맵을 매번 재구성하지 않게 한다.
func Incoming(d *Document) map[string][]string {
	inc := make(map[string][]string, len(d.Vertices))
	for _, e := range d.Edges {
		if e.Kind == EdgeContains {
			continue
		}
		inc[e.To] = append(inc[e.To], e.From)
	}
	for k := range inc {
		sort.Strings(inc[k])
	}
	return inc
}

// Neighbor는 의존 간선 하나의 상대 정점과 관계 종류다.
// 이웃 목록에 종류를 함께 실으면 두 정점 사이의 관계 종류를 그 목록 안에서 읽는다 —
// 이웃마다 EdgeKinds로 문서 전체 간선을 다시 훑으면 질의 비용이 결과 크기 × 간선 수가 된다.
type Neighbor struct {
	ID   string
	Kind EdgeKind
}

// KindedAdjacency는 Adjacency에 간선 종류를 함께 실은 나가는 이웃 목록이다.
// 목록은 (ID, Kind) 순이라 한 이웃의 관계 종류가 연속 구간을 이루고, 이웃 순서는
// Adjacency와 같다. contains는 Adjacency처럼 뺀다.
// (from,to) 쌍마다 항목을 두는 문서 전체 맵은 일부러 만들지 않는다 — 쌍마다 할당하면
// 이웃 몇 개만 묻는 질의가 맵을 만드는 데 시간을 다 쓴다.
func KindedAdjacency(d *Document) map[string][]Neighbor {
	return kindedNeighbors(d, false)
}

// KindedIncoming은 Incoming에 간선 종류를 함께 실은 들어오는 이웃 목록이다.
// Neighbor.ID는 간선의 출발(의존하는 쪽)이고 Kind는 그 간선의 종류다 — 들어오는 방향
// 질의도 간선 방향을 뒤집어 다시 찾지 않는다. 정렬·구간 규칙은 KindedAdjacency와 같다.
func KindedIncoming(d *Document) map[string][]Neighbor {
	return kindedNeighbors(d, true)
}

// kindedNeighbors는 의존 간선으로 정점별 종류 포함 이웃 목록을 만든다.
// reverse면 도착 정점의 목록에 출발 정점을 담는다.
// 이웃 수를 먼저 세어 모든 목록을 배열 하나의 구간으로 나눠 담는다 — 목록마다 append로
// 키우면 할당이 정점 수의 몇 배가 되고, 호출마다 목록을 새로 만드는 MCP 저차수 질의가
// 그 비용을 그대로 낸다. 구간은 용량을 잘라 둬서 한 목록에 덧붙여도 이웃 구간을 덮지 않는다.
func kindedNeighbors(d *Document, reverse bool) map[string][]Neighbor {
	owners, counts, edgeSlots := countNeighbors(d, reverse)
	// starts[s]는 s번째 목록의 시작이고, starts[s+1]이 끝이다.
	starts := make([]int, len(owners)+1)
	for s, c := range counts {
		starts[s+1] = starts[s] + c
	}
	all := placeNeighbors(d, reverse, starts, edgeSlots)
	out := make(map[string][]Neighbor, len(owners))
	for s, owner := range owners {
		ns := all[starts[s]:starts[s+1]:starts[s+1]]
		slices.SortFunc(ns, compareNeighbors)
		out[owner] = ns
	}
	return out
}

// countNeighbors는 의존 간선을 한 번 훑어 목록 주인(owners) 순번별 이웃 수를 센다.
// edgeSlots는 의존 간선마다 그 주인의 순번이다 — 이웃을 놓는 두 번째 훑기가 주인을
// 다시 해시하지 않게 한다. 두 훑기는 같은 간선(contains 제외)을 같은 순서로 본다.
func countNeighbors(d *Document, reverse bool) (owners []string, counts, edgeSlots []int) {
	slot := make(map[string]int, len(d.Vertices))
	owners = make([]string, 0, len(d.Vertices))
	counts = make([]int, 0, len(d.Vertices))
	edgeSlots = make([]int, 0, len(d.Edges))
	for _, e := range d.Edges {
		if e.Kind == EdgeContains {
			continue
		}
		owner, _ := neighborOf(e, reverse)
		s, ok := slot[owner]
		if !ok {
			s = len(owners)
			slot[owner] = s
			owners = append(owners, owner)
			counts = append(counts, 0)
		}
		counts[s]++
		edgeSlots = append(edgeSlots, s)
	}
	return owners, counts, edgeSlots
}

// placeNeighbors는 의존 간선을 다시 훑어 이웃을 주인별 구간(starts)에 문서 순서대로 놓는다.
func placeNeighbors(d *Document, reverse bool, starts, edgeSlots []int) []Neighbor {
	next := append([]int(nil), starts[:len(starts)-1]...)
	all := make([]Neighbor, len(edgeSlots))
	i := 0
	for _, e := range d.Edges {
		if e.Kind == EdgeContains {
			continue
		}
		_, n := neighborOf(e, reverse)
		s := edgeSlots[i]
		i++
		all[next[s]] = n
		next[s]++
	}
	return all
}

// neighborOf는 의존 간선 하나를 (목록 주인, 이웃)으로 나눈다.
// reverse면 도착 정점이 주인이고 출발 정점이 이웃이다 — 종류는 간선 그대로다.
func neighborOf(e Edge, reverse bool) (string, Neighbor) {
	if reverse {
		return e.To, Neighbor{ID: e.From, Kind: e.Kind}
	}
	return e.From, Neighbor{ID: e.To, Kind: e.Kind}
}

// compareNeighbors는 (ID, Kind) 순서다 — ID 순서는 Adjacency의 sort.Strings와 같고,
// 같은 이웃 안의 종류 순서는 EdgeKinds와 같다.
func compareNeighbors(a, b Neighbor) int {
	if c := strings.Compare(a.ID, b.ID); c != 0 {
		return c
	}
	return strings.Compare(string(a.Kind), string(b.Kind))
}

// EdgeKinds는 두 정점 사이의 모든 관계 종류를 정렬해 돌려준다.
// 하나만 고르면 나머지 관계가 사라지고 무엇을 고를지가 실행마다 달라진다.
func EdgeKinds(d *Document, from, to string) []EdgeKind {
	var kinds []EdgeKind
	for _, e := range d.Edges {
		if e.From == from && e.To == to {
			kinds = append(kinds, e.Kind)
		}
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}

// VertexByID는 정점을 ID로 찾는다.
// 없는 정점을 질의한 것과 도구가 못 본 정점을 소비자가 구분해야 한다.
func (d *Document) VertexByID(id string) (*Vertex, bool) {
	for i := range d.Vertices {
		if d.Vertices[i].ID == id {
			return &d.Vertices[i], true
		}
	}
	return nil, false
}

// HasVertex는 정점 존재 여부만 빠르게 확인한다.
func (d *Document) HasVertex(id string) bool {
	_, ok := d.VertexByID(id)
	return ok
}

// VertexIndex는 정점 ID → 정점 색인이다. 같은 ID의 정점이 여럿이면 VertexByID처럼 앞의 것을 담는다.
// 정점 존재나 속성을 여러 번 묻는 질의는 이 색인을 한 번 만들어 쓴다 — 물을 때마다 VertexByID로
// 정점 목록을 훑으면 비용이 묻는 수 × 정점 수가 되고, --tests 문서의 보존 루트는 수만 개다.
// 값은 d.Vertices의 원소를 가리킨다 — 정점 목록을 바꾸면(Sort 포함) 색인을 다시 만든다.
func VertexIndex(d *Document) map[string]*Vertex {
	out := make(map[string]*Vertex, len(d.Vertices))
	// 뒤에서부터 덮어써 같은 ID 중 앞의 정점이 남게 한다.
	for i := len(d.Vertices) - 1; i >= 0; i-- {
		out[d.Vertices[i].ID] = &d.Vertices[i]
	}
	return out
}

// quote는 에러 메시지 안의 사용자 입력을 안전하게 감싼다.
func quote(s string) string {
	var b strings.Builder
	b.WriteString(strconv.Quote(s))
	return b.String()
}
