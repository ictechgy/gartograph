package graph

import (
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

// quote는 에러 메시지 안의 사용자 입력을 안전하게 감싼다.
func quote(s string) string {
	var b strings.Builder
	b.WriteString(strconv.Quote(s))
	return b.String()
}
