package analysis

import (
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// TestScaffoldConfig는 관찰된 import가 deps 허용 목록으로 옮겨지고,
// 모든 컴포넌트가 deps 키를 갖는지 확인한다 — 생성 즉시 rules가
// 통과해야 "현실 기록 → 점진 조임" 도입이 성립한다.
func TestScaffoldConfig(t *testing.T) {
	d := &graph.Document{
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "example.com/m", Kind: graph.KindPackage, Name: "main"},
			{ID: "example.com/m/web", Kind: graph.KindPackage},
			{ID: "example.com/m/db", Kind: graph.KindPackage},
			{ID: "example.com/m/db/store", Kind: graph.KindPackage},
			{ID: "github.com/x/lib", Kind: graph.KindPackage}, // --deps 외부 정점
		},
		Edges: []graph.Edge{
			{From: "example.com/m", To: "example.com/m/web", Kind: graph.EdgeImport},
			{From: "example.com/m/web", To: "example.com/m/db", Kind: graph.EdgeImport},
			{From: "example.com/m/web", To: "github.com/x/lib", Kind: graph.EdgeImport},
		},
	}
	cfg := ScaffoldConfig(d)
	// 루트 패키지는 "root" 컴포넌트가 되고, 외부 패키지는 컴포넌트가 없다.
	if pats := cfg.Components["root"]; len(pats) != 1 || pats[0] != "." {
		t.Fatalf("root component: %v", cfg.Components)
	}
	if _, ok := cfg.Components["github.com/x/lib"]; ok {
		t.Fatal("external package must not become a component")
	}
	// web의 외부 의존은 컴포넌트가 없어 deps에 접히지 않는다.
	if len(cfg.Deps["web"]) != 1 || cfg.Deps["web"][0] != "db" {
		t.Fatalf("web deps must be [db] (external folded out): %v", cfg.Deps)
	}
	if deps, ok := cfg.Deps["db/store"]; !ok || len(deps) != 0 {
		t.Fatalf("every component needs an explicit (possibly empty) deps key: %v", cfg.Deps)
	}
	// 생성된 설정이 그 문서에 대해 위반을 만들지 않는지 — 스캐폴드 계약.
	rep := CheckRules(d, cfg)
	if len(rep.Violations) != 0 {
		t.Fatalf("scaffolded config must pass on the observed graph: %+v", rep.Violations)
	}
}
