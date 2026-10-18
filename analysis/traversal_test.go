package analysis

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// oracleAdj는 구현과 독립적인 쌍 수준 인접 목록이다(순회 방향 기준).
type oracleAdj struct {
	next   map[string][]string
	direct map[string][]string
	kinds  map[[2]string][]graph.EdgeKind
}

// newOracleAdj는 문서의 의존 간선(contains 제외)으로 인접 목록을 만든다.
func newOracleAdj(d *graph.Document, direction string) oracleAdj {
	o := oracleAdj{next: map[string][]string{}, direct: map[string][]string{},
		kinds: map[[2]string][]graph.EdgeKind{}}
	isDirect := map[[2]string]bool{}
	for _, e := range d.Edges {
		if e.Kind == graph.EdgeContains {
			continue
		}
		k := [2]string{e.From, e.To}
		if direction == DirectionDependents {
			k = [2]string{e.To, e.From}
		}
		if _, ok := o.kinds[k]; !ok {
			o.next[k[0]] = append(o.next[k[0]], k[1])
		}
		o.kinds[k] = mergeKinds(o.kinds[k], []graph.EdgeKind{e.Kind})
		isDirect[k] = isDirect[k] || !e.Candidate
	}
	for k, ok := range isDirect {
		if ok {
			o.direct[k[0]] = append(o.direct[k[0]], k[1])
		}
	}
	return o
}

// bfs는 root 하나의 깊이 제한 없는 최단 거리다.
func bfs(next map[string][]string, root string) map[string]int {
	dist := map[string]int{root: 0}
	queue := []string{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range next[cur] {
			if _, ok := dist[nb]; !ok {
				dist[nb] = dist[cur] + 1
				queue = append(queue, nb)
			}
		}
	}
	return dist
}

// oracleTraverse는 root마다 따로 BFS를 돌려 계약의 정의를 그대로 계산한다.
func oracleTraverse(d *graph.Document, req TraversalRequest) (*TraversalResult, bool) {
	o := newOracleAdj(d, req.Direction)
	full := make([]map[string]int, len(req.Roots))
	direct := make([]map[string]int, len(req.Roots))
	for i, r := range req.Roots {
		full[i] = bfs(o.next, r)
		direct[i] = bfs(o.direct, r)
	}
	nodes := map[string]bool{}
	for _, dist := range full {
		for n := range dist {
			nodes[n] = true
		}
	}
	res := &TraversalResult{EvidenceClassified: d.DispatchEvidence}
	depthCut := false
	for node := range nodes {
		var idx []int
		for i, r := range req.Roots {
			dist, ok := full[i][node]
			if ok && dist == req.MaxDepth+1 {
				depthCut = true
			}
			if ok && node != r && dist >= 1 && dist <= req.MaxDepth {
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 {
			continue
		}
		depth, nearest := full[idx[0]][node], idx[0]
		evidence := EvidenceDirect
		for _, i := range idx {
			if full[i][node] < depth {
				depth, nearest = full[i][node], i
			}
			if dd, ok := direct[i][node]; !ok || dd > req.MaxDepth {
				evidence = EvidenceCandidate
			}
		}
		var preds []string
		for from, tos := range o.next {
			if slices.Contains(tos, node) {
				if dist, ok := full[nearest][from]; ok && dist == depth-1 {
					preds = append(preds, from)
				}
			}
		}
		sort.Slice(preds, func(a, b int) bool { return utf16Less(preds[a], preds[b]) })
		row := TraversalReached{ID: node, Via: preds[0], Depth: depth, Roots: idx,
			Relationships: o.kinds[[2]string{preds[0], node}]}
		if len(row.Roots) > MaxRootsPerReached {
			row.Roots = row.Roots[:MaxRootsPerReached]
			res.RootsTruncated = true
		}
		if res.EvidenceClassified {
			row.Evidence = evidence
		}
		res.Reached = append(res.Reached, row)
	}
	sort.Slice(res.Reached, func(a, b int) bool {
		if res.Reached[a].Depth != res.Reached[b].Depth {
			return res.Reached[a].Depth < res.Reached[b].Depth
		}
		return utf16Less(res.Reached[a].ID, res.Reached[b].ID)
	})
	return res, depthCut
}

// randomTraversalDoc는 무작위 그래프다. ID에 보조 평면 문자와 U+E000대 문자를 섞어
// UTF-16 순서가 바이트 순서와 갈리는 경우를 만든다.
func randomTraversalDoc(rng *rand.Rand, n, m int) *graph.Document {
	names := []string{"a", "b", "Z", "é", "\U0001F600", "\uE000", "\uFFFD", "a/b.(T).M"}
	d := &graph.Document{DispatchEvidence: rng.Intn(5) != 0}
	for i := 0; i < n; i++ {
		d.Vertices = append(d.Vertices, graph.Vertex{ID: fmt.Sprintf("%s%d", names[rng.Intn(len(names))], i)})
	}
	kinds := []graph.EdgeKind{graph.EdgeCall, graph.EdgeReferences, graph.EdgeSignature, graph.EdgeContains}
	for i := 0; i < m; i++ {
		d.Edges = append(d.Edges, graph.Edge{
			From:      d.Vertices[rng.Intn(n)].ID,
			To:        d.Vertices[rng.Intn(n)].ID,
			Kind:      kinds[rng.Intn(len(kinds))],
			Candidate: rng.Intn(3) == 0,
		})
	}
	return d
}

// randomRoots는 정점과 정점이 아닌 id를 섞은 유일한 root 목록이다.
func randomRoots(rng *rand.Rand, d *graph.Document, k int) []string {
	seen := map[string]bool{}
	var roots []string
	for len(roots) < k {
		id := d.Vertices[rng.Intn(len(d.Vertices))].ID
		if rng.Intn(15) == 0 {
			id = fmt.Sprintf("missing%d", rng.Intn(5))
		}
		if !seen[id] {
			seen[id] = true
			roots = append(roots, id)
		}
	}
	return roots
}

// TestTraverseOracle은 다중 root 한 번 순회를 root별 BFS 정의와 무작위 그래프에서 대조한다.
func TestTraverseOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	pruned, candidates := 0, 0
	for iter := 0; iter < 600; iter++ {
		n := 2 + rng.Intn(40)
		d := randomTraversalDoc(rng, n, rng.Intn(4*n+1))
		k := 1 + rng.Intn(min(n, 8))
		if iter%10 == 0 {
			// root를 70개 넘게 줘 65-root 전파 중단과 rootsTruncated를 연다.
			d = randomTraversalDoc(rng, 120, 900)
			k = 70 + rng.Intn(40)
		}
		depths := []int{1, 2, 3, 5, MaxTraversalDepth}
		req := TraversalRequest{Roots: randomRoots(rng, d, k), MaxDepth: depths[rng.Intn(len(depths))],
			MaxReached: MaxTraversalReached, Direction: DirectionDependencies}
		if rng.Intn(2) == 0 {
			req.Direction = DirectionDependents
		}
		got := Traverse(d, req)
		want, depthCut := oracleTraverse(d, req)
		if got.RootsTruncated {
			pruned++
		}
		for _, r := range got.Reached {
			if r.Evidence == EvidenceCandidate {
				candidates++
			}
		}
		compareTraversal(t, iter, got, want)
		gotCut := slices.Contains(got.TruncationReasons, "depth")
		// 전파 중단은 출력에 보이지 않는 큰 인덱스 root의 잘림을 볼 수 없다.
		if gotCut != depthCut && !(depthCut && got.RootsTruncated) {
			t.Fatalf("iter %d: depth cut = %v, oracle %v", iter, gotCut, depthCut)
		}
	}
	// 대조가 공허하지 않도록 전파 중단·candidate 경로가 실제로 열렸는지 확인한다.
	if pruned == 0 || candidates == 0 {
		t.Fatalf("oracle never exercised pruning (%d) or candidate evidence (%d)", pruned, candidates)
	}
}

// hubTraversalDoc는 root n개(R00000…)가 모두 허브 X를 부르고 X가 함수 m개(C0000…)를 부르는
// 문서와 ID 내림차순 root 목록이다. 경계는 정점 ID 순으로 훑으므로 X에는 큰 인덱스 root부터
// 도착한다 — 도착 순서대로 받아들이면 65-root 전파 중단이 한 번도 걸리지 않는 순서다.
func hubTraversalDoc(n, m int) (*graph.Document, []string) {
	d := &graph.Document{}
	roots := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("R%05d", i)
		roots = append(roots, id)
		d.Edges = append(d.Edges, graph.Edge{From: id, To: "X", Kind: graph.EdgeCall})
	}
	for i := 0; i < m; i++ {
		d.Edges = append(d.Edges, graph.Edge{From: "X", To: fmt.Sprintf("C%04d", i), Kind: graph.EdgeCall})
	}
	slices.Reverse(roots)
	return d, roots
}

// TestTraverseRootOrderMatchesOracle은 root 입력 순서를 ID 오름차순·내림차순·무작위로 바꿔도 결과가
// root별 BFS 정의와 같은지 본다. 65-root 전파 중단은 인덱스 순서와 도착 순서(경계 정점 ID 순)가
// 엇갈릴 때 갈리므로 순서마다 따로 대조한다.
func TestTraverseRootOrderMatchesOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	orders := []struct {
		name  string
		apply func([]string)
	}{
		{"ascending", func(r []string) { slices.Sort(r) }},
		{"descending", func(r []string) { slices.Sort(r); slices.Reverse(r) }},
		{"random", func(r []string) { rng.Shuffle(len(r), func(i, j int) { r[i], r[j] = r[j], r[i] }) }},
	}
	pruned := 0
	for iter := 0; iter < 60; iter++ {
		d := randomTraversalDoc(rng, 120, 900)
		base := randomRoots(rng, d, 70+rng.Intn(40))
		if iter%4 == 0 {
			d, base = hubTraversalDoc(80+rng.Intn(60), 1+rng.Intn(20))
		}
		depths := []int{1, 2, 3, MaxTraversalDepth}
		maxDepth := depths[rng.Intn(len(depths))]
		for _, order := range orders {
			roots := slices.Clone(base)
			order.apply(roots)
			req := TraversalRequest{Roots: roots, MaxDepth: maxDepth, MaxReached: MaxTraversalReached,
				Direction: DirectionDependencies}
			if iter%2 == 1 {
				req.Direction = DirectionDependents
			}
			got := Traverse(d, req)
			want, depthCut := oracleTraverse(d, req)
			if got.RootsTruncated {
				pruned++
			}
			t.Run(fmt.Sprintf("%d/%s", iter, order.name), func(t *testing.T) {
				compareTraversal(t, iter, got, want)
				gotCut := slices.Contains(got.TruncationReasons, "depth")
				if gotCut != depthCut && !(depthCut && got.RootsTruncated) {
					t.Fatalf("depth cut = %v, oracle %v", gotCut, depthCut)
				}
			})
		}
	}
	if pruned == 0 {
		t.Fatal("no case exercised the 65-root propagation stop")
	}
}

// TestTraverseHubBoundsHeldRoots는 큰 인덱스 root가 먼저 도착하는 허브(root 4000개가 X를 부르고 X가
// 함수 200개를 부른다, root는 ID 내림차순)에서도 정점이 가진 root가 65개 남짓으로 묶이는지 본다.
// 도착한 순서대로 받아들이면 X와 그 아래 200개가 root 4000개를 모두 가진다 — 출력은 작은 인덱스
// 64개뿐인데 시간은 root 수의 제곱, 메모리는 root 수에 비례한다(root 순서를 고른 분석 대상이 일으킨다).
// 보유 수가 그 비용을 정하므로 시간 대신 보유 수를 단언한다.
func TestTraverseHubBoundsHeldRoots(t *testing.T) {
	d, roots := hubTraversalDoc(4000, 200)
	adj, _ := buildTraversalAdjacency(d, DirectionDependencies, false, "")
	p := propagate(adj, roots, MaxTraversalDepth)
	for v, held := range p.levels {
		// 허브의 정점은 한 단계에서만 root를 받는다 — 단계마다 받아들이는 수의 상한이 곧 전체 상한이다.
		if len(held) > dominatingRoots+1 {
			t.Fatalf("%s holds %d roots; a step must accept arriving roots in index order and stop "+
				"after %d smaller ones", v, len(held), dominatingRoots)
		}
	}
	res := Traverse(d, TraversalRequest{Roots: roots, Direction: DirectionDependencies,
		MaxDepth: MaxTraversalDepth, MaxReached: MaxTraversalReached})
	smallest := make([]int, MaxRootsPerReached)
	for i := range smallest {
		smallest[i] = i
	}
	if len(res.Reached) != 201 || !res.RootsTruncated || len(res.TruncationReasons) != 0 {
		t.Fatalf("reached %d, rootsTruncated %v, reasons %v", len(res.Reached), res.RootsTruncated,
			res.TruncationReasons)
	}
	for _, r := range res.Reached {
		wantDepth, wantVia := 2, "X"
		if r.ID == "X" {
			// 모든 root가 1걸음이라 가장 가까운 root는 인덱스 0(가장 큰 ID)이다.
			wantDepth, wantVia = 1, roots[0]
		}
		if r.Depth != wantDepth || r.Via != wantVia || !slices.Equal(r.Roots, smallest) {
			t.Fatalf("row %+v, want depth %d via %s and the 64 smallest root indices", r, wantDepth, wantVia)
		}
	}
}

// TestTraverseDepthCutIgnoresLateArrival은 최단 경로에서 전파가 멈춘 큰 인덱스 root가 더 긴 경로로
// 마지막 단계에 늦게 도착해도 depth 잘림으로 세지 않는지 본다. root S00…S64(0~64)와 T(65)가 모두 W를
// 부르면 W에서 T가 막힌다. W→S00에서 S00은 자기 인덱스 0을 빼면 더 작은 root가 64개뿐이다. T는 T→A→B→U→S00으로
// 3단계에 U에 닿는다. 깊이 3에서 T는 S00에 이미 2걸음에 닿으므로(T→W→S00) 4걸음째 정점은 없다.
func TestTraverseDepthCutIgnoresLateArrival(t *testing.T) {
	d := &graph.Document{}
	var roots []string
	for i := 0; i <= 64; i++ {
		id := fmt.Sprintf("S%02d", i)
		roots = append(roots, id)
		d.Edges = append(d.Edges, graph.Edge{From: id, To: "W", Kind: graph.EdgeCall})
	}
	roots = append(roots, "T")
	for _, e := range [][2]string{{"T", "W"}, {"W", "S00"}, {"T", "A"}, {"A", "B"}, {"B", "U"}, {"U", "S00"}} {
		d.Edges = append(d.Edges, graph.Edge{From: e[0], To: e[1], Kind: graph.EdgeCall})
	}
	req := TraversalRequest{Roots: roots, Direction: DirectionDependencies, MaxDepth: 3,
		MaxReached: MaxTraversalReached}
	got := Traverse(d, req)
	want, depthCut := oracleTraverse(d, req)
	compareTraversal(t, 0, got, want)
	if depthCut || slices.Contains(got.TruncationReasons, "depth") {
		t.Fatalf("no symbol is 4 steps from a root, but reasons = %v (oracle cut %v)", got.TruncationReasons, depthCut)
	}
}

// compareTraversal은 두 결과의 도달 행을 필드별로 대조한다.
func compareTraversal(t *testing.T, iter int, got, want *TraversalResult) {
	t.Helper()
	if len(got.Reached) != len(want.Reached) {
		t.Fatalf("iter %d: reached %d rows, oracle %d", iter, len(got.Reached), len(want.Reached))
	}
	for i := range want.Reached {
		g, w := got.Reached[i], want.Reached[i]
		if g.ID != w.ID || g.Via != w.Via || g.Depth != w.Depth || !slices.Equal(g.Roots, w.Roots) ||
			!slices.Equal(g.Relationships, w.Relationships) || g.Evidence != w.Evidence {
			t.Fatalf("iter %d row %d:\n got  %+v\n want %+v", iter, i, g, w)
		}
	}
	if got.RootsTruncated != want.RootsTruncated {
		t.Fatalf("iter %d: rootsTruncated = %v, oracle %v", iter, got.RootsTruncated, want.RootsTruncated)
	}
}

// TestTraverseEvidenceApproximation은 메모리 상한 근사가 등급을 부풀리지 않는지 본다 —
// 정확한 등급이 candidate인 정점은 근사에서도 candidate여야 한다.
func TestTraverseEvidenceApproximation(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	approximated := 0
	for iter := 0; iter < 200; iter++ {
		d := randomTraversalDoc(rng, 30, 90)
		d.DispatchEvidence = true
		req := TraversalRequest{Roots: randomRoots(rng, d, 5), MaxDepth: MaxTraversalDepth,
			MaxReached: MaxTraversalReached, Direction: DirectionDependencies}
		exact := Traverse(d, req)
		req.evidenceMemory = 1
		approx := Traverse(d, req)
		if approx.EvidenceApproximated {
			approximated++
		}
		for i := range exact.Reached {
			if exact.Reached[i].Evidence == EvidenceCandidate && approx.Reached[i].Evidence != EvidenceCandidate {
				t.Fatalf("iter %d: approximation inflated %s to %s", iter, exact.Reached[i].ID,
					approx.Reached[i].Evidence)
			}
		}
	}
	if approximated == 0 {
		t.Fatal("tiny memory budget must take the approximation path")
	}
}

// TestTraverseSpecExamples는 LANGUAGE-TRAVERSAL 문서의 예시를 그대로 확인한다.
func TestTraverseSpecExamples(t *testing.T) {
	t.Run("다른 root에서 닿은 root", func(t *testing.T) {
		// root A(0)·B(1), B가 A의 의존자, C가 B의 의존자.
		d := &graph.Document{Edges: []graph.Edge{
			{From: "B", To: "A", Kind: graph.EdgeCall}, {From: "C", To: "B", Kind: graph.EdgeCall},
		}}
		res := Traverse(d, TraversalRequest{Roots: []string{"A", "B"}, Direction: DirectionDependents,
			MaxDepth: MaxTraversalDepth, MaxReached: MaxTraversalReached})
		want := []TraversalReached{
			{ID: "B", Via: "A", Depth: 1, Roots: []int{0}, Relationships: []graph.EdgeKind{graph.EdgeCall}},
			{ID: "C", Via: "B", Depth: 1, Roots: []int{0, 1}, Relationships: []graph.EdgeKind{graph.EdgeCall}},
		}
		compareTraversal(t, 0, res, &TraversalResult{Reached: want})
	})
	t.Run("순환 목격", func(t *testing.T) {
		// root A(0)·R(1), 간선 A→W→V→R과 R→V.
		d := &graph.Document{Edges: []graph.Edge{
			{From: "A", To: "W", Kind: graph.EdgeCall}, {From: "W", To: "V", Kind: graph.EdgeCall},
			{From: "V", To: "R", Kind: graph.EdgeCall}, {From: "R", To: "V", Kind: graph.EdgeCall},
		}}
		res := Traverse(d, TraversalRequest{Roots: []string{"A", "R"}, Direction: DirectionDependencies,
			MaxDepth: MaxTraversalDepth, MaxReached: MaxTraversalReached})
		byID := map[string]TraversalReached{}
		for _, r := range res.Reached {
			byID[r.ID] = r
		}
		if v := byID["V"]; v.Via != "R" || v.Depth != 1 || !slices.Equal(v.Roots, []int{0, 1}) {
			t.Fatalf("V = %+v", v)
		}
		if r := byID["R"]; r.Via != "V" || r.Depth != 3 || !slices.Equal(r.Roots, []int{0}) {
			t.Fatalf("R = %+v", r)
		}
	})
	t.Run("자기 자신에서만 닿는 root는 싣지 않는다", func(t *testing.T) {
		d := &graph.Document{Edges: []graph.Edge{
			{From: "A", To: "B", Kind: graph.EdgeCall}, {From: "B", To: "A", Kind: graph.EdgeCall},
		}}
		res := Traverse(d, TraversalRequest{Roots: []string{"A"}, Direction: DirectionDependencies,
			MaxDepth: MaxTraversalDepth, MaxReached: MaxTraversalReached})
		if len(res.Reached) != 1 || res.Reached[0].ID != "B" {
			t.Fatalf("reached = %+v", res.Reached)
		}
	})
}

// TestTraverseTruncation은 depth·max-reached 잘림 이유와 표시 없는 문서의 등급 생략을 본다.
func TestTraverseTruncation(t *testing.T) {
	d := &graph.Document{Edges: []graph.Edge{
		{From: "a", To: "b", Kind: graph.EdgeCall}, {From: "b", To: "c", Kind: graph.EdgeCall},
		{From: "c", To: "d", Kind: graph.EdgeCall, Candidate: true},
	}}
	res := Traverse(d, TraversalRequest{Roots: []string{"a"}, Direction: DirectionDependencies,
		MaxDepth: 2, MaxReached: 1})
	if !slices.Equal(res.TruncationReasons, []string{"depth", "max-reached"}) || len(res.Reached) != 1 {
		t.Fatalf("truncation = %v, reached = %+v", res.TruncationReasons, res.Reached)
	}
	if res.EvidenceClassified || res.Reached[0].Evidence != "" {
		t.Fatalf("documents without dispatchEvidence must not classify: %+v", res)
	}
	d.DispatchEvidence = true
	full := Traverse(d, TraversalRequest{Roots: []string{"a"}, Direction: DirectionDependencies,
		MaxDepth: MaxTraversalDepth, MaxReached: MaxTraversalReached})
	got := map[string]string{}
	for _, r := range full.Reached {
		got[r.ID] = r.Evidence
	}
	if got["c"] != EvidenceDirect || got["d"] != EvidenceCandidate || len(full.TruncationReasons) != 0 {
		t.Fatalf("evidence = %v, reasons = %v", got, full.TruncationReasons)
	}
}

// TestUTF16Less는 보조 평면 문자가 U+E000보다 앞서는 UTF-16 순서를 확인한다.
func TestUTF16Less(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"\U0001F600", "\uE000", true}, // 서로게이트(0xD83D) < 0xE000 — UTF-8 바이트 순서와 반대
		{"\uE000", "\U0001F600", false},
		{"a", "ab", true},
		{"ab", "a", false},
		{"a", "a", false},
		{"\U0001F600", "\U0001F601", true},
		{"Z", "a", true},
	}
	for _, c := range cases {
		if got := utf16Less(c.a, c.b); got != c.want {
			t.Errorf("utf16Less(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
