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

// TestScaffoldExternalFlag은 External 표시된 정점이 경로 접두사와 무관하게
// 외부로 분류되는지 확인한다 — 주 모듈 안의 중첩 모듈(example.com/m/plugin)
// 같은 경로는 접두사 추론으로는 내부로 오분된다.
func TestScaffoldExternalFlag(t *testing.T) {
	d := &graph.Document{
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "example.com/m/app", Kind: graph.KindPackage},
			// 중첩 모듈 패키지 — 경로는 내부처럼 보이지만 소속은 다르다.
			{ID: "example.com/m/plugin", Kind: graph.KindPackage, External: true},
		},
	}
	cfg := ScaffoldConfig(d)
	if _, ok := cfg.Components["plugin"]; ok {
		t.Fatal("external vertex must not scaffold a component")
	}
	m := MapComponents(d, cfg)
	if len(m.UnmappedExternal) != 1 || m.UnmappedExternal[0] != "example.com/m/plugin" {
		t.Fatalf("external vertex must report as unmappedExternal: %+v", m)
	}
	if len(m.Unmapped) != 0 {
		t.Fatalf("external vertex must not pollute internal unmapped: %+v", m)
	}
}

// TestScaffoldNameCollision은 모듈 루트 패키지와 같은 이름의 하위
// 패키지가 있을 때 컴포넌트 키가 충돌하지 않는지 확인한다 —
// 충돌하면 한 패키지의 패턴이 덮여 규칙이 그 패키지를 모른다.
func TestScaffoldNameCollision(t *testing.T) {
	d := &graph.Document{
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "example.com/m", Kind: graph.KindPackage, Name: "main"},
			{ID: "example.com/m/root", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			{From: "example.com/m", To: "example.com/m/root", Kind: graph.EdgeImport},
		},
	}
	cfg := ScaffoldConfig(d)
	if len(cfg.Components) != 2 {
		t.Fatalf("collision must produce two components: %v", cfg.Components)
	}
	// 어느 쪽이 "root"를 가져가든 두 패턴이 모두 살아 있어야 한다.
	var patterns int
	for _, pats := range cfg.Components {
		patterns += len(pats)
	}
	if patterns != 2 {
		t.Fatalf("one package's pattern was overwritten: %v", cfg.Components)
	}
	// deps가 어떤 이름으로든 관찰된 의존을 보존하는지.
	for from, tos := range cfg.Deps {
		for _, to := range tos {
			if _, ok := cfg.Components[to]; !ok {
				t.Fatalf("deps %s -> %s references a missing component", from, to)
			}
		}
	}
}
