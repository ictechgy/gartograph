package analysis

import (
	"context"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/ictechgy/gartograph/graph"
)

// TestQueryMaxDepth는 이웃을 모두 방문한 뒤 요청 깊이만큼 빈 순회를 하지 않게 한다.
// 회귀가 생겨도 테스트 프로세스가 무한 대기하지 않도록 자식 실행에 기한을 둔다.
func TestQueryMaxDepth(t *testing.T) {
	if os.Getenv("GARTOGRAPH_TEST_MAX_DEPTH") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestQueryMaxDepth$")
		cmd.Env = append(os.Environ(), "GARTOGRAPH_TEST_MAX_DEPTH=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("maximum-depth query did not finish: %v (%v)\n%s", err, ctx.Err(), output)
		}
		return
	}
	isolated := &graph.Document{Vertices: []graph.Vertex{{ID: "only"}}}
	result, err := Query(isolated, "only", math.MaxInt, 0)
	if err != nil || result.Depth != math.MaxInt || result.Truncated ||
		len(result.DependsOn) != 0 || len(result.DependedBy) != 0 {
		t.Fatalf("isolated query: %+v, %v", result, err)
	}
	result, err = Query(doc(), "a", math.MaxInt, 1)
	if err != nil || !result.Truncated || len(result.DependsOn) != 1 || len(result.DependedBy) != 1 {
		t.Fatalf("cyclic truncated query: %+v, %v", result, err)
	}
}

// TestQueryMatchesEdgeScan은 무작위 문서에서 Query가 이웃마다 간선 전체를 훑던
// 구현(edgeScanQuery)과 같은 결과를 내는지 확인한다. 들어오는 방향의 간선 종류 방향,
// 한 쌍의 여러 종류, 중복 간선(EdgeKinds처럼 종류가 거듭 실린다), 자기 루프, contains,
// --max 절단을 섞는다.
func TestQueryMatchesEdgeScan(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	sawTruncated, sawMultiKind := false, false
	for trial := 0; trial < 500; trial++ {
		d := randomDependencyDocument(r)
		depth, maxNeighbors := r.Intn(5), r.Intn(4)
		id := d.Vertices[r.Intn(len(d.Vertices))].ID
		got, err := Query(d, id, depth, maxNeighbors)
		if err != nil {
			t.Fatal(err)
		}
		want := edgeScanQuery(d, id, depth, maxNeighbors)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d query %s depth=%d max=%d diverged:\ngot  %+v\nwant %+v\nedges %+v",
				trial, id, depth, maxNeighbors, got, want, d.Edges)
		}
		sawTruncated = sawTruncated || got.Truncated
		for _, ns := range [][]NeighborEdge{got.DependsOn, got.DependedBy} {
			for _, n := range ns {
				sawMultiKind = sawMultiKind || len(n.Kinds) > 1
			}
		}
	}
	if !sawTruncated || !sawMultiKind {
		t.Fatalf("random documents must exercise truncation (%v) and multi-kind edges (%v)",
			sawTruncated, sawMultiKind)
	}
}

// edgeScanQuery는 종류 포함 이웃 목록을 쓰기 전의 Query다 — 이웃마다 EdgeKinds로 문서
// 전체 간선을 훑어 종류를 모은다. 출력이 지금 구현과 같아야 하는 대조 기준이다.
func edgeScanQuery(d *graph.Document, id string, depth, maxNeighbors int) *Neighbors {
	vtx, _ := d.VertexByID(id)
	if depth < 1 {
		depth = 1
	}
	dependsOn, truncOut := edgeScanWalk(d, graph.Adjacency(d), id, depth, maxNeighbors, false)
	dependedBy, truncIn := edgeScanWalk(d, graph.Incoming(d), id, depth, maxNeighbors, true)
	return &Neighbors{ID: id, Kind: vtx.Kind, Depth: depth, DependsOn: dependsOn,
		DependedBy: dependedBy, Truncated: truncOut || truncIn, Limitations: d.Limitations}
}

// edgeScanWalk는 이전 walk다. reverse면 인접 맵이 들어오는 방향이라 간선 조회를 (to→cur)로 뒤집는다.
func edgeScanWalk(d *graph.Document, adj map[string][]string, root string,
	depth, maxNeighbors int, reverse bool) ([]NeighborEdge, bool) {
	seen := map[string]bool{root: true}
	frontier := []string{root}
	var out []NeighborEdge
	truncated := false
	for step := 0; step < depth && len(frontier) > 0; step++ {
		var next []string
		for _, cur := range frontier {
			for _, to := range adj[cur] {
				if seen[to] {
					continue
				}
				seen[to] = true
				next = append(next, to)
				if maxNeighbors > 0 && len(out) >= maxNeighbors {
					truncated = true
					continue
				}
				from, target := cur, to
				if reverse {
					from, target = to, cur
				}
				out = append(out, NeighborEdge{ID: to, Kinds: edgeScanKinds(d, from, target)})
			}
		}
		frontier = next
	}
	return out, truncated
}

// BenchmarkQuery는 약 3만 정점 합성 문서에서 허브의 3단계 이웃과 저차수 정점의 직접
// 이웃을 묻는다. 허브는 이웃마다 간선 전체를 훑던 이차 비용을, 저차수 질의는 MCP처럼
// 호출마다 이웃 목록을 만드는 고정 비용이 늘지 않았는지를 본다.
func BenchmarkQuery(b *testing.B) {
	d := syntheticSymbolDocument(300, 100, 250)
	for _, c := range []struct {
		name  string
		id    string
		depth int
	}{
		{"hub-depth3", syntheticHub, 3},
		{"leaf-depth1", "example.com/big/p0150.Fn050", 1},
	} {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := Query(d, c.id, c.depth, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
