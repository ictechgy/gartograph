package analysis

import (
	"reflect"
	"strings"
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
	rep := CheckRules(rulesDoc(), rulesCfg())
	violations, unmapped := rep.Violations, rep.Unmapped
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
	violations := CheckRules(rulesDoc(), cfg).Violations
	if len(violations) != 0 {
		t.Fatalf("allowed dep reported as violation: %+v", violations)
	}
	// 역방향(db→web)은 허용 목록에 없으므로 여전히 위반이다.
	d := rulesDoc()
	d.Edges[0].From, d.Edges[0].To = "example.com/m/db", "example.com/m/web"
	violations = CheckRules(d, cfg).Violations
	if len(violations) != 1 {
		t.Fatalf("reverse dep must violate, got %+v", violations)
	}
}

// TestCheckRulesDeny는 deny가 허용 목록보다 우선하는지 확인한다.
// deps에 있어도 deny가 이겨야 "보통 허용, 이 조합은 금지"가 성립한다.
func TestCheckRulesDeny(t *testing.T) {
	cfg := rulesCfg()
	cfg.Deps["web"] = []string{"db"}
	cfg.Deny = map[string][]config.DenyEntry{"web": {{To: "db", Reason: "use core instead"}}}
	violations := CheckRules(rulesDoc(), cfg).Violations
	if len(violations) != 1 || violations[0].Rule != "deny" {
		t.Fatalf("deny must win over allow, got %+v", violations)
	}
	if violations[0].Reason != "use core instead" {
		t.Fatalf("deny reason must reach the violation, got %+v", violations[0])
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
	violations := CheckRules(d, cfg).Violations
	if len(violations) != 1 {
		t.Fatalf("expected exactly the exported signature violation, got %+v", violations)
	}
	v := violations[0]
	if v.Rule != "signature" || v.From != "example.com/m/api.Open" {
		t.Fatalf("unexpected violation: %+v", v)
	}
	// signature 목록에 db를 허용하면 위반은 사라진다 — 본문 deps와는 별개 축이다.
	cfg.Signature["api"] = []string{"db"}
	violations = CheckRules(d, cfg).Violations
	if len(violations) != 0 {
		t.Fatalf("allowed signature dep reported: %+v", violations)
	}
}

// workspaceDoc은 go.work 워크스페이스(모듈 a·b)를 첫 모듈 a로 수확한 모양의 문서다. 모듈 b의 패키지 정점은
// module 사실을 싣는다. a/util과 b/util은 모듈 상대 경로가 같고, github.com/aws/s3는 --deps로 들어온 외부
// 패키지, b/tool은 아무도 import하지 않는 형제 모듈 패키지다.
func workspaceDoc() *graph.Document {
	return &graph.Document{
		Module: "example.com/a", ModuleDir: "/w/a", Root: "/w/a",
		WorkspaceModules: []graph.WorkspaceModule{
			{Path: "example.com/a", Dir: "/w/a"}, {Path: "example.com/b", Dir: "/w/b"}},
		Vertices: []graph.Vertex{
			{ID: "example.com/a/app", Kind: graph.KindPackage},
			{ID: "example.com/a/lib", Kind: graph.KindPackage},
			{ID: "example.com/a/util", Kind: graph.KindPackage},
			{ID: "example.com/b/tool", Kind: graph.KindPackage, Module: "example.com/b"},
			{ID: "example.com/b/util", Kind: graph.KindPackage, Module: "example.com/b"},
			{ID: "github.com/aws/s3", Kind: graph.KindPackage, External: true},
		},
		Edges: []graph.Edge{
			{From: "example.com/a/app", To: "example.com/a/util", Kind: graph.EdgeImport},
			{From: "example.com/a/app", To: "example.com/b/util", Kind: graph.EdgeImport,
				Positions: []graph.Position{{File: "/w/a/app/app.go", Line: 3}}},
			{From: "example.com/a/app", To: "github.com/aws/s3", Kind: graph.EdgeImport},
			{From: "example.com/b/util", To: "example.com/a/lib", Kind: graph.EdgeImport,
				Positions: []graph.Position{{File: "/w/b/util/util.go", Line: 3}}},
		},
	}
}

// oldWorkspaceDoc은 workspaceDoc에서 모듈 소속 사실(workspaceModules·정점 module)을 뺀 문서다 — 그 사실을
// 수확하기 전의 저장 문서 모양이다.
func oldWorkspaceDoc() *graph.Document {
	d := workspaceDoc()
	d.WorkspaceModules = nil
	for i := range d.Vertices {
		d.Vertices[i].Module = ""
	}
	return d
}

// workspaceCfg는 컴포넌트 app·util·lib·aws와 "util 패키지 파일은 lib를 import하지 않는다"는 fileRules다.
// app은 aws와 util을 쓸 수 있고 util은 lib를 쓸 수 있다.
func workspaceCfg() *config.File {
	return &config.File{
		Components: map[string][]string{
			"app": {"app"}, "util": {"util"}, "lib": {"lib"}, "aws": {"github.com/aws/**"},
		},
		Deps: map[string][]string{"app": {"aws"}, "util": {"lib"}},
		FileRules: []config.FileRule{
			{Name: "util-files-no-lib", From: "util/*.go", To: "lib"},
		},
	}
}

// TestWorkspacePackagesUseOwnModule은 워크스페이스 문서에서 컴포넌트 패턴이 패키지마다 그 패키지가 속한 모듈
// 기준 상대 경로로 맞는지 본다(DECISIONS gartograph "go.work의 exclude·components"). b/util은 util 컴포넌트에
// 들고 a/util과 같은 패턴을 공유한다. 형제 모듈 패키지는 외부가 아니라서 그쪽 의존을 검사하고(app→b/util은
// deps에 util이 없어 위반), 미매핑 b/tool은 내부 unmapped다. --deps 외부 패키지는 지금처럼 전체 import 경로로
// 맞는다. fileRules의 파일 경로도 파일이 속한 모듈(b) 기준이라 util/util.go가 "util/*.go"에 맞는다.
func TestWorkspacePackagesUseOwnModule(t *testing.T) {
	rep := CheckRules(workspaceDoc(), workspaceCfg())
	var got []string
	for _, v := range rep.Violations {
		got = append(got, v.From+" -> "+v.To+" "+v.Rule)
	}
	want := []string{
		"example.com/a/app -> example.com/a/util allow",
		"example.com/a/app -> example.com/b/util allow",
		"example.com/b/util -> example.com/a/lib fileScope",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %q, got %q", want, got)
	}
	m := MapComponents(workspaceDoc(), workspaceCfg())
	if !reflect.DeepEqual(m.Components["util"], []string{"example.com/a/util", "example.com/b/util"}) ||
		!reflect.DeepEqual(m.Components["aws"], []string{"github.com/aws/s3"}) ||
		!reflect.DeepEqual(m.Unmapped, []string{"example.com/b/tool"}) ||
		len(m.UnmappedExternal) != 0 || len(m.UnmatchedComponents) != 0 {
		t.Fatalf("workspace mapping: %+v", m)
	}
	// --deps로 들어온 중첩 모듈 패키지(경로가 모듈 a 밑이지만 소속이 다르다)도 외부라 전체 import 경로로
	// 맞는다 — exclude와 같은 기준이다. 옛 문서는 지금처럼 문서 module 접두사를 벗겨 맞춘다.
	nested := func(d *graph.Document) *graph.Document {
		d.Vertices = append(d.Vertices, graph.Vertex{ID: "example.com/a/plugin", Kind: graph.KindPackage, External: true})
		return d
	}
	cfg := &config.File{Components: map[string][]string{
		"plugin": {"plugin"}, "vendored": {"example.com/a/plugin"},
	}}
	if got := MapComponents(nested(workspaceDoc()), cfg).Components; !reflect.DeepEqual(got["vendored"],
		[]string{"example.com/a/plugin"}) || len(got["plugin"]) != 0 {
		t.Fatalf("an external package must match by its full import path in a workspace document: %v", got)
	}
	if got := MapComponents(nested(oldWorkspaceDoc()), cfg).Components; !reflect.DeepEqual(got["plugin"],
		[]string{"example.com/a/plugin"}) {
		t.Fatalf("an old document must keep stripping the document module: %v", got)
	}
}

// TestWorkspacePatternsFallBackWithoutModuleFacts는 모듈 소속 사실이 없는 문서(단일 모듈과 옛 저장 문서)가
// 예전 해석을 그대로 쓰는지 본다 — 문서 module 접두사가 없는 패키지는 외부이고, 패턴은 문서 module 하나를
// 기준으로 맞추며, 파일 경로는 moduleDir 기준이다.
func TestWorkspacePatternsFallBackWithoutModuleFacts(t *testing.T) {
	rep := CheckRules(oldWorkspaceDoc(), workspaceCfg())
	if len(rep.Violations) != 1 || rep.Violations[0].To != "example.com/a/util" {
		t.Fatalf("an old document must check only module-prefixed packages: %+v", rep.Violations)
	}
	if !reflect.DeepEqual(rep.UnmappedExternal, []string{"example.com/b/tool", "example.com/b/util"}) ||
		!reflect.DeepEqual(rep.UnmatchedComponents, []string(nil)) {
		t.Fatalf("an old document must keep the module-prefix fallback: %+v", rep)
	}
	if got := relFile(oldWorkspaceDoc(), "/w/b/util/util.go"); got != "../b/util/util.go" {
		t.Fatalf("an old document must resolve files against moduleDir: %q", got)
	}
}

// TestRelFileUsesContainingModule은 워크스페이스 문서의 파일 경로가 파일을 담는 가장 깊은 워크스페이스 모듈
// 기준인지 본다 — 다른 모듈 디렉터리 안에 놓인 워크스페이스 모듈(c: /w/a/c, 워크스페이스 루트 모듈 /w 안의 a
// 안)의 파일은 c 기준이다. 목록 순서(경로순)와 무관하게 가장 깊은 모듈을 고른다. 어느 모듈에도 없는 파일(cgo가
// 만든 빌드 캐시 파일 등)은 예전처럼 moduleDir 기준이다.
func TestRelFileUsesContainingModule(t *testing.T) {
	d := workspaceDoc()
	d.WorkspaceModules = append(d.WorkspaceModules, graph.WorkspaceModule{Path: "example.com/c", Dir: "/w/a/c"},
		graph.WorkspaceModule{Path: "example.com/root", Dir: "/w"})
	cases := map[string]string{
		"/w/a/app/app.go":   "app/app.go",
		"/w/b/util/util.go": "util/util.go",
		"/w/a/c/x/x.go":     "x/x.go",
		"/w/ab/x.go":        "ab/x.go", // /w/a의 이웃 디렉터리 — 접두사가 같아도 a가 아니라 루트 모듈이 담는다.
		"/cache/x.cgo1.go":  "../../cache/x.cgo1.go",
	}
	for file, want := range cases {
		if got := relFile(d, file); got != want {
			t.Errorf("relFile(%q) = %q, want %q", file, got, want)
		}
	}
}

// TestCheckRulesExternal은 --deps로 들어온 외부 패키지가 컴포넌트 패턴에
// 매칭되어 vendor 규칙이 걸리는지, 미매핑 외부는 UnmappedExternal로
// 분리되는지 확인한다.
func TestCheckRulesExternal(t *testing.T) {
	d := &graph.Document{
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "example.com/m/web", Kind: graph.KindPackage},
			{ID: "example.com/m/db", Kind: graph.KindPackage},
			{ID: "github.com/aws/s3", Kind: graph.KindPackage},
			{ID: "golang.org/x/tools", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			{From: "example.com/m/web", To: "github.com/aws/s3", Kind: graph.EdgeImport},
			{From: "example.com/m/db", To: "github.com/aws/s3", Kind: graph.EdgeImport},
		},
	}
	cfg := &config.File{
		Components: map[string][]string{
			"web": {"web"},
			"db":  {"db"},
			"aws": {"github.com/aws/**"},
		},
		Deps: map[string][]string{"db": {"aws"}},
	}
	rep := CheckRules(d, cfg)
	// web→aws는 허용 목록에 없어 위반, db→aws는 허용이다.
	if len(rep.Violations) != 1 || rep.Violations[0].ToComponent != "aws" {
		t.Fatalf("vendor rule: %+v", rep.Violations)
	}
	// golang.org/x/tools는 외부이고 미매핑 — 내부 unmapped와 섞이지 않는다.
	if len(rep.UnmappedExternal) != 1 ||
		rep.UnmappedExternal[0] != "golang.org/x/tools" {
		t.Fatalf("external unmapped: %+v", rep.UnmappedExternal)
	}
	if len(rep.Unmapped) != 0 {
		t.Fatalf("internal unmapped must be empty: %+v", rep.Unmapped)
	}
}

// TestUnmatchedComponents는 어느 정점에도 매칭되지 않은 컴포넌트가
// 보고되는지 확인한다 — 오타나 --deps 누락의 신호다.
func TestUnmatchedComponents(t *testing.T) {
	cfg := rulesCfg()
	cfg.Components["ghost"] = []string{"ghost/**"}
	rep := CheckRules(rulesDoc(), cfg)
	if len(rep.UnmatchedComponents) != 1 || rep.UnmatchedComponents[0] != "ghost" {
		t.Fatalf("expected ghost unmatched: %+v", rep.UnmatchedComponents)
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
	rep := CheckRules(d, cfg)
	violations, unmapped := rep.Violations, rep.Unmapped
	if len(violations) != 0 || len(unmapped) != 0 {
		t.Fatalf("self-component dep: violations=%v unmapped=%v", violations, unmapped)
	}
}

// TestCheckRulesCommon은 common 컴포넌트가 모든 deps에 적지 않아도
// 허용되는지 확인한다 — 공통 부품을 매 deps에 반복 적는 boilerplate를 없앤다.
func TestCheckRulesCommon(t *testing.T) {
	cfg := rulesCfg()
	cfg.Common = []string{"db"}
	violations := CheckRules(rulesDoc(), cfg).Violations
	if len(violations) != 0 {
		t.Fatalf("common dep must be allowed everywhere: %+v", violations)
	}
}

// TestCheckRulesVisibleTo는 공급자 측 규칙을 확인한다 —
// deps가 허용해도 공급자가 닫아 두면 위반이다.
func TestCheckRulesVisibleTo(t *testing.T) {
	cfg := rulesCfg()
	cfg.Deps["web"] = []string{"db"}                   // 소비자는 허용하고
	cfg.VisibleTo = map[string][]string{"db": {"api"}} // 공급자는 api만 받는다
	violations := CheckRules(rulesDoc(), cfg).Violations
	if len(violations) != 1 || violations[0].Rule != "visibleTo" {
		t.Fatalf("visibleTo must block an otherwise-allowed dep: %+v", violations)
	}
	// 공급자가 소비자를 열어 주면 통과다.
	cfg.VisibleTo["db"] = []string{"web"}
	if v := CheckRules(rulesDoc(), cfg).Violations; len(v) != 0 {
		t.Fatalf("listed consumer must pass visibleTo: %+v", v)
	}
}

// TestCheckRulesForbidden은 간접 도달 금지를 확인한다 —
// deps가 직접 간선만 보는 것과 달리 중간 컴포넌트를 경유한 도달도 위반이다.
func TestCheckRulesForbidden(t *testing.T) {
	d := &graph.Document{
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "example.com/m/api", Kind: graph.KindPackage},
			{ID: "example.com/m/svc", Kind: graph.KindPackage},
			{ID: "example.com/m/db", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			// api가 db를 직접 import하지 않는다 — svc를 경유해 도달할 뿐이다.
			{From: "example.com/m/api", To: "example.com/m/svc", Kind: graph.EdgeImport},
			{From: "example.com/m/svc", To: "example.com/m/db", Kind: graph.EdgeImport},
		},
	}
	cfg := &config.File{
		Components: map[string][]string{
			"api": {"api"}, "svc": {"svc"}, "db": {"db"},
		},
		Deps:      map[string][]string{"api": {"svc"}, "svc": {"db"}},
		Forbidden: []config.ForbiddenRule{{From: "api", To: "db"}},
	}
	violations := CheckRules(d, cfg).Violations
	if len(violations) != 1 {
		t.Fatalf("indirect reachability must violate forbidden: %+v", violations)
	}
	v := violations[0]
	if v.Rule != "forbidden" || v.FromComponent != "api" || v.ToComponent != "db" {
		t.Fatalf("unexpected forbidden violation: %+v", v)
	}
	// 목격 경로가 api→svc→db여야 에이전트가 사슬을 바로 본다.
	want := []string{"example.com/m/api", "example.com/m/svc", "example.com/m/db"}
	if len(v.Path) != len(want) {
		t.Fatalf("expected witness path %v, got %v", want, v.Path)
	}
	for i := range want {
		if v.Path[i] != want[i] {
			t.Fatalf("expected witness path %v, got %v", want, v.Path)
		}
	}
	// 계약을 빼면 직접 간선은 전부 deps에 있으므로 위반이 없다 —
	// forbidden만이 간접 도달을 잡는다는 것을 함께 확인한다.
	cfg.Forbidden = nil
	if v := CheckRules(d, cfg).Violations; len(v) != 0 {
		t.Fatalf("without forbidden the chain is legal: %+v", v)
	}
}

// TestCheckRulesIndependent는 양방향 독립 계약을 확인한다 —
// 어느 방향의 도달도 위반이고, 두 방향이 다 뚫리면 두 건이다.
func TestCheckRulesIndependent(t *testing.T) {
	d := &graph.Document{
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "example.com/m/a", Kind: graph.KindPackage},
			{ID: "example.com/m/b", Kind: graph.KindPackage},
			{ID: "example.com/m/c", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			{From: "example.com/m/a", To: "example.com/m/c", Kind: graph.EdgeImport},
			{From: "example.com/m/c", To: "example.com/m/b", Kind: graph.EdgeImport},
		},
	}
	cfg := &config.File{
		Components: map[string][]string{
			"a": {"a"}, "b": {"b"}, "c": {"c"},
		},
		Deps:        map[string][]string{"a": {"c"}, "c": {"b"}},
		Independent: []string{"a", "b"},
	}
	// a→c→b 한 방향만 뚫려 있다 — 위반 하나.
	violations := CheckRules(d, cfg).Violations
	if len(violations) != 1 {
		t.Fatalf("one-way reach must be one violation: %+v", violations)
	}
	v := violations[0]
	if v.Rule != "independence" || v.FromComponent != "a" || v.ToComponent != "b" {
		t.Fatalf("unexpected independence violation: %+v", v)
	}
	// 반대 방향도 뚫리면 양쪽 모두 보고한다 — 직접 간선은 deps가
	// 허용하게 두어 independence 위반만 남긴다.
	cfg.Deps["b"] = []string{"a"}
	d.Edges = append(d.Edges, graph.Edge{
		From: "example.com/m/b", To: "example.com/m/a", Kind: graph.EdgeImport})
	if v := CheckRules(d, cfg).Violations; len(v) != 2 {
		t.Fatalf("both directions must be reported: %+v", v)
	}
	// 독립 목록에서 빠지면 직접 간선만 남아 deps가 허용한다.
	cfg.Independent = nil
	if v := CheckRules(d, cfg).Violations; len(v) != 0 {
		t.Fatalf("without independent the graph is legal: %+v", v)
	}
}

// TestFileRules는 import 지점의 파일 패턴으로 스코프가 좁혀지는지 확인한다.
// dep-cruiser의 not-to-dev-dep 계약이다 — "!*_test.go"는 프로덕션
// 파일에서의 import만 위반으로 잡아야 하고 _test.go 지점은 무사해야 한다.
func TestFileRules(t *testing.T) {
	doc := &graph.Document{
		Module: "example.com/m", ModuleDir: "/m",
		Vertices: []graph.Vertex{
			{ID: "example.com/m/web", Kind: graph.KindPackage},
			{ID: "example.com/m/testhelp", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			{From: "example.com/m/web", To: "example.com/m/testhelp",
				Kind: graph.EdgeImport, Positions: []graph.Position{
					{File: "/m/web/web.go", Line: 3},
					{File: "/m/web/web_test.go", Line: 3},
				}},
		},
	}
	cfg := &config.File{
		Components: map[string][]string{
			"web": {"web"}, "testhelp": {"testhelp"},
		},
		Deps: map[string][]string{"web": {"testhelp"}},
		FileRules: []config.FileRule{
			{Name: "no-testdeps-in-prod", From: "!*_test.go", To: "testhelp",
				Reason: "keep test helpers out of production code"},
		},
	}
	rep := CheckRules(doc, cfg)
	if len(rep.Violations) != 1 {
		t.Fatalf("expected exactly 1 fileScope violation (prod file only): %+v",
			rep.Violations)
	}
	v := rep.Violations[0]
	if v.Rule != "fileScope" || v.Name != "no-testdeps-in-prod" ||
		v.Position == nil || !strings.HasSuffix(v.Position.File, "web/web.go") {
		t.Fatalf("violation must point at the production file: %+v", v)
	}
	if v.Reason == "" {
		t.Fatal("configured reason must ride the violation")
	}
}

// TestFileRulesNoPositions는 지점이 없는 import 간선이 조용히 통과하지 않고
// fileScopeUnchecked로 세어지는지 확인한다 — 검사 불가를 숨기면
// "규칙이 지켜졌다"는 착각을 만든다.
func TestFileRulesNoPositions(t *testing.T) {
	doc := rulesDoc()
	cfg := rulesCfg()
	cfg.FileRules = []config.FileRule{
		{Name: "r", From: "!*_test.go", To: "db"},
	}
	rep := CheckRules(doc, cfg)
	if rep.FileScopeUnchecked != 1 {
		t.Fatalf("positionless edge to a fileRules target must be counted: %+v", rep)
	}
}

// TestStability는 안정성 방향 규칙 — 더 안정된 컴포넌트가 덜 안정된
// 쪽을 향해 의존하면 위반인지 확인한다. dep-cruiser moreUnstable 패리티:
// 불안정도 I = Ce/(Ca+Ce)가 target > source면 깨진다.
func TestStability(t *testing.T) {
	// I 값: a = 1/3(in 2, out 1), b = 1/2(in 1, out 1), c = 0,
	// leaf1·leaf2 = 1. a→b만 0.5 > 0.33으로 위반이다.
	doc := &graph.Document{
		Module: "m",
		Vertices: []graph.Vertex{
			{ID: "m/a", Kind: graph.KindPackage},
			{ID: "m/b", Kind: graph.KindPackage},
			{ID: "m/c", Kind: graph.KindPackage},
			{ID: "m/leaf1", Kind: graph.KindPackage},
			{ID: "m/leaf2", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			{From: "m/leaf1", To: "m/a", Kind: graph.EdgeImport},
			{From: "m/leaf2", To: "m/a", Kind: graph.EdgeImport},
			{From: "m/a", To: "m/b", Kind: graph.EdgeImport},
			{From: "m/b", To: "m/c", Kind: graph.EdgeImport},
		},
	}
	cfg := &config.File{
		Components: map[string][]string{
			"a": {"a"}, "b": {"b"}, "c": {"c"},
			"leaf1": {"leaf1"}, "leaf2": {"leaf2"},
		},
		// 허용 목록을 채워 두어야 stability 위반만이 보인다 —
		// allow 위반이 섞이면 방향 규칙의 검증이 아니게 된다.
		Deps: map[string][]string{
			"leaf1": {"a"}, "leaf2": {"a"},
			"a": {"b"}, "b": {"c"},
		},
		Stability: true,
	}
	rep := CheckRules(doc, cfg)
	var stab *Violation
	for i := range rep.Violations {
		if rep.Violations[i].Rule == "stability" {
			stab = &rep.Violations[i]
		}
	}
	if stab == nil {
		t.Fatalf("a(I=1/3) depending on b(I=1/2) must violate: %+v",
			rep.Violations)
	}
	if stab.FromComponent != "a" || stab.ToComponent != "b" {
		t.Fatalf("violation must name the components: %+v", stab)
	}
	// leaf1(I=1) → a(I=1/3): 불안정한 쪽이 안정한 쪽을 향하는 건 허용.
	for _, v := range rep.Violations {
		if v.Rule == "stability" && v.FromComponent == "leaf1" {
			t.Fatalf("unstable→stable direction is allowed: %+v", v)
		}
	}
	// 끄면 조용해야 한다 — opt-in 규칙이다.
	cfg.Stability = false
	rep = CheckRules(doc, cfg)
	for _, v := range rep.Violations {
		if v.Rule == "stability" {
			t.Fatal("stability must not fire when disabled")
		}
	}
}

// TestLimits는 컴포넌트 의존 수 상한 위반을 확인한다.
// 상한 초과는 간선 하나의 위반이 아니라 컴포넌트 덩치의 사실이다 —
// 정점 From/To는 비고 실제 수치가 사유에 실려야 한다.
func TestLimits(t *testing.T) {
	// web은 svc와 db 둘에 의존한다 — maxOut 1이면 위반이다.
	doc := &graph.Document{
		Module: "m",
		Vertices: []graph.Vertex{
			{ID: "m/web", Kind: graph.KindPackage},
			{ID: "m/svc", Kind: graph.KindPackage},
			{ID: "m/db", Kind: graph.KindPackage},
			{ID: "m/api", Kind: graph.KindPackage},
		},
		Edges: []graph.Edge{
			{From: "m/web", To: "m/svc", Kind: graph.EdgeImport},
			{From: "m/web", To: "m/db", Kind: graph.EdgeImport},
			{From: "m/api", To: "m/db", Kind: graph.EdgeImport},
		},
	}
	maxOut, maxIn := 1, 1
	cfg := &config.File{
		Components: map[string][]string{
			"web": {"web"}, "svc": {"svc"}, "db": {"db"}, "api": {"api"},
		},
		Deps: map[string][]string{
			"web": {"svc", "db"}, "api": {"db"},
		},
		Limits: []config.LimitRule{
			{Component: "web", MaxOut: &maxOut},
			{Component: "db", MaxIn: &maxIn},
		},
	}
	rep := CheckRules(doc, cfg)
	var outV, inV *Violation
	for i := range rep.Violations {
		v := &rep.Violations[i]
		if v.Rule != "limit" {
			continue
		}
		switch v.Name {
		case "maxOut":
			outV = v
		case "maxIn":
			inV = v
		}
	}
	if outV == nil || outV.FromComponent != "web" ||
		!strings.Contains(outV.Reason, "depends on 2") {
		t.Fatalf("web maxOut=1 with 2 deps must violate: %+v", rep.Violations)
	}
	if inV == nil || inV.ToComponent != "db" ||
		!strings.Contains(inV.Reason, "depended on by 2") {
		t.Fatalf("db maxIn=1 with 2 dependents must violate: %+v", rep.Violations)
	}
	// 경계 — 상한과 같은 수는 위반이 아니다.
	maxOut = 2
	maxIn = 2
	rep = CheckRules(doc, cfg)
	for _, v := range rep.Violations {
		if v.Rule == "limit" {
			t.Fatalf("at-limit must not violate: %+v", v)
		}
	}
	// baseline 키는 수치가 변해도 같아야 한다 — 덩치가 더 커져도 같은 위반이다.
	v1 := Violation{Rule: "limit", Name: "maxOut", FromComponent: "web",
		Reason: "depends on 2 components; limit maxOut is 1"}
	v2 := Violation{Rule: "limit", Name: "maxOut", FromComponent: "web",
		Reason: "depends on 5 components; limit maxOut is 1"}
	if ViolationBaselineKey(v1) != ViolationBaselineKey(v2) {
		t.Fatal("limit key must not depend on the observed count")
	}
}
