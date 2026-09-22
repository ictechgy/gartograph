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

// TestRelPath는 모듈 경로 벗기기의 경계 조건을 확인한다.
// 모듈 루트 패키지는 "."이 되고, 모듈이 없는 문서는 경로가 그대로다.
func TestRelPath(t *testing.T) {
	if got := relPath("example.com/m", "example.com/m"); got != "." {
		t.Fatalf("module root must map to ., got %s", got)
	}
	if got := relPath("x/y", ""); got != "x/y" {
		t.Fatalf("empty module must leave path alone, got %s", got)
	}
	if got := relPath("example.com/m/a/b", "example.com/m"); got != "a/b" {
		t.Fatalf("nested package: got %s", got)
	}
	// 모듈 접두사가 우연이 겹치는 경로(example.com/mx)는 벗기면 안 된다.
	if got := relPath("example.com/mx/p", "example.com/m"); got != "example.com/mx/p" {
		t.Fatalf("prefix collision stripped wrongly: %s", got)
	}
}

// TestCheckRulesDeny는 deny가 허용 목록보다 우선하는지 확인한다.
// deps에 있어도 deny가 이겨야 "보통 허용, 이 조합은 금지"가 성립한다.
func TestCheckRulesDeny(t *testing.T) {
	cfg := rulesCfg()
	cfg.Deps["web"] = []string{"db"}
	cfg.Deny = map[string][]string{"web": {"db"}}
	violations, _ := CheckRules(rulesDoc(), cfg)
	if len(violations) != 1 || violations[0].Rule != "deny" {
		t.Fatalf("deny must win over allow, got %+v", violations)
	}
}

// TestCheckRulesSignature는 공개 API 시그니처의 타입 누출을 잡는지 확인한다.
// 비공개 심볼의 시그니처는 공개 API가 아니므로 검사하지 않는다.
func TestCheckRulesSignature(t *testing.T) {
	d := &graph.Document{
		Module: "example.com/m",
		Level:  graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "example.com/m/api", Kind: graph.KindPackage},
			{ID: "example.com/m/db", Kind: graph.KindPackage},
			{ID: "example.com/m/api.Open", Kind: graph.KindFunc,
				Package: "example.com/m/api", Exported: true},
			{ID: "example.com/m/api.hidden", Kind: graph.KindFunc,
				Package: "example.com/m/api"},
			{ID: "example.com/m/db.Conn", Kind: graph.KindType,
				Package: "example.com/m/db", Exported: true},
		},
		Edges: []graph.Edge{
			{From: "example.com/m/api.Open", To: "example.com/m/db.Conn",
				Kind: graph.EdgeSignature},
			{From: "example.com/m/api.hidden", To: "example.com/m/db.Conn",
				Kind: graph.EdgeSignature},
		},
	}
	cfg := &config.File{
		Components: map[string][]string{"api": {"api"}, "db": {"db"}},
		Deps:       map[string][]string{"api": {"db"}},
		Signature:  map[string][]string{"api": {}},
	}
	violations, _ := CheckRules(d, cfg)
	if len(violations) != 1 {
		t.Fatalf("expected exactly the exported signature violation, got %+v", violations)
	}
	v := violations[0]
	if v.Rule != "signature" || v.From != "example.com/m/api.Open" {
		t.Fatalf("unexpected violation: %+v", v)
	}
	// signature 목록에 db를 허용하면 위반은 사라진다 — 본문 deps와는 별개 축이다.
	cfg.Signature["api"] = []string{"db"}
	violations, _ = CheckRules(d, cfg)
	if len(violations) != 0 {
		t.Fatalf("allowed signature dep reported: %+v", violations)
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
