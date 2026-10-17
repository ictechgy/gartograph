package analysis

import (
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// doc은 테스트용 그래프다: a→b→c→a 순환, d→a 비순환, e 단독.
func doc() *graph.Document {
	return &graph.Document{
		Vertices: []graph.Vertex{
			{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"},
		},
		Edges: []graph.Edge{
			{From: "a", To: "b", Kind: graph.EdgeImport},
			{From: "b", To: "c", Kind: graph.EdgeImport},
			{From: "c", To: "a", Kind: graph.EdgeImport},
			{From: "d", To: "a", Kind: graph.EdgeImport},
			{From: "e", To: "e", Kind: graph.EdgeImport},
		},
	}
}

// TestCycles는 SCC 순환과 자기루프를 찾고 비순환 정점을 제외하는지 확인한다.
// 자기루프 없는 단일 정점을 순환으로 보고하는 것은 오탐이라 회귀를 막는다.
func TestCycles(t *testing.T) {
	cycles := Cycles(doc())
	if len(cycles) != 2 {
		t.Fatalf("expected 2 cycles, got %d: %+v", len(cycles), cycles)
	}
	found := map[string]bool{}
	for _, c := range cycles {
		for _, m := range c.Members {
			found[m] = true
		}
	}
	for _, want := range []string{"a", "b", "c", "e"} {
		if !found[want] {
			t.Fatalf("expected %s in a cycle: %+v", want, cycles)
		}
	}
	if found["d"] {
		t.Fatalf("d is not in a cycle: %+v", cycles)
	}
	// evidence 간선이 실제로 실렸는지 확인한다.
	if len(cycles[0].Edges) == 0 || len(cycles[1].Edges) == 0 {
		t.Fatalf("cycle without evidence edges: %+v", cycles)
	}
}

// TestCycleEvidenceDistinct는 순환 증거 간선이 (from,to,kind)마다 한 번만 실리는지
// 확인한다. 같은 쌍에 call과 references가 함께 있으면(Go 호출에서 흔하다) 이웃 목록의
// 중복 to마다 종류를 다시 붙여 증거가 종류 수만큼 부풀었다 — 고칠 간선 수를 세는
// 소비자가 두 배로 센다. 증거 간선에는 위치를 싣지 않는다(cycles JSON·baseline 계약).
func TestCycleEvidenceDistinct(t *testing.T) {
	d := &graph.Document{
		Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "m.A", Kind: graph.KindFunc}, {ID: "m.B", Kind: graph.KindFunc},
			{ID: "m.C", Kind: graph.KindFunc},
		},
		Edges: []graph.Edge{
			{From: "m.A", To: "m.B", Kind: graph.EdgeReferences, Positions: []graph.Position{
				{File: "/m/a.go", Line: 9, Column: 2}, {File: "/m/a.go", Line: 4, Column: 2}}},
			{From: "m.B", To: "m.A", Kind: graph.EdgeCall, Positions: []graph.Position{
				{File: "/m/b.go", Line: 3}}},
			{From: "m.B", To: "m.A", Kind: graph.EdgeReferences},
			// 자기 루프에 종류가 둘이면 같은 증거가 네 번 실렸다.
			{From: "m.C", To: "m.C", Kind: graph.EdgeCall},
			{From: "m.C", To: "m.C", Kind: graph.EdgeReferences},
		},
	}
	view, err := d.View(graph.LevelSymbol)
	if err != nil {
		t.Fatal(err)
	}
	cycles := Cycles(view)
	wantEdges := [][]graph.Edge{
		{
			{From: "m.A", To: "m.B", Kind: graph.EdgeReferences},
			{From: "m.B", To: "m.A", Kind: graph.EdgeCall},
			{From: "m.B", To: "m.A", Kind: graph.EdgeReferences},
		},
		{
			{From: "m.C", To: "m.C", Kind: graph.EdgeCall},
			{From: "m.C", To: "m.C", Kind: graph.EdgeReferences},
		},
	}
	if len(cycles) != len(wantEdges) {
		t.Fatalf("expected %d cycles, got %+v", len(wantEdges), cycles)
	}
	for i, c := range cycles {
		if !reflect.DeepEqual(c.Edges, wantEdges[i]) {
			t.Fatalf("cycle %v evidence must name each relation once:\ngot  %+v\nwant %+v",
				c.Members, c.Edges, wantEdges[i])
		}
	}
	// SARIF 물리 위치는 처음으로 위치가 있는 증거 간선(A→B)의 첫 지점이다 — 위치가
	// 없는 순환(C 자기 루프)은 지어내지 않고 비워 둔다.
	want := &graph.Position{File: "/m/a.go", Line: 4, Column: 2}
	if !reflect.DeepEqual(cycles[0].Position, want) || cycles[1].Position != nil {
		t.Fatalf("cycle positions: got %+v and %+v, want %+v and none",
			cycles[0].Position, cycles[1].Position, want)
	}
	data, err := json.Marshal(cycles)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "positions") || strings.Contains(string(data), "/m/") {
		t.Fatalf("cycles JSON must not start carrying positions: %s", data)
	}
}

// TestCyclesMatchesEdgeScan은 무작위 문서에서 Cycles가 이전 구현(edgeScanCycles)과 같은
// 순환을 찾고, 증거 간선은 이전 증거에서 중복과 contains만 뺀 것과 같은지 확인한다 —
// 증거를 문서 간선 한 번 훑기로 모으게 한 변경은 중복 제거 말고는 아무것도 바꾸면 안 된다.
func TestCyclesMatchesEdgeScan(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	sawDuplicate := false
	for trial := 0; trial < 500; trial++ {
		d := randomDependencyDocument(r)
		got, want := Cycles(d), edgeScanCycles(d)
		if len(got) != len(want) {
			t.Fatalf("trial %d: %d cycles, want %d\nedges %+v", trial, len(got), len(want), d.Edges)
		}
		for i := range want {
			distinct := distinctDependencyEdges(want[i].Edges)
			sawDuplicate = sawDuplicate || len(distinct) < len(want[i].Edges)
			if !reflect.DeepEqual(got[i].Members, want[i].Members) ||
				!reflect.DeepEqual(got[i].Edges, distinct) {
				t.Fatalf("trial %d cycle %d diverged:\ngot  %+v\nwant %+v %+v\nedges %+v",
					trial, i, got[i], want[i].Members, distinct, d.Edges)
			}
		}
	}
	if !sawDuplicate {
		t.Fatal("random documents must exercise duplicated evidence")
	}
}

// distinctDependencyEdges는 (from,to,kind) 순으로 정렬된 증거에서 contains와 연속 중복을 뺀다.
func distinctDependencyEdges(edges []graph.Edge) []graph.Edge {
	var out []graph.Edge
	for _, e := range edges {
		if e.Kind == graph.EdgeContains {
			continue
		}
		if n := len(out); n > 0 && out[n-1].From == e.From && out[n-1].To == e.To && out[n-1].Kind == e.Kind {
			continue
		}
		out = append(out, e)
	}
	return out
}

// edgeScanCycles는 증거를 SCC 멤버의 이웃마다 EdgeKinds로 모으던 이전 Cycles다 —
// 순환 멤버와 증거의 대조 기준이라 느리고 중복이 있어도 그대로 둔다.
func edgeScanCycles(d *graph.Document) []Cycle {
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
		if low[v] != index[v] {
			return
		}
		var members []string
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			members = append(members, w)
			if w == v {
				break
			}
		}
		if len(members) == 1 && !hasSelfLoop(adj, members[0]) {
			return
		}
		sort.Strings(members)
		cycles = append(cycles, Cycle{Members: members, Edges: edgeScanCycleEdges(members, adj, d)})
	}
	for _, vtx := range d.Vertices {
		if _, seen := index[vtx.ID]; !seen {
			strongConnect(vtx.ID)
		}
	}
	sort.Slice(cycles, func(i, j int) bool { return cycles[i].Members[0] < cycles[j].Members[0] })
	return cycles
}

// edgeScanCycleEdges는 이전 cycleEdges다 — 멤버의 이웃(종류마다 중복)마다 EdgeKinds를 붙인다.
func edgeScanCycleEdges(members []string, adj map[string][]string, d *graph.Document) []graph.Edge {
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

// BenchmarkCycles는 약 3만 정점 합성 문서의 심볼 뷰에서 순환을 찾는다 — 큰 SCC의
// 이웃마다 간선 전체를 훑던 증거 수집은 이 규모에서 호출당 수십 초가 걸렸다.
func BenchmarkCycles(b *testing.B) {
	view, err := syntheticSymbolDocument(300, 100, 250).View(graph.LevelSymbol)
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if len(Cycles(view)) == 0 {
			b.Fatal("synthetic document must contain cycles")
		}
	}
}

// TestQueryNotFound는 없는 정점 질의가 ErrNotFound로 구분되는지 확인한다.
func TestQueryNotFound(t *testing.T) {
	_, err := Query(doc(), "missing", 1, 0)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestQueryDepth는 depth별 이웃 범위를 확인한다.
func TestQueryDepth(t *testing.T) {
	res, err := Query(doc(), "a", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DependsOn) != 1 || res.DependsOn[0].ID != "b" {
		t.Fatalf("depth1 dependsOn: %+v", res.DependsOn)
	}
	if len(res.DependedBy) != 2 { // c, d
		t.Fatalf("depth1 dependedBy: %+v", res.DependedBy)
	}
	res2, err := Query(doc(), "d", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	// d→a→b: depth2면 b까지 도달한다.
	ids := map[string]bool{}
	for _, n := range res2.DependsOn {
		ids[n.ID] = true
	}
	if !ids["a"] || !ids["b"] {
		t.Fatalf("depth2 dependsOn missing: %+v", res2.DependsOn)
	}
}

// TestQueryTruncated는 잘림이 조용히 버려지지 않고 표시되는지 확인한다.
func TestQueryTruncated(t *testing.T) {
	res, err := Query(doc(), "a", 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated result")
	}
}

// TestNeighborKinds는 이웃 간선 종류가 모두 실리는지 확인한다.
// 관계를 하나만 고르면 나머지 사실이 소비자에게 사라진다.
func TestNeighborKinds(t *testing.T) {
	d := &graph.Document{
		Vertices: []graph.Vertex{{ID: "a"}, {ID: "b"}},
		Edges: []graph.Edge{
			{From: "a", To: "b", Kind: graph.EdgeImport},
			{From: "a", To: "b", Kind: graph.EdgeCall},
		},
	}
	res, err := Query(d, "a", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DependsOn[0].Kinds) != 2 {
		t.Fatalf("expected both kinds reported, got %+v", res.DependsOn[0].Kinds)
	}
}
