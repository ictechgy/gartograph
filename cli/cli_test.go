package cli

import (
	"bytes"
	"encoding/json"
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
