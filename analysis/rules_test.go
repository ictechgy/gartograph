package analysis

import (
	"testing"

	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/graph"
)

// rulesDoc은 web→db import 간선과 매핑 안 된 util 패키지를 가진 문서다.
func rulesDoc() *graph.Document {
	return &graph.Document{
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "example.com/m/web", Kind: graph.KindPackage},
			{ID: "example.com/m/db", Kind: graph.KindPackage},
			{ID: "example.com/m/util", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			{From: "example.com/m/web", To: "example.com/m/db", Kind: graph.EdgeImport},
		},
	}
}

// rulesCfg는 web은 db에 의존 불가, db는 아무것도 못 쓰는 규칙이다.
func rulesCfg() *config.File {
	return &config.File{
		Components: map[string][]string{
			"web": {"web"},
			"db":  {"db"},
		},
		Deps: map[string][]string{},
	}
}

// TestCheckRules는 금지 의존이 위반으로, 매핑 구멍이 unmapped로 나오는지 확인한다.
func TestCheckRules(t *testing.T) {
	violations, unmapped := CheckRules(rulesDoc(), rulesCfg())
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %+v", violations)
	}
	v := violations[0]
	if v.FromComponent != "web" || v.ToComponent != "db" ||
		v.From != "example.com/m/web" {
		t.Fatalf("unexpected violation: %+v", v)
	}
	if len(unmapped) != 1 || unmapped[0] != "example.com/m/util" {
		t.Fatalf("expected util unmapped, got %v", unmapped)
	}
}

// TestCheckRulesAllowed는 허용된 의존이 위반으로 나오지 않는지 확인한다.
// 허용 목록 방식에서 누락이 "허용"으로 새면 안 된다 — 반대 방향도 검사한다.
func TestCheckRulesAllowed(t *testing.T) {
	cfg := rulesCfg()
	cfg.Deps["web"] = []string{"db"}
	violations, _ := CheckRules(rulesDoc(), cfg)
	if len(violations) != 0 {
		t.Fatalf("allowed dep reported as violation: %+v", violations)
	}
	// 역방향(db→web)은 허용 목록에 없으므로 여전히 위반이다.
	d := rulesDoc()
	d.Edges[0].From, d.Edges[0].To = "example.com/m/db", "example.com/m/web"
	violations, _ = CheckRules(d, cfg)
	if len(violations) != 1 {
		t.Fatalf("reverse dep must violate, got %+v", violations)
	}
}

// TestCheckRulesSelfDep는 같은 컴포넌트 안의 의존이 규칙 밖인지 확인한다.
func TestCheckRulesSelfDep(t *testing.T) {
	d := &graph.Document{
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "example.com/m/web", Kind: graph.KindPackage},
			{ID: "example.com/m/web/internal", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			{From: "example.com/m/web", To: "example.com/m/web/internal",
				Kind: graph.EdgeImport},
		},
	}
	cfg := &config.File{Components: map[string][]string{"web": {"web/**"}}}
	violations, unmapped := CheckRules(d, cfg)
	if len(violations) != 0 || len(unmapped) != 0 {
		t.Fatalf("self-component dep: violations=%v unmapped=%v", violations, unmapped)
	}
}
