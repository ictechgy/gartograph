package analysis

import "github.com/ictechgy/gartograph/graph"

// Index는 바뀌지 않는 문서 하나 위의 질의 색인이다. 정점 ID 색인, 의존 인접 맵, 종류 포함 이웃
// 목록, 도달성 인접 맵, 살아 있는 문서 루트를 처음 필요할 때 한 번만 만들고, 같은 Index로 묻는 다음
// 질의는 그것을 그대로 쓴다.
// MCP 서버처럼 한 스냅샷에 질의를 거듭하는 쪽을 위한 것이다 — 호출마다 다시 만들면 답이 몇 줄이어도
// 호출마다 문서 크기만큼의 비용이 붙는다. 한 번만 묻는 FindImpact·Shared·RetentionRoots는 새 Index로
// 같은 메서드를 타고, Query·Path는 정점을 목록에서 찾되 본체(neighborsOf·shortestPath)를 함께 쓴다 —
// 그래서 색인을 나눠 쓴 답과 한 번만 물은 답이 갈라지지 않는다.
// 색인은 처음 만들 때의 문서를 비춘다 — 문서를 바꿨으면 새 Index를 만든다. 첫 질의가 필드를 채우므로
// 여러 고루틴이 함께 쓰면 안 된다.
type Index struct {
	doc *graph.Document
	// 아래 필드는 처음 필요할 때 채운다. 만든 맵은 비어 있어도 nil이 아니므로 nil이 "아직 안 만듦"이다.
	byID     map[string]*graph.Vertex
	out, in  map[string][]graph.Neighbor
	adj      map[string][]string
	reach    map[string][]string
	rootIDs  []string
	rootsSet bool
}

// NewIndex는 d 위의 질의 색인을 만든다. 실제 색인은 질의가 처음 필요로 할 때 만든다 —
// 도구 하나만 쓰는 세션이 쓰지 않을 인접 맵까지 만들지 않게 한다.
func NewIndex(d *graph.Document) *Index {
	return &Index{doc: d}
}

// vertices는 정점 ID 색인(graph.VertexIndex)이다.
func (x *Index) vertices() map[string]*graph.Vertex {
	if x.byID == nil {
		x.byID = graph.VertexIndex(x.doc)
	}
	return x.byID
}

// outgoing은 나가는 방향의 종류 포함 이웃 목록(graph.KindedAdjacency)이다.
func (x *Index) outgoing() map[string][]graph.Neighbor {
	if x.out == nil {
		x.out = graph.KindedAdjacency(x.doc)
	}
	return x.out
}

// incoming은 들어오는 방향의 종류 포함 이웃 목록(graph.KindedIncoming)이다.
func (x *Index) incoming() map[string][]graph.Neighbor {
	if x.in == nil {
		x.in = graph.KindedIncoming(x.doc)
	}
	return x.in
}

// adjacency는 의존 인접 맵(graph.Adjacency)이다. 이 맵을 쓰는 질의는 목록을 바꾸지 않는다.
func (x *Index) adjacency() map[string][]string {
	if x.adj == nil {
		x.adj = graph.Adjacency(x.doc)
	}
	return x.adj
}

// reachAdjacency는 dead의 도달성 인접 맵(ReachAdjacency)이다. ReachAdjacency는 자기가 만든
// 인접 맵에 외부 디스패치를 덧붙이므로 adjacency와 맵을 나눠 쓰지 않는다 — 나눠 쓰면 path·shared에
// 디스패치 걸음이 섞인다.
func (x *Index) reachAdjacency() map[string][]string {
	if x.reach == nil {
		x.reach = ReachAdjacency(x.doc)
	}
	return x.reach
}

// liveRoots는 문서 루트 중 정점이 있는 것(Document.RootIDs)이다. 돌려준 목록은 바꾸지 않는다.
func (x *Index) liveRoots() []string {
	if !x.rootsSet {
		x.rootIDs, x.rootsSet = x.doc.RootIDs(), true
	}
	return x.rootIDs
}
