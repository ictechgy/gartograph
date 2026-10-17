package graph

import (
	"reflect"
	"testing"
)

// TestSort는 정렬이 결정적이고 중복 간선을 제거하는지 확인한다.
// 출력 결정성은 리포트 diff와 캐시의 기반이라 회귀를 막아야 한다.
func TestSort(t *testing.T) {
	doc := &Document{
		Vertices: []Vertex{{ID: "b"}, {ID: "a"}},
		Edges: []Edge{
			{From: "b", To: "a", Kind: EdgeImport},
			{From: "a", To: "b", Kind: EdgeImport},
			{From: "a", To: "b", Kind: EdgeImport},
		},
	}
	doc.Sort()
	if doc.Vertices[0].ID != "a" {
		t.Fatalf("vertices not sorted: %+v", doc.Vertices)
	}
	if len(doc.Edges) != 2 {
		t.Fatalf("expected 2 deduped edges, got %d", len(doc.Edges))
	}
}

// TestAdjacencyExcludesContains는 contains 간선이 의존 질의에서
// 빠지는지 확인한다 — 소유 관계를 의존으로 세면 가짜 순환이 나온다.
func TestAdjacencyExcludesContains(t *testing.T) {
	doc := &Document{
		Vertices: []Vertex{{ID: "pkg"}, {ID: "sym"}, {ID: "dep"}},
		Edges: []Edge{
			{From: "pkg", To: "sym", Kind: EdgeContains},
			{From: "pkg", To: "dep", Kind: EdgeImport},
		},
	}
	adj := Adjacency(doc)
	if len(adj["pkg"]) != 1 || adj["pkg"][0] != "dep" {
		t.Fatalf("contains edge leaked into adjacency: %v", adj["pkg"])
	}
	inc := Incoming(doc)
	if len(inc["sym"]) != 0 {
		t.Fatalf("contains edge leaked into incoming: %v", inc["sym"])
	}
}

// TestKindedAdjacency는 종류 포함 이웃 목록이 contains를 빼고 (ID, Kind) 순으로
// 정렬돼 한 이웃의 관계 종류가 연속 구간을 이루는지 확인한다. 질의는 이 구간에서
// 두 정점 사이의 관계 종류를 읽으므로, 정렬이 어긋나면 종류가 갈라지거나 사라진다.
// 정렬되지 않은 문서와 중복 간선도 Adjacency·Incoming·EdgeKinds와 같은 사실을 담아야 한다.
func TestKindedAdjacency(t *testing.T) {
	doc := &Document{
		Edges: []Edge{
			{From: "a", To: "c", Kind: EdgeReferences},
			{From: "a", To: "b", Kind: EdgeImport},
			{From: "pkg", To: "a", Kind: EdgeContains},
			{From: "a", To: "c", Kind: EdgeCall},
			{From: "b", To: "c", Kind: EdgeCall},
			{From: "a", To: "a", Kind: EdgeCall},
			{From: "a", To: "c", Kind: EdgeCall},
		},
	}
	out := KindedAdjacency(doc)
	wantOut := map[string][]Neighbor{
		"a": {{ID: "a", Kind: EdgeCall}, {ID: "b", Kind: EdgeImport},
			{ID: "c", Kind: EdgeCall}, {ID: "c", Kind: EdgeCall}, {ID: "c", Kind: EdgeReferences}},
		"b": {{ID: "c", Kind: EdgeCall}},
	}
	if !reflect.DeepEqual(out, wantOut) {
		t.Fatalf("outgoing kinded adjacency:\ngot  %v\nwant %v", out, wantOut)
	}
	in := KindedIncoming(doc)
	wantIn := map[string][]Neighbor{
		"a": {{ID: "a", Kind: EdgeCall}},
		"b": {{ID: "a", Kind: EdgeImport}},
		"c": {{ID: "a", Kind: EdgeCall}, {ID: "a", Kind: EdgeCall},
			{ID: "a", Kind: EdgeReferences}, {ID: "b", Kind: EdgeCall}},
	}
	if !reflect.DeepEqual(in, wantIn) {
		t.Fatalf("incoming kinded adjacency:\ngot  %v\nwant %v", in, wantIn)
	}
	// 이웃 순서는 Adjacency·Incoming과 같아야 기존 질의의 출력 순서가 그대로다.
	adj, inc := Adjacency(doc), Incoming(doc)
	for key, ns := range out {
		if ids := neighborIDs(ns); !reflect.DeepEqual(ids, adj[key]) {
			t.Fatalf("outgoing order of %s differs from Adjacency: %v", key, ids)
		}
	}
	for key, ns := range in {
		if ids := neighborIDs(ns); !reflect.DeepEqual(ids, inc[key]) {
			t.Fatalf("incoming order of %s differs from Incoming: %v", key, ids)
		}
	}
}

// neighborIDs는 종류 포함 이웃 목록에서 ID만 꺼낸다.
func neighborIDs(ns []Neighbor) []string {
	ids := make([]string, len(ns))
	for i, n := range ns {
		ids[i] = n.ID
	}
	return ids
}

// TestEdgeKinds는 한 쌍의 모든 관계 종류가 돌아오는지 확인한다.
func TestEdgeKinds(t *testing.T) {
	doc := &Document{
		Edges: []Edge{
			{From: "a", To: "b", Kind: EdgeImport},
			{From: "a", To: "b", Kind: EdgeCall},
			{From: "a", To: "c", Kind: EdgeImport},
		},
	}
	kinds := EdgeKinds(doc, "a", "b")
	if len(kinds) != 2 || kinds[0] != EdgeCall || kinds[1] != EdgeImport {
		t.Fatalf("expected [call import], got %v", kinds)
	}
}

// TestSortEdgePositions는 같은 관계의 간선이 하나로 합쳐지고
// 사용 지점이 정렬·중복 제거되는지 확인한다 — v2의 "관계가 단위,
// 지점은 집합" 계약이다.
func TestSortEdgePositions(t *testing.T) {
	doc := &Document{
		Edges: []Edge{
			{From: "a", To: "b", Kind: EdgeCall, Positions: []Position{
				{File: "x.go", Line: 9}, {File: "x.go", Line: 2},
			}},
			{From: "a", To: "b", Kind: EdgeCall, Positions: []Position{
				{File: "x.go", Line: 5}, {File: "x.go", Line: 2},
			}},
			{From: "a", To: "b", Kind: EdgeCall},
		},
	}
	doc.Sort()
	if len(doc.Edges) != 1 {
		t.Fatalf("same relation must merge into one edge: %+v", doc.Edges)
	}
	got := doc.Edges[0].Positions
	want := []Position{
		{File: "x.go", Line: 2}, {File: "x.go", Line: 5}, {File: "x.go", Line: 9},
	}
	if len(got) != len(want) {
		t.Fatalf("positions must merge and dedupe: %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("positions not sorted: %+v", got)
		}
	}
}

// TestLimitation은 limitation이 문서에 순서대로 기록되는지 확인한다 —
// "알릴 것이 없으면 조용하다"의 대칭으로, 알릴 것은 실제로 남아야 한다.
func TestLimitation(t *testing.T) {
	d := &Document{}
	if len(d.Limitations) != 0 {
		t.Fatal("fresh document must have no limitations")
	}
	d.Limitation("a")
	d.Limitation("b")
	if len(d.Limitations) != 2 || d.Limitations[0] != "a" || d.Limitations[1] != "b" {
		t.Fatalf("limitations must record in order: %v", d.Limitations)
	}
}

// TestVertexByID는 정점 조회와 미존재 구분을 확인한다.
func TestVertexByID(t *testing.T) {
	doc := &Document{Vertices: []Vertex{{ID: "x", Kind: KindFunc}}}
	if _, ok := doc.VertexByID("x"); !ok {
		t.Fatal("expected vertex x")
	}
	if doc.HasVertex("y") {
		t.Fatal("unexpected vertex y")
	}
}

// TestSortMergesCandidate는 같은 관계를 합칠 때 한 지점이라도 확정이면 확정으로
// 남는지 확인한다 — 팬아웃 표시가 확정 지점을 덮으면 근거 등급이 약해진다.
func TestSortMergesCandidate(t *testing.T) {
	doc := &Document{Edges: []Edge{
		{From: "a", To: "b", Kind: EdgeCall, Candidate: true},
		{From: "a", To: "b", Kind: EdgeCall},
		{From: "a", To: "c", Kind: EdgeCall, Candidate: true},
		{From: "a", To: "c", Kind: EdgeCall, Candidate: true},
	}}
	doc.Sort()
	if len(doc.Edges) != 2 || doc.Edges[0].Candidate || !doc.Edges[1].Candidate {
		t.Fatalf("candidate merge wrong: %+v", doc.Edges)
	}
}
