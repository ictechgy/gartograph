package analysis

import (
	"errors"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// TestPath는 최단 경로와 홉별 간선 종류를 확인한다.
// doc()의 d→a→b→c 체인에서 d→c는 3홉이다.
func TestPath(t *testing.T) {
	res, err := Path(doc(), "d", "c")
	if err != nil {
		t.Fatalf("path failed: %v", err)
	}
	if !res.Found || len(res.Hops) != 4 {
		t.Fatalf("expected d->a->b->c, got %+v", res)
	}
	if res.Hops[0].ID != "d" || len(res.Hops[0].Kinds) != 0 {
		t.Fatalf("first hop must be the source without kinds: %+v", res.Hops[0])
	}
	if res.Hops[3].ID != "c" || res.Hops[3].Kinds[0] != graph.EdgeImport {
		t.Fatalf("last hop must carry the incoming edge kind: %+v", res.Hops[3])
	}
}

// TestPathNotFound는 도달 불가능한 쌍이 found:false로 오는지 확인한다.
// 경로 부재는 그래프 사실이라 에러가 아니다 — 정점 부재만 에러다.
func TestPathNotFound(t *testing.T) {
	res, err := Path(doc(), "c", "d")
	if err != nil {
		t.Fatalf("unreachable pair is a fact, not an error: %v", err)
	}
	if res.Found {
		t.Fatalf("c->d must not be found: %+v", res)
	}
	for _, id := range []string{"ghost", "c"} {
		if _, err := Path(doc(), id, "ghost2"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing vertex must be ErrNotFound: %v", err)
		}
	}
}

// TestPathSelf는 from==to가 한 홉짜리 경로인지 확인한다.
func TestPathSelf(t *testing.T) {
	res, err := Path(doc(), "a", "a")
	if err != nil || !res.Found || len(res.Hops) != 1 {
		t.Fatalf("self path must be a single hop: %v %+v", err, res)
	}
}

// TestPathSkipsContains는 contains 간선이 경로에 쓰이지 않는지 확인한다.
// 소유 관계를 의존으로 세면 패키지→심볼→패키지 같은 가짜 경로가 생긴다.
func TestPathSkipsContains(t *testing.T) {
	d := &graph.Document{
		Vertices: []graph.Vertex{{ID: "pkg"}, {ID: "pkg.F"}, {ID: "other"}},
		Edges: []graph.Edge{
			{From: "pkg", To: "pkg.F", Kind: graph.EdgeContains},
			{From: "pkg.F", To: "other", Kind: graph.EdgeContains},
		},
	}
	res, err := Path(d, "pkg", "other")
	if err != nil {
		t.Fatalf("path failed: %v", err)
	}
	if res.Found {
		t.Fatalf("contains-only chain must not form a dependency path: %+v", res)
	}
}
