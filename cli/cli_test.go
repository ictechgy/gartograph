package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
				RuleID string `json:"ruleId"`
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
