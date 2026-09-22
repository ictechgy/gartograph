package analysis

import (
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
