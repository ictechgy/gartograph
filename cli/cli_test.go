package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// run은 CLI를 메모리 스트림으로 실행해 종료 코드와 출력을 잡는다.
// 종료 코드 계약(0/1/2)은 에이전트 소비의 핵심이라 실제 호출 경로로 검증한다.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func fixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

import _ "example.com/fixture/b"
`,
		"b/b.go": `package b
`,
	})
}

// TestUsageErrors는 사용법 오류가 2로 돌아가는지 확인한다.
func TestUsageErrors(t *testing.T) {
	if code, _, _ := run(t); code != 2 {
		t.Fatalf("no args: expected 2, got %d", code)
	}
	if code, _, _ := run(t, "nope"); code != 2 {
		t.Fatalf("unknown command: expected 2, got %d", code)
	}
	if code, _, _ := run(t, "query"); code != 2 {
		t.Fatalf("query without id: expected 2, got %d", code)
	}
	if code, _, _ := run(t, "version"); code != 0 {
		t.Fatalf("version: expected 0, got %d", code)
	}
}

// TestGraphJSON은 graph 명령이 파싱 가능한 결정적 JSON을 내는지 확인한다.
func TestGraphJSON(t *testing.T) {
	dir := fixture(t)
	code, out, errb := run(t, "graph", "--dir", dir)
	if code != 0 {
		t.Fatalf("graph failed: %d %s", code, errb)
	}
	var doc struct {
		Vertices []struct{ ID string } `json:"vertices"`
		Edges    []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("graph output is not JSON: %v\n%s", err, out)
	}
	if len(doc.Vertices) != 2 || len(doc.Edges) != 1 {
		t.Fatalf("unexpected graph: %+v", doc)
	}
}

// TestCyclesStrict는 순환이 없는 모듈에서 strict가 0을 돌려주는지 확인한다.
// Go 컴파일러가 패키지 순환을 막으므로 fixture로 순환 케이스는 만들 수 없다 —
// 순환 케이스는 analysis 단위 테스트가 합성 문서로 검증한다.
func TestCyclesStrict(t *testing.T) {
	dir := fixture(t)
	code, out, _ := run(t, "cycles", "--dir", dir, "--strict")
	if code != 0 {
		t.Fatalf("cycles --strict on acyclic module: %d %s", code, out)
	}
}

// TestQuery는 query 명령의 JSON 계약을 확인한다.
func TestQuery(t *testing.T) {
	dir := fixture(t)
	code, out, _ := run(t, "query", "example.com/fixture/a", "--dir", dir)
	if code != 0 {
		t.Fatalf("query failed: %d", code)
	}
	var res struct {
		Depth     int `json:"depth"`
		DependsOn []struct {
			ID    string   `json:"id"`
			Kinds []string `json:"edges"`
		} `json:"dependsOn"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("query output is not JSON: %v", err)
	}
	if res.Depth != 1 || len(res.DependsOn) != 1 ||
		res.DependsOn[0].ID != "example.com/fixture/b" ||
		res.DependsOn[0].Kinds[0] != "import" {
		t.Fatalf("unexpected query result: %s", out)
	}
}

// TestQueryNotFound는 없는 정점이 2로 돌아가는지 확인한다.
func TestQueryNotFound(t *testing.T) {
	dir := fixture(t)
	code, _, errb := run(t, "query", "example.com/missing", "--dir", dir)
	if code != 2 {
		t.Fatalf("expected 2, got %d", code)
	}
	if !strings.Contains(errb, "not found") {
		t.Fatalf("expected not-found message: %s", errb)
	}
}

// TestIDCommandsFindSymbolVertices는 정점 ID를 받는 명령이 --level 없이도
// 함수·메서드·타입 ID를 찾는지 확인한다. 기본 수확이 package 레벨이면
// 이 정점들이 문서에 없어 "vertex not found"로 오답이 났다.
func TestIDCommandsFindSymbolVertices(t *testing.T) {
	dir := deadFixture(t)
	for _, args := range [][]string{
		{"query", "example.com/fixture/lib.Run"},
		{"impact", "example.com/fixture/lib.helper"},
		{"path", "example.com/fixture/lib.Run", "example.com/fixture/lib.helper"},
		{"shared", "example.com/fixture/lib.Run", "example.com/fixture.main"},
	} {
		code, _, errb := run(t, append(args, "--dir", dir)...)
		if code != 0 {
			t.Fatalf("%v: expected 0, got %d %s", args, code, errb)
		}
	}
}

// TestIDCommandsLevelFlag는 --level package가 수확을 좁히고, 그 레벨에서
// 못 찾은 심볼 ID는 "없다"가 아니라 "그 레벨이라 못 봤다"로 알리는지 확인한다.
func TestIDCommandsLevelFlag(t *testing.T) {
	dir := deadFixture(t)
	code, _, errb := run(t, "query", "example.com/fixture/lib.Run",
		"--dir", dir, "--level", "package")
	if code != 2 {
		t.Fatalf("symbol ID at package level: expected 2, got %d", code)
	}
	if !strings.Contains(errb, "not found") || !strings.Contains(errb, "package level") {
		t.Fatalf("expected level hint on not-found: %s", errb)
	}
	if code, _, errb := run(t, "query", "example.com/fixture/lib",
		"--dir", dir, "--level", "package"); code != 0 {
		t.Fatalf("package ID at package level: %d %s", code, errb)
	}
	if code, _, _ := run(t, "path", "a", "b", "--dir", dir, "--level", "nope"); code != 2 {
		t.Fatalf("bad level must exit 2, got %d", code)
	}
}

// deadFixture는 main과 미도달 심볼이 있는 모듈이다.
func deadFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Run() }
`,
		"lib/lib.go": `package lib

func Run() { helper() }
func helper() {}
func Unused() {}
`,
	})
}

// TestDead는 도달 불가 보고와 strict 종료 코드를 확인한다.
// unreachable은 그래프 사실이지 삭제 판정이 아니다.
func TestDead(t *testing.T) {
	dir := deadFixture(t)
	code, out, errb := run(t, "dead", "--dir", dir, "--strict")
	if code != 1 {
		t.Fatalf("dead --strict with unreachable: expected 1, got %d %s", code, errb)
	}
	if !strings.Contains(out, "example.com/fixture/lib.Unused") {
		t.Fatalf("expected Unused reported: %s", out)
	}
	if strings.Contains(out, "lib.helper") || strings.Contains(out, "lib.Run") {
		t.Fatalf("reachable symbol reported dead: %s", out)
	}

	// --retain-public이면 공개 Unused도 루트가 되어 findings가 빈다.
	code, out, _ = run(t, "dead", "--dir", dir, "--retain-public")
	if code != 0 || !strings.Contains(out, "0 unreachable") {
		t.Fatalf("retain-public: %d %s", code, out)
	}
}

// TestDeadExplain은 --explain의 경로 출력을 확인한다.
func TestDeadExplain(t *testing.T) {
	dir := deadFixture(t)
	code, out, errb := run(t, "dead", "--dir", dir,
		"--explain", "example.com/fixture/lib.helper")
	if code != 0 {
		t.Fatalf("explain failed: %d %s", code, errb)
	}
	if !strings.Contains(out, "example.com/fixture.main") ||
		!strings.Contains(out, "lib.helper") {
		t.Fatalf("expected root-to-target path: %s", out)
	}
}

// TestDeadRTA는 --algo rta가 CHA의 과대 근사를 좁히는지 확인한다.
// 인터페이스 디스패치는 CHA에서 모든 구현으로 팬아웃하지만, 인스턴스화
// 되지 않은 구현체는 RTA에서 도달 불가다.
func TestDeadRTA(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/impl"

func main() {
	var d impl.Doer = impl.A{}
	d.Do()
}
`,
		"impl/impl.go": `package impl

type Doer interface{ Do() }

type A struct{}

func (A) Do() {}

// B는 어디서도 만들어지지 않는다 — CHA는 B.Do를 살아 있다고 보지만
// RTA는 보지 않는다.
type B struct{}

func (B) Do() {}
`,
	})
	// CHA는 B.Do를 도달 가능으로 본다(과대 근사).
	code, out, _ := run(t, "dead", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("dead cha failed: %d", code)
	}
	if strings.Contains(out, "(B).Do") {
		t.Fatalf("cha must keep B.Do alive: %s", out)
	}
	// RTA는 인스턴스화된 타입만 보므로 B.Do가 unreachable로 나온다.
	code, out, _ = run(t, "dead", "--dir", dir, "--format", "json", "--algo", "rta")
	if code != 0 {
		t.Fatalf("dead rta failed: %d", code)
	}
	var rep struct {
		Algorithm   string `json:"algorithm"`
		Unreachable []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		} `json:"unreachable"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if rep.Algorithm != "rta" {
		t.Fatalf("report must name the algorithm: %s", out)
	}
	var found bool
	for _, f := range rep.Unreachable {
		if strings.HasSuffix(f.ID, "(B).Do") {
			found = true
			if f.Reason != "not reachable under rapid type analysis" {
				t.Fatalf("rta finding must carry the rta reason: %+v", f)
			}
		}
	}
	if !found {
		t.Fatalf("rta must report uninstantiated (B).Do: %s", out)
	}
	// 저장 문서 위에서는 rta가 성립하지 않는다 — SSA 재료가 없다.
	if code, _, _ := run(t, "dead", "--algo", "rta", "--graph", "x.json"); code != 2 {
		t.Fatalf("rta on a saved graph must be a usage error: %d", code)
	}
	if code, _, _ := run(t, "dead", "--algo", "bogus", "--dir", dir); code != 2 {
		t.Fatalf("unknown algo must be a usage error: %d", code)
	}
}

// TestRules는 규칙 위반과 strict 종료 코드를 확인한다.
func TestRules(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"web/web.go": `package web

import _ "example.com/fixture/db"
`,
		"db/db.go": `package db
`,
		".gartograph.yml": `components:
  web: ["web"]
  db: ["db"]
deps: {}
`,
	})
	code, out, errb := run(t, "rules", "--dir", dir, "--strict")
	if code != 1 {
		t.Fatalf("rules --strict with violation: expected 1, got %d %s", code, errb)
	}
	if !strings.Contains(out, "violation") || !strings.Contains(out, "web") {
		t.Fatalf("expected violation output: %s", out)
	}
}

// TestImpact는 역방향 전이 질의의 종료 코드와 내용을 확인한다.
func TestImpact(t *testing.T) {
	dir := fixture(t)
	code, out, errb := run(t, "impact", "example.com/fixture/b", "--dir", dir)
	if code != 0 {
		t.Fatalf("impact failed: %d %s", code, errb)
	}
	var res struct {
		ID        string `json:"id"`
		Dependers []struct {
			ID    string `json:"id"`
			Depth int    `json:"depth"`
		} `json:"dependers"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("impact output is not JSON: %v", err)
	}
	if len(res.Dependers) != 1 || res.Dependers[0].ID != "example.com/fixture/a" ||
		res.Dependers[0].Depth != 1 {
		t.Fatalf("expected a as depth-1 depender of b: %s", out)
	}
	if code, _, _ := run(t, "impact", "example.com/missing", "--dir", dir); code != 2 {
		t.Fatalf("impact on missing vertex: expected 2, got %d", code)
	}
	if code, _, _ := run(t, "impact", "--dir", dir); code != 2 {
		t.Fatalf("impact without id: expected 2, got %d", code)
	}
}

// TestImpactFiles는 파일 집합 모드의 루트 해석과 역방향 클로저를 확인한다.
// lib/lib.go를 바꾸면 그 안의 심볼들과 lib 패키지가 루트가 되고,
// 그 의존자로 main이 모여야 한다.
func TestImpactFiles(t *testing.T) {
	dir := deadFixture(t)
	code, out, errb := run(t, "impact", "--dir", dir,
		"--files", "lib/lib.go", "--files", "README.md")
	if code != 0 {
		t.Fatalf("impact --files failed: %d %s", code, errb)
	}
	var res struct {
		Files         []string `json:"files"`
		Roots         []string `json:"roots"`
		UnmappedFiles []string `json:"unmappedFiles"`
		Dependers     []struct {
			ID string `json:"id"`
		} `json:"dependers"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	var rootSet = map[string]bool{}
	for _, r := range res.Roots {
		rootSet[r] = true
	}
	if !rootSet["example.com/fixture/lib"] || !rootSet["example.com/fixture/lib.Unused"] {
		t.Fatalf("lib/lib.go must map to package and declared symbols: %v", res.Roots)
	}
	if len(res.UnmappedFiles) != 1 || res.UnmappedFiles[0] != "README.md" {
		t.Fatalf("README.md must be unmapped: %v", res.UnmappedFiles)
	}
	var sawMain bool
	for _, d := range res.Dependers {
		sawMain = sawMain || d.ID == "example.com/fixture.main" ||
			d.ID == "example.com/fixture"
	}
	if !sawMain {
		t.Fatalf("main must depend on changed lib: %s", out)
	}
}

// TestImpactSince는 git diff 기반 모드가 실제 git worktree에서 동작하는지 확인한다.
func TestImpactSince(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := deadFixture(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	// lib.go를 바꾼다 — 작업 트리가 HEAD와 달라진다.
	lib := filepath.Join(dir, "lib", "lib.go")
	src, _ := os.ReadFile(lib)
	if err := os.WriteFile(lib, append(src, []byte("func Changed() {}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb := run(t, "impact", "--dir", dir, "--since", "HEAD")
	if code != 0 {
		t.Fatalf("impact --since failed: %d %s", code, errb)
	}
	var res struct {
		Files []string `json:"files"`
		Roots []string `json:"roots"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(res.Files) != 1 || res.Files[0] != "lib/lib.go" {
		t.Fatalf("git diff must report lib/lib.go: %s", out)
	}
	if len(res.Roots) == 0 {
		t.Fatalf("changed file must resolve to vertices: %s", out)
	}
	// 존재하지 않는 rev는 git 오류를 그대로 2로 돌린다 — 조용히 빈 diff로
	// 보면 "영향 없음"으로 오독된다.
	if code, _, _ := run(t, "impact", "--dir", dir, "--since", "no-such-rev"); code != 2 {
		t.Fatalf("bad rev must be an error: got %d", code)
	}
}

// TestRulesSarif는 SARIF 출력이 스키마를 갖춘 JSON인지 확인한다.
func TestRulesSarif(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"web/web.go": `package web

import _ "example.com/fixture/db"
`,
		"db/db.go": `package db
`,
		".gartograph.yml": `components:
  web: ["web"]
  db: ["db"]
deps: {}
`,
	})
	code, out, errb := run(t, "rules", "--dir", dir, "--format", "sarif")
	if code != 0 {
		t.Fatalf("rules --format sarif failed: %d %s", code, errb)
	}
	var doc struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []struct {
				RuleID    string `json:"ruleId"`
				Locations []struct {
					PhysicalLocation *struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("sarif output is not JSON: %v", err)
	}
	if doc.Version != "2.1.0" || len(doc.Runs) != 1 {
		t.Fatalf("not a SARIF 2.1.0 log: %s", out[:120])
	}
	if len(doc.Runs[0].Results) != 1 || doc.Runs[0].Results[0].RuleID != "layer-allow" {
		t.Fatalf("expected one layer-allow result: %s", out)
	}
	// v2 문서는 위반 지점을 안다 — SARIF에 물리 위치가 실려야 한다.
	phys := doc.Runs[0].Results[0].Locations[0].PhysicalLocation
	if phys == nil || !strings.HasSuffix(phys.ArtifactLocation.URI, "web/web.go") ||
		phys.Region.StartLine == 0 {
		t.Fatalf("violation must carry physicalLocation: %s", out)
	}
}

// TestRulesDeny는 deny 규칙이 deps를 이기고 위반으로 보고되는지 확인한다.
func TestRulesDeny(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"web/web.go": `package web

import _ "example.com/fixture/db"
`,
		"db/db.go": `package db
`,
		".gartograph.yml": `components:
  web: ["web"]
  db: ["db"]
deps:
  web: ["db"]
deny:
  web: ["db"]
`,
	})
	code, out, _ := run(t, "rules", "--dir", dir)
	if code != 0 {
		t.Fatalf("rules failed: %d", code)
	}
	if !strings.Contains(out, "violation[deny]") {
		t.Fatalf("deny must override deps: %s", out)
	}
}

// rulesFixture는 web→db 위반이 있는 모듈과 규칙 파일을 만든다.
// api 패키지는 비어 있지만 컴포넌트에 매핑돼 있어, import가 생기면
// 즉시 규칙 검사 대상이 된다 — baseline 테스트의 "새 위반" 시나리오용이다.
func rulesFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"web/web.go": `package web

import _ "example.com/fixture/db"
`,
		"db/db.go": `package db
`,
		"api/api.go": `package api
`,
		".gartograph.yml": `components:
  web: ["web"]
  db: ["db"]
  api: ["api"]
deps: {}
`,
	})
}

// TestRulesBaseline은 baseline 기록→재비교→신규 위반 검출의 전체 흐름을 확인한다.
func TestRulesBaseline(t *testing.T) {
	dir := rulesFixture(t)
	base := filepath.Join(t.TempDir(), "baseline.json")

	// 위반을 baseline으로 기록한다.
	code, _, errb := run(t, "rules", "--dir", dir, "--write-baseline", base)
	if code != 0 {
		t.Fatalf("--write-baseline failed: %d %s", code, errb)
	}
	// 기록된 위반은 baselined로 넘어가고 strict도 0이다.
	code, out, errb := run(t, "rules", "--dir", dir,
		"--baseline", base, "--strict", "--format", "json")
	if code != 0 {
		t.Fatalf("baselined violation must not fail strict: %d %s", code, errb)
	}
	var rep struct {
		Violations []struct{ ID string } `json:"violations"`
		Baselined  []struct {
			From string `json:"from"`
		} `json:"baselined"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rep.Violations) != 0 || len(rep.Baselined) != 1 {
		t.Fatalf("expected 0 fresh + 1 baselined: %s", out)
	}

	// 새 위반이 생기면 baseline을 뚫고 fresh로 보고된다.
	if err := os.WriteFile(filepath.Join(dir, "api", "api.go"),
		[]byte("package api\n\nimport _ \"example.com/fixture/db\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = run(t, "rules", "--dir", dir, "--baseline", base, "--strict")
	if code != 1 {
		t.Fatalf("new violation past baseline must fail strict: %d %s", code, out)
	}
	if !strings.Contains(out, "1 violations (1 baselined") {
		t.Fatalf("expected 1 fresh + 1 baselined: %s", out)
	}
}

// TestRulesBaselineStale는 고쳐진 위반이 stale로 보고되는지 확인한다.
func TestRulesBaselineStale(t *testing.T) {
	dir := rulesFixture(t)
	base := filepath.Join(t.TempDir(), "baseline.json")
	if code, _, _ := run(t, "rules", "--dir", dir, "--write-baseline", base); code != 0 {
		t.Fatal("write baseline")
	}
	// 위반을 고친다 — web의 db import를 제거.
	if err := os.WriteFile(filepath.Join(dir, "web", "web.go"),
		[]byte("package web\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "rules", "--dir", dir, "--baseline", base)
	if code != 0 {
		t.Fatalf("rules failed: %d", code)
	}
	if !strings.Contains(out, "regenerate the baseline") {
		t.Fatalf("expected stale baseline notice: %s", out)
	}
}

// TestRulesNoConfig는 설정 파일이 없으면 사용법 오류(2)인지 확인한다.
// 규칙 없이 "위반 없음"을 뱉으면 소비자가 규칙이 검사됐다고 오해한다.
func TestRulesNoConfig(t *testing.T) {
	code, _, _ := run(t, "rules", "--dir", fixture(t))
	if code != 2 {
		t.Fatalf("rules without config: expected 2, got %d", code)
	}
}

// TestGraphFileRoundTrip은 --out으로 저장한 문서를 --graph로 읽는 경로를 확인한다.
func TestGraphFileRoundTrip(t *testing.T) {
	dir := deadFixture(t)
	path := filepath.Join(t.TempDir(), "graph.json")
	code, _, errb := run(t, "graph", "--dir", dir, "--level", "symbol", "--out", path)
	if code != 0 {
		t.Fatalf("graph --out failed: %d %s", code, errb)
	}
	// 저장 문서로 dead를 돌린다 — 수확 없이 파일만으로 분석돼야 한다.
	code, out, errb := run(t, "dead", "--graph", path)
	if code != 0 {
		t.Fatalf("dead --graph failed: %d %s", code, errb)
	}
	if !strings.Contains(out, "lib.Unused") {
		t.Fatalf("expected Unused from saved doc: %s", out)
	}
	// 패키지 레벨 문서에 심볼 질의를 걸면 레벨 부족을 알려야 한다.
	code, _, errb = run(t, "graph", "--dir", dir, "--out", path)
	if code != 0 {
		t.Fatalf("graph --out (package) failed: %d %s", code, errb)
	}
	code, _, errb = run(t, "dead", "--graph", path)
	if code != 2 || !strings.Contains(errb, "re-harvest") {
		t.Fatalf("expected level-mismatch error, got %d %s", code, errb)
	}
}

// TestUnknownFormat은 지원하지 않는 형식이 사용법 오류(2)인지 확인한다.
// 모르는 형식을 조용히 text로 출력하면 소비자가 깨진 출력을 받는다.
func TestUnknownFormat(t *testing.T) {
	dir := deadFixture(t)
	for _, args := range [][]string{
		{"cycles", "--dir", dir, "--format", "xml"},
		{"dead", "--dir", dir, "--format", "xml"},
		{"rules", "--dir", dir, "--config", "/dev/null", "--format", "xml"},
		{"graph", "--dir", dir, "--format", "xml"},
	} {
		if code, _, _ := run(t, args...); code != 2 {
			t.Fatalf("%v: expected 2, got %d", args, code)
		}
	}
}

// TestBaselineEnvelope는 그래프 문서나 빈 JSON을 baseline으로 읽지 않는지
// 확인한다 — 봉투 없는 파일을 받으면 모든 위반이 fresh로 오인된다.
func TestBaselineEnvelope(t *testing.T) {
	dir := rulesFixture(t)
	// 그래프 JSON을 baseline으로 먹이면 형식 오류(2)여야 한다.
	gpath := filepath.Join(t.TempDir(), "graph.json")
	if code, _, _ := run(t, "graph", "--dir", dir, "--out", gpath); code != 0 {
		t.Fatal("graph --out failed")
	}
	if code, _, _ := run(t, "rules", "--dir", dir, "--baseline", gpath); code != 2 {
		t.Fatalf("graph JSON must not be accepted as baseline: got %d", code)
	}
	// 잘못된 --format과 --write-baseline 조합은 파일을 쓰기 전에 거부다.
	bpath := filepath.Join(t.TempDir(), "baseline.json")
	if code, _, _ := run(t, "rules", "--dir", dir,
		"--write-baseline", bpath, "--format", "xml"); code != 2 {
		t.Fatalf("bad format must be rejected before writing baseline: got %d", code)
	}
	if _, err := os.Stat(bpath); !os.IsNotExist(err) {
		t.Fatal("rejected invocation must not have written a baseline file")
	}
}

// TestModuleLevelRejected는 모듈 레벨 문서가 패키지 데이터를 필요로 하는
// 명령에서 성공(0)처럼 빈 리포트를 내지 않는지 확인한다.
func TestModuleLevelRejected(t *testing.T) {
	dir := rulesFixture(t)
	gpath := filepath.Join(t.TempDir(), "mod.json")
	code, _, errb := run(t, "graph", "--dir", dir, "--level", "module", "--out", gpath)
	if code != 0 {
		t.Fatalf("module graph failed: %d %s", code, errb)
	}
	if code, _, _ := run(t, "metrics", "--graph", gpath); code != 2 {
		t.Fatalf("metrics on module doc: expected 2, got %d", code)
	}
	if code, _, _ := run(t, "mapping", "--dir", dir, "--graph", gpath); code != 2 {
		t.Fatalf("mapping on module doc: expected 2, got %d", code)
	}
}

// TestHarvestFlags는 --pattern·--root 같은 반복 플래그가 파싱되는지 확인한다.
// flag.Value.Set이 실제 호출 경로에서 동작해야 한다.
func TestHarvestFlags(t *testing.T) {
	dir := deadFixture(t)
	// --pattern은 루트만 좁힌다 — import로 도달되는 내부 패키지는 여전히 정점이다.
	// ./lib만 루트로 잡으면 main 패키지가 빠지고 lib만 남는다.
	code, out, errb := run(t, "graph", "--dir", dir, "--pattern", "./lib")
	if code != 0 {
		t.Fatalf("graph --pattern failed: %d %s", code, errb)
	}
	if !strings.Contains(out, "example.com/fixture/lib") ||
		strings.Contains(out, `"id": "example.com/fixture"`) {
		t.Fatalf("--pattern ./lib must hold only lib: %s", out)
	}
	// dead --root는 추가 보존 루트를 받는다 — 없는 루트는 unknownRoots로 나온다.
	code, out, _ = run(t, "dead", "--dir", dir, "--format", "json",
		"--root", "example.com/fixture/lib.Unused", "--root", "ghost")
	if code != 0 {
		t.Fatalf("dead --root failed: %d", code)
	}
	var rep struct {
		Roots        []string              `json:"roots"`
		UnknownRoots []string              `json:"unknownRoots"`
		Unreachable  []struct{ ID string } `json:"unreachable"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rep.Unreachable) != 0 {
		t.Fatalf("Unused must be retained as explicit root: %s", out)
	}
	if len(rep.UnknownRoots) != 1 || rep.UnknownRoots[0] != "ghost" {
		t.Fatalf("expected ghost in unknownRoots: %s", out)
	}
}

// TestCyclesJSON은 cycles의 JSON 출력과 --level 투영을 확인한다.
func TestCyclesJSON(t *testing.T) {
	dir := deadFixture(t)
	code, out, errb := run(t, "cycles", "--dir", dir,
		"--level", "symbol", "--format", "json", "--strict")
	if code != 0 {
		t.Fatalf("cycles json failed: %d %s", code, errb)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[") &&
		!strings.Contains(out, "null") {
		t.Fatalf("cycles --format json must emit JSON: %s", out)
	}
}

// TestQueryDependedBy는 역방향 이웃이 정렬·보고되는지 확인한다.
func TestQueryDependedBy(t *testing.T) {
	dir := deadFixture(t)
	code, out, errb := run(t, "query", "example.com/fixture/lib", "--dir", dir)
	if code != 0 {
		t.Fatalf("query failed: %d %s", code, errb)
	}
	var res struct {
		DependedBy []struct{ ID string } `json:"dependedBy"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(res.DependedBy) != 1 ||
		res.DependedBy[0].ID != "example.com/fixture" {
		t.Fatalf("expected dependedBy main pkg: %s", out)
	}
}

// TestGraphMermaid는 graph의 mermaid 출력 경로를 확인한다.
func TestGraphMermaid(t *testing.T) {
	dir := fixture(t)
	code, out, errb := run(t, "graph", "--dir", dir, "--format", "mermaid")
	if code != 0 {
		t.Fatalf("mermaid failed: %d %s", code, errb)
	}
	if !strings.Contains(out, "flowchart LR") {
		t.Fatalf("expected mermaid flowchart: %s", out)
	}
}

// TestPath는 path 명령의 최단 경로와 notFound/사용법 계약을 확인한다.
func TestPath(t *testing.T) {
	dir := fixture(t)
	code, out, errb := run(t, "path", "example.com/fixture/a",
		"example.com/fixture/b", "--dir", dir)
	if code != 0 {
		t.Fatalf("path failed: %d %s", code, errb)
	}
	var res struct {
		Found bool `json:"found"`
		Hops  []struct {
			ID    string   `json:"id"`
			Kinds []string `json:"edges"`
		} `json:"hops"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("path output is not JSON: %v", err)
	}
	if !res.Found || len(res.Hops) != 2 ||
		res.Hops[1].ID != "example.com/fixture/b" ||
		res.Hops[1].Kinds[0] != "import" {
		t.Fatalf("unexpected path result: %s", out)
	}
	// 역방향 경로는 없다 — found:false가 그래프 사실로 돌아와야 한다.
	code, out, _ = run(t, "path", "example.com/fixture/b",
		"example.com/fixture/a", "--dir", dir)
	if code != 0 {
		t.Fatalf("path (no route) must still exit 0: %d", code)
	}
	var none struct {
		Found bool `json:"found"`
	}
	if err := json.Unmarshal([]byte(out), &none); err != nil || none.Found {
		t.Fatalf("expected found:false, got %s", out)
	}
	if code, _, _ := run(t, "path", "example.com/fixture/a", "ghost",
		"--dir", dir); code != 2 {
		t.Fatalf("path to missing vertex: expected 2, got %d", code)
	}
	if code, _, _ := run(t, "path", "onlyone", "--dir", dir); code != 2 {
		t.Fatalf("path with one arg: expected 2, got %d", code)
	}
}

// TestGraphDot은 graph의 Graphviz dot 출력 경로를 확인한다.
func TestGraphDot(t *testing.T) {
	dir := fixture(t)
	code, out, errb := run(t, "graph", "--dir", dir, "--format", "dot")
	if code != 0 {
		t.Fatalf("dot failed: %d %s", code, errb)
	}
	if !strings.Contains(out, "digraph") ||
		!strings.Contains(out, `"example.com/fixture/a" -> "example.com/fixture/b"`) {
		t.Fatalf("expected dot digraph with the import edge: %s", out)
	}
}

// TestMetrics는 결합도 출력과 설정 없을 때의 패키지 단위 폴백을 확인한다.
func TestMetrics(t *testing.T) {
	dir := fixture(t)
	// 설정이 없는 fixture — 패키지 단위로 계산되고 limitation에 남는다.
	code, out, errb := run(t, "metrics", "--dir", dir)
	if code != 0 {
		t.Fatalf("metrics failed: %d %s", code, errb)
	}
	if !strings.Contains(out, "example.com/fixture/a") ||
		!strings.Contains(out, "per package") {
		t.Fatalf("expected per-package metrics + limitation: %s", out)
	}
	// 설정이 있으면 컴포넌트 단위다.
	dir2 := rulesFixture(t)
	code, out, _ = run(t, "metrics", "--dir", dir2)
	if code != 0 {
		t.Fatalf("metrics with config failed: %d", code)
	}
	if !strings.Contains(out, "web:") || !strings.Contains(out, "Ce=1") {
		t.Fatalf("expected component metrics: %s", out)
	}
	// web→db가 있어 web Ce=1, db Ca=1. api는 고립 — orphan이어야 한다.
	if !strings.Contains(out, "orphan: example.com/fixture/api") {
		t.Fatalf("api has no importers — expected orphan: %s", out)
	}
	// JSON 형식도 파싱 가능해야 한다.
	code, out, _ = run(t, "metrics", "--dir", dir2, "--format", "json")
	if code != 0 {
		t.Fatalf("metrics json failed: %d", code)
	}
	var rep struct {
		Components []struct {
			Name        string   `json:"name"`
			Afferent    int      `json:"afferent"`
			Efferent    int      `json:"efferent"`
			Instability *float64 `json:"instability"`
		} `json:"components"`
		Orphans []string `json:"orphans"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("metrics output is not JSON: %v", err)
	}
	// web도 importer가 없는 최상위 소비자라 orphan으로 보고된다.
	if len(rep.Components) != 3 || len(rep.Orphans) != 2 {
		t.Fatalf("unexpected metrics report: %s", out)
	}
	// 깨진 설정 파일을 명시하면 조용한 폴백이 아니라 오류(2)다.
	bad := filepath.Join(t.TempDir(), "bad.yml")
	if err := os.WriteFile(bad, []byte("components: {a: [a]}\ndeps: {a: [ghost]}\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "metrics", "--dir", dir2, "--config", bad); code != 2 {
		t.Fatalf("broken --config must fail: got %d", code)
	}
}

// TestMapping은 컴포넌트↔패키지 매핑 보고를 확인한다.
func TestMapping(t *testing.T) {
	dir := rulesFixture(t)
	code, out, errb := run(t, "mapping", "--dir", dir)
	if code != 0 {
		t.Fatalf("mapping failed: %d %s", code, errb)
	}
	if !strings.Contains(out, "web:") ||
		!strings.Contains(out, "example.com/fixture/web") {
		t.Fatalf("expected component->package mapping: %s", out)
	}
	// 설정 없는 디렉터리는 mapping이 성립하지 않는다 — 2.
	if code, _, _ := run(t, "mapping", "--dir", fixture(t)); code != 2 {
		t.Fatalf("mapping without config: expected 2, got %d", code)
	}
	// JSON 형식은 컴포넌트→패키지 역방향 맵이다.
	code, out, _ = run(t, "mapping", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("mapping json failed: %d", code)
	}
	var rep struct {
		Components map[string][]string `json:"components"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("mapping output is not JSON: %v", err)
	}
	if len(rep.Components["web"]) != 1 ||
		rep.Components["web"][0] != "example.com/fixture/web" {
		t.Fatalf("unexpected mapping: %s", out)
	}
}

// TestInitEmptyModule은 내부 패키지가 하나도 없는 디렉터리에서 init이
// 빈 설정을 쓰지 않고 오류(2)인지 확인한다 — 써진 설정이 바로 rules를
// 통과해야 하는 계약상 components가 비면 성공이 성립하지 않는다.
func TestInitEmptyModule(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{})
	if code, _, _ := run(t, "init", "--dir", dir); code != 2 {
		t.Fatalf("init with no packages must fail: got %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gartograph.yml")); !os.IsNotExist(err) {
		t.Fatal("failed init must not leave a config file")
	}
}

// TestInit은 스캐폴드된 규칙 파일이 곧바로 rules를 통과하는지 확인한다.
func TestInit(t *testing.T) {
	dir := fixture(t)
	code, out, errb := run(t, "init", "--dir", dir)
	if code != 0 {
		t.Fatalf("init failed: %d %s", code, errb)
	}
	if !strings.Contains(out, "wrote") {
		t.Fatalf("expected wrote message: %s", out)
	}
	// 생성된 설정으로 rules --strict가 통과해야 한다 — 관찰된 현실을 기록했으므로.
	code, out, errb = run(t, "rules", "--dir", dir, "--strict")
	if code != 0 {
		t.Fatalf("rules on scaffolded config must pass: %d %s %s", code, out, errb)
	}
	// 두 번째 init은 덮어쓰기를 거부한다.
	code, _, errb = run(t, "init", "--dir", dir)
	if code != 2 || !strings.Contains(errb, "already exists") {
		t.Fatalf("init must refuse to overwrite: %d %s", code, errb)
	}
}

// TestBridges는 isthmus bridge-facts v1 문서의 형태와 cgo 관측을 확인한다.
// go 문서는 계약상 사실을 담지 않고 unscanned-ffi-interop limitation만 실는다.
func TestBridges(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() {}
`,
		"native/native.go": `package native

/*
#include <stdlib.h>
*/
import "C"

//export Add
func Add(a, b C.int) C.int { return a + b }
`,
		"native/more.go": `package native

import "C"

//export Mul
func Mul(a, b C.int) C.int { return a * b }
`,
	})
	code, out, errb := run(t, "bridges", "--dir", dir)
	if code != 0 {
		t.Fatalf("bridges failed: %d %s", code, errb)
	}
	var doc struct {
		Format      string   `json:"format"`
		Version     int      `json:"version"`
		Platform    string   `json:"platform"`
		Target      any      `json:"target"`
		Project     string   `json:"project"`
		Facts       []any    `json:"facts"`
		Limitations []string `json:"limitations"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if doc.Format != "bridge-facts" || doc.Version != 1 {
		t.Fatalf("bad envelope: %s", out)
	}
	if doc.Platform != "go" || doc.Target != nil {
		t.Fatalf("go documents must carry platform go and null target: %s", out)
	}
	if doc.Facts == nil || len(doc.Facts) != 0 {
		t.Fatalf("go documents carry no facts in v1: %s", out)
	}
	// project는 realpath로 정규화된 절대 경로여야 다른 생산자 문서와 조인된다.
	resolved, _ := filepath.EvalSymlinks(dir)
	if doc.Project != resolved {
		t.Fatalf("project must be the realpath of --dir: %q vs %q", doc.Project, resolved)
	}
	// cgo 파일 둘 + //export 둘을 세어야 한다.
	var found bool
	for _, l := range doc.Limitations {
		if strings.HasPrefix(l, "unscanned-ffi-interop:") &&
			strings.Contains(l, "2 Go source files") && strings.Contains(l, "2 //export") {
			found = true
		}
	}
	if !found {
		t.Fatalf("cgo observation must be reported as unscanned-ffi-interop: %v", doc.Limitations)
	}
	// --out으로 파일에 쓸 수 있다.
	outPath := filepath.Join(t.TempDir(), "facts.json")
	if code, _, errb := run(t, "bridges", "--dir", dir, "--out", outPath); code != 0 {
		t.Fatalf("bridges --out failed: %d %s", code, errb)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("out file missing: %v", err)
	}
}

// TestBridgesNoInterop은 cgo가 없는 프로젝트가 조용한 문서를 내는지 확인한다.
func TestBridgesNoInterop(t *testing.T) {
	dir := fixture(t)
	code, out, errb := run(t, "bridges", "--dir", dir)
	if code != 0 {
		t.Fatalf("bridges failed: %d %s", code, errb)
	}
	var doc struct {
		Limitations []string `json:"limitations"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	for _, l := range doc.Limitations {
		if strings.HasPrefix(l, "unscanned-ffi-interop:") {
			t.Fatalf("no cgo means no interop limitation: %v", doc.Limitations)
		}
	}
}

// TestSchema는 persistence 생산자가 SQL 관계 참조를 relation-use 사실로
// 내는지 확인한다 — 리터럴·한정 이름·동적 인자·테이블 바인딩 태그를 함께 본다.
func TestSchema(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import (
	"context"
	"database/sql"
)

type User struct {
	ID   int    ` + "`db:\"id\"`" + `
	Name string ` + "`db:\"name\"`" + `
}

func (User) TableName() string { return "users" }

func run(ctx context.Context, db *sql.DB, q string) {
	db.QueryRow("SELECT id FROM users WHERE id = 1")
	db.ExecContext(ctx, "INSERT INTO audit.events (id) VALUES (1)")
	db.Query(q)
}

var kept = "SELECT o.id FROM orders o JOIN users u ON o.uid = u.id"

func main() {}
`,
	})
	code, out, errb := run(t, "schema", "--dir", dir)
	if code != 0 {
		t.Fatalf("schema failed: %d %s", code, errb)
	}
	var doc struct {
		Format   string `json:"format"`
		Version  int    `json:"version"`
		Platform string `json:"platform"`
		Target   any    `json:"target"`
		Project  string `json:"project"`
		Facts    []struct {
			Kind     string `json:"kind"`
			Channel  string `json:"channel"`
			Method   string `json:"method"`
			Dynamic  bool   `json:"dynamic"`
			Location struct {
				Path   string `json:"path"`
				Line   int    `json:"line"`
				Column int    `json:"column"`
			} `json:"location"`
		} `json:"facts"`
		Limitations []string `json:"limitations"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if doc.Format != "bridge-facts" || doc.Version != 1 {
		t.Fatalf("bad envelope: %s", out)
	}
	if doc.Platform != "go" || doc.Target != "persistence" {
		t.Fatalf("persistence documents carry platform go and target persistence: %s", out)
	}
	channels := map[string]int{}
	var dynamic, columns int
	for _, f := range doc.Facts {
		if f.Kind != "relation-use" {
			t.Fatalf("unexpected fact kind: %s", f.Kind)
		}
		if f.Dynamic {
			dynamic++
			continue
		}
		if f.Method != "" {
			columns++
		}
		channels[f.Channel]++
	}
	// 리터럴 스캔: users(QueryRow)·audit.events(한정)·orders+users(JOIN)·
	// TableName 바인딩의 users — 각 관계가 사실로 보인다.
	for _, want := range []string{"users", "audit.events", "orders"} {
		if channels[want] == 0 {
			t.Fatalf("missing relation use %q: %v", want, channels)
		}
	}
	// db 태그가 TableName에 귀속해 컬럼 사실(id·name)이 된다.
	if columns != 2 {
		t.Fatalf("expected 2 column facts bound to users, got %d", columns)
	}
	// 비리터럴 인자 db.Query(q)는 버리지 않고 dynamic으로 보존한다.
	if dynamic != 1 {
		t.Fatalf("expected 1 dynamic fact for db.Query(q), got %d", dynamic)
	}
	// 위치는 프로젝트 상대 경로다 — 절대 경로가 새면 문서가 이식 불가하다.
	for _, f := range doc.Facts {
		if filepath.IsAbs(f.Location.Path) {
			t.Fatalf("location path must be project-relative: %s", f.Location.Path)
		}
	}
}

// TestSchemaDynamicLimitation은 동적 SQL 인자가 있을 때 문서가
// unjoined-dynamic-relations limitation을 실는지 확인한다.
func TestSchemaDynamicLimitation(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "database/sql"

func run(db *sql.DB, table string) {
	db.Query("SELECT * FROM " + table)
}

func main() {}
`,
	})
	code, out, errb := run(t, "schema", "--dir", dir)
	if code != 0 {
		t.Fatalf("schema failed: %d %s", code, errb)
	}
	var doc struct {
		Facts []struct {
			Dynamic bool `json:"dynamic"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	var dynamic bool
	for _, f := range doc.Facts {
		if f.Dynamic {
			dynamic = true
		}
	}
	if !dynamic {
		t.Fatalf("concatenated query arg must surface as dynamic: %s", out)
	}
}

// TestSchemaEmpty는 SQL 참조가 없는 모듈이 target null의 빈 문서를 내는지
// 확인한다 — 계약상 target은 사실이 있을 때만 설정된다.
func TestSchemaEmpty(t *testing.T) {
	dir := fixture(t)
	code, out, errb := run(t, "schema", "--dir", dir)
	if code != 0 {
		t.Fatalf("schema failed: %d %s", code, errb)
	}
	var doc struct {
		Target any   `json:"target"`
		Facts  []any `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if doc.Target != nil {
		t.Fatalf("documents without facts must carry a null target: %s", out)
	}
	if len(doc.Facts) != 0 {
		t.Fatalf("expected no facts: %s", out)
	}
	// limitations는 계약상 항상 배열이다 — 키가 빠지거나 null이면
	// isthmus 파서가 문서를 거부한다.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	lim, ok := raw["limitations"]
	if !ok || string(lim) != "[]" {
		t.Fatalf("limitations must always be an array (got %v): %s", ok, out)
	}
}

// TestDiff는 두 저장 문서의 차이와 --strict의 breaking 계약을 확인한다.
func TestDiff(t *testing.T) {
	dir := deadFixture(t)
	tmp := t.TempDir()
	oldPath := filepath.Join(tmp, "old.json")
	newPath := filepath.Join(tmp, "new.json")
	if code, _, errb := run(t, "graph", "--dir", dir, "--level", "symbol",
		"--out", oldPath); code != 0 {
		t.Fatalf("graph old failed: %d %s", code, errb)
	}
	// 공개 함수 Unused를 지우면 exported 정점 제거 = breaking이다.
	lib := filepath.Join(dir, "lib", "lib.go")
	src, _ := os.ReadFile(lib)
	src = bytes.Replace(src,
		[]byte("func Unused() {}\n"), nil, 1)
	if err := os.WriteFile(lib, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := run(t, "graph", "--dir", dir, "--level", "symbol",
		"--out", newPath); code != 0 {
		t.Fatalf("graph new failed: %d %s", code, errb)
	}
	code, out, errb := run(t, "diff", oldPath, newPath, "--strict")
	if code != 1 {
		t.Fatalf("diff --strict with removed exported vertex: expected 1, got %d %s",
			code, errb)
	}
	if !strings.Contains(out, "lib.Unused") {
		t.Fatalf("expected Unused in diff output: %s", out)
	}
	// strict 없이는 차이가 있어도 0이다.
	if code, _, _ := run(t, "diff", oldPath, newPath); code != 0 {
		t.Fatalf("diff without --strict: expected 0, got %d", code)
	}
	// 같은 파일의 diff는 차이 없음.
	code, out, _ = run(t, "diff", oldPath, oldPath)
	if code != 0 || strings.Contains(out, "breaking:") {
		t.Fatalf("self diff must be empty: %d %s", code, out)
	}
	// JSON 형식도 파싱 가능해야 한다 — diff의 구조 계약.
	code, out, _ = run(t, "diff", oldPath, newPath, "--format", "json")
	if code != 0 {
		t.Fatalf("diff --format json failed: %d", code)
	}
	var rep struct {
		RemovedVertices []string `json:"removedVertices"`
		Breaking        []string `json:"breaking"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil ||
		len(rep.RemovedVertices) == 0 || len(rep.Breaking) == 0 {
		t.Fatalf("diff json must carry the removal and breaking signal: %s", out)
	}
}

// TestGraphLevel은 --level이 문서의 레벨과 정점 종류를 바꾸는지 확인한다.
func TestGraphLevel(t *testing.T) {
	dir := deadFixture(t)
	code, out, errb := run(t, "graph", "--dir", dir, "--level", "symbol")
	if code != 0 {
		t.Fatalf("graph --level symbol failed: %d %s", code, errb)
	}
	var doc struct {
		Level    string `json:"level"`
		Vertices []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"vertices"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if doc.Level != "symbol" {
		t.Fatalf("expected symbol level, got %s", doc.Level)
	}
	var hasFunc bool
	for _, v := range doc.Vertices {
		if v.Kind == "func" && strings.HasSuffix(v.ID, ".main") {
			hasFunc = true
		}
	}
	if !hasFunc {
		t.Fatal("symbol level missing func vertices")
	}
}

// TestRulesFileScope는 fileRules가 프로덕션 파일의 import만 위반으로
// 잡는지 종단으로 확인한다 — _test.go에서 온 지점은 위반이 아니다.
func TestRulesFileScope(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"web/web.go": `package web

import _ "example.com/fixture/testhelp"
`,
		"web/web_test.go": `package web

import _ "example.com/fixture/testhelp"
`,
		"testhelp/t.go": `package testhelp
`,
		".gartograph.yml": `components:
  web: ["web"]
  testhelp: ["testhelp"]
deps:
  web: ["testhelp"]
fileRules:
  - name: no-testdeps-in-prod
    from: "!*_test.go"
    to: testhelp
    reason: "keep test helpers out of production code"
`,
	})
	// --tests를 켜도 테스트 변형이 실은 _test.go 지점은 위반이 아니다.
	code, out, errb := run(t, "rules", "--dir", dir, "--tests")
	if code != 0 {
		t.Fatalf("rules failed: %d %s", code, errb)
	}
	if strings.Count(out, "violation[fileScope:no-testdeps-in-prod]") != 1 {
		t.Fatalf("only the production file must violate: %s", out)
	}
	if !strings.Contains(out, "web.go") || strings.Contains(out, "web_test.go") {
		t.Fatalf("violation must point at web.go only: %s", out)
	}
	if !strings.Contains(out, "keep test helpers") {
		t.Fatalf("reason must be reported: %s", out)
	}
}

// cycleFixture는 심볼 레벨 상호 재귀 순환을 가진 모듈을 만든다.
func cycleFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

func A() { B() }
func B() { A() }
`,
	})
}

// TestCyclesSarif는 순환이 SARIF 결과로 직렬화되는지 확인한다.
func TestCyclesSarif(t *testing.T) {
	dir := cycleFixture(t)
	code, out, errb := run(t, "cycles", "--dir", dir,
		"--level", "symbol", "--format", "sarif")
	if code != 0 {
		t.Fatalf("cycles sarif failed: %d %s", code, errb)
	}
	var doc struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
				Level  string `json:"level"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("sarif output is not JSON: %v", err)
	}
	if doc.Version != "2.1.0" || len(doc.Runs) != 1 ||
		len(doc.Runs[0].Results) != 1 ||
		doc.Runs[0].Results[0].RuleID != "dependency-cycle" {
		t.Fatalf("expected one dependency-cycle result: %s", out)
	}
}

// TestCyclesBaseline은 순환 baseline의 기록→비교→신규 검출 흐름을 확인한다.
func TestCyclesBaseline(t *testing.T) {
	dir := cycleFixture(t)
	base := filepath.Join(t.TempDir(), "cycles.json")
	if code, _, errb := run(t, "cycles", "--dir", dir,
		"--level", "symbol", "--write-baseline", base); code != 0 {
		t.Fatalf("--write-baseline failed: %d %s", code, errb)
	}
	// 알려진 순환은 strict를 통과한다.
	code, out, _ := run(t, "cycles", "--dir", dir,
		"--level", "symbol", "--baseline", base, "--strict")
	if code != 0 {
		t.Fatalf("baselined cycle must pass strict: %d %s", code, out)
	}
	if !strings.Contains(out, "0 cycles (1 baselined)") {
		t.Fatalf("expected baselined cycle: %s", out)
	}
	// 새 순환은 baseline을 뚫는다.
	if err := os.WriteFile(filepath.Join(dir, "a", "a.go"),
		[]byte("package a\n\nfunc A() { B() }\nfunc B() { A() }\n"+
			"func C() { D() }\nfunc D() { C() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := run(t, "cycles", "--dir", dir,
		"--level", "symbol", "--baseline", base, "--strict"); code != 1 {
		t.Fatalf("new cycle past baseline must fail strict: %d %s", code, out)
	}
	// 다른 kind의 baseline 파일은 거부된다.
	rulesBase := filepath.Join(t.TempDir(), "v.json")
	if err := os.WriteFile(rulesBase, []byte(
		`{"tool":"gartograph","kind":"violations-baseline","version":1,"violations":[]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := run(t, "cycles", "--dir", dir,
		"--baseline", rulesBase); code != 2 {
		t.Fatalf("wrong-kind baseline must be rejected: %d %s", code, errb)
	}
}

// TestDeadSarif는 unreachable 보고가 warning SARIF로 직렬화되는지 확인한다 —
// 삭제 판정이 아니라 그래프 사실이므로 error가 아니다.
func TestDeadSarif(t *testing.T) {
	dir := deadFixture(t)
	code, out, errb := run(t, "dead", "--dir", dir, "--format", "sarif")
	if code != 0 {
		t.Fatalf("dead sarif failed: %d %s", code, errb)
	}
	var doc struct {
		Runs []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
				Level  string `json:"level"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("sarif output is not JSON: %v", err)
	}
	if len(doc.Runs) != 1 || len(doc.Runs[0].Results) == 0 {
		t.Fatalf("expected unreachable-symbol results: %s", out)
	}
	for _, r := range doc.Runs[0].Results {
		if r.RuleID != "unreachable-symbol" || r.Level != "warning" {
			t.Fatalf("unreachable is a fact, not an error: %+v", r)
		}
	}
}

// TestDeadBaseline은 unreachable baseline의 기록→비교→신규 검출을 확인한다.
func TestDeadBaseline(t *testing.T) {
	dir := deadFixture(t)
	base := filepath.Join(t.TempDir(), "dead.json")
	code, _, errb := run(t, "dead", "--dir", dir, "--write-baseline", base)
	if code != 0 {
		t.Fatalf("--write-baseline failed: %d %s", code, errb)
	}
	code, out, _ := run(t, "dead", "--dir", dir, "--baseline", base, "--strict")
	if code != 0 {
		t.Fatalf("baselined findings must pass strict: %d %s", code, out)
	}
	// 새 unreachable 심볼은 baseline을 뚫는다.
	if err := os.WriteFile(filepath.Join(dir, "lib", "lib.go"),
		[]byte("package lib\n\nfunc Run() { helper() }\nfunc helper() {}\n"+
			"func Unused() {}\nfunc unused2() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = run(t, "dead", "--dir", dir, "--baseline", base, "--strict")
	if code != 1 {
		t.Fatalf("new finding past baseline must fail strict: %d %s", code, out)
	}
	if !strings.Contains(out, "baselined") {
		t.Fatalf("baselined count must be reported: %s", out)
	}
}

// TestShared는 두 루트의 공통 도달 집합 명령의 JSON 계약을 확인한다.
func TestShared(t *testing.T) {
	dir := fixture(t)
	code, out, _ := run(t, "shared",
		"example.com/fixture/a", "example.com/fixture/b", "--dir", dir)
	if code != 0 {
		t.Fatalf("shared failed: %d", code)
	}
	var res struct {
		Shared []string            `json:"shared"`
		Only   map[string][]string `json:"only"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("shared output is not JSON: %v", err)
	}
	// a→b라 b는 둘 다 도달 — shared는 b뿐이다.
	if len(res.Shared) != 1 || res.Shared[0] != "example.com/fixture/b" {
		t.Fatalf("shared must be the intersection: %s", out)
	}
	if len(res.Only["example.com/fixture/a"]) != 1 {
		t.Fatalf("a alone reaches itself: %s", out)
	}
	if code, _, _ := run(t, "shared", "example.com/ghost",
		"example.com/fixture/b", "--dir", dir); code != 2 {
		t.Fatalf("missing root must exit 2, got %d", code)
	}
}

// TestUnusedDeps는 어느 패키지도 import하지 않는 require의 보고와
// strict 종료 코드를 확인한다.
func TestUnusedDeps(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": `module example.com/fixture

go 1.27

require golang.org/x/mod v0.41.0
`,
		"main.go": `package main

func main() {}
`,
	})
	code, out, _ := run(t, "unused-deps", "--dir", dir)
	if code != 0 {
		t.Fatalf("unused-deps failed: %d", code)
	}
	if !strings.Contains(out, "golang.org/x/mod") {
		t.Fatalf("unimported require must be reported: %s", out)
	}
	// direct 미사용이 있으면 strict는 1이다.
	if code, _, _ := run(t, "unused-deps", "--dir", dir,
		"--strict"); code != 1 {
		t.Fatalf("strict with unused direct require must exit 1, got %d", code)
	}
	// require가 없으면 strict도 0이다.
	clean := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() {}
`,
	})
	if code, _, _ := run(t, "unused-deps", "--dir", clean,
		"--strict"); code != 0 {
		t.Fatalf("clean module must pass strict")
	}
	// JSON 계약.
	code, out, _ = run(t, "unused-deps", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("unused-deps json failed: %d", code)
	}
	var rep struct {
		Unused         []string `json:"unused"`
		UnusedIndirect []string `json:"unusedIndirect"`
		UsedRequires   int      `json:"usedRequires"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unused-deps output is not JSON: %v", err)
	}
	if len(rep.Unused) != 1 || rep.Unused[0] != "golang.org/x/mod" ||
		len(rep.UnusedIndirect) != 0 {
		t.Fatalf("expected one direct unused require: %s", out)
	}
	// --graph는 이 명령의 사실이 아니다 — go.mod에서 오므로 거부한다.
	if code, _, _ := run(t, "unused-deps", "--dir", dir,
		"--graph", "x.json"); code != 2 {
		t.Fatalf("--graph must be a usage error, got %d", code)
	}
}

// TestDeadExplainRTA는 --explain이 --algo rta일 때 RTA 콜그래프 위의
// 경로를 보여주는지 확인한다 — CHA 그래프의 경로는 다른 알고리즘의 말이다.
func TestDeadExplainRTA(t *testing.T) {
	dir := deadFixture(t)
	code, out, errb := run(t, "dead", "--dir", dir,
		"--algo", "rta", "--explain", "example.com/fixture/lib.helper")
	if code != 0 {
		t.Fatalf("dead --explain --algo rta failed: %d %s", code, errb)
	}
	if !strings.Contains(out, "example.com/fixture.main") ||
		!strings.Contains(out, "lib.Run") ||
		!strings.Contains(out, "lib.helper") {
		t.Fatalf("RTA explain must show the call-graph path: %s", out)
	}
	// RTA가 죽인 심볼은 RTA 그래프에 경로가 없다.
	code, out, _ = run(t, "dead", "--dir", dir,
		"--algo", "rta", "--explain", "example.com/fixture/lib.Unused")
	if code != 0 {
		t.Fatalf("rta explain for dead symbol: %d", code)
	}
	if !strings.Contains(out, "no path") || !strings.Contains(out, "rta") {
		t.Fatalf("unreachable under rta must say so: %s", out)
	}
}

// TestExcludeConfig는 .gartograph.yml의 exclude가 수확에서 패키지를
// 빼는지 확인한다 — 설정과 수확이 같은 해석을 써야 한다.
func TestExcludeConfig(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		".gartograph.yml": `components: {a: [a]}
exclude: [gen/**]
`,
		"a/a.go": `package a

import _ "example.com/fixture/gen"
`,
		"gen/gen.go": `package gen
`,
	})
	code, out, _ := run(t, "graph", "--dir", dir)
	if code != 0 {
		t.Fatalf("graph failed: %d", code)
	}
	var doc struct {
		Vertices    []struct{ ID string } `json:"vertices"`
		Limitations []string              `json:"limitations"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("graph output is not JSON: %v", err)
	}
	for _, v := range doc.Vertices {
		if strings.HasSuffix(v.ID, "/gen") {
			t.Fatalf("excluded package must not be a vertex: %s", out)
		}
	}
	var noted bool
	for _, l := range doc.Limitations {
		if strings.Contains(l, "exclude") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("exclusion must be a limitation: %s", out)
	}
}

// TestSharedUsage는 shared가 루트 하나만으로는 사용법 오류인지 확인한다 —
// 교집합은 둘 이상의 루트가 있어야 의미가 있다.
func TestSharedUsage(t *testing.T) {
	dir := fixture(t)
	if code, _, _ := run(t, "shared",
		"example.com/fixture/a", "--dir", dir); code != 2 {
		t.Fatalf("single root must be a usage error, got %d", code)
	}
}

// TestUnusedDepsErrors는 go.mod가 없는 디렉터리와 손상된 go.mod의
// 에러 경로를 확인한다 — require 목록은 그래프가 아니라 파일에서 온다.
func TestUnusedDepsErrors(t *testing.T) {
	// go.mod 없는 디렉터리 — 주 모듈을 찾을 수 없어 에러.
	empty := t.TempDir()
	if code, _, errb := run(t, "unused-deps", "--dir", empty); code != 2 ||
		!strings.Contains(errb, "go.mod") {
		t.Fatalf("missing module must exit 2: %d %s", code, errb)
	}
	// 손상된 go.mod.
	bad := testutil.WriteModule(t, map[string]string{
		"go.mod": "module [invalid\n",
		"main.go": `package main

func main() {}
`,
	})
	if code, _, _ := run(t, "unused-deps", "--dir", bad); code != 2 {
		t.Fatalf("broken go.mod must exit 2, got %d", code)
	}
	// 지원하지 않는 형식.
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() {}
`,
	})
	if code, _, _ := run(t, "unused-deps", "--dir", dir,
		"--format", "yaml"); code != 2 {
		t.Fatalf("unknown format must exit 2, got %d", code)
	}
}

// TestUnusedDepsIndirect는 // indirect 표시 require가 direct와 분리되어
// 보고되고 strict가 울리지 않는지 확인한다 — indirect는 다른 의존이
// 끌어오는 핀이라 "지워도 된다"는 표명이 아니다.
func TestUnusedDepsIndirect(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": `module example.com/fixture

go 1.27

require golang.org/x/sync v0.23.0 // indirect
`,
		"main.go": `package main

func main() {}
`,
	})
	code, out, _ := run(t, "unused-deps", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("unused-deps failed: %d", code)
	}
	var rep struct {
		Unused         []string `json:"unused"`
		UnusedIndirect []string `json:"unusedIndirect"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(rep.Unused) != 0 || len(rep.UnusedIndirect) != 1 ||
		rep.UnusedIndirect[0] != "golang.org/x/sync" {
		t.Fatalf("indirect require must be reported separately: %s", out)
	}
	if code, _, _ := run(t, "unused-deps", "--dir", dir,
		"--strict"); code != 0 {
		t.Fatal("indirect-only unused must not fail strict")
	}
}

// TestCyclesSARIF는 cycles의 SARIF 출력이 유효한 봉투인지 확인한다 —
// 저장 문서로 순환을 만들어 실제 경로를 돌린다.
func TestCyclesSARIF(t *testing.T) {
	docJSON := `{"version":2,"level":"package","vertices":[
{"id":"m/a","kind":"package"},{"id":"m/b","kind":"package"}],
"edges":[{"from":"m/a","to":"m/b","kind":"import",
"positions":[{"file":"a/a.go","line":3,"column":1}]},
{"from":"m/b","to":"m/a","kind":"import",
"positions":[{"file":"b/b.go","line":3,"column":1}]}]}`
	dir := t.TempDir()
	p := filepath.Join(dir, "g.json")
	if err := os.WriteFile(p, []byte(docJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "cycles", "--graph", p, "--format", "sarif")
	if code != 0 {
		t.Fatalf("cycles sarif failed: %d", code)
	}
	var sarif struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &sarif); err != nil {
		t.Fatalf("not SARIF JSON: %v", err)
	}
	if sarif.Version != "2.1.0" || len(sarif.Runs) != 1 ||
		len(sarif.Runs[0].Results) != 1 ||
		sarif.Runs[0].Results[0].RuleID != "dependency-cycle" {
		t.Fatalf("unexpected SARIF: %s", out)
	}
	// baseline 왕복 — 쓰고 읽으면 같은 순환이 baselined로 간다.
	bp := filepath.Join(dir, "b.json")
	if code, _, _ := run(t, "cycles", "--graph", p,
		"--write-baseline", bp); code != 0 {
		t.Fatalf("write-baseline failed: %d", code)
	}
	code, out, _ = run(t, "cycles", "--graph", p, "--baseline", bp,
		"--format", "json")
	if code != 0 {
		t.Fatalf("baseline read failed: %d", code)
	}
	var rep struct {
		Cycles    []any `json:"cycles"`
		Baselined []any `json:"baselined"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(rep.Cycles) != 0 || len(rep.Baselined) != 1 {
		t.Fatalf("known cycle must be baselined: %s", out)
	}
}

// TestDiffText는 diff의 텍스트 출력 계약을 확인한다.
func TestDiffText(t *testing.T) {
	dir := t.TempDir()
	old := `{"version":2,"level":"symbol","vertices":[
{"id":"m.F","kind":"func","exported":true}]}`
	newD := `{"version":2,"level":"symbol","vertices":[]}`
	po, pn := filepath.Join(dir, "old.json"), filepath.Join(dir, "new.json")
	for p, c := range map[string]string{po: old, pn: newD} {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out, _ := run(t, "diff", po, pn)
	if code != 0 {
		t.Fatalf("diff failed: %d", code)
	}
	if !strings.Contains(out, "breaking") || !strings.Contains(out, "m.F") {
		t.Fatalf("text diff must report the breaking removal: %s", out)
	}
}

// TestDeadBaselineRoundtrip은 dead의 baseline 쓰기·읽기 왕복을 확인한다.
func TestDeadBaselineRoundtrip(t *testing.T) {
	dir := deadFixture(t)
	bp := filepath.Join(t.TempDir(), "dead-baseline.json")
	if code, _, _ := run(t, "dead", "--dir", dir,
		"--write-baseline", bp); code != 0 {
		t.Fatalf("write-baseline failed: %d", code)
	}
	code, out, _ := run(t, "dead", "--dir", dir, "--baseline", bp,
		"--format", "json")
	if code != 0 {
		t.Fatalf("baseline run failed: %d", code)
	}
	var rep struct {
		Findings  []any `json:"findings"`
		Baselined []any `json:"baselined"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(rep.Findings) != 0 || len(rep.Baselined) == 0 {
		t.Fatalf("known finding must be baselined: %s", out)
	}
}

// TestQueryNeighborSort는 이웃 목록이 실제로 정렬되어 나오는지 확인한다 —
// 정렬 클로저는 원소가 둘 이상일 때만 실행되므로 두 임포터가 필요하다.
func TestQueryNeighborSort(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

import _ "example.com/fixture/b"
`,
		"c/c.go": `package c

import _ "example.com/fixture/b"
`,
		"b/b.go": `package b
`,
	})
	code, out, _ := run(t, "query", "example.com/fixture/b", "--dir", dir)
	if code != 0 {
		t.Fatalf("query failed: %d", code)
	}
	var res struct {
		DependedBy []struct{ ID string } `json:"dependedBy"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(res.DependedBy) != 2 ||
		res.DependedBy[0].ID != "example.com/fixture/a" ||
		res.DependedBy[1].ID != "example.com/fixture/c" {
		t.Fatalf("dependents must be sorted: %s", out)
	}
}

// TestUnusedDepsUsed는 실제로 import되는 require가 미사용으로 오보하지
// 않는지 확인한다 — "쓰였다"의 판정은 패키지의 모듈 소속이다.
// 로컬 replace로 진짜 의존을 만든다 — 원격 require는 go.sum이 필요하고
// 테스트를 네트워크에 묶지 않기 위함이다.
func TestUnusedDepsUsed(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": `module example.com/fixture

go 1.27

require example.com/dep v0.0.0

replace example.com/dep => ./dep
`,
		"dep/go.mod": `module example.com/dep

go 1.27
`,
		"dep/dep.go": `package dep

func F() {}
`,
		"main.go": `package main

import "example.com/dep"

func main() { dep.F() }
`,
	})
	code, out, _ := run(t, "unused-deps", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("unused-deps failed: %d", code)
	}
	var rep struct {
		Unused       []string `json:"unused"`
		UsedRequires int      `json:"usedRequires"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(rep.Unused) != 0 || rep.UsedRequires != 1 {
		t.Fatalf("imported require must count as used: %s", out)
	}
}

// TestRulesSARIFPosition은 rules SARIF가 위반의 물리 위치를 싣는지
// 확인한다 — 수확 문서의 import 지점이 physicalLocation이 된다.
func TestRulesSARIFPosition(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		".gartograph.yml": `components: {a: [a], b: [b]}
deps: {a: [], b: []}
deny: {a: [b]}
`,
		"a/a.go": `package a

import _ "example.com/fixture/b"
`,
		"b/b.go": `package b
`,
	})
	code, out, _ := run(t, "rules", "--dir", dir, "--format", "sarif")
	if code != 0 {
		t.Fatalf("rules sarif failed: %d", code)
	}
	var sarif struct {
		Runs []struct {
			Results []struct {
				Locations []struct {
					PhysicalLocation *struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &sarif); err != nil {
		t.Fatalf("not SARIF: %v", err)
	}
	locs := sarif.Runs[0].Results[0].Locations
	if len(locs) == 0 || locs[0].PhysicalLocation == nil ||
		!strings.HasSuffix(locs[0].PhysicalLocation.ArtifactLocation.URI,
			"a/a.go") {
		t.Fatalf("violation must carry its import site: %s", out)
	}
}

// TestDeadFieldAndKeep은 멤버 레벨 보고와 keep 어노테이션의 종단 계약을 확인한다:
// 참조 안 된 필드는 kind=field로 보고되고, 참조된 필드와 keep 표지 심볼은
// 보고되지 않으며, 공개 심볼은 exported 표지를 싣는다.
func TestDeadFieldAndKeep(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Use() }
`,
		"lib/lib.go": `package lib

type Cfg struct {
	Addr  string
	stale int
}

func Use() int {
	var c Cfg
	return len(c.Addr)
}

//deadcode:keep — plugin loader calls this by name
func Register() {}

// ExportedUnused is public API — reported but marked exported.
func ExportedUnused() {}
`,
	})
	code, out, errb := run(t, "dead", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("dead failed: %d %s", code, errb)
	}
	var rep struct {
		Unreachable []struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Exported bool   `json:"exported"`
		} `json:"unreachable"`
		Limitations []string `json:"limitations"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("dead output is not JSON: %v\n%s", err, out)
	}
	ids := map[string]bool{}
	exported := map[string]bool{}
	for _, f := range rep.Unreachable {
		ids[f.ID] = true
		exported[f.ID] = f.Exported
	}
	if !ids["example.com/fixture/lib.(Cfg).stale"] {
		t.Fatalf("unreferenced field must be reported: %s", out)
	}
	if ids["example.com/fixture/lib.(Cfg).Addr"] {
		t.Fatalf("referenced field must not be reported: %s", out)
	}
	if ids["example.com/fixture/lib.Register"] {
		t.Fatalf("keep-annotated symbol must be retained: %s", out)
	}
	if !ids["example.com/fixture/lib.ExportedUnused"] ||
		!exported["example.com/fixture/lib.ExportedUnused"] {
		t.Fatalf("exported unreachable must be flagged exported: %s", out)
	}
	var fieldLimitation bool
	for _, l := range rep.Limitations {
		if strings.Contains(l, "field reachability") {
			fieldLimitation = true
		}
	}
	if !fieldLimitation {
		t.Fatalf("field findings must carry the access-path limitation: %v",
			rep.Limitations)
	}
}

// TestMetricsAD는 타입 레벨 수확에서 abstractness·distance가 나오는지 확인한다.
func TestMetricsAD(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/svc"

func main() { svc.Run() }
`,
		"svc/svc.go": `package svc

type Store interface{ Get() int }
type mem struct{}

func (mem) Get() int { return 0 }
func Run()         {}
`,
	})
	code, out, errb := run(t, "metrics", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("metrics failed: %d %s", code, errb)
	}
	var rep struct {
		Components []struct {
			Name         string   `json:"name"`
			Abstractness *float64 `json:"abstractness"`
			Distance     *float64 `json:"distance"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("metrics output is not JSON: %v\n%s", err, out)
	}
	var found bool
	for _, c := range rep.Components {
		if c.Name == "example.com/fixture/svc" {
			found = true
			if c.Abstractness == nil || *c.Abstractness != 0.5 {
				t.Fatalf("svc A must be 0.5: %+v", c)
			}
			if c.Distance == nil {
				t.Fatalf("svc D must be present when I and A are defined: %+v", c)
			}
		}
	}
	if !found {
		t.Fatalf("svc package metric missing: %s", out)
	}
}

// TestRulesLimits는 limits 규칙이 실제 위반과 strict 종료를 내는지 확인한다.
func TestRulesLimits(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"web/web.go": `package web

import (
	_ "example.com/fixture/db"
	_ "example.com/fixture/cache"
)
`,
		"db/db.go":       `package db`,
		"cache/cache.go": `package cache`,
		".gartograph.yml": `components:
  web: ["web"]
  db: ["db"]
  cache: ["cache"]
deps:
  web: ["db", "cache"]
limits:
  - {component: web, maxOut: 1}
`,
	})
	code, out, errb := run(t, "rules", "--dir", dir, "--strict", "--format", "json")
	if code != 1 {
		t.Fatalf("rules --strict over limit: expected 1, got %d %s", code, errb)
	}
	var rep struct {
		Violations []struct {
			Rule          string `json:"rule"`
			Name          string `json:"name"`
			FromComponent string `json:"fromComponent"`
			Reason        string `json:"reason"`
		} `json:"violations"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("rules output is not JSON: %v\n%s", err, out)
	}
	var limitV bool
	for _, v := range rep.Violations {
		if v.Rule == "limit" && v.Name == "maxOut" &&
			v.FromComponent == "web" && strings.Contains(v.Reason, "depends on 2") {
			limitV = true
		}
	}
	if !limitV {
		t.Fatalf("expected limit violation for web: %s", out)
	}
}

// TestDeadExternalDispatch는 모듈 밖 인터페이스로만 불리는 메서드의 오탐
// 회귀다. 실측(gartograph 자기 저장소)에서 flag.Value(fs.Var)·error 반환·
// 외부 라이브러리 unmarshal 훅과 그 전이 호출(Error→quote)이 unreachable로
// 잘못 나왔다. 리시버가 살아 있으면 살리고, 무관한 메서드와 리시버가 죽은
// 메서드는 그대로 보고하며 보고에 satisfies 사실을 싣는지 본다.
func TestDeadExternalDispatch(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import (
	"encoding/json"
	"flag"

	"example.com/fixture/lib"
)

func main() {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	var p lib.Patterns
	fs.Var(&p, "pattern", "repeatable")
	var cfg lib.Config
	_ = json.Unmarshal([]byte("{}"), &cfg)
	_, _ = lib.Parse("x")
	_ = lib.Wrapper{}
}
`,
		"lib/lib.go": `package lib

type Named struct{}

func (Named) String() string { return "named" }

type Wrapper struct{ Named }

type Patterns []string

func (p *Patterns) Set(v string) error { *p = append(*p, v); return nil }
func (p *Patterns) String() string     { return "" }
func (p *Patterns) Helper()            {}

type Level int

func (l *Level) UnmarshalText(b []byte) error { *l = Level(len(b)); return nil }

type Config struct{ Level Level }

type ParseError struct{ Input string }

func (e *ParseError) Error() string { return quote(e.Input) }

func quote(s string) string { return "'" + s + "'" }

func Parse(s string) (int, error) { return 0, &ParseError{Input: s} }

type Orphan struct{}

func (Orphan) String() string { return "orphan" }
`,
	})
	code, out, errb := run(t, "dead", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("dead failed: %d %s", code, errb)
	}
	var rep struct {
		Unreachable []struct {
			ID        string   `json:"id"`
			Satisfies []string `json:"satisfies"`
		} `json:"unreachable"`
		Limitations []string `json:"limitations"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("dead output is not JSON: %v\n%s", err, out)
	}
	found := map[string][]string{}
	for _, f := range rep.Unreachable {
		found[f.ID] = f.Satisfies
	}
	const lib = "example.com/fixture/lib"
	for _, alive := range []string{
		lib + ".(Patterns).Set", lib + ".(Patterns).String",
		lib + ".(Level).UnmarshalText", lib + ".(ParseError).Error", lib + ".quote",
		lib + ".(Named).String", // 임베딩 승격 — Wrapper가 Named를 embeds로 끌어온다
	} {
		if _, ok := found[alive]; ok {
			t.Fatalf("%s is reachable through external dispatch but was reported: %s", alive, out)
		}
	}
	if _, ok := found[lib+".(Patterns).Helper"]; !ok {
		t.Fatalf("method unrelated to external interfaces must still be reported: %s", out)
	}
	if s, ok := found[lib+".(Orphan).String"]; !ok || !slices.Contains(s, "fmt.Stringer") {
		t.Fatalf("method of an unreachable receiver must be reported with its satisfies fact: %s", out)
	}
	var mentions bool
	for _, l := range rep.Limitations {
		mentions = mentions || strings.Contains(l, "receiver type is reachable")
	}
	if !mentions {
		t.Fatalf("method findings must state the external dispatch rule: %v", rep.Limitations)
	}

	code, out, errb = run(t, "dead", "--dir", dir, "--explain", lib+".quote")
	if code != 0 || !strings.Contains(out, lib+".(ParseError).Error (external dispatch: error)") {
		t.Fatalf("explain must mark the synthetic hop through the receiver type: %d %s %s", code, out, errb)
	}

	// RTA는 외부 호출을 SSA로 직접 보므로 리시버 규칙 문구가 붙으면 거짓이다.
	code, out, errb = run(t, "dead", "--dir", dir, "--algo", "rta", "--format", "json")
	if code != 0 {
		t.Fatalf("dead --algo rta failed: %d %s", code, errb)
	}
	if strings.Contains(out, "receiver type is reachable") {
		t.Fatalf("rta report must not claim the receiver rule: %s", out)
	}
	// 문구를 빼는 근거: RTA는 SSA 전체 프로그램으로 외부 호출을 직접 봐서 이 메서드들을 살린다.
	for _, alive := range []string{lib + ".(Patterns).Set", lib + ".(ParseError).Error", lib + ".quote"} {
		if strings.Contains(out, `"`+alive+`"`) {
			t.Fatalf("rta must keep %s reachable through external calls: %s", alive, out)
		}
	}
}

// TestDeadExternalDispatchOldDocument는 satisfies 사실이 없는 옛 저장 문서가
// 규칙이 적용됐다고 거짓 문구를 내지 않고, 재수확을 권하는지 확인한다.
func TestDeadExternalDispatchOldDocument(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { _, _ = lib.Parse("x") }
`,
		"lib/lib.go": `package lib

type ParseError struct{}

func (*ParseError) Error() string { return "e" }

func Parse(s string) (int, error) { return 0, &ParseError{} }
`,
	})
	code, out, errb := run(t, "graph", "--level", "symbol", "--format", "json", "--dir", dir)
	if code != 0 {
		t.Fatalf("graph failed: %d %s", code, errb)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	for _, v := range doc["vertices"].([]any) {
		delete(v.(map[string]any), "satisfies")
		delete(v.(map[string]any), "receiver")
	}
	old, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "old.json")
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb = run(t, "dead", "--graph", path, "--format", "json")
	if code != 0 {
		t.Fatalf("dead --graph failed: %d %s", code, errb)
	}
	if !strings.Contains(out, "(ParseError).Error") {
		t.Fatalf("without facts the old false positive is expected to remain: %s", out)
	}
	if strings.Contains(out, "receiver type is reachable") || !strings.Contains(out, "re-harvest") {
		t.Fatalf("old document must get the re-harvest limitation, not the rule claim: %s", out)
	}
}
