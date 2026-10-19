package analysis

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// impactDoc은 a → b → c 호출 사슬과 c를 가리키는 contains를 가진 문서다.
// contains는 소유 관계라 영향 전이에 섞이면 안 된다 — 부모가 자식을
// 바꾼다고 깨지는 것이 아니다.
func impactDoc() *graph.Document {
	return &graph.Document{
		Vertices: []graph.Vertex{
			{ID: "pkg", Kind: graph.KindPackage},
			{ID: "pkg.a", Kind: graph.KindFunc, Package: "pkg"},
			{ID: "pkg.b", Kind: graph.KindFunc, Package: "pkg"},
			{ID: "pkg.c", Kind: graph.KindFunc, Package: "pkg"},
		},
		Edges: []graph.Edge{
			{From: "pkg.a", To: "pkg.b", Kind: graph.EdgeCall},
			{From: "pkg.b", To: "pkg.c", Kind: graph.EdgeCall},
			{From: "pkg", To: "pkg.c", Kind: graph.EdgeContains},
		},
	}
}

// TestFindImpact는 역방향 전이가 거리를 싣고 모이는지 확인한다.
func TestFindImpact(t *testing.T) {
	res, err := FindImpact(impactDoc(), "pkg.c", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dependers) != 2 {
		t.Fatalf("expected b and a, got %+v", res.Dependers)
	}
	if res.Dependers[0].ID != "pkg.b" || res.Dependers[0].Depth != 1 {
		t.Fatalf("nearest depender should be b at depth 1: %+v", res.Dependers[0])
	}
	if res.Dependers[1].ID != "pkg.a" || res.Dependers[1].Depth != 2 {
		t.Fatalf("transitive depender should be a at depth 2: %+v", res.Dependers[1])
	}
	// contains는 의존이 아니다 — 패키지 정점이 depender로 나오면 안 된다.
	for _, e := range res.Dependers {
		if e.ID == "pkg" {
			t.Fatalf("contains edge leaked into impact: %+v", res.Dependers)
		}
	}
}

// TestFindImpactDepth는 깊이 제한이 전이를 자르는지 확인한다.
func TestFindImpactDepth(t *testing.T) {
	res, err := FindImpact(impactDoc(), "pkg.c", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dependers) != 1 || res.Dependers[0].ID != "pkg.b" {
		t.Fatalf("depth 1 should keep only direct depender: %+v", res.Dependers)
	}
}

// TestFindImpactNotFound는 없는 정점 질의가 ErrNotFound인지 확인한다.
// "없는 것"과 "못 본 것"을 소비자가 구분할 수 있어야 한다.
func TestFindImpactNotFound(t *testing.T) {
	if _, err := FindImpact(impactDoc(), "pkg.missing", 0, 0); err == nil {
		t.Fatal("expected error for missing vertex")
	}
}

// TestImpactKindsTowardPreviousLayer는 의존자의 edges가 최단 경로의 직전 층(depth 1은
// 대상)으로 가는 간선 종류만 담는지 확인한다. 같은 층·더 깊은 층·자기 자신으로 가는
// 간선까지 합치면 B가 R을 call로만 부르는데 "참조로도 쓴다"로 오독되고, 합쳐지는
// 종류가 BFS 처리 순서에 따라 달라진다. 직전 층의 선행 정점이 여럿이면 그 종류는 모두 남는다.
func TestImpactKindsTowardPreviousLayer(t *testing.T) {
	d := &graph.Document{
		Vertices: []graph.Vertex{
			{ID: "m.A", Kind: graph.KindFunc}, {ID: "m.B", Kind: graph.KindFunc},
			{ID: "m.R", Kind: graph.KindFunc}, {ID: "m.X", Kind: graph.KindFunc},
		},
		Edges: []graph.Edge{
			{From: "m.A", To: "m.R", Kind: graph.EdgeCall},
			{From: "m.B", To: "m.R", Kind: graph.EdgeCall},
			// 같은 층(depth 1)끼리의 간선 — B의 edges에 섞이면 안 된다.
			{From: "m.B", To: "m.A", Kind: graph.EdgeReferences},
			// X는 depth 2다. 직전 층의 두 선행 정점으로 가는 종류는 둘 다 남는다.
			{From: "m.X", To: "m.A", Kind: graph.EdgeCall},
			{From: "m.X", To: "m.B", Kind: graph.EdgeEmbeds},
			// 더 깊은 층으로 가는 간선과 자기 루프도 섞이면 안 된다.
			{From: "m.A", To: "m.X", Kind: graph.EdgeSignature},
			{From: "m.X", To: "m.X", Kind: graph.EdgeImplements},
		},
	}
	res, err := FindImpact(d, "m.R", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []ImpactEntry{
		{ID: "m.A", Kind: graph.KindFunc, Depth: 1, Kinds: []graph.EdgeKind{graph.EdgeCall}},
		{ID: "m.B", Kind: graph.KindFunc, Depth: 1, Kinds: []graph.EdgeKind{graph.EdgeCall}},
		{ID: "m.X", Kind: graph.KindFunc, Depth: 2,
			Kinds: []graph.EdgeKind{graph.EdgeCall, graph.EdgeEmbeds}},
	}
	if !reflect.DeepEqual(res.Dependers, want) {
		t.Fatalf("edges must only name kinds toward the previous layer:\ngot  %+v\nwant %+v",
			res.Dependers, want)
	}
}

// TestAffectedMergesKinds는 여러 루트에 다른 간선 종류로 닿는 의존자의
// edges가 합쳐지는지 확인한다 — 먼저 발견된 경로의 종류만 남으면
// "어떤 관계로 깨지는가"라는 사실이 유실된다.
func TestAffectedMergesKinds(t *testing.T) {
	d := &graph.Document{
		Vertices: []graph.Vertex{
			{ID: "pkg.a", Kind: graph.KindFunc, Package: "pkg"},
			{ID: "pkg.b", Kind: graph.KindFunc, Package: "pkg"},
			{ID: "pkg.x", Kind: graph.KindFunc, Package: "pkg"},
		},
		Edges: []graph.Edge{
			{From: "pkg.x", To: "pkg.a", Kind: graph.EdgeCall},
			{From: "pkg.x", To: "pkg.b", Kind: graph.EdgeReferences},
		},
	}
	res, err := AffectedByFiles(d, nil, []string{"pkg.a", "pkg.b"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dependers) != 1 || res.Dependers[0].ID != "pkg.x" {
		t.Fatalf("expected single depender pkg.x: %+v", res.Dependers)
	}
	if len(res.Dependers[0].Kinds) != 2 {
		t.Fatalf("x reaches both roots via different kinds — expected both: %+v",
			res.Dependers[0].Kinds)
	}
}

// TestImpactMatchesEdgeScan은 무작위 문서에서 FindImpact·AffectedByFiles가 의존자마다
// 간선 전체를 훑던 구현(edgeScanImpactFrom)과 같은 결과를 내는지 확인한다. 한 쌍의
// 여러 종류, 같은 관계의 중복 간선, 자기 루프, contains, 정점 없는 끝, --max 절단,
// 다중 루트를 섞는다 — 간선 종류를 이웃 목록에서 읽게 한 것은 비용만 줄여야 하고
// 출력은 그대로여야 한다.
func TestImpactMatchesEdgeScan(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	sawTruncated, sawMultiKind := false, false
	for trial := 0; trial < 500; trial++ {
		d := randomDependencyDocument(r)
		depth, maxEntries := r.Intn(4), r.Intn(4)
		root := d.Vertices[r.Intn(len(d.Vertices))].ID
		got, err := FindImpact(d, root, depth, maxEntries)
		if err != nil {
			t.Fatal(err)
		}
		want, wantTruncated := edgeScanImpactFrom(d, []string{root}, depth, maxEntries)
		if !reflect.DeepEqual(got.Dependers, want) || got.Truncated != wantTruncated {
			t.Fatalf("trial %d impact %s depth=%d max=%d diverged:\ngot  %+v %v\nwant %+v %v\nedges %+v",
				trial, root, depth, maxEntries, got.Dependers, got.Truncated, want, wantTruncated, d.Edges)
		}
		roots := []string{root, d.Vertices[r.Intn(len(d.Vertices))].ID,
			d.Vertices[r.Intn(len(d.Vertices))].ID}
		aff, err := AffectedByFiles(d, nil, roots, depth, maxEntries)
		if err != nil {
			t.Fatal(err)
		}
		want, wantTruncated = edgeScanImpactFrom(d, aff.Roots, depth, maxEntries)
		if !reflect.DeepEqual(aff.Dependers, want) || aff.Truncated != wantTruncated {
			t.Fatalf("trial %d affected %v depth=%d max=%d diverged:\ngot  %+v %v\nwant %+v %v\nedges %+v",
				trial, aff.Roots, depth, maxEntries, aff.Dependers, aff.Truncated, want, wantTruncated, d.Edges)
		}
		sawTruncated = sawTruncated || got.Truncated || aff.Truncated
		for _, deps := range [][]ImpactEntry{got.Dependers, aff.Dependers} {
			for _, e := range deps {
				sawMultiKind = sawMultiKind || len(e.Kinds) > 1
			}
		}
	}
	// 생성기가 쉬운 경우만 만들면 대조가 아무것도 증명하지 못한다.
	if !sawTruncated || !sawMultiKind {
		t.Fatalf("random documents must exercise truncation (%v) and multi-kind edges (%v)",
			sawTruncated, sawMultiKind)
	}
}

// randomDependencyDocument는 동등성 대조용 무작위 문서다. 정렬하지 않은 정점·간선에
// 한 쌍의 여러 종류, 같은 관계의 중복 간선, 자기 루프, contains, 정점 없는 끝(다른 생산자의
// 문서)을 섞는다 — 정규화된 저장 문서가 아니어도 질의는 같은 사실을 내야 한다.
func randomDependencyDocument(r *rand.Rand) *graph.Document {
	vertexKinds := []graph.VertexKind{graph.KindFunc, graph.KindMethod, graph.KindType, graph.KindPackage}
	edgeKinds := []graph.EdgeKind{graph.EdgeCall, graph.EdgeReferences, graph.EdgeImplements,
		graph.EdgeEmbeds, graph.EdgeSignature, graph.EdgeImport, graph.EdgeContains}
	d := &graph.Document{}
	n := 2 + r.Intn(12)
	ends := []string{"m.ghost"}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("m.v%02d", i)
		d.Vertices = append(d.Vertices, graph.Vertex{ID: id, Kind: vertexKinds[r.Intn(len(vertexKinds))]})
		ends = append(ends, id)
	}
	r.Shuffle(len(d.Vertices), func(i, j int) { d.Vertices[i], d.Vertices[j] = d.Vertices[j], d.Vertices[i] })
	for m := 1 + r.Intn(4*n); m > 0; m-- {
		e := graph.Edge{From: ends[r.Intn(len(ends))], To: ends[r.Intn(len(ends))],
			Kind: edgeKinds[r.Intn(len(edgeKinds))]}
		if r.Intn(6) == 0 {
			e.To = e.From
		}
		d.Edges = append(d.Edges, e)
		if r.Intn(5) == 0 {
			d.Edges = append(d.Edges, e)
		}
		if r.Intn(3) == 0 {
			e.Kind = edgeKinds[r.Intn(len(edgeKinds))]
			d.Edges = append(d.Edges, e)
		}
	}
	r.Shuffle(len(d.Edges), func(i, j int) { d.Edges[i], d.Edges[j] = d.Edges[j], d.Edges[i] })
	return d
}

// edgeScanImpactFrom은 종류 포함 이웃 목록을 쓰기 전의 impactFrom이다 — 들어오는
// 이웃마다 EdgeKinds로 문서 전체 간선을 훑고 VertexByID로 정점 전체를 훑는다. 출력은
// 지금 구현과 같아야 하므로 느려도 대조 기준으로 그대로 둔다.
func edgeScanImpactFrom(d *graph.Document, roots []string, depth, maxEntries int) ([]ImpactEntry, bool) {
	in := graph.Incoming(d)
	seen := map[string]bool{}
	for _, r := range roots {
		seen[r] = true
	}
	frontier := append([]string(nil), roots...)
	var out []ImpactEntry
	index := map[string]int{}
	truncated := false
	for step := 1; len(frontier) > 0 && (depth <= 0 || step <= depth); step++ {
		var next []string
		for _, cur := range frontier {
			for _, from := range in[cur] {
				if seen[from] {
					if i, ok := index[from]; ok && out[i].Depth == step {
						out[i].Kinds = mergeKinds(out[i].Kinds, edgeScanKinds(d, from, cur))
					}
					continue
				}
				seen[from] = true
				next = append(next, from)
				if maxEntries > 0 && len(out) >= maxEntries {
					truncated = true
					continue
				}
				dep := ImpactEntry{ID: from, Depth: step, Kinds: edgeScanKinds(d, from, cur)}
				if v, ok := d.VertexByID(from); ok {
					dep.Kind = v.Kind
				}
				index[from] = len(out)
				out = append(out, dep)
			}
		}
		frontier = next
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].ID < out[j].ID
	})
	return out, truncated
}

// edgeScanKinds는 문서 전체 간선을 훑어 from→to의 의존 관계 종류를 모은다(contains 제외).
func edgeScanKinds(d *graph.Document, from, to string) []graph.EdgeKind {
	var kinds []graph.EdgeKind
	for _, k := range graph.EdgeKinds(d, from, to) {
		if k != graph.EdgeContains {
			kinds = append(kinds, k)
		}
	}
	return kinds
}

// syntheticHub는 합성 문서에서 패키지 30%가 직접 부르는 허브 함수(로깅 함수 같은 것)다.
const syntheticHub = "example.com/big/p0000.Fn000"

// syntheticSymbolDocument는 벤치마크용 합성 심볼 문서다. 패키지마다 함수 syms개와
// contains를 두고, 함수마다 같은 패키지와 import한 패키지의 함수로 호출 3개를 둔다.
// 7번째 함수마다 같은 쌍에 call과 references를 함께 둔다. 패키지 30%가 허브를 부르고,
// ring개 함수가 패키지 경계를 거꾸로 넘는 큰 SCC를 이룬다 — 대형 저장소의 허브 영향과
// 거대 순환 모양이다. 대형 testdata 파일 대신 고정 시드로 매번 같은 문서를 만든다.
func syntheticSymbolDocument(pkgs, syms, ring int) *graph.Document {
	r := rand.New(rand.NewSource(1))
	d := &graph.Document{Version: graph.Version, Tool: graph.Tool, Level: graph.LevelSymbol,
		Module: "example.com/big"}
	pkgID := func(p int) string { return fmt.Sprintf("example.com/big/p%04d", p) }
	fnID := func(p, s int) string { return fmt.Sprintf("%s.Fn%03d", pkgID(p), s) }
	at := func(p, line int) []graph.Position {
		return []graph.Position{{File: fmt.Sprintf("/work/big/p%04d/f%d.go", p, line%5), Line: line + 1}}
	}
	imports := make([][]int, pkgs)
	for p := 0; p < pkgs; p++ {
		d.Vertices = append(d.Vertices, graph.Vertex{ID: pkgID(p), Kind: graph.KindPackage})
		for k := 0; k < 8 && p > 0; k++ {
			q := r.Intn(p)
			imports[p] = append(imports[p], q)
			d.Edges = append(d.Edges, graph.Edge{From: pkgID(p), To: pkgID(q), Kind: graph.EdgeImport})
		}
		for s := 0; s < syms; s++ {
			id := fnID(p, s)
			d.Vertices = append(d.Vertices, graph.Vertex{ID: id, Kind: graph.KindFunc, Package: pkgID(p)})
			d.Edges = append(d.Edges, graph.Edge{From: pkgID(p), To: id, Kind: graph.EdgeContains})
			for c := 0; c < 3; c++ {
				to := fnID(p, r.Intn(syms))
				if c > 0 && len(imports[p]) > 0 {
					to = fnID(imports[p][r.Intn(len(imports[p]))], r.Intn(syms))
				}
				d.Edges = append(d.Edges, graph.Edge{From: id, To: to, Kind: graph.EdgeCall,
					Positions: at(p, s*10+c)})
			}
			if s%7 == 0 {
				next := fnID(p, (s+1)%syms)
				d.Edges = append(d.Edges,
					graph.Edge{From: id, To: next, Kind: graph.EdgeReferences, Positions: at(p, s*10+5)},
					graph.Edge{From: id, To: next, Kind: graph.EdgeCall, Positions: at(p, s*10+6)})
			}
		}
		if p > 0 && r.Intn(10) < 3 {
			d.Edges = append(d.Edges, graph.Edge{From: fnID(p, r.Intn(syms)), To: syntheticHub,
				Kind: graph.EdgeCall, Positions: at(p, 999)})
		}
	}
	members := make([]string, ring)
	for i := range members {
		members[i] = fnID(r.Intn(pkgs), r.Intn(syms))
	}
	for i, m := range members {
		d.Edges = append(d.Edges,
			graph.Edge{From: m, To: members[(i+1)%ring], Kind: graph.EdgeCall, Candidate: true},
			graph.Edge{From: m, To: members[r.Intn(ring)], Kind: graph.EdgeCall, Candidate: true})
	}
	d.Sort()
	return d
}

// BenchmarkFindImpact는 약 3만 정점 합성 문서에서 허브 함수의 전이 의존자 전체를 모은다.
// 의존자마다 간선 전체를 훑던 구현은 이 규모에서 호출당 수십 초가 걸렸다 — 이차 비용이
// 돌아오면 이 벤치마크가 드러낸다.
func BenchmarkFindImpact(b *testing.B) {
	d := syntheticSymbolDocument(300, 100, 250)
	for b.Loop() {
		res, err := FindImpact(d, syntheticHub, 0, 0)
		if err != nil || len(res.Dependers) == 0 {
			b.Fatalf("hub impact: %d dependers, %v", len(res.Dependers), err)
		}
	}
}
