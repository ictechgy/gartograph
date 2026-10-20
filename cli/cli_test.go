package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ictechgy/gartograph/analysis"
	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/export"
	"github.com/ictechgy/gartograph/graph"
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

// TestVersionFlag는 --version이 version 하위 명령과 같은 한 줄을 내는지 확인한다.
// isthmus verify-installed-compatibility.mjs는 `<tool> --version` 출력 끝의 semver를 읽는다.
func TestVersionFlag(t *testing.T) {
	_, want, _ := run(t, "version")
	code, got, errb := run(t, "--version")
	if code != 0 || errb != "" {
		t.Fatalf("--version: expected 0 and empty stderr, got %d %q", code, errb)
	}
	if got != want || got != "gartograph "+Version+"\n" {
		t.Fatalf("--version output %q, want %q", got, want)
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
	// 파일 모드는 symbol로 덮어쓰지만, 오타는 ID 모드처럼 거부해야 한다.
	if code, _, _ := run(t, "impact", "--files", "lib/lib.go",
		"--dir", dir, "--level", "nope"); code != 2 {
		t.Fatalf("bad level in file mode must exit 2, got %d", code)
	}
}

// TestIDCommandsLevelHintSavedGraph는 저장 문서에서 못 찾은 ID의 힌트가
// 무시되는 --level이 아니라 다시 저장하는 길을 알리는지 확인한다.
func TestIDCommandsLevelHintSavedGraph(t *testing.T) {
	dir := deadFixture(t)
	saved := filepath.Join(t.TempDir(), "pkg.json")
	if code, _, errb := run(t, "graph", "--dir", dir, "--out", saved); code != 0 {
		t.Fatalf("graph --out failed: %d %s", code, errb)
	}
	for _, args := range [][]string{
		{"query", "example.com/fixture/lib.Run"},
		{"impact", "example.com/fixture/lib.Run"},
		{"path", "example.com/fixture/lib.Run", "example.com/fixture/lib"},
		{"shared", "example.com/fixture/lib.Run", "example.com/fixture/lib"},
	} {
		code, _, errb := run(t, append(args, "--graph", saved)...)
		if code != 2 || !strings.Contains(errb, "drop --graph") {
			t.Fatalf("%v: expected saved-graph hint, got %d %s", args, code, errb)
		}
	}
}

// TestIDCommandsPackageAnswersAcrossLevels는 패키지 ID의 답(경로·집합·의존자)이
// package와 symbol 수확에서 같은지 확인한다. symbol이 기본이 되어도 기존
// 패키지 질의가 달라지면 안 된다 — contains는 의존이 아니다. limitations는
// 수확 레벨마다 못 본 영역이 달라 비교에서 뺀다.
func TestIDCommandsPackageAnswersAcrossLevels(t *testing.T) {
	dir := deadFixture(t)
	for _, args := range [][]string{
		{"query", "example.com/fixture/lib"},
		{"impact", "example.com/fixture/lib"},
		{"path", "example.com/fixture", "example.com/fixture/lib"},
		{"shared", "example.com/fixture", "example.com/fixture/lib"},
	} {
		answers := map[string]map[string]any{}
		for _, level := range []string{"package", "symbol"} {
			code, out, errb := run(t, append(args, "--dir", dir, "--level", level)...)
			if code != 0 {
				t.Fatalf("%v at %s: %d %s", args, level, code, errb)
			}
			var res map[string]any
			if err := json.Unmarshal([]byte(out), &res); err != nil {
				t.Fatalf("%v at %s: not JSON: %v", args, level, err)
			}
			delete(res, "limitations")
			answers[level] = res
		}
		if !reflect.DeepEqual(answers["package"], answers["symbol"]) {
			t.Fatalf("%v: package answer changed across levels:\npackage %v\nsymbol  %v",
				args, answers["package"], answers["symbol"])
		}
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

// TestImpactSinceGuardsGit은 --since 값이 git 옵션으로 읽히지 않고(git을 실행하기 전에 사용법
// 오류), 분석 대상 .git/config의 core.fsmonitor 훅이 돌지 않으며, 범위 표기는 계속 동작하는지
// 확인한다 — `--since=--output=FILE`은 git이 임의 파일을 덮어쓰고도 빈 변경 목록으로 성공했다.
func TestImpactSinceGuardsGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the fsmonitor hook fixture is a POSIX shell script")
	}
	dir := deadFixture(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "init")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	appendTo := func(rel, text string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(src, text...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	appendTo("lib/lib.go", "func Changed() {}\n")
	git("commit", "-qam", "change lib")

	tmp := t.TempDir()
	victim := filepath.Join(tmp, "victim")
	if err := os.WriteFile(victim, []byte("ORIGINAL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rev := range []string{"--output=" + victim, "-p", "HEAD\n--output=" + victim, "HEAD\x1b[2J"} {
		code, out, errb := run(t, "impact", "--dir", dir, "--since="+rev)
		if code != 2 || out != "" || !strings.Contains(errb, "HEAD~1") {
			t.Fatalf("--since %q must be a usage error before git runs: %d %q %q", rev, code, out, errb)
		}
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "ORIGINAL\n" {
		t.Fatalf("an option-like --since must not reach git: victim is %q (%v)", got, err)
	}

	code, out, errb := run(t, "impact", "--dir", dir, "--since", "origin/main...HEAD")
	if code != 0 || !strings.Contains(out, `"lib/lib.go"`) {
		t.Fatalf("range revisions must keep working: %d %s %s", code, out, errb)
	}

	marker := filepath.Join(tmp, "fsmonitor-ran")
	hook := filepath.Join(tmp, "fsmonitor.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho ran >> '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git("config", "core.fsmonitor", hook)
	appendTo("main.go", "func extra() {}\n")
	code, out, errb = run(t, "impact", "--dir", dir, "--since", "HEAD")
	if code != 0 || !strings.Contains(out, `"main.go"`) {
		t.Fatalf("impact --since HEAD on a dirty tree failed: %d %s %s", code, out, errb)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the work tree's core.fsmonitor hook must not run (stat err %v)", err)
	}
}

// gitIn은 dir에서 git을 실행해 출력을 돌려주는 함수다 — 실패하면 테스트를 끝낸다. 커밋 신원과 로컬
// 서브모듈 clone 허용(git 2.38.1부터 기본 거부)을 인자로 준다.
func gitIn(t *testing.T, dir string) func(args ...string) string {
	t.Helper()
	return func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t",
			"-c", "protocol.file.allow=always"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
}

// commitFixture는 dir을 git 저장소로 만들어 전부 커밋하고, 그 저장소에서 git을 부르는 함수를 돌려준다.
// prepare는 첫 커밋 전에 돈다(커밋할 때 이미 있어야 하는 filter 설정용). 사용자·시스템 git 설정은 읽지
// 않는다 — 개발자 머신의 전역 설정(git-lfs의 filter.lfs 같은)이 결과를 바꾸지 않게 하기 위해서다.
// gartograph가 띄우는 git도 이 환경을 물려받는다. 셸 스크립트 fixture라 windows에서는 건너뛴다.
func commitFixture(t *testing.T, dir string, prepare func(git func(args ...string) string)) func(args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the repository configuration fixtures are POSIX shell scripts")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git := gitIn(t, dir)
	git("init", "-q")
	if prepare != nil {
		prepare(git)
	}
	git("add", "-A")
	git("commit", "-qm", "init")
	return git
}

// writeTestFile은 path에 내용을 쓴다(상위 디렉터리를 만든다). mode가 0이면 0o644다.
func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if mode == 0 {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// markerCommand는 실행되면 markers 안의 name 파일에 한 줄을 남기고 then을 실행하는 셸 스크립트를
// at에 만든다 — 분석 대상 설정이 띄우는 명령(filter·훅)이 실제로 돌았는지 파일로 남긴다.
func markerCommand(t *testing.T, at, markers, name, then string) string {
	t.Helper()
	writeTestFile(t, at, "#!/bin/sh\necho ran >> '"+filepath.Join(markers, "ran-"+name)+"'\n"+then+"\n", 0o755)
	return at
}

// assertNoRepositoryCommandRan은 markerCommand가 남긴 마커가 하나도 없는지 본다.
func assertNoRepositoryCommandRan(t *testing.T, markers, what string) {
	t.Helper()
	entries, err := os.ReadDir(markers)
	if err != nil {
		t.Fatal(err)
	}
	var ran []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ran-") {
			ran = append(ran, e.Name())
		}
	}
	if len(ran) > 0 {
		t.Fatalf("%s ran commands from the analyzed repository's git configuration: %v", what, ran)
	}
}

// staleStat은 파일의 시각을 과거로 돌려 git의 stat 캐시를 낡게 만든다 — 그래야 git이 내용을 다시
// 읽어(clean filter를 거쳐) 바뀌었는지 본다. 내용은 그대로다.
func staleStat(t *testing.T, at time.Time, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

// hasLimitation은 prefix로 시작하고 want를 담은 limitation이 있는지 본다.
func hasLimitation(limitations []string, prefix, want string) bool {
	for _, l := range limitations {
		if strings.HasPrefix(l, prefix) && strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// TestGitCallsDoNotRunRepositoryCommands는 분석용 git 호출(impact --since의 diff, 순회 revision 판독)이
// 분석 대상 저장소 설정의 filter 드라이버와 post-index-change 훅을 실행하지 않는지 본다(DECISIONS D1).
// 드라이버는 .git/info/attributes와 커밋된 .gitattributes 양쪽에서 걸고, 점이 든 이름과 장기 실행
// process 키도 둔다. stat 캐시를 낡게 만들어야 git이 내용을 다시 읽는다 — 수정 전에는 그때 clean·process
// 명령이 돌았고, git diff의 index 갱신이 post-index-change 훅을 실행했다. 내용을 바꾸지 않는 filter라
// 순회는 여전히 깨끗한 HEAD를 싣고, --since는 stat만 바뀐 파일도 바뀐 파일로 낸다(diff.autoRefreshIndex=false).
func TestGitCallsDoNotRunRepositoryCommands(t *testing.T) {
	variants := []struct{ name, driver, key, attributes, then string }{
		{"info attributes clean", "evil", "clean", ".git/info/attributes", "cat"},
		{"committed gitattributes dotted name", "a.b", "clean", ".gitattributes", "cat"},
		{"process", "p", "process", ".git/info/attributes", "exit 1"},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			dir := traversalFixture(t)
			git := commitFixture(t, dir, func(func(...string) string) {
				if v.attributes == ".gitattributes" {
					writeTestFile(t, filepath.Join(dir, ".gitattributes"), "*.go filter="+v.driver+"\n", 0)
				}
			})
			head := git("rev-parse", "HEAD")
			markers := t.TempDir()
			git("config", "filter."+v.driver+"."+v.key,
				markerCommand(t, filepath.Join(markers, "driver.sh"), markers, "filter", v.then))
			if v.attributes != ".gitattributes" {
				writeTestFile(t, filepath.Join(dir, v.attributes), "*.go filter="+v.driver+"\n", 0)
			}
			markerCommand(t, filepath.Join(dir, ".git", "hooks", "post-index-change"), markers, "hook", "")
			mainGo := filepath.Join(dir, "main.go")
			old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

			staleStat(t, old, mainGo)
			code, out, errb := run(t, "impact", "--dir", dir, "--since", "HEAD")
			assertNoRepositoryCommandRan(t, markers, "impact --since HEAD")
			if code != 0 || !strings.Contains(out, `"main.go"`) {
				t.Fatalf("impact --since HEAD must list the file whose stat changed: %d %s %s", code, out, errb)
			}
			for _, args := range [][]string{{"reach", fx + ".main"}, {"impact", "--format", "language-traversal", fx + ".handleUsers"}} {
				old = old.Add(time.Hour)
				staleStat(t, old, mainGo)
				code, out, errb := run(t, append(args, "--dir", dir)...)
				assertNoRepositoryCommandRan(t, markers, args[0]+" (revision)")
				if doc := parseTraversal(t, out); code != 0 || doc.Revision == nil || *doc.Revision != head {
					t.Fatalf("%v: a filter that keeps the content must leave the clean HEAD %s: %d %s %s", args, head, code, out, errb)
				}
			}
		})
	}
}

// TestGitCallsRefuseUnneutralizableFilter는 이름에 '='가 든 filter 드라이버가 설정돼 있으면 분석용 git을
// 실행하지 않는지 본다 — git은 -c 값을 첫 '='에서 나눠 그런 드라이버를 빈 명령으로 덮을 수 없다(fail-closed).
// impact --since는 원인과 --files를 담은 오류(2)로 끝나고, 순회는 revision을 싣지 않고 그 이유를
// limitation으로 알린다.
func TestGitCallsRefuseUnneutralizableFilter(t *testing.T) {
	dir := traversalFixture(t)
	git := commitFixture(t, dir, nil)
	markers := t.TempDir()
	git("config", "filter.a=b.clean", markerCommand(t, filepath.Join(markers, "driver.sh"), markers, "filter", "cat"))
	writeTestFile(t, filepath.Join(dir, ".git", "info", "attributes"), "*.go filter=a=b\n", 0)
	mainGo := filepath.Join(dir, "main.go")

	staleStat(t, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), mainGo)
	code, out, errb := run(t, "impact", "--dir", dir, "--since", "HEAD")
	assertNoRepositoryCommandRan(t, markers, "impact --since HEAD")
	if code != 2 || out != "" || !strings.Contains(errb, "'='") || !strings.Contains(errb, "--files") {
		t.Fatalf("an unneutralizable filter driver must stop impact --since with a --files hint: %d %q %q", code, out, errb)
	}

	staleStat(t, time.Date(2002, 1, 1, 0, 0, 0, 0, time.UTC), mainGo)
	code, out, errb = run(t, "reach", "--dir", dir, fx+".main")
	assertNoRepositoryCommandRan(t, markers, "reach")
	doc := parseTraversal(t, out)
	if code != 0 || doc.Revision != nil || !hasLimitation(doc.Limitations, "revision-unverified:", "--revision") {
		t.Fatalf("reach must omit the revision and say why: %d %s %s", code, out, errb)
	}
}

// TestGitCallsStayOutOfSubmodules는 서브모듈 저장소 자신의 설정(그 안의 filter 드라이버)을 실행하지 않는지
// 본다 — 상위 저장소의 status·diff는 기본값이면 서브모듈 안에서 git을 띄워 그 설정을 따른다. 순회는 서브모듈
// 작업 트리를 볼 수 없어 revision을 싣지 않고 limitation으로 알린다(fail-safe). impact --since는 서브모듈에
// 기록된 커밋과 서브모듈 HEAD가 다르면 지금처럼 그 경로를 바뀐 파일로 낸다.
func TestGitCallsStayOutOfSubmodules(t *testing.T) {
	subSrc := t.TempDir()
	writeTestFile(t, filepath.Join(subSrc, "notes.txt"), "v1\n", 0)
	gitSrc := commitFixture(t, subSrc, nil)
	writeTestFile(t, filepath.Join(subSrc, "notes.txt"), "v2\n", 0)
	gitSrc("commit", "-qam", "v2")

	dir := traversalFixture(t)
	git := commitFixture(t, dir, nil)
	git("submodule", "add", "-q", subSrc, "sub")
	git("commit", "-qm", "add submodule")
	subWork := filepath.Join(dir, "sub")
	gitSub := gitIn(t, subWork)
	markers := t.TempDir()
	gitSub("config", "filter.subevil.clean", markerCommand(t, filepath.Join(markers, "driver.sh"), markers, "filter", "cat"))
	writeTestFile(t, filepath.Join(gitSub("rev-parse", "--absolute-git-dir"), "info", "attributes"), "* filter=subevil\n", 0)
	notes := filepath.Join(subWork, "notes.txt")

	staleStat(t, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), notes)
	code, out, errb := run(t, "reach", "--dir", dir, fx+".main")
	assertNoRepositoryCommandRan(t, markers, "reach")
	doc := parseTraversal(t, out)
	if code != 0 || doc.Revision != nil || !hasLimitation(doc.Limitations, "revision-unverified:", "1 git submodule") {
		t.Fatalf("reach must omit the revision of a tree with submodules and say why: %d %s %s", code, out, errb)
	}

	staleStat(t, time.Date(2002, 1, 1, 0, 0, 0, 0, time.UTC), notes)
	code, out, errb = run(t, "impact", "--dir", dir, "--since", "HEAD")
	assertNoRepositoryCommandRan(t, markers, "impact --since HEAD")
	if code != 0 || strings.Contains(out, `"sub"`) {
		t.Fatalf("a submodule at its recorded commit is not a changed path: %d %s %s", code, out, errb)
	}

	// 악성 설정을 걷어 낸 뒤 서브모듈을 기록과 다른 커밋으로 옮긴다 — 테스트 자신의 git이 마커를 남기지 않게.
	gitSub("config", "--unset", "filter.subevil.clean")
	gitSub("checkout", "-q", "--detach", "HEAD~1")
	code, out, errb = run(t, "impact", "--dir", dir, "--since", "HEAD")
	if code != 0 || !strings.Contains(out, `"sub"`) {
		t.Fatalf("a submodule moved off its recorded commit must stay a changed path: %d %s %s", code, out, errb)
	}
}

// TestGitTreatsFilteredFilesAsChanged는 clean filter가 내용을 바꾸던 파일(git-lfs 같은)을 filter 없이 읽으면
// 바뀐 파일로 보는지 본다 — git은 작업 트리 원문과 커밋된(filter를 거친) blob을 비교한다. stat 캐시가
// 그대로면 다시 읽지 않으므로 깨끗하다. 낡으면 순회는 깨끗하다고 주장하지 않고 revision을 싣지 않으며
// (fail-safe), impact --since는 그 파일을 바뀐 파일로 낸다.
func TestGitTreatsFilteredFilesAsChanged(t *testing.T) {
	dir := traversalFixture(t)
	data := filepath.Join(dir, "data.txt")
	writeTestFile(t, data, "payload\n", 0)
	writeTestFile(t, filepath.Join(dir, ".gitattributes"), "data.txt filter=upper\n", 0)
	// 커밋 전에 시각을 과거로 둔다 — index 기록보다 한참 앞선 파일은 racy-clean으로 다시 읽히지 않는다.
	staleStat(t, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), data)
	git := commitFixture(t, dir, func(git func(...string) string) {
		git("config", "filter.upper.clean", "tr a-z A-Z")
	})
	head := git("rev-parse", "HEAD")
	if blob := git("show", "HEAD:data.txt"); blob != "PAYLOAD" {
		t.Fatalf("fixture: the committed blob must be the filter's output, got %q", blob)
	}

	code, out, errb := run(t, "reach", "--dir", dir, fx+".main")
	if doc := parseTraversal(t, out); code != 0 || doc.Revision == nil || *doc.Revision != head {
		t.Fatalf("an untouched filtered file must not make the tree dirty: %d %s %s", code, out, errb)
	}

	staleStat(t, time.Date(2002, 1, 1, 0, 0, 0, 0, time.UTC), data)
	code, out, errb = run(t, "reach", "--dir", dir, fx+".main")
	doc := parseTraversal(t, out)
	if code != 0 || doc.Revision != nil || hasLimitation(doc.Limitations, "revision-unverified:", "") {
		t.Fatalf("a re-read filtered file must make the tree dirty (no revision, no extra limitation): %d %s %s",
			code, out, errb)
	}
	code, out, errb = run(t, "impact", "--dir", dir, "--since", "HEAD")
	if code != 0 || !strings.Contains(out, `"data.txt"`) {
		t.Fatalf("impact --since must list the re-read filtered file: %d %s %s", code, out, errb)
	}
}

// TestChangedFilesSinceExplainsUsageErrors는 git diff가 인자를 사용법 오류(종료 코드 129)로 거부하면
// 원인과 해결 방향을 담은 한 줄 오류를 내는지 본다. git 2.24보다 오래된 git은 --end-of-options를 모르는
// 옵션으로 보고 수십 줄 사용법만 찍는다 — 그대로 옮기면 왜 실패했는지 보이지 않는다. 그런 git을
// 흉내 내는 가짜 git을 PATH에 둔다. 다른 실패(종료 코드 128의 bad revision)는 git의 말을 그대로 옮긴다.
// diff 앞의 filter 드라이버 조회(git config)에는 "일치 없음"(종료 1)으로 답한다 — 오래된 git도 그 조회는 안다.
func TestChangedFilesSinceExplainsUsageErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a POSIX shell script")
	}
	fake := t.TempDir()
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = config ]; then exit 1; fi\n" +
		"  if [ \"$a\" = nope ]; then echo \"fatal: bad revision 'nope'\" >&2; exit 128; fi\n" +
		"done\n" +
		"echo 'usage: git diff [<options>] [<commit> [<commit>]] [--] [<path>...]' >&2\n" +
		"echo '   or: git diff [<options>] --cached [<commit>] [--] [<path>...]' >&2\n" +
		"exit 129\n"
	if err := os.WriteFile(filepath.Join(fake, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake)

	_, err := changedFilesSince(t.TempDir(), "origin/main...HEAD")
	if err == nil {
		t.Fatal("a usage error from git must fail")
	}
	msg := err.Error()
	for _, want := range []string{"(usage: git diff [<options>]", "git 2.24 or later", "--end-of-options", "--files"} {
		if !strings.Contains(msg, want) {
			t.Errorf("usage error must say %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "\n") {
		t.Errorf("usage error must keep only git's first usage line: %q", msg)
	}

	_, err = changedFilesSince(t.TempDir(), "nope")
	if err == nil || err.Error() != "git diff nope: fatal: bad revision 'nope'" {
		t.Fatalf("other git failures must keep git's own message: %v", err)
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

// TestBadFormatLeavesArtifactsUntouched는 거부될 --format 호출이 기존 baseline·저장 문서를
// 덮어쓰지 않는지 확인한다 — 형식 오타 한 번이 커밋된 baseline을 지우면 다음 --baseline
// 실행에서 알려진 항목이 전부 새 항목이 된다. rules처럼 형식 검증이 부수 효과보다 먼저다.
func TestBadFormatLeavesArtifactsUntouched(t *testing.T) {
	dir := cycleFixture(t)
	tmp := t.TempDir()
	cases := []struct {
		path string
		args []string
	}{
		{filepath.Join(tmp, "cycles.json"), []string{"cycles", "--dir", dir, "--write-baseline"}},
		{filepath.Join(tmp, "dead.json"), []string{"dead", "--dir", dir, "--write-baseline"}},
		{filepath.Join(tmp, "graph.json"), []string{"graph", "--dir", dir, "--out"}},
	}
	const sentinel = "existing artifact\n"
	for _, c := range cases {
		t.Run(c.args[0], func(t *testing.T) {
			if err := os.WriteFile(c.path, []byte(sentinel), 0o644); err != nil {
				t.Fatal(err)
			}
			args := append(append([]string(nil), c.args...), c.path, "--format", "bogus")
			code, out, errb := run(t, args...)
			if code != 2 || out != "" || !strings.Contains(errb, `unknown format "bogus"`) {
				t.Fatalf("%v: bad format must be a usage error before any output: %d %q %q", args, code, out, errb)
			}
			if got, err := os.ReadFile(c.path); err != nil || string(got) != sentinel {
				t.Fatalf("%v: rejected invocation rewrote %s: %q %v", args, c.path, got, err)
			}
			// 같은 호출이 올바른 형식이면 실제로 쓴다 — 경로가 틀려 위 검사가 헛도는 것을 막는다.
			if code, _, errb := run(t, append(c.args, c.path)...); code != 0 {
				t.Fatalf("%v: valid invocation failed: %d %s", c.args, code, errb)
			}
			if got, err := os.ReadFile(c.path); err != nil || string(got) == sentinel {
				t.Fatalf("%v: valid invocation must write %s: %v", c.args, c.path, err)
			}
		})
	}
}

// failingWriter는 모든 쓰기를 실패시킨다 — 디스크가 가득 찬 표준 출력을 흉내 낸다.
type failingWriter struct{}

// Write는 io.Writer 계약이다.
func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("no space left on device")
}

// TestStdoutWriteErrorsFail은 산출물을 표준 출력에 쓰다 실패하면 2로 끝나는지 확인한다 —
// 잘린 그래프·SARIF·사실 문서가 성공(0)으로 끝나면 소비자가 그것을 완전한 산출물로 읽는다.
func TestStdoutWriteErrorsFail(t *testing.T) {
	dead, rules := deadFixture(t), rulesFixture(t)
	for _, args := range [][]string{
		{"graph", "--dir", dead},
		{"graph", "--dir", dead, "--format", "dot"},
		{"cycles", "--dir", dead, "--format", "sarif"},
		{"dead", "--dir", dead, "--format", "sarif"},
		{"rules", "--dir", rules, "--format", "sarif"},
		{"rules", "--dir", rules, "--format", "json"},
		{"bridges", "--dir", dead},
		{"schema", "--dir", dead},
		{"routes", "--dir", dead},
		{"reach", "example.com/fixture/lib.Run", "--dir", dead},
	} {
		var errb bytes.Buffer
		code := Run(args, failingWriter{}, &errb)
		if code != 2 || !strings.Contains(errb.String(), "no space left on device") {
			t.Errorf("%v: a failed write must exit 2 with its cause, got %d %q", args, code, errb.String())
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
	var rep struct {
		Cycles []json.RawMessage `json:"cycles"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil || rep.Cycles == nil || len(rep.Cycles) != 0 {
		t.Fatalf("cycles --format json must emit an envelope with an empty cycles array: %v %s", err, out)
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

// TestBridgesSkipsNonRegularSources는 bridges가 정규 파일이 아닌 .go 항목을 읽지 않고
// unparsed로 세는지 확인한다. 분석 대상이 .go 이름의 FIFO나 장치 링크를 두면 파서가 open에서
// 막히거나(FIFO) 끝없이 읽었다(/dev/zero — 회귀해도 메모리를 쓰지 않는 FIFO로 검증한다).
// 건너뛴 파일은 limitation으로 남아야 "cgo 없음"과 구분되고, 읽지 않았으니 sourceModifiedAt
// (이번 추출에서 읽은 소스의 최신 mtime)에도 들어가지 않는다. 나머지 파일의 관측은 그대로다.
func TestBridgesSkipsNonRegularSources(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
		"native/native.go": "package native\n\nimport \"C\"\n\n" +
			"//export Add\nfunc Add(a, b C.int) C.int { return a + b }\n",
	})
	testutil.MakeFIFO(t, filepath.Join(dir, "pipe.go"))
	elsewhere := filepath.Join(t.TempDir(), "pipe")
	testutil.MakeFIFO(t, elsewhere)
	if err := os.Symlink(elsewhere, filepath.Join(dir, "native", "linked.go")); err != nil {
		t.Fatal(err)
	}
	read := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, f := range []string{"main.go", "native/native.go"} {
		if err := os.Chtimes(filepath.Join(dir, f), read, read); err != nil {
			t.Fatal(err)
		}
	}
	var code int
	var out, errb string
	testutil.FinishWithin(t, testutil.BlockDeadline, func() { code, out, errb = run(t, "bridges", "--dir", dir) })
	if code != 0 {
		t.Fatalf("bridges failed: %d %s", code, errb)
	}
	var doc struct {
		SourceModifiedAt string   `json:"sourceModifiedAt"`
		Limitations      []string `json:"limitations"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := []string{
		"unscanned-ffi-interop: 1 Go source files use cgo interop (1 //export symbols)",
		"unparsed-sources: 2 Go files could not be parsed; cgo use there is uncounted",
	}
	if !reflect.DeepEqual(doc.Limitations, want) || doc.SourceModifiedAt != "2020-01-02T03:04:05.000Z" {
		t.Fatalf("non-regular .go entries must be skipped as unparsed, unread:\n%s", out)
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

// assertTextSafe는 텍스트 출력의 줄에 원시 제어 문자(forbiddenChars, 잘못된 UTF-8)가 없고
// "::"로 시작하는 줄이 없는지 본다 — GitHub Actions는 줄 앞 공백을 지운 뒤 "::"로 시작하는
// 줄을 워크플로 명령으로 읽고, ESC 시퀀스는 터미널 화면을 지우거나 가짜 줄을 그린다.
func assertTextSafe(t *testing.T, label, out string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if forbiddenChars.MatchString(line) || !utf8.ValidString(line) {
			t.Errorf("%s: raw control character in text output line %q", label, line)
		}
		if strings.HasPrefix(strings.TrimSpace(line), "::") || strings.Contains(line, "##[") {
			t.Errorf("%s: text output line reads as a workflow command: %q", label, line)
		}
	}
}

// TestRulesTextEscapesControlCharacters는 분석 대상이 고른 파일 이름과 설정의 reason에 든
// 개행·ESC가 rules 텍스트 출력에서 이스케이프되는지 본다. JSON은 encoding/json이 이미
// 이스케이프하므로 원문 그대로여야 한다.
func TestRulesTextEscapesControlCharacters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file names with control characters are not valid on windows")
	}
	newlineFile := "web/w\n::error title=gartograph::injected\nx.go"
	dir := testutil.WriteModule(t, map[string]string{
		newlineFile:                   "package web\n\nimport _ \"example.com/fixture/db\"\n",
		"api/a\x1b[2J\x1b[31mFAKE.go": "package api\n\nimport _ \"example.com/fixture/db\"\n",
		"db/db.go":                    "package db\n",
		".gartograph.yml": `components:
  web: ["web"]
  api: ["api"]
  db: ["db"]
deps: {}
deny:
  api:
    - {to: db, reason: "line one\n::warning::fake \e[31mred"}
`,
	})
	code, out, errb := run(t, "rules", "--dir", dir)
	if code != 0 {
		t.Fatalf("rules failed: %d %s", code, errb)
	}
	assertTextSafe(t, "rules", out)
	for _, want := range []string{
		`w\x0a::error title=gartograph::injected\x0ax.go:3:8`,
		`a\x1b[2J\x1b[31mFAKE.go:3:8`,
		`reason: line one\x0a::warning::fake \x1b[31mred`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rules text must keep the escaped text %q:\n%s", want, out)
		}
	}
	code, out, errb = run(t, "rules", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("rules json failed: %d %s", code, errb)
	}
	var rep struct {
		Violations []struct {
			Reason   string `json:"reason"`
			Position *struct {
				File string `json:"file"`
			} `json:"position"`
		} `json:"violations"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("rules json: %v\n%s", err, out)
	}
	var raw []string
	for _, v := range rep.Violations {
		raw = append(raw, v.Reason)
		if v.Position != nil {
			raw = append(raw, v.Position.File)
		}
	}
	joined := strings.Join(raw, "|")
	if !strings.Contains(joined, "line one\n::warning::fake \x1b[31mred") ||
		!strings.Contains(joined, filepath.FromSlash(newlineFile)) {
		t.Fatalf("JSON must keep the original strings (encoding/json escapes them): %q", joined)
	}
}

// TestComponentNameLinesDoNotStartWorkflowCommands는 설정의 컴포넌트 이름이 줄 첫머리에 오는
// mapping·metrics 텍스트 출력이 `::`로 시작해 GitHub Actions 워크플로 명령으로 읽히지 않는지 본다.
// 러너는 앞 공백류(NBSP·U+3000 포함)를 지운 뒤 `::`를 보므로 제어 문자 이스케이프만으로는 막히지
// 않는다. 줄 어디에든 있는 옛 형식 `##[명령]`도 러너가 읽으므로 함께 막는다.
func TestComponentNameLinesDoNotStartWorkflowCommands(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"web/web.go":   "package web\n\nimport _ \"example.com/fixture/db\"\n",
		"db/db.go":     "package db\n",
		"nbsp/nbsp.go": "package nbsp\n",
		"wide/wide.go": "package wide\n",
		"old/old.go":   "package old\n",
		".gartograph.yml": `components:
  "::error::injected": ["web"]
  "  ::warning::spaced": ["db"]
  "\u00a0::error::nbsp": ["nbsp"]
  "\u3000::error::wide": ["wide"]
  "legacy ##[error]old": ["old"]
deps: {}
`,
	})
	for _, command := range []string{"mapping", "metrics"} {
		code, out, errb := run(t, command, "--dir", dir)
		if code != 0 {
			t.Fatalf("%s failed: %d %s", command, code, errb)
		}
		assertTextSafe(t, command, out)
		for _, want := range []string{`\x3a:error::injected`, "\u00a0\\x3a:error::nbsp", "\u3000\\x3a:error::wide",
			`legacy \x23#[error]old`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s text must keep the component name with only the command marker escaped (%q):\n%s",
					command, want, out)
			}
		}
	}
}

// TestNeutralizeWorkflowCommand는 러너가 읽는 두 명령 형식만 바꾸고 나머지 줄은 바이트 그대로 두는지
// 본다 — 앞 공백류 뒤의 `::`(유니코드 공백 포함)와 줄 어디에든 있는 `##[`.
func TestNeutralizeWorkflowCommand(t *testing.T) {
	for in, want := range map[string]string{
		"::error::x":               `\x3a:error::x`,
		"   ::warning::x":          `   \x3a:warning::x`,
		"\u00a0::error::x":         "\u00a0" + `\x3a:error::x`,
		"\u2003\u3000 ::notice::x": "\u2003\u3000 " + `\x3a:notice::x`,
		"see ##[error]x":           `see \x23#[error]x`,
		"###[a] ##[b]":             `#\x23#[a] \x23#[b]`,
		"::a ##[b]":                `\x3a:a \x23#[b]`,
		"a::b":                     "a::b",
		"x ::y":                    "x ::y",
		":single":                  ":single",
		"## [x] #[y]":              "## [x] #[y]",
		`C:\work\m\x.go`:           `C:\work\m\x.go`,
		"cycle: [m.A m.B]":         "cycle: [m.A m.B]",
		"":                         "",
	} {
		if got := neutralizeWorkflowCommand(in); got != want {
			t.Errorf("neutralizeWorkflowCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

// controlCharDocuments는 정점 ID·limitation에 제어 문자를 담은 저장 문서 두 개(old·new)를
// 쓰고 경로를 돌려준다 — --graph 문서는 분석 대상이 고를 수 있는 입력이다. 제어 문자가 없어도
// 워크플로 명령으로 읽히는 ID(앞 NBSP 뒤 `::` — 설정 없는 metrics에서 줄 첫머리에 온다 — 와 옛
// 형식 `##[`)도 담는다.
func controlCharDocuments(t *testing.T) (string, string) {
	t.Helper()
	const (
		pkg    = "example.com/m"
		evil   = "example.com/m/x\n::error::pkg"
		lead   = "\u00a0::error::lead"
		legacy = "example.com/m/y##[error]legacy"
		a      = "example.com/m.A\x1b[2J"
		b      = "example.com/m.B\u2028::error::b"
	)
	newDoc := &graph.Document{Version: graph.Version, Tool: graph.Tool, Level: graph.LevelSymbol,
		Module: pkg, Limitations: []string{"note\n::warning::lim \x1b[31m"},
		Vertices: []graph.Vertex{
			{ID: pkg, Kind: graph.KindPackage}, {ID: evil, Kind: graph.KindPackage},
			{ID: lead, Kind: graph.KindPackage}, {ID: legacy, Kind: graph.KindPackage},
			{ID: a, Kind: graph.KindFunc, Exported: true}, {ID: b, Kind: graph.KindFunc, Exported: true},
		},
		Edges: []graph.Edge{
			{From: evil, To: pkg, Kind: graph.EdgeImport},
			{From: a, To: b, Kind: graph.EdgeCall}, {From: b, To: a, Kind: graph.EdgeCall},
		}}
	oldDoc := &graph.Document{Version: graph.Version, Tool: graph.Tool, Level: graph.LevelSymbol,
		Module: pkg, Limitations: []string{"old\n::notice::lim"},
		Vertices: []graph.Vertex{{ID: pkg, Kind: graph.KindPackage}, {ID: a, Kind: graph.KindFunc, Exported: true}}}
	tmp := t.TempDir()
	oldPath, newPath := filepath.Join(tmp, "old.json"), filepath.Join(tmp, "new.json")
	for path, doc := range map[string]*graph.Document{oldPath: oldDoc, newPath: newDoc} {
		if err := export.SaveFile(doc, path); err != nil {
			t.Fatal(err)
		}
	}
	return oldPath, newPath
}

// TestTextOutputsEscapeDocumentStrings는 --graph 문서의 ID·limitation에 든 제어 문자가
// cycles·dead·explain·diff·metrics·mapping 텍스트 출력에서 이스케이프되는지 본다.
func TestTextOutputsEscapeDocumentStrings(t *testing.T) {
	oldPath, newPath := controlCharDocuments(t)
	empty := t.TempDir()
	cfg := filepath.Join(empty, "rules.yml")
	if err := os.WriteFile(cfg, []byte("components:\n  other: [\"nothing\"]\ndeps: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"cycles", "--graph", newPath, "--level", "symbol"},
		{"dead", "--graph", newPath},
		{"dead", "--graph", newPath, "--root", "example.com/m.A\x1b[2J", "--explain", "example.com/m.B\u2028::error::b"},
		{"dead", "--graph", newPath, "--explain", "example.com/m.A\x1b[2J"},
		{"diff", oldPath, newPath},
		{"metrics", "--graph", newPath, "--dir", empty},
		{"mapping", "--graph", newPath, "--config", cfg},
	} {
		code, out, errb := run(t, args...)
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, errb)
		}
		assertTextSafe(t, args[0], out)
		if !strings.Contains(out, `\x0a::`) && !strings.Contains(out, `\x1b[2J`) {
			t.Errorf("%v: text output must keep the escaped strings:\n%s", args, out)
		}
		// 설정 없는 metrics는 패키지 ID가 줄 첫머리다 — 앞 공백류 뒤 `::`가 그대로면 명령 줄이다.
		if args[0] == "metrics" && !strings.Contains(out, "\u00a0\\x3a:error::lead: 1 pkgs") {
			t.Errorf("%v: a package ID line must not start a workflow command after leading whitespace:\n%s", args, out)
		}
	}
}

// TestTextSafe는 이스케이프 규칙을 직접 확인한다 — 제어 문자가 없는 문자열은 같은 값이고
// (일반 출력 바이트 보존), 백슬래시는 그대로다(windows 경로).
func TestTextSafe(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"example.com/m.(T).Run":  "example.com/m.(T).Run",
		`C:\work\m\x.go`:         `C:\work\m\x.go`,
		"한글/경로.go":               "한글/경로.go",
		"a\nb\tc\rd":             `a\x0ab\x09c\x0dd`,
		"\x00\x1b[2J\x7f":        `\x00\x1b[2J\x7f`,
		"c1\u0085\u009b":         `c1\u0085\u009b`,
		"ls\u2028ps\u2029":       `ls\u2028ps\u2029`,
		"bad\xffutf8\xc3":        `bad\xffutf8\xc3`,
		"mixed 한\x1b글 \u2028end": `mixed 한\x1b글 \u2028end`,
	} {
		if got := textSafe(in); got != want {
			t.Errorf("textSafe(%q) = %q, want %q", in, got, want)
		}
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

// TestRulesRejectsUnmatchablePattern은 MatchPath가 적힌 그대로 맞출 수 없는 패턴이 든 설정을
// 규칙 검사와 수확이 모두 2로 거부하는지 종단으로 확인한다. 받아 주던 때는 "app/**/*.go"
// fileRules가 영원히 맞지 않아 프로덕션 import를 두고도 rules --strict가 0으로 통과했고,
// "app/su?" exclude는 아무것도 빼지 않았다.
func TestRulesRejectsUnmatchablePattern(t *testing.T) {
	module := func(from, exclude string) string {
		return testutil.WriteModule(t, map[string]string{
			"app/app.go":         "package app\n\nimport _ \"example.com/fixture/testkit\"\n",
			"app/sub/sub.go":     "package sub\n\nimport _ \"example.com/fixture/testkit\"\n",
			"testkit/testkit.go": "package testkit\n",
			".gartograph.yml": `components:
  app: ["app/**"]
  testkit: ["testkit"]
deps:
  app: ["testkit"]
fileRules:
  - name: no-testkit
    from: "` + from + `"
    to: testkit
exclude: ["` + exclude + `"]
`,
		})
	}
	// 대조: 지원 문법이면 같은 규칙이 실제로 위반을 잡는다.
	if code, out, errb := run(t, "rules", "--dir", module("app/*.go", "gen/**"), "--strict"); code != 1 ||
		!strings.Contains(out, "violation[fileScope:no-testkit]") {
		t.Fatalf("a supported pattern must catch the import: %d %s %s", code, out, errb)
	}
	for _, c := range []struct{ from, exclude, want string }{
		{"app/**/*.go", "gen/**", `fileRules "no-testkit": from pattern "app/**/*.go"`},
		{"app/*.go", "app/su?", `exclude: entry 0 "app/su?"`},
	} {
		dir := module(c.from, c.exclude)
		for _, args := range [][]string{{"rules", "--dir", dir, "--strict"}, {"graph", "--dir", dir}} {
			code, out, errb := run(t, args...)
			if code != 2 || !strings.Contains(errb, c.want) || !strings.Contains(errb, "supported patterns") {
				t.Fatalf("%s: an unmatchable pattern must fail with exit 2 naming it: %d %s %s",
					args[0], code, out, errb)
			}
		}
	}
}

// TestRulesFileScopeBaselineAcrossCheckouts는 한 체크아웃에서 쓴 fileScope baseline이
// 같은 경로와 다른 경로로 복사한 저장소 모두에서 맞는지 종단으로 확인한다 — README대로
// baseline을 커밋하면 CI는 다른 절대 경로에서 수확한다. baselined 항목은 지금 체크아웃의
// 지점을 가리켜야 한다(표시 위치는 바뀌지 않는다).
func TestRulesFileScopeBaselineAcrossCheckouts(t *testing.T) {
	first := testutil.WriteModule(t, map[string]string{
		"web/web.go":    "package web\n\nimport _ \"example.com/fixture/testhelp\"\n",
		"testhelp/t.go": "package testhelp\n",
		".gartograph.yml": `components:
  web: ["web"]
  testhelp: ["testhelp"]
deps:
  web: ["testhelp"]
fileRules:
  - name: no-testdeps-in-prod
    from: "!*_test.go"
    to: testhelp
`,
	})
	moved := filepath.Join(t.TempDir(), "checkout")
	if err := os.CopyFS(moved, os.DirFS(first)); err != nil {
		t.Fatalf("copying the fixture to a second checkout: %v", err)
	}
	base := filepath.Join(t.TempDir(), "baseline.json")
	if code, _, errb := run(t, "rules", "--dir", first, "--write-baseline", base); code != 0 {
		t.Fatalf("--write-baseline failed: %d %s", code, errb)
	}
	// 임시 디렉터리가 심링크 아래(macOS의 /var)일 수 있어 양쪽을 실제 경로로 비교한다.
	realPath := func(p string) string {
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatalf("resolving %s: %v", p, err)
		}
		return resolved
	}
	for _, dir := range []string{first, moved} {
		code, out, errb := run(t, "rules", "--dir", dir,
			"--baseline", base, "--strict", "--format", "json")
		if code != 0 {
			t.Fatalf("%s: baselined fileScope violation must not fail strict: %d %s %s",
				dir, code, out, errb)
		}
		var rep struct {
			Violations []json.RawMessage `json:"violations"`
			Baselined  []struct {
				Rule     string `json:"rule"`
				Position struct {
					File string `json:"file"`
				} `json:"position"`
			} `json:"baselined"`
			StaleBaseline []json.RawMessage `json:"staleBaseline"`
		}
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if len(rep.Violations) != 0 || len(rep.Baselined) != 1 || len(rep.StaleBaseline) != 0 {
			t.Fatalf("%s: expected 0 fresh + 1 baselined + 0 stale: %s", dir, out)
		}
		if rep.Baselined[0].Rule != "fileScope" || rep.Baselined[0].Position.File == "" ||
			realPath(rep.Baselined[0].Position.File) != realPath(filepath.Join(dir, "web", "web.go")) {
			t.Fatalf("%s: baselined entry must point at this checkout's web.go: %s", dir, out)
		}
	}
}

// portableFixture는 fileScope 위반(web/web.go), unreachable 심볼(lib.unusedHelper), 심볼 순환
// (main.A↔main.B)이 함께 있는 모듈을 만든다 — baseline과 SARIF가 위치를 어떻게 적는지
// rules·dead·cycles에서 한 번에 보려는 fixture다.
func portableFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func A() { B() }
func B() { A() }

func main() { lib.Run(); A() }
`,
		"lib/lib.go": `package lib

func Run() {}
func unusedHelper() {}
`,
		"web/web.go":    "package web\n\nimport _ \"example.com/fixture/testhelp\"\n",
		"testhelp/t.go": "package testhelp\n",
		".gartograph.yml": `components:
  web: ["web"]
  testhelp: ["testhelp"]
deps:
  web: ["testhelp"]
fileRules:
  - name: no-testdeps-in-prod
    from: "!*_test.go"
    to: testhelp
`,
	})
}

// resolvedPath는 심링크를 푼 경로를 돌려준다 — macOS 임시 디렉터리(/var → /private/var)에서
// 수확한 위치와 테스트가 만든 경로를 같은 형태로 견주기 위해서다.
func resolvedPath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("resolving %s: %v", p, err)
	}
	return resolved
}

// assertNoCheckoutPath는 산출물에 dir의 절대 경로가 없는지 본다. 적힌 경로와 심링크를 푼 경로를,
// JSON 문자열로 이스케이프한 형태(윈도 역슬래시)까지 함께 찾는다.
func assertNoCheckoutPath(t *testing.T, what string, data []byte, dir string) {
	t.Helper()
	for _, p := range []string{dir, resolvedPath(t, dir)} {
		quoted, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("quoting %s: %v", p, err)
		}
		if bytes.Contains(data, []byte(p)) || bytes.Contains(data, quoted[1:len(quoted)-1]) {
			t.Fatalf("%s must not carry the checkout path %s:\n%s", what, p, data)
		}
	}
}

// TestBaselinesWriteModuleRelativePositions는 rules·dead --write-baseline이 위치 파일을 모듈 루트
// 기준 '/' 구분 상대 경로로 쓰는지 확인한다 — 절대 경로를 쓰면 커밋한 baseline에 수확한 머신의
// 사용자 이름과 디렉터리 구조가 남는다. 다른 경로로 복사한 체크아웃에서도 같은 baseline이 맞아야
// 하고, 보고서의 위치(같은 실행의 위반·unreachable, 다음 실행의 baselined)는 지금처럼 그
// 체크아웃의 절대 경로다.
func TestBaselinesWriteModuleRelativePositions(t *testing.T) {
	first := portableFixture(t)
	moved := filepath.Join(t.TempDir(), "checkout")
	if err := os.CopyFS(moved, os.DirFS(first)); err != nil {
		t.Fatalf("copying the fixture to a second checkout: %v", err)
	}
	for _, c := range []struct{ cmd, file string }{
		{"rules", "web/web.go"},
		{"dead", "lib/lib.go"},
	} {
		path := filepath.Join(t.TempDir(), c.cmd+"-baseline.json")
		code, out, errb := run(t, c.cmd, "--dir", first, "--write-baseline", path, "--format", "json")
		if code != 0 {
			t.Fatalf("%s --write-baseline failed: %d %s", c.cmd, code, errb)
		}
		var report struct {
			Violations []struct {
				Position *graph.Position `json:"position"`
			} `json:"violations"`
			Unreachable []struct {
				Position *graph.Position `json:"position"`
			} `json:"unreachable"`
		}
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		// baseline에 쓸 사본만 상대 경로가 된다 — 같은 실행의 보고서는 바뀌지 않는다.
		reported := append(report.Violations, report.Unreachable...)
		if len(reported) != 1 || reported[0].Position == nil ||
			resolvedPath(t, reported[0].Position.File) != resolvedPath(t, filepath.Join(first, filepath.FromSlash(c.file))) {
			t.Fatalf("%s: writing the baseline must leave the report's absolute position alone: %s", c.cmd, out)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the %s baseline: %v", c.cmd, err)
		}
		assertNoCheckoutPath(t, c.cmd+" baseline", data, first)
		var written struct {
			Violations []struct {
				Position *graph.Position `json:"position"`
			} `json:"violations"`
			Findings []struct {
				Position *graph.Position `json:"position"`
			} `json:"findings"`
		}
		if err := json.Unmarshal(data, &written); err != nil {
			t.Fatalf("baseline is not JSON: %v\n%s", err, data)
		}
		entries := append(written.Violations, written.Findings...)
		if len(entries) != 1 || entries[0].Position == nil || entries[0].Position.File != c.file {
			t.Fatalf("%s baseline must record %s relative to the module root:\n%s", c.cmd, c.file, data)
		}
		for _, dir := range []string{first, moved} {
			code, out, errb := run(t, c.cmd, "--dir", dir,
				"--baseline", path, "--strict", "--format", "json")
			if code != 0 {
				t.Fatalf("%s in %s: a baselined entry must not fail --strict: %d %s %s",
					c.cmd, dir, code, out, errb)
			}
			var rep struct {
				Violations  []json.RawMessage `json:"violations"`
				Unreachable []json.RawMessage `json:"unreachable"`
				Baselined   []struct {
					Position *graph.Position `json:"position"`
				} `json:"baselined"`
				Stale []json.RawMessage `json:"staleBaseline"`
			}
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out)
			}
			if len(rep.Violations)+len(rep.Unreachable) != 0 || len(rep.Baselined) != 1 || len(rep.Stale) != 0 {
				t.Fatalf("%s in %s: expected 0 fresh + 1 baselined + 0 stale: %s", c.cmd, dir, out)
			}
			// 보고서의 위치는 바꾸지 않는다 — 이 체크아웃의 절대 경로다.
			if p := rep.Baselined[0].Position; p == nil ||
				resolvedPath(t, p.File) != resolvedPath(t, filepath.Join(dir, filepath.FromSlash(c.file))) {
				t.Fatalf("%s in %s: baselined entry must point at this checkout's %s: %s",
					c.cmd, dir, c.file, out)
			}
		}
	}
}

// TestBaselinePositionsUseModuleRoot는 --dir이 모듈의 하위 디렉터리여도 baseline 위치와 SARIF URI가
// go.mod 디렉터리 기준인지 rules·dead·cycles에서 확인한다 — 기준이 --dir을 따르면 같은 파일이
// 실행마다 다른 경로로 적힌다.
func TestBaselinePositionsUseModuleRoot(t *testing.T) {
	dir := portableFixture(t)
	cycleDir := cycleFixture(t)
	for _, c := range []struct {
		args     []string
		file     string
		baseline bool // cycles baseline에는 위치가 없다
	}{
		{[]string{"dead", "--dir", filepath.Join(dir, "lib")}, "lib/lib.go", true},
		{[]string{"rules", "--dir", filepath.Join(dir, "web"),
			"--config", filepath.Join(dir, ".gartograph.yml")}, "web/web.go", true},
		{[]string{"cycles", "--dir", filepath.Join(cycleDir, "a"), "--level", "symbol"}, "a/a.go", false},
	} {
		path := filepath.Join(t.TempDir(), "baseline.json")
		args := append(slices.Clone(c.args), "--format", "sarif")
		if c.baseline {
			args = append(args, "--write-baseline", path)
		}
		code, out, errb := run(t, args...)
		if code != 0 {
			t.Fatalf("%v failed: %d %s", args, code, errb)
		}
		results := parseSARIFReport(t, out).Runs[0].Results
		if len(results) == 0 {
			t.Fatalf("%s: expected results: %s", c.args[0], out)
		}
		for _, r := range results {
			if phys := r.Locations[0].PhysicalLocation; phys == nil || phys.ArtifactLocation["uri"] != c.file {
				t.Fatalf("%s: SARIF URIs must be relative to the go.mod directory, not --dir: %s", c.args[0], out)
			}
		}
		if !c.baseline {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var written struct {
			Violations []struct {
				Position *graph.Position `json:"position"`
			} `json:"violations"`
			Findings []struct {
				Position *graph.Position `json:"position"`
			} `json:"findings"`
		}
		if err := json.Unmarshal(data, &written); err != nil {
			t.Fatalf("baseline is not JSON: %v\n%s", err, data)
		}
		entries := append(written.Violations, written.Findings...)
		if len(entries) == 0 {
			t.Fatalf("%s: the harvest under --dir must still report entries: %s", c.args[0], data)
		}
		for _, e := range entries {
			if e.Position == nil || e.Position.File != c.file {
				t.Fatalf("%s: baseline positions must be relative to the go.mod directory, not --dir: %s",
					c.args[0], data)
			}
		}
	}
}

// TestBaselineMatchesAbsolutePathEntries는 예전 판이 절대 경로로 쓴 baseline이 계속 맞는지 확인한다 —
// 짝짓기 키는 경로를 보지 않으므로(fileScope는 규칙·import 간선·파일 이름) 다른 체크아웃의 절대
// 경로로 적힌 항목도 지금 위반과 같은 항목이다. 더 이상 나오지 않는 항목은 baseline에 적힌 그대로
// staleBaseline으로 돌아온다 — 읽을 때 경로를 고쳐 쓰지 않는다.
func TestBaselineMatchesAbsolutePathEntries(t *testing.T) {
	dir := portableFixture(t)
	elsewhere := filepath.Join(t.TempDir(), "old-checkout")
	pos := func(parts ...string) map[string]any {
		return map[string]any{"file": filepath.Join(append([]string{elsewhere}, parts...)...),
			"line": 3, "column": 8}
	}
	write := func(body map[string]any) string {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encoding the baseline: %v", err)
		}
		path := filepath.Join(t.TempDir(), "baseline.json")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	gone := pos("web", "gone.go")
	rulesBase := write(map[string]any{
		"tool": "gartograph", "kind": "violations-baseline", "version": 1,
		"violations": []map[string]any{
			{"from": "example.com/fixture/web", "to": "example.com/fixture/testhelp",
				"fromComponent": "web", "toComponent": "testhelp", "kind": "import",
				"rule": "fileScope", "name": "no-testdeps-in-prod", "position": pos("web", "web.go")},
			{"from": "example.com/fixture/web", "to": "example.com/fixture/lib",
				"fromComponent": "web", "toComponent": "lib", "kind": "import",
				"rule": "allow", "position": gone},
		},
	})
	code, out, errb := run(t, "rules", "--dir", dir,
		"--baseline", rulesBase, "--strict", "--format", "json")
	if code != 0 {
		t.Fatalf("an absolute-path baseline must still match: %d %s %s", code, out, errb)
	}
	var rules struct {
		Violations []json.RawMessage `json:"violations"`
		Baselined  []json.RawMessage `json:"baselined"`
		Stale      []struct {
			Position *graph.Position `json:"position"`
		} `json:"staleBaseline"`
	}
	if err := json.Unmarshal([]byte(out), &rules); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rules.Violations) != 0 || len(rules.Baselined) != 1 || len(rules.Stale) != 1 ||
		rules.Stale[0].Position == nil || rules.Stale[0].Position.File != gone["file"] {
		t.Fatalf("expected 0 fresh + 1 baselined + the stale entry exactly as stored: %s", out)
	}

	deadBase := write(map[string]any{
		"tool": "gartograph", "kind": "dead-baseline", "version": 1,
		"findings": []map[string]any{{
			"id": "example.com/fixture/lib.unusedHelper", "kind": "func",
			"package": "example.com/fixture/lib", "position": pos("lib", "lib.go"),
			"exported": false, "state": "unreachable", "reason": "not reachable from retention roots",
		}},
	})
	code, out, errb = run(t, "dead", "--dir", dir,
		"--baseline", deadBase, "--strict", "--format", "json")
	if code != 0 {
		t.Fatalf("an absolute-path dead baseline must still match: %d %s %s", code, out, errb)
	}
	var dead struct {
		Unreachable []json.RawMessage `json:"unreachable"`
		Baselined   []json.RawMessage `json:"baselined"`
	}
	if err := json.Unmarshal([]byte(out), &dead); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(dead.Unreachable) != 0 || len(dead.Baselined) != 1 {
		t.Fatalf("expected 0 fresh + 1 baselined: %s", out)
	}
}

// sarifReportResult는 SARIF 결과 하나에서 위치 테스트들이 보는 부분이다. artifactLocation은
// 키 집합까지 견주려고 맵으로 읽는다.
type sarifReportResult struct {
	Message struct {
		Text string `json:"text"`
	} `json:"message"`
	Locations []struct {
		PhysicalLocation *struct {
			ArtifactLocation map[string]any `json:"artifactLocation"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}

// sarifReport는 SARIF 로그에서 위치 테스트들이 보는 부분이다. originalUriBaseIds 항목도
// 키 집합까지 견주려고 맵으로 읽는다.
type sarifReport struct {
	Runs []struct {
		OriginalURIBaseIDs map[string]map[string]any `json:"originalUriBaseIds"`
		Results            []sarifReportResult       `json:"results"`
	} `json:"runs"`
}

// parseSARIFReport는 SARIF 출력을 읽고 실행(run)이 하나인지 확인한다.
func parseSARIFReport(t *testing.T, out string) sarifReport {
	t.Helper()
	var log sarifReport
	if err := json.Unmarshal([]byte(out), &log); err != nil {
		t.Fatalf("not SARIF JSON: %v\n%s", err, out)
	}
	if len(log.Runs) != 1 {
		t.Fatalf("expected one SARIF run: %s", out)
	}
	return log
}

// wantOmittedNote는 모듈 루트 밖 파일의 결과 메시지 끝에 붙어야 하는 문구다 — 구현 상수와 따로 적어 문구 회귀를 잡는다.
const wantOmittedNote = " (location omitted: the file is not under the module root)"

// TestSARIFLocationsAreModuleRelative는 rules·cycles·dead SARIF의 artifactLocation이 모듈 루트 기준
// 상대 URI와 uriBaseId %SRCROOT%를 쓰고, run.originalUriBaseIds가 %SRCROOT%를 절대 경로 없이
// 설명하며, 로그 어디에도 체크아웃 경로가 없는지 확인한다 — 업로드한 SARIF에 수확한 머신의
// 경로가 남지 않아야 한다.
func TestSARIFLocationsAreModuleRelative(t *testing.T) {
	dir := portableFixture(t)
	for _, c := range []struct {
		args []string
		uri  string
	}{
		{[]string{"rules"}, "web/web.go"},
		{[]string{"cycles", "--level", "symbol"}, "main.go"},
		{[]string{"dead"}, "lib/lib.go"},
	} {
		args := append(slices.Clone(c.args), "--dir", dir, "--format", "sarif")
		code, out, errb := run(t, args...)
		if code != 0 {
			t.Fatalf("%v failed: %d %s", args, code, errb)
		}
		assertNoCheckoutPath(t, c.args[0]+" SARIF", []byte(out), dir)
		run0 := parseSARIFReport(t, out).Runs[0]
		if len(run0.Results) != 1 || len(run0.Results[0].Locations) != 1 {
			t.Fatalf("%s: expected one result with one location: %s", c.args[0], out)
		}
		want := map[string]any{"uri": c.uri, "uriBaseId": "%SRCROOT%"}
		if phys := run0.Results[0].Locations[0].PhysicalLocation; phys == nil ||
			!reflect.DeepEqual(phys.ArtifactLocation, want) {
			t.Fatalf("%s: artifactLocation must be %v: %s", c.args[0], want, out)
		}
		srcRoot, ok := run0.OriginalURIBaseIDs["%SRCROOT%"]
		if _, hasURI := srcRoot["uri"]; !ok || hasURI || srcRoot["description"] == nil {
			t.Fatalf("%s: originalUriBaseIds must describe %%SRCROOT%% without an absolute uri: %s",
				c.args[0], out)
		}
	}
}

// saveDoc은 문서를 임시 파일로 저장하고 그 경로를 돌려준다 — --graph로 실제 CLI 경로를 돌리려는 것이다.
func saveDoc(t *testing.T, doc *graph.Document) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graph.json")
	if err := export.SaveFile(doc, path); err != nil {
		t.Fatalf("saving the graph document: %v", err)
	}
	return path
}

// TestSavedDocumentPositions는 저장 문서(--graph)의 위치를 dead SARIF·baseline이 어떻게 적는지 본다.
//   - 모듈 루트 안 파일: SARIF URI는 경로 세그먼트마다 RFC 3986으로 퍼센트 인코딩한다. 공백·#·%·비ASCII·
//     콜론은 인코딩하고 sub-delims와 @는 그대로 둔다(첫 세그먼트의 콜론은 scheme으로 읽힌다). baseline은
//     인코딩하지 않은 파일 경로다. 상대 위치는 이미 모듈 루트 기준으로 본다.
//   - 모듈 루트 밖 파일(".."로 벗어나는 상대 위치 포함): SARIF는 physicalLocation 없이 메시지로 알리고,
//     baseline은 파일 이름만 남긴다 — "../" 경로는 머신마다 다른 위치를 담는다.
//   - 모듈 루트를 싣지 않은 문서의 절대 위치와, 윈도에서 수확한 문서를 다른 OS에서 읽은 위치("C:\…"·
//     "\\server\…"를 이 OS는 절대 경로로 알아보지 못한다)도 상대 형태가 없는 위치로 다룬다.
//
// 어느 경우든 그 머신의 경로가 산출물에 옮겨지면 안 된다.
func TestSavedDocumentPositions(t *testing.T) {
	work := t.TempDir()
	modRoot := filepath.Join(work, "mod root")
	winRoot := `C:\Users\someone\repo`
	type symbol struct{ name, file, uri, baseline string } // uri ""는 physicalLocation 생략
	for _, g := range []struct {
		name, root string
		symbols    []symbol
	}{
		{"module root", modRoot, []symbol{
			{"Space", filepath.Join(modRoot, "a dir", "x#1%.go"), "a%20dir/x%231%25.go", "a dir/x#1%.go"},
			{"Hangul", filepath.Join(modRoot, "한글", "파일.go"),
				"%ED%95%9C%EA%B8%80/%ED%8C%8C%EC%9D%BC.go", "한글/파일.go"},
			{"Delims", filepath.Join(modRoot, "x+y(1)@v:2.go"), "x+y(1)@v%3A2.go", "x+y(1)@v:2.go"},
			{"Relative", "rel/r.go", "rel/r.go", "rel/r.go"},
			{"Outside", filepath.Join(work, "other", "o.go"), "", "o.go"},
			{"Escaping", "../up/e.go", "", "e.go"},
		}},
		{"no module root", "", []symbol{
			{"Abs", filepath.Join(work, "elsewhere", "z.go"), "", "z.go"},
			{"Rel", "a/g.go", "a/g.go", "a/g.go"},
		}},
		{"windows document", winRoot, []symbol{
			{"Drive", winRoot + `\a\drive.go`, "", "drive.go"},
			{"Share", `\\server\share\someone\a\share.go`, "", "share.go"},
		}},
	} {
		t.Run(g.name, func(t *testing.T) {
			if g.root == winRoot && runtime.GOOS == "windows" {
				t.Skip("on windows these are ordinary absolute paths under the module root")
			}
			doc := &graph.Document{Version: graph.Version, Tool: graph.Tool, Level: graph.LevelSymbol,
				Root: g.root, Module: "m", ModuleDir: g.root,
				Vertices: []graph.Vertex{{ID: "m/a", Kind: graph.KindPackage, Name: "a"}}}
			for _, s := range g.symbols {
				doc.Vertices = append(doc.Vertices, graph.Vertex{ID: "m/a." + s.name, Kind: graph.KindFunc,
					Name: s.name, Package: "m/a", Position: &graph.Position{File: s.file, Line: 3, Column: 6}})
			}
			graphPath := saveDoc(t, doc)
			assertNoMachinePath := func(what string, data []byte) {
				t.Helper()
				assertNoCheckoutPath(t, what, data, work)
				if bytes.Contains(data, []byte("someone")) {
					t.Fatalf("%s must not carry the windows machine's path:\n%s", what, data)
				}
			}

			code, out, errb := run(t, "dead", "--graph", graphPath, "--format", "sarif")
			if code != 0 {
				t.Fatalf("dead sarif failed: %d %s", code, errb)
			}
			assertNoMachinePath("dead SARIF", []byte(out))
			results := parseSARIFReport(t, out).Runs[0].Results
			if len(results) != len(g.symbols) {
				t.Fatalf("expected %d results: %s", len(g.symbols), out)
			}
			for _, s := range g.symbols {
				i := slices.IndexFunc(results, func(r sarifReportResult) bool {
					return strings.Contains(r.Message.Text, " m/a."+s.name+":")
				})
				if i < 0 || len(results[i].Locations) != 1 {
					t.Fatalf("%s: expected a result with one location: %s", s.name, out)
				}
				phys := results[i].Locations[0].PhysicalLocation
				if s.uri == "" {
					if phys != nil || !strings.HasSuffix(results[i].Message.Text, wantOmittedNote) {
						t.Fatalf("%s: a file not under the module root must have no physicalLocation and say so: %s",
							s.name, out)
					}
					continue
				}
				want := map[string]any{"uri": s.uri, "uriBaseId": "%SRCROOT%"}
				if phys == nil || !reflect.DeepEqual(phys.ArtifactLocation, want) ||
					strings.Contains(results[i].Message.Text, "location omitted") {
					t.Fatalf("%s: artifactLocation must be %v: %s", s.name, want, out)
				}
			}

			basePath := filepath.Join(t.TempDir(), "dead-baseline.json")
			if code, _, errb := run(t, "dead", "--graph", graphPath, "--write-baseline", basePath); code != 0 {
				t.Fatalf("dead --write-baseline failed: %d %s", code, errb)
			}
			data, err := os.ReadFile(basePath)
			if err != nil {
				t.Fatal(err)
			}
			assertNoMachinePath("dead baseline", data)
			base, err := loadDeadBaseline(basePath)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range g.symbols {
				i := slices.IndexFunc(base.Findings, func(f analysis.Finding) bool { return f.ID == "m/a."+s.name })
				if i < 0 || base.Findings[i].Position == nil || base.Findings[i].Position.File != s.baseline {
					t.Fatalf("%s: baseline position must be %q: %s", s.name, s.baseline, data)
				}
			}
		})
	}
}

// TestSARIFOmitsOutsideFilesForRulesAndCycles는 rules·cycles SARIF도 모듈 루트 밖 위치(예: 다른
// go.work 모듈의 파일)를 physicalLocation 없이 메시지 문구로 알리는지 확인한다 — 세 명령이 같은
// 규칙을 따라야 한다.
func TestSARIFOmitsOutsideFilesForRulesAndCycles(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "mod")
	elsewhere := func(name string) []graph.Position {
		return []graph.Position{{File: filepath.Join(work, "sibling", name), Line: 3, Column: 8}}
	}
	graphPath := saveDoc(t, &graph.Document{Version: graph.Version, Tool: graph.Tool,
		Level: graph.LevelPackage, Root: root, Module: "m", ModuleDir: root,
		Vertices: []graph.Vertex{
			{ID: "m/db", Kind: graph.KindPackage, Name: "db"},
			{ID: "m/web", Kind: graph.KindPackage, Name: "web"},
		},
		Edges: []graph.Edge{
			{From: "m/db", To: "m/web", Kind: graph.EdgeImport, Positions: elsewhere("db.go")},
			{From: "m/web", To: "m/db", Kind: graph.EdgeImport, Positions: elsewhere("web.go")},
		}})
	cfg := filepath.Join(t.TempDir(), "rules.yml")
	if err := os.WriteFile(cfg, []byte("components:\n  web: [web]\n  db: [db]\ndeps: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"cycles", "--graph", graphPath, "--format", "sarif"},
		{"rules", "--graph", graphPath, "--config", cfg, "--format", "sarif"},
	} {
		code, out, errb := run(t, args...)
		if code != 0 {
			t.Fatalf("%v failed: %d %s", args, code, errb)
		}
		assertNoCheckoutPath(t, args[0]+" SARIF", []byte(out), work)
		run0 := parseSARIFReport(t, out).Runs[0]
		results := run0.Results
		if len(results) == 0 {
			t.Fatalf("%s: expected results: %s", args[0], out)
		}
		// %SRCROOT%를 쓰는 결과가 없으면 그 설명도 싣지 않는다 — 위치 없는 로그는 예전 바이트 그대로다.
		if run0.OriginalURIBaseIDs != nil {
			t.Fatalf("%s: a log with no %%SRCROOT%% location must not describe it: %s", args[0], out)
		}
		for _, r := range results {
			if len(r.Locations) != 1 || r.Locations[0].PhysicalLocation != nil ||
				!strings.HasSuffix(r.Message.Text, wantOmittedNote) {
				t.Fatalf("%s: a file outside the module root must have no physicalLocation and say so: %s",
					args[0], out)
			}
		}
	}
}

// TestPositionsOutsideModuleRootFromHarvest는 실제 수확이 모듈 밖 위치를 내는 흔한 경우를 본다 —
// 표준 라이브러리 struct를 밑 타입으로 쓰는 정의 타입(type stamp time.Time)의 필드 정점은 GOROOT의
// 필드 선언에 놓인다. baseline은 그 위치를 파일 이름만 남기고 SARIF는 물리 위치 없이 메시지로
// 알린다 — 어느 쪽에도 GOROOT 경로(설치 위치에 따라 사용자 이름이 든다)가 남지 않아야 한다.
func TestPositionsOutsideModuleRootFromHarvest(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport \"time\"\n\ntype stamp time.Time\n\nfunc main() { _ = stamp{} }\n",
	})
	const stampFields = "example.com/fixture.(stamp)."
	basePath := filepath.Join(t.TempDir(), "dead-baseline.json")
	if code, _, errb := run(t, "dead", "--dir", dir, "--write-baseline", basePath); code != 0 {
		t.Fatalf("dead --write-baseline failed: %d %s", code, errb)
	}
	data, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	base, err := loadDeadBaseline(basePath)
	if err != nil {
		t.Fatal(err)
	}
	fields := 0
	for _, f := range base.Findings {
		if !strings.HasPrefix(f.ID, stampFields) {
			continue
		}
		fields++
		if f.Position == nil || f.Position.File != "time.go" {
			t.Fatalf("%s: a field declared in GOROOT must keep only its file name: %s", f.ID, data)
		}
	}
	if fields == 0 || bytes.Contains(data, []byte("/src/time/")) {
		t.Fatalf("expected stamp's fields with no GOROOT path in the baseline: %s", data)
	}

	code, out, errb := run(t, "dead", "--dir", dir, "--format", "sarif")
	if code != 0 {
		t.Fatalf("dead sarif failed: %d %s", code, errb)
	}
	if strings.Contains(out, "/src/time/") {
		t.Fatalf("SARIF must not carry the GOROOT path: %s", out)
	}
	seen := 0
	for _, r := range parseSARIFReport(t, out).Runs[0].Results {
		if !strings.Contains(r.Message.Text, " "+stampFields) {
			continue
		}
		seen++
		if r.Locations[0].PhysicalLocation != nil || !strings.HasSuffix(r.Message.Text, wantOmittedNote) {
			t.Fatalf("a field declared in GOROOT must have no physicalLocation and say so: %s", out)
		}
	}
	if seen != fields {
		t.Fatalf("SARIF must report the same %d stamp fields as the baseline: %s", fields, out)
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

// TestDeadRTAWithoutWellTypedPackages는 RTA가 분석할 타입 검사된 패키지가 없을 때(유일한 패키지의
// 타입 오류, 아무것도 고르지 않는 패턴) dead --algo rta와 --explain이 패닉 대신 원인과 해결 방향을
// 담은 분석 오류(exit 2)를 내는지 확인한다. CHA 경로는 그대로 동작한다 — 타입 오류는 limitation이다.
func TestDeadRTAWithoutWellTypedPackages(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nfunc helper() int { return undefinedName }\n\nfunc main() { _ = helper() }\n",
	})
	for _, args := range [][]string{
		{"dead", "--dir", dir, "--algo", "rta"},
		{"dead", "--dir", dir, "--algo", "rta", "--explain", "example.com/fixture.helper"},
		{"dead", "--dir", dir, "--algo", "rta", "--pattern", "./nothing/..."},
	} {
		code, _, errb := runRecovered(t, args...)
		if code != 2 || !strings.Contains(errb, "rta: no well-typed packages to analyze") ||
			!strings.Contains(errb, "without --algo rta") {
			t.Errorf("%v: want exit 2 with the cause and the way out, got %d %q", args[3:], code, errb)
		}
	}
	if code, _, errb := run(t, "dead", "--dir", dir); code != 0 {
		t.Fatalf("CHA dead must still work on an ill-typed package: %d %s", code, errb)
	}
}

// runRecovered는 run과 같되 패닉을 테스트 실패로 바꾼다 — 패닉이 테스트 바이너리를 죽이면 같은
// 실행의 다른 결과까지 잃는다.
func runRecovered(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%v panicked instead of reporting an error: %v", args, r)
		}
	}()
	return run(t, args...)
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

// configScopeFixture는 app이 lib와 gen을 import하는 모듈과, gen을 수확에서 빼고 app→lib를 금지하는 규칙
// 파일(모듈 밖 strict.yml)을 만든다. dirConfig가 비어 있지 않으면 --dir의 .gartograph.yml로 둔다.
func configScopeFixture(t *testing.T, dirConfig string) (dir, strict string) {
	t.Helper()
	files := map[string]string{
		"app/app.go": "package app\n\nimport (\n\t_ \"example.com/fixture/gen\"\n\t_ \"example.com/fixture/lib\"\n)\n",
		"lib/lib.go": "package lib\n",
		"gen/gen.go": "package gen\n",
	}
	if dirConfig != "" {
		files[".gartograph.yml"] = dirConfig
	}
	dir = testutil.WriteModule(t, files)
	strict = filepath.Join(t.TempDir(), "strict.yml")
	writeTestFile(t, strict, "components:\n  app: [app]\n  lib: [lib]\ndeps:\n  app: []\nexclude: [gen]\n", 0o644)
	return dir, strict
}

// TestConfigFlagSetsHarvestScope는 --config FILE을 받는 명령(rules·metrics·mapping·mcp)이 그 파일의 exclude로만
// 수확하고 --dir의 .gartograph.yml을 읽지 않는지 본다(DECISIONS: --config의 exclude). 예전에는 규칙은 --config
// 파일에서, 수확 범위는 .gartograph.yml에서 와서, .gartograph.yml이 app을 빼면 `rules --config strict.yml
// --strict`가 app→lib 위반을 두고도 0으로 통과했고, strict.yml의 exclude는 읽히고도 효과가 없어 gen이 unmapped로
// 섞였다. --config가 없으면 지금처럼 .gartograph.yml이 규칙과 수확 범위를 함께 정한다.
func TestConfigFlagSetsHarvestScope(t *testing.T) {
	const appOnly = "components:\n  app: [app]\n  lib: [lib]\n  gen: [gen]\ndeps:\n  app: [lib, gen]\nexclude: [app]\n"
	cases := []struct {
		name, dirConfig string
		useConfig       bool
		strictCode      int
		components      map[string][]string // mapping의 컴포넌트 → 패키지
		unmatched       []string
	}{
		{"--config alone", "", true, 1,
			map[string][]string{"app": {"example.com/fixture/app"}, "lib": {"example.com/fixture/lib"}}, nil},
		{"--config over a different .gartograph.yml exclude", appOnly, true, 1,
			map[string][]string{"app": {"example.com/fixture/app"}, "lib": {"example.com/fixture/lib"}}, nil},
		{"--config over a broken .gartograph.yml", "components: [\n", true, 1,
			map[string][]string{"app": {"example.com/fixture/app"}, "lib": {"example.com/fixture/lib"}}, nil},
		{"no --config", appOnly, false, 0,
			map[string][]string{"app": {}, "lib": {"example.com/fixture/lib"}, "gen": {"example.com/fixture/gen"}},
			[]string{"app"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, strict := configScopeFixture(t, c.dirConfig)
			flags := []string{"--dir", dir}
			if c.useConfig {
				flags = append(flags, "--config", strict)
			}
			code, out, errb := run(t, append([]string{"rules", "--strict"}, flags...)...)
			if code != c.strictCode {
				t.Fatalf("rules --strict: want exit %d, got %d\n%s%s", c.strictCode, code, out, errb)
			}
			var mapping struct {
				Components          map[string][]string `json:"components"`
				Unmapped            []string            `json:"unmapped"`
				UnmatchedComponents []string            `json:"unmatchedComponents"`
			}
			mappingOut := cliReport(t, append([]string{"mapping", "--format", "json"}, flags...)...)
			if err := json.Unmarshal([]byte(mappingOut), &mapping); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(mapping.Components, c.components) || len(mapping.Unmapped) != 0 ||
				!reflect.DeepEqual(mapping.UnmatchedComponents, c.unmatched) {
				t.Fatalf("mapping must reflect the harvest scope of the rules file in use: %s", mappingOut)
			}
			// metrics도 같은 범위를 센다 — --config(gen 제외)로는 gen이 어디에도 없고 unmapped도 없다.
			metricsOut := cliReport(t, append([]string{"metrics", "--format", "json"}, flags...)...)
			harvestsGen := strings.Contains(metricsOut, `"example.com/fixture/gen"`)
			if harvestsGen == c.useConfig || strings.Contains(metricsOut, `"unmapped"`) {
				t.Fatalf("metrics must count the harvest scope of the rules file in use: %s", metricsOut)
			}
			// MCP는 같은 플래그·같은 수확 레벨에서 CLI와 같은 보고를 낸다(수확 레벨: rules·mapping package, metrics type).
			for _, tool := range []struct{ name, level string }{
				{"rules", "package"}, {"mapping", "package"}, {"metrics", "type"},
			} {
				answers := runMcpTools(t, append([]string{"--level", tool.level}, flags...),
					mcpCall(1, "gartograph_"+tool.name, `{}`))
				requireCLIReport(t, tool.name, answers[0],
					cliReport(t, append([]string{tool.name, "--format", "json"}, flags...)...))
			}
		})
	}
}

// workspaceFixture는 go.work 워크스페이스(모듈 a·b)를 만들고 그 루트를 돌려준다. a/app은 b의 루트 패키지와
// b/util·b/gen을, b/util은 a/lib를 import한다. extra는 더 쓸 파일(루트 기준 경로 → 내용)이고 같은 경로면 기본
// 파일을 덮는다. 루트는 심링크를 푼 경로다 — go가 알리는 모듈 디렉터리와 파일 위치는 심링크를 푼 경로라서,
// --dir 기준 상대 경로(--files)가 같은 파일을 가리키게 한다.
func workspaceFixture(t *testing.T, extra map[string]string) string {
	t.Helper()
	root := resolvedPath(t, t.TempDir())
	files := map[string]string{
		"go.work":  "go 1.27\n\nuse ./a\nuse ./b\n",
		"a/go.mod": "module example.com/a\n\ngo 1.27\n",
		"b/go.mod": "module example.com/b\n\ngo 1.27\n",
		"a/app/app.go": "package app\n\nimport (\n\t\"example.com/b\"\n\t\"example.com/b/gen\"\n\t\"example.com/b/util\"\n)\n\n" +
			"func Run() int { return b.N + util.U() + gen.G() }\n",
		"a/lib/lib.go":   "package lib\n\nfunc L() int { return 1 }\n",
		"b/b.go":         "package b\n\nconst N = 0\n",
		"b/util/util.go": "package util\n\nimport \"example.com/a/lib\"\n\nfunc U() int { return lib.L() }\n",
		"b/gen/gen.go":   "package gen\n\nfunc G() int { return 2 }\n",
	}
	for name, content := range extra {
		files[name] = content
	}
	for name, content := range files {
		writeTestFile(t, filepath.Join(root, name), content, 0o644)
	}
	return root
}

// workspaceConfig는 workspaceFixture의 규칙이다. gen은 수확에서 빼고, app은 아무것도 쓸 수 없으며(app→b와
// app→util이 allow 위반), util 패키지 파일이 lib를 import하면 fileScope 위반이다. 패턴은 모두 패키지·파일이
// 속한 모듈 기준 상대 경로다 — 모듈 루트 패키지(".")·util·gen은 모듈 b, app·lib는 모듈 a에 있다.
const workspaceConfig = `exclude: [gen]
components:
  app: [app]
  util: [util]
  lib: [lib]
  broot: ["."]
deps:
  app: []
  util: [lib]
  lib: []
  broot: []
fileRules:
  - name: util-files-no-lib
    from: "util/*.go"
    to: lib
`

// decodeCLIReport는 종료 코드 0으로 끝난 CLI JSON 보고를 v로 읽는다.
func decodeCLIReport(t *testing.T, v any, args ...string) {
	t.Helper()
	out := cliReport(t, args...)
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("%v: not JSON: %v\n%s", args, err, out)
	}
}

// TestWorkspacePatternsUseEachPackagesModule은 go.work 워크스페이스에서 components·exclude·fileRules 패턴과
// impact --files가 패키지(파일)마다 그것이 속한 모듈 기준 상대 경로로 해석되는지 본다(DECISIONS gartograph
// "go.work의 exclude·components"). 예전에는 exclude만 패키지마다 자기 모듈을 벗기고 컴포넌트·fileRules·
// --files는 문서의 첫 모듈만 기준으로 했다. 그래서 util 컴포넌트가 example.com/b/util에 맞지 않았고(형제 모듈
// 패키지는 외부로 분류됐다) rules --strict가 app→util 위반을 두고 0으로 끝났다. 첫 모듈이 a인 수확(--dir a)과
// b인 수확(워크스페이스 루트에서 ./b/...를 먼저 준 --pattern — 의존이 없는 b 루트 패키지가 먼저 로드된다)이
// 같은 답을 내야 한다. MCP도 같은 보고를 낸다.
func TestWorkspacePatternsUseEachPackagesModule(t *testing.T) {
	root := workspaceFixture(t, map[string]string{
		"a/.gartograph.yml": workspaceConfig,
		".gartograph.yml":   workspaceConfig,
	})
	cases := []struct {
		name          string
		flags         []string
		first         string // 문서 module(수확이 처음 만난 주 모듈)
		siblingFile   string // 첫 모듈이 아닌 모듈의 파일(--dir 기준)
		siblingPkg    string // 그 파일의 패키지
		siblingImport string // 그 패키지를 import하는 다른 모듈의 패키지
	}{
		{"first module a", []string{"--dir", filepath.Join(root, "a")}, "example.com/a",
			"../b/util/util.go", "example.com/b/util", "example.com/a/app"},
		{"first module b", []string{"--dir", root, "--pattern", "./b/...", "--pattern", "./a/..."}, "example.com/b",
			"a/lib/lib.go", "example.com/a/lib", "example.com/b/util"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var doc graph.Document
			decodeCLIReport(t, &doc, append([]string{"graph", "--level", "package"}, c.flags...)...)
			if doc.Module != c.first {
				t.Fatalf("premise: the harvest must start at %s, got %q", c.first, doc.Module)
			}
			var mapping analysis.Mapping
			decodeCLIReport(t, &mapping, append([]string{"mapping", "--format", "json"}, c.flags...)...)
			want := map[string][]string{"app": {"example.com/a/app"}, "lib": {"example.com/a/lib"},
				"util": {"example.com/b/util"}, "broot": {"example.com/b"}}
			if !reflect.DeepEqual(mapping.Components, want) || len(mapping.Unmapped) != 0 ||
				len(mapping.UnmappedExternal) != 0 || len(mapping.UnmatchedComponents) != 0 {
				t.Fatalf("components must match each package's module-relative path: %+v", mapping)
			}
			code, out, errb := run(t, append([]string{"rules", "--strict", "--format", "json"}, c.flags...)...)
			var rep struct {
				Violations  []analysis.Violation `json:"violations"`
				Limitations []string             `json:"limitations"`
			}
			if err := json.Unmarshal([]byte(out), &rep); err != nil || code != 1 {
				t.Fatalf("rules --strict must fail on the sibling-module violations: %d %v\n%s%s", code, err, out, errb)
			}
			var got []string
			for _, v := range rep.Violations {
				got = append(got, v.From+" -> "+v.To+" "+v.Rule+" "+v.Name)
			}
			wantViolations := []string{
				"example.com/a/app -> example.com/b allow ",
				"example.com/a/app -> example.com/b/util allow ",
				"example.com/b/util -> example.com/a/lib fileScope util-files-no-lib",
			}
			if !reflect.DeepEqual(got, wantViolations) ||
				!strings.HasSuffix(filepath.ToSlash(rep.Violations[2].Position.File), "b/util/util.go") {
				t.Fatalf("want %q, got %q (%+v)", wantViolations, got, rep.Violations)
			}
			// exclude [gen]도 같은 기준이다 — 모듈 b의 gen이 수확에서 빠진다.
			if !hasLimitation(rep.Limitations, "1 packages matched exclude patterns", "") ||
				hasLimitation(rep.Limitations, "", "external packages") {
				t.Fatalf("exclude must drop b/gen and no sibling package may count as external: %q", rep.Limitations)
			}
			var affected analysis.Affected
			decodeCLIReport(t, &affected, append([]string{"impact", "--files", c.siblingFile, "--format", "json"},
				c.flags...)...)
			var dependers []string
			for _, d := range affected.Dependers {
				dependers = append(dependers, d.ID)
			}
			if !slices.Contains(affected.Roots, c.siblingPkg) || len(affected.UnmappedFiles) != 0 ||
				!slices.Contains(dependers, c.siblingImport) {
				t.Fatalf("impact --files must resolve a sibling-module file to its package: %+v", affected)
			}
			for _, tool := range []string{"rules", "mapping"} {
				answers := runMcpTools(t, append([]string{"--level", "package"}, c.flags...),
					mcpCall(1, "gartograph_"+tool, `{}`))
				requireCLIReport(t, tool, answers[0],
					cliReport(t, append([]string{tool, "--format", "json"}, c.flags...)...))
			}
		})
	}
}

// TestWorkspaceSavedDocuments는 저장 문서(--graph)의 워크스페이스 해석을 본다. 새 수확을 저장한 문서는 모듈
// 소속 사실이 따라가 수확과 같은 답을 낸다. 그 사실(workspaceModules·정점 module)이 없는 옛 문서는 예전처럼
// 문서 module 하나로 해석한다 — 형제 모듈 패키지는 외부로, util 컴포넌트는 매칭 없음으로 남는다.
func TestWorkspaceSavedDocuments(t *testing.T) {
	root := workspaceFixture(t, map[string]string{"a/.gartograph.yml": workspaceConfig})
	dir := filepath.Join(root, "a")
	saved := filepath.Join(t.TempDir(), "graph.json")
	writeTestFile(t, saved, cliReport(t, "graph", "--dir", dir, "--level", "package"), 0o644)
	if code, out, errb := run(t, "rules", "--dir", dir, "--graph", saved, "--strict"); code != 1 {
		t.Fatalf("a saved workspace document must keep the module facts: %d %s%s", code, out, errb)
	}
	var doc map[string]any
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "workspaceModules")
	for _, v := range doc["vertices"].([]any) {
		delete(v.(map[string]any), "module")
	}
	old, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(t.TempDir(), "old.json")
	writeTestFile(t, oldPath, string(old), 0o644)
	var mapping analysis.Mapping
	decodeCLIReport(t, &mapping, "mapping", "--dir", dir, "--graph", oldPath, "--format", "json")
	if len(mapping.Components["util"]) != 0 ||
		!reflect.DeepEqual(mapping.UnmatchedComponents, []string{"broot", "util"}) ||
		!reflect.DeepEqual(mapping.UnmappedExternal, []string{"example.com/b", "example.com/b/util"}) {
		t.Fatalf("a document without module facts must keep the module-prefix fallback: %+v", mapping)
	}
}

// TestWorkspaceInitScaffoldsSiblingModules는 go.work 워크스페이스의 init이 형제 모듈 패키지에도 컴포넌트를 만들고,
// 두 모듈의 같은 상대 경로(a/util·b/util)는 같은 패턴에 맞으므로 한 컴포넌트로 묶어 첫 rules --strict가
// 통과하는지 본다. 예전에는 형제 모듈 패키지를 외부로 보고 컴포넌트를 만들지 않았다.
func TestWorkspaceInitScaffoldsSiblingModules(t *testing.T) {
	root := workspaceFixture(t, map[string]string{"a/util/util.go": "package util\n\nfunc A() int { return 3 }\n"})
	dir := filepath.Join(root, "a")
	if code, out, errb := run(t, "init", "--dir", dir); code != 0 || !strings.Contains(out, "(5 components)") {
		t.Fatalf("init must scaffold app, lib, util, gen, and b's root package: %d %s%s", code, out, errb)
	}
	cfg, err := config.Load(filepath.Join(dir, ".gartograph.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wantComponents := map[string][]string{"app": {"app/**"}, "gen": {"gen/**"}, "lib": {"lib/**"},
		"root": {"."}, "util": {"util/**"}}
	wantDeps := map[string][]string{"app": {"gen", "root", "util"}, "gen": nil, "lib": nil, "root": nil,
		"util": {"lib"}}
	if !reflect.DeepEqual(cfg.Components, wantComponents) || !reflect.DeepEqual(depsOrNil(cfg.Deps), wantDeps) {
		t.Fatalf("scaffold: components %v deps %v", cfg.Components, cfg.Deps)
	}
	if code, out, errb := run(t, "rules", "--dir", dir, "--strict"); code != 0 {
		t.Fatalf("a scaffolded workspace config must pass its first check: %d %s%s", code, out, errb)
	}
	var mapping analysis.Mapping
	decodeCLIReport(t, &mapping, "mapping", "--dir", dir, "--format", "json")
	if !reflect.DeepEqual(mapping.Components["util"], []string{"example.com/a/util", "example.com/b/util"}) {
		t.Fatalf("one component must hold both modules' util: %+v", mapping)
	}
}

// depsOrNil은 deps의 빈 목록을 nil로 맞춘다 — 규칙 파일을 읽은 빈 목록과 비교 기대값의 nil을 같게 본다.
func depsOrNil(deps map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range deps {
		if len(v) == 0 {
			v = nil
		}
		out[k] = v
	}
	return out
}

// TestConfigMustBeRegularFile은 분석 대상의 .gartograph.yml이 정규 파일이 아니면 명령이
// 멈추지 않고 원인을 담아 2로 끝나는지 확인한다. 수확 명령은 전부 exclude를 읽으려고 이
// 파일을 연다 — FIFO는 open에서, /dev/zero 링크는 읽기에서 끝나지 않았다(장치는 회귀해도
// 메모리를 쓰지 않는 /dev/null로 검증한다). 정규 파일을 가리키는 링크는 지금처럼 읽힌다.
func TestConfigMustBeRegularFile(t *testing.T) {
	// replaced는 rulesFixture의 규칙 파일 자리에 place가 만든 파일을 둔 모듈이다.
	replaced := func(place func(cfg string)) string {
		dir := rulesFixture(t)
		cfg := filepath.Join(dir, ".gartograph.yml")
		if err := os.Remove(cfg); err != nil {
			t.Fatal(err)
		}
		place(cfg)
		return dir
	}
	fifoDir := replaced(func(cfg string) { testutil.MakeFIFO(t, cfg) })
	deviceDir := replaced(func(cfg string) {
		if err := os.Symlink(os.DevNull, cfg); err != nil {
			t.Fatal(err)
		}
	})
	for _, dir := range []string{deviceDir, fifoDir} {
		for _, cmd := range []string{"graph", "rules"} {
			var code int
			var errb string
			testutil.FinishWithin(t, testutil.BlockDeadline, func() { code, _, errb = run(t, cmd, "--dir", dir) })
			if code != 2 || !strings.Contains(errb, "is not a regular file") {
				t.Fatalf("%s --dir %s: want exit 2 naming the cause, got %d %s", cmd, dir, code, errb)
			}
		}
	}
	shared := filepath.Join(t.TempDir(), "shared.yml")
	if err := os.WriteFile(shared, []byte("components:\n  web: [\"web\"]\n  db: [\"db\"]\ndeps: {}\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := replaced(func(cfg string) {
		if err := os.Symlink(shared, cfg); err != nil {
			t.Fatal(err)
		}
	})
	if code, out, errb := run(t, "rules", "--dir", linkDir, "--strict"); code != 1 ||
		!strings.Contains(out, "example.com/fixture/web") {
		t.Fatalf("a linked regular rules file must still be checked: %d %s %s", code, out, errb)
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
	if err := json.Unmarshal([]byte(out), &sarif); err != nil {
		t.Fatalf("not SARIF JSON: %v", err)
	}
	if sarif.Version != "2.1.0" || len(sarif.Runs) != 1 ||
		len(sarif.Runs[0].Results) != 1 ||
		sarif.Runs[0].Results[0].RuleID != "dependency-cycle" {
		t.Fatalf("unexpected SARIF: %s", out)
	}
	// 순환을 이루는 간선의 첫 지점이 물리 위치로 실려야 code scanning이 결과를
	// 파일·줄에 놓는다 — 논리 위치만으로는 표시할 곳이 없다.
	locs := sarif.Runs[0].Results[0].Locations
	if len(locs) != 1 || locs[0].PhysicalLocation == nil ||
		locs[0].PhysicalLocation.ArtifactLocation.URI != "a/a.go" ||
		locs[0].PhysicalLocation.Region.StartLine != 3 {
		t.Fatalf("cycle result must carry the first evidence site as physicalLocation: %s", out)
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
	// 위치는 SARIF에만 싣는다 — cycles JSON과 baseline 파일의 형식은 그대로다.
	written, err := os.ReadFile(bp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "positions") || strings.Contains(string(written), "positions") {
		t.Fatalf("cycles JSON and baseline must not carry positions:\n%s\n%s", out, written)
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

// TestCyclesEvidenceOnce는 CLI JSON과 MCP gartograph_cycles가 같은 쌍의 관계 종류마다
// 증거 간선을 한 번만 싣는지 확인한다. call과 references가 함께 있는 쌍(Go 호출에서
// 흔하다)의 증거가 종류 수만큼 거듭 실리면, 고칠 간선 수를 세는 에이전트가 부풀려 센다.
func TestCyclesEvidenceOnce(t *testing.T) {
	docJSON := `{"version":2,"level":"symbol","vertices":[
{"id":"m.A","kind":"func"},{"id":"m.B","kind":"func"}],
"edges":[{"from":"m.A","to":"m.B","kind":"call","positions":[{"file":"a.go","line":3}]},
{"from":"m.A","to":"m.B","kind":"references","positions":[{"file":"a.go","line":4}]},
{"from":"m.B","to":"m.A","kind":"call","positions":[{"file":"b.go","line":5}]}]}`
	p := filepath.Join(t.TempDir(), "g.json")
	if err := os.WriteFile(p, []byte(docJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	want := `[{"from":"m.A","kind":"call","to":"m.B"},` +
		`{"from":"m.A","kind":"references","to":"m.B"},{"from":"m.B","kind":"call","to":"m.A"}]`
	type cycleEvidence struct {
		Edges []map[string]any `json:"edges"`
	}
	// evidenceOf는 순환 하나의 증거 간선을 비교할 수 있게 다시 직렬화한다.
	evidenceOf := func(cycles []cycleEvidence) string {
		t.Helper()
		if len(cycles) != 1 {
			t.Fatalf("expected one cycle, got %d", len(cycles))
		}
		got, err := json.Marshal(cycles[0].Edges)
		if err != nil {
			t.Fatal(err)
		}
		return string(got)
	}
	code, out, errb := run(t, "cycles", "--graph", p, "--level", "symbol", "--format", "json")
	if code != 0 {
		t.Fatalf("cycles json failed: %d %s", code, errb)
	}
	var report struct {
		Cycles []cycleEvidence `json:"cycles"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("cycles output is not JSON: %v\n%s", err, out)
	}
	if got := evidenceOf(report.Cycles); got != want {
		t.Fatalf("CLI evidence must name each relation once:\ngot  %s\nwant %s", got, want)
	}
	doc, err := export.LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	res := mcpSession(t, &mcpServer{doc: doc},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gartograph_cycles","arguments":{"level":"symbol"}}}`)
	result := res[0]["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("MCP cycles errored: %v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	// MCP도 CLI와 같은 {cycles, limitations} 보고를 돌려준다.
	var mcpReport struct {
		Cycles []cycleEvidence `json:"cycles"`
	}
	if err := json.Unmarshal([]byte(text), &mcpReport); err != nil {
		t.Fatalf("MCP cycles text is not the cycles report: %v\n%s", err, text)
	}
	if got := evidenceOf(mcpReport.Cycles); got != want {
		t.Fatalf("MCP evidence must match CLI:\ngot  %s\nwant %s", got, want)
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
	// 오류도 제외 파일도 없는 모듈은 limitations 키가 없다 — 알릴 것이 없으면 조용하다.
	if strings.Contains(out, "limitations") {
		t.Fatalf("a clean module must not carry limitations: %s", out)
	}
}

// unusedDepsReport는 unused-deps JSON 보고에서 테스트가 보는 필드다.
type unusedDepsReport struct {
	Unused       []string `json:"unused"`
	UsedRequires int      `json:"usedRequires"`
	Limitations  []string `json:"limitations"`
}

// runUnusedDepsJSON은 unused-deps를 JSON으로 실행해 보고를 읽는다.
func runUnusedDepsJSON(t *testing.T, dir string) unusedDepsReport {
	t.Helper()
	code, out, errb := run(t, "unused-deps", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("unused-deps failed: %d %s", code, errb)
	}
	var rep unusedDepsReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return rep
}

// TestUnusedDepsCountsConstraintExcludedFiles는 빌드 제약으로 빠진 파일의 import도 "사용"으로
// 세는지 확인한다 — go mod tidy는 ignore 외의 모든 태그·플랫폼 파일을 보므로, 다른 OS 전용 파일과
// //go:build integration 파일에서만 쓰는 require는 tidy가 지킨다. //go:build ignore 파일만 쓰는
// require는 tidy가 지우므로 미사용이다. 로드하지 않은 패키지(other·integ)를 import하는 제외 파일
// 수는 limitation으로 남는다.
func TestUnusedDepsCountsConstraintExcludedFiles(t *testing.T) {
	otherOS := "windows"
	if runtime.GOOS == "windows" {
		otherOS = "linux"
	}
	files := map[string]string{
		"go.mod": `module example.com/fixture

go 1.27

require (
	example.com/ignored v0.0.0
	example.com/integ v0.0.0
	example.com/other v0.0.0
)

replace (
	example.com/ignored => ./ignored
	example.com/integ => ./integ
	example.com/other => ./other
)
`,
		"main.go":                "package main\n\nfunc main() {}\n",
		"run_" + otherOS + ".go": "package main\n\nimport \"example.com/other\"\n\nfunc init() { other.F() }\n",
		"integ_test.go": "//go:build integration\n\npackage main\n\nimport (\n\t\"testing\"\n\n" +
			"\t\"example.com/integ\"\n)\n\nfunc TestInteg(t *testing.T) { integ.F() }\n",
		"gen.go": "//go:build ignore\n\npackage main\n\nimport \"example.com/ignored\"\n\nfunc main() { ignored.F() }\n",
	}
	for _, m := range []string{"ignored", "integ", "other"} {
		files[m+"/go.mod"] = "module example.com/" + m + "\n\ngo 1.27\n"
		files[m+"/"+m+".go"] = "package " + m + "\n\nfunc F() {}\n"
	}
	dir := testutil.WriteModule(t, files)
	rep := runUnusedDepsJSON(t, dir)
	if !slices.Equal(rep.Unused, []string{"example.com/ignored"}) || rep.UsedRequires != 2 {
		t.Fatalf("excluded-file imports must count as used, ignore-tagged ones must not: %+v", rep)
	}
	if len(rep.Limitations) != 1 || !strings.HasPrefix(rep.Limitations[0],
		"2 source files excluded by build constraints import packages that were not loaded;") {
		t.Fatalf("the two read excluded files must be counted: %v", rep.Limitations)
	}
	if code, _, _ := run(t, "unused-deps", "--dir", dir, "--strict"); code != 1 {
		t.Fatalf("the ignore-only require is still a direct unused require, strict must exit 1: %d", code)
	}
}

// TestUnusedDepsExcludedFileLimitationCountsUnseenImports는 제외 파일 limitation이 실제 공백만 세는지
// 확인한다 — 제외 파일이 std나 이미 로드된 패키지만 import하면 그 패키지의 의존은 이미 셌거나(로드됨)
// require에 기대지 않는다(std). 대부분 저장소의 OS별 파일이 그렇다 — 알릴 것이 없으면 조용해야 한다.
// 이 플랫폼 파일이 없어 로드되지 않은 주 모듈 패키지를 import하는 제외 파일은 공백이다. 그 패키지의
// 디렉터리(winonly)도 이 플랫폼 파일이 없는 디렉터리로 따로 센다 — 두 limitation은 다른 공백이다.
func TestUnusedDepsExcludedFileLimitationCountsUnseenImports(t *testing.T) {
	otherOS := "windows"
	if runtime.GOOS == "windows" {
		otherOS = "linux"
	}
	files := map[string]string{
		"go.mod": "module example.com/fixture\n\ngo 1.27\n\nrequire example.com/dep v0.0.0\n\n" +
			"replace example.com/dep => ./dep\n",
		"dep/go.mod": "module example.com/dep\n\ngo 1.27\n",
		"dep/dep.go": "package dep\n\nfunc F() {}\n",
		"lib/lib.go": "package lib\n\nfunc G() {}\n",
		"main.go": "package main\n\nimport (\n\t\"example.com/dep\"\n\t\"example.com/fixture/lib\"\n)\n\n" +
			"func main() { dep.F(); lib.G() }\n",
		"run_" + otherOS + ".go": "package main\n\nimport (\n\t\"os\"\n\t\"syscall\"\n\n\t\"example.com/dep\"\n" +
			"\t\"example.com/fixture/lib\"\n)\n\nfunc init() { _, _ = os.Getpid(), syscall.Getpid(); dep.F(); lib.G() }\n",
		"stub.go": "//go:build js || wasip1\n\npackage main\n\nimport \"os\"\n\nvar _ = os.O_RDONLY\n",
	}
	rep := runUnusedDepsJSON(t, testutil.WriteModule(t, files))
	if len(rep.Unused) != 0 || rep.UsedRequires != 1 || len(rep.Limitations) != 0 {
		t.Fatalf("excluded files importing only std and loaded packages leave no gap: %+v", rep)
	}
	files["winonly/w_"+otherOS+".go"] = "package winonly\n\nfunc W() {}\n"
	files["svc_"+otherOS+".go"] = "package main\n\nimport \"example.com/fixture/winonly\"\n\nfunc init() { winonly.W() }\n"
	rep = runUnusedDepsJSON(t, testutil.WriteModule(t, files))
	if len(rep.Limitations) != 2 || !strings.HasPrefix(rep.Limitations[0],
		"1 source files excluded by build constraints import packages that were not loaded;") ||
		rep.Limitations[1] != platformGapLimitation(1) {
		t.Fatalf("an excluded file importing an unloaded module package must be counted: %v", rep.Limitations)
	}
}

// TestUnusedDepsLoadErrors는 받을 수 없는 require(오프라인·인증 실패)를 import하는 패키지가 로드에
// 실패해도 그 require를 미사용으로 오보하지 않는지 확인한다 — 실패한 import 경로를 require 목록의
// 가장 긴 모듈에 귀속하고, 로드 오류 수를 limitation으로 남긴다. 판정 근거가 빈 사실을 숨기면
// 소비자가 쓰는 require를 지운다.
func TestUnusedDepsLoadErrors(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod":  "module example.com/fixture\n\ngo 1.27\n\nrequire example.com/missing v1.0.0\n",
		"main.go": "package main\n\nimport _ \"example.com/missing/pkg\"\n\nfunc main() {}\n",
	})
	rep := runUnusedDepsJSON(t, dir)
	if len(rep.Unused) != 0 || rep.UsedRequires != 1 {
		t.Fatalf("a require whose package failed to load is still imported: %+v", rep)
	}
	if len(rep.Limitations) != 1 || !strings.HasPrefix(rep.Limitations[0],
		"1 packages reported load errors;") {
		t.Fatalf("load errors must be counted: %v", rep.Limitations)
	}
	code, out, _ := run(t, "unused-deps", "--dir", dir)
	if code != 0 || !strings.Contains(out, "limitation: 1 packages reported load errors;") {
		t.Fatalf("text output must show the limitation: %d %s", code, out)
	}
}

// platformGapLimitation은 이 플랫폼 파일이 없는 디렉터리 n개를 알리는 unused-deps limitation 문구다.
func platformGapLimitation(n int) string {
	return fmt.Sprintf("%d directories have Go files but none for this platform or build tags, so go list "+
		"loaded no package from them; requires imported only there may be reported as unused "+
		"(load them with --goos, --goarch, or --tags)", n)
}

// TestUnusedDepsCountsDirectoriesWithoutPlatformFiles는 이 플랫폼 파일이 없어 go list가 패키지로 내지 않는
// 디렉터리(다른 OS 전용 svc)를 JSON·텍스트 보고의 limitation으로 싣는지 확인한다. 판정과 종료 코드는 그대로다
// — 그 디렉터리의 import는 읽지 않으므로 거기서만 쓰는 require는 여전히 unused이고 --strict는 1이다. 그 OS로
// 수확하면(--goos) 디렉터리가 패키지가 되어 require가 쓰이고 알릴 것이 없다.
func TestUnusedDepsCountsDirectoriesWithoutPlatformFiles(t *testing.T) {
	otherOS := "windows"
	if runtime.GOOS == "windows" {
		otherOS = "linux"
	}
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/fixture\n\ngo 1.27\n\nrequire example.com/winreq v0.0.0\n\n" +
			"replace example.com/winreq => ./winreq\n",
		"main.go":                    "package main\n\nfunc main() {}\n",
		"svc/svc_" + otherOS + ".go": "package svc\n\nimport \"example.com/winreq\"\n\nfunc Run() { winreq.Do() }\n",
		"winreq/go.mod":              "module example.com/winreq\n\ngo 1.27\n",
		"winreq/winreq.go":           "package winreq\n\nfunc Do() {}\n",
	})
	rep := runUnusedDepsJSON(t, dir)
	if !slices.Equal(rep.Unused, []string{"example.com/winreq"}) ||
		!slices.Equal(rep.Limitations, []string{platformGapLimitation(1)}) {
		t.Fatalf("the %s-only directory must be counted and the verdict kept: %+v", otherOS, rep)
	}
	code, out, _ := run(t, "unused-deps", "--dir", dir)
	if code != 0 || !strings.Contains(out, "\nlimitation: "+platformGapLimitation(1)+"\n") {
		t.Fatalf("text output must show the limitation: %d %s", code, out)
	}
	if code, _, _ := run(t, "unused-deps", "--dir", dir, "--strict"); code != 1 {
		t.Fatalf("the require is still reported unused, strict must exit 1: %d", code)
	}
	code, out, errb := run(t, "unused-deps", "--dir", dir, "--goos", otherOS, "--format", "json")
	if code != 0 || strings.Contains(out, "limitations") ||
		!strings.Contains(out, `"unused": []`) || !strings.Contains(out, `"usedRequires": 1`) {
		t.Fatalf("harvested for %s the directory is a package: %d %s %s", otherOS, code, out, errb)
	}
}

// TestHarvestRefusesToolchainSwitch는 설치된 Go보다 새 Go를 요구하는 모듈(go 1.99.0, toolchain go1.99.0)을
// 수확하는 명령마다 툴체인을 내려받지 않고, 원인과 해결 방향을 담아 종료 코드 2로 끝나는지 본다(README
// Security 절의 계약). GOTOOLCHAIN 환경 변수가 없을 때가 기본이다. GOPROXY=off라 회귀해도 네트워크에 닿지 않는다.
func TestHarvestRefusesToolchainSwitch(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "")
	if err := os.Unsetenv("GOTOOLCHAIN"); err != nil {
		t.Fatal(err)
	}
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod":  "module example.com/fixture\n\ngo 1.99.0\n\ntoolchain go1.99.0\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})
	for _, args := range [][]string{{"graph"}, {"dead", "--algo", "rta"}, {"unused-deps"}, {"schema"},
		{"routes", "--role", "client", "--pattern", "./..."}, {"mcp"}} {
		code, out, errb := run(t, append(args, "--dir", dir)...)
		if code != 2 || out != "" {
			t.Errorf("%v: exit %d with stdout %q, want exit 2 and no output", args, code, out)
		}
		if strings.Contains(errb, "downloading go1.99.0") || !strings.Contains(errb, "GOTOOLCHAIN=local") ||
			!strings.Contains(errb, "rerun with GOTOOLCHAIN=auto") {
			t.Errorf("%v: stderr must refuse the toolchain switch and say how to proceed:\n%s", args, errb)
		}
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
	"errors"
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
	_ = errors.Is(lib.WrapErr{}, nil)
}
`,
		"lib/lib.go": `package lib

// WrapErr.Unwrap은 errors가 이름 없는 interface{ Unwrap() error }로만 부른다.
type WrapErr struct{}

func (WrapErr) Error() string  { return "wrap" }
func (WrapErr) Unwrap() error { return nil }

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
		lib + ".(Named).String",   // 임베딩 승격 — Wrapper가 Named를 embeds로 끌어온다
		lib + ".(WrapErr).Unwrap", // 의존 소스의 이름 없는 인터페이스(errors의 Unwrap 단언)
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
	for _, alive := range []string{lib + ".(Patterns).Set", lib + ".(ParseError).Error", lib + ".quote",
		lib + ".(WrapErr).Unwrap"} {
		if strings.Contains(out, `"`+alive+`"`) {
			t.Fatalf("rta must keep %s reachable through external calls: %s", alive, out)
		}
	}
}

// TestDeadAnonymousDispatchOldDocument는 명명 인터페이스 satisfies만 있고
// 이름 없는 인터페이스 수확 표시가 없는 문서(v0.7 이전 수확)가 "이름 없는 것도
// 셌다"고 거짓 문구를 내지 않고 재수확을 권하는지 확인한다.
func TestDeadAnonymousDispatchOldDocument(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "errors"

type WrapErr struct{}

func (WrapErr) Error() string  { return "wrap" }
func (WrapErr) Unwrap() error { return nil }
func (WrapErr) Helper()       {}

func main() { _ = errors.Is(WrapErr{}, nil) }
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
	if doc["anonymousDispatch"] != true {
		t.Fatalf("symbol harvest must mark anonymous-interface facts: %v", doc["anonymousDispatch"])
	}
	delete(doc, "anonymousDispatch")
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
	if strings.Contains(out, "anonymous literals in dependency source") ||
		!strings.Contains(out, "predates anonymous-interface facts") {
		t.Fatalf("old document must not claim anonymous-interface facts: %s", out)
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
	delete(doc, "anonymousDispatch")
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

// TestDeadBlankInitializers는 빈 식별자 변수 초기화식에서만 쓰는 심볼이 dead로
// 보고되지 않는지 확인한다 — var _ = f()는 초기화 때 실행되고, 컴파일 타임
// 단언 var _ I = T{}는 T·I를 쓴다. 빈 함수 본문에서만 쓰는 심볼은 여전히 보고된다.
func TestDeadBlankInitializers(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

type I interface{ M() }
type T struct{}

func (T) M() {}

var _ I = T{}
var _ = first()

func first() int { return 1 }

func _() { onlyBlank() }

func onlyBlank() {}

func main() {}
`,
	})
	code, out, errb := run(t, "dead", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("dead failed: %d %s", code, errb)
	}
	var rep struct {
		Unreachable []struct{ ID string } `json:"unreachable"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("dead output is not JSON: %v", err)
	}
	reported := map[string]bool{}
	for _, f := range rep.Unreachable {
		reported[f.ID] = true
	}
	const m = "example.com/fixture"
	for _, alive := range []string{m + ".first", m + ".T", m + ".I"} {
		if reported[alive] {
			t.Fatalf("%s runs at init or is used by a blank assertion but was reported: %s", alive, out)
		}
	}
	if !reported[m+".onlyBlank"] {
		t.Fatalf("a symbol used only by a blank function must still be reported: %s", out)
	}
	// RTA도 빈 초기화식을 실행 지점으로 본다 — 합성 init이 그 초기화식을 돈다.
	code, out, errb = run(t, "dead", "--dir", dir, "--algo", "rta", "--format", "json")
	if code != 0 {
		t.Fatalf("dead --algo rta failed: %d %s", code, errb)
	}
	if strings.Contains(out, `"`+m+`.first"`) {
		t.Fatalf("rta must keep a function called by a blank initializer: %s", out)
	}
	// 판정이 살렸으면 explain도 경로를 보여야 한다 — 합성 init은 pkg._로 옮겨진다.
	_, out, _ = run(t, "dead", "--dir", dir, "--algo", "rta", "--explain", m+".first")
	if !strings.Contains(out, "root: "+m+"._") {
		t.Fatalf("rta explain must show the blank-initializer root: %s", out)
	}
}

// TestDeadCollidingSymbol은 점 경로 패키지와 ID가 겹치는 함수가 접미사 ID로 남아
// 그 피호출자가 dead로 보고되지 않고, CHA·RTA explain이 접미사 ID 경로를 보이며
// 패키지 정점으로 "호출"하지 않는지 확인한다.
func TestDeadCollidingSymbol(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.27\n",
		"main.go": `package main

import (
	"example.com/m/x"
	xy "example.com/m/x.y"
)

func main() { x.Use(); xy.Other() }
`,
		"x/x.go":    "package x\n\nfunc y() { helper() }\n\nfunc helper() {}\n\nfunc Use() { y() }\n",
		"x.y/xy.go": "package xy\n\nfunc Other() {}\n",
	})
	for _, algo := range []string{"cha", "rta"} {
		code, out, errb := run(t, "dead", "--dir", dir, "--algo", algo)
		if code != 0 || strings.Contains(out, "example.com/m/x.helper") {
			t.Fatalf("%s: callee of a colliding function must stay reachable: %d %s %s", algo, code, out, errb)
		}
		_, out, _ = run(t, "dead", "--dir", dir, "--algo", algo, "--explain", "example.com/m/x.helper")
		if !strings.Contains(out, "-> example.com/m/x.y#symbol") || strings.Contains(out, "-> example.com/m/x.y\n") {
			t.Fatalf("%s: explain must route through the suffixed ID, not the package: %s", algo, out)
		}
	}
}

// TestDeadRTAExplainTransitiveInit은 다른 패키지(q)의 빈 선언이 끌어오는 패키지(r)의
// 변수 초기화식도 RTA가 살리고 explain이 경로를 보이는지 확인한다 — 판정은 살렸는데
// "no path"라고 하면 모순이다. r의 초기화식은 호출을 실행하므로 r 자신의 초기화
// 루트(r._)에서 닿는다.
func TestDeadRTAExplainTransitiveInit(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport _ \"example.com/fixture/q\"\n\nfunc main() {}\n",
		"q/q.go":  "package q\n\nimport \"example.com/fixture/r\"\n\nvar _ = r.X\n",
		"r/r.go":  "package r\n\nvar X = rreg()\n\nfunc rreg() int { return 1 }\n",
	})
	const target = "example.com/fixture/r.rreg"
	code, out, errb := run(t, "dead", "--dir", dir, "--algo", "rta", "--format", "json")
	if code != 0 || strings.Contains(out, `"`+target+`"`) {
		t.Fatalf("rta must keep rreg reachable through package initializers: %d %s %s", code, out, errb)
	}
	_, out, _ = run(t, "dead", "--dir", dir, "--algo", "rta", "--explain", target)
	if !strings.Contains(out, "root: example.com/fixture/r._") || !strings.Contains(out, "-> "+target) {
		t.Fatalf("rta explain must route from the package initializer root: %s", out)
	}
}

// TestDeadSideEffectInitializers는 초기화 때 실행되는 호출의 대상이 CHA·RTA 모두에서
// dead로 보고되지 않고, 읽히지 않는 변수 자체는 여전히 보고되는지 확인한다. RTA는
// 모든 패키지의 합성 init을 루트로 삼는다 — CHA가 모든 사용자 init을 루트로 두는 것과
// 같은 기준이다.
func TestDeadSideEffectInitializers(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport _ \"example.com/fixture/p\"\n\nfunc main() {}\n",
		"p/p.go":  "package p\n\nvar registered = register()\n\nfunc register() int { return 1 }\n",
	})
	for _, algo := range []string{"cha", "rta"} {
		code, out, errb := run(t, "dead", "--dir", dir, "--algo", algo, "--format", "json")
		if code != 0 {
			t.Fatalf("%s: dead failed: %d %s", algo, code, errb)
		}
		if strings.Contains(out, `"example.com/fixture/p.register"`) {
			t.Fatalf("%s: register runs at init but was reported: %s", algo, out)
		}
		if !strings.Contains(out, `"example.com/fixture/p.registered"`) {
			t.Fatalf("%s: the never-read variable must still be reported: %s", algo, out)
		}
	}
	_, out, _ := run(t, "dead", "--dir", dir, "--algo", "rta", "--explain", "example.com/fixture/p.register")
	if !strings.Contains(out, "-> example.com/fixture/p.register") {
		t.Fatalf("rta explain must show the initializer path: %s", out)
	}
}

// TestDiffInterfaceGainedMethodHarvested는 실제 수확 문서로 인터페이스 메서드 추가가
// breaking으로 잡히는지 확인한다 — 수확기는 메서드 contains를 패키지에서 긋는데,
// 판정이 인터페이스 타입에서 나가는 contains만 보면 한 번도 작동하지 않는다.
func TestDiffInterfaceGainedMethodHarvested(t *testing.T) {
	v1 := testutil.WriteModule(t, map[string]string{
		"a/a.go": "package a\n\ntype I interface{ Do() }\n",
	})
	v2 := testutil.WriteModule(t, map[string]string{
		"a/a.go": "package a\n\ntype I interface {\n\tDo()\n\tRun()\n}\n",
	})
	tmp := t.TempDir()
	oldPath, newPath := filepath.Join(tmp, "old.json"), filepath.Join(tmp, "new.json")
	for dir, out := range map[string]string{v1: oldPath, v2: newPath} {
		if code, _, errb := run(t, "graph", "--level", "symbol", "--dir", dir, "--out", out); code != 0 {
			t.Fatalf("graph failed: %d %s", code, errb)
		}
	}
	code, out, _ := run(t, "diff", oldPath, newPath, "--strict")
	if code != 1 || !strings.Contains(out, "a.I gained method Run — ") {
		t.Fatalf("interface method addition must be breaking on harvested documents: %d %s", code, out)
	}
	// 비공개 임베드 인터페이스가 메서드를 얻어도 공개 임베더가 깨진다.
	e1 := testutil.WriteModule(t, map[string]string{
		"a/a.go": "package a\n\ntype j interface{ M() }\n\ntype I interface{ j }\n",
	})
	e2 := testutil.WriteModule(t, map[string]string{
		"a/a.go": "package a\n\ntype j interface {\n\tM()\n\tN()\n}\n\ntype I interface{ j }\n",
	})
	for dir, out := range map[string]string{e1: oldPath, e2: newPath} {
		if code, _, errb := run(t, "graph", "--level", "symbol", "--dir", dir, "--out", out); code != 0 {
			t.Fatalf("graph failed: %d %s", code, errb)
		}
	}
	code, out, _ = run(t, "diff", oldPath, newPath, "--strict")
	if code != 1 || !strings.Contains(out, "a.I gained method N via embedded example.com/fixture/a.j") {
		t.Fatalf("a method gained through an embedded interface must be breaking: %d %s", code, out)
	}
	// 모듈 밖 인터페이스 임베드는 간선이 없다 — 메서드 집합 사실로만 보인다.
	x1 := testutil.WriteModule(t, map[string]string{
		"a/a.go": "package a\n\ntype I interface{ Do() }\n",
	})
	x2 := testutil.WriteModule(t, map[string]string{
		"a/a.go": "package a\n\nimport \"io\"\n\ntype I interface {\n\tDo()\n\tio.Reader\n}\n",
	})
	for dir, out := range map[string]string{x1: oldPath, x2: newPath} {
		if code, _, errb := run(t, "graph", "--level", "symbol", "--dir", dir, "--out", out); code != 0 {
			t.Fatalf("graph failed: %d %s", code, errb)
		}
	}
	code, out, _ = run(t, "diff", oldPath, newPath, "--strict")
	if code != 1 || !strings.Contains(out, "a.I gained method Read via embedding") {
		t.Fatalf("embedding an interface from outside the module must be breaking: %d %s", code, out)
	}
}

// TestDiffEmbeddedFieldRetypeHarvested는 실제 수확 문서에서 공개 struct가 임베드한 비공개 타입의
// 재형이 diff --strict를 막는지 확인한다 — *inner → inner면 값 T가 포인터 리시버 메서드 Foo를 잃어
// `var _ fooer = T{}` 같은 소비자가 깨지는데, 수확은 구체 타입을 포인터 메서드 집합으로 싣고 승격
// 메서드를 선언 타입 아래에 둬 시그니처·정점 비교로는 보이지 않는다. 같은 이름의 명명 필드와 내부
// 필드(mu) 재형은 계속 게이트를 막지 않는다.
func TestDiffEmbeddedFieldRetypeHarvested(t *testing.T) {
	const inner = "package a\n\ntype inner struct{ n int }\n\nfunc (i *inner) Foo() int { return i.n }\n\n"
	const generic = "package a\n\ntype inner[V any] struct{ v V }\n\nfunc (i inner[V]) Get() V { return i.v }\n\n"
	for _, tc := range []struct {
		name     string
		old, new string
		code     int
		want     string
	}{
		{"pointer embed to value embed", inner + "type T struct {\n\t*inner\n\tName string\n}\n",
			inner + "type T struct {\n\tinner\n\tName string\n}\n", 1,
			"breaking: embedded field example.com/fixture/a.T.inner changed type: " +
				"*example.com/fixture/a.inner -> example.com/fixture/a.inner"},
		{"generic embed type argument", generic + "type T struct{ inner[int] }\n",
			generic + "type T struct{ inner[string] }\n", 1,
			"breaking: embedded field example.com/fixture/a.T.inner changed type: " +
				"example.com/fixture/a.inner[int] -> example.com/fixture/a.inner[string]"},
		{"named field typed like its name", inner + "type T struct{ inner *inner }\n",
			inner + "type T struct{ inner inner }\n", 0, "0 breaking"},
		{"internal mutex", "package a\n\nimport \"sync\"\n\ntype T struct {\n\tName string\n\tmu sync.Mutex\n}\n",
			"package a\n\nimport \"sync\"\n\ntype T struct {\n\tName string\n\tmu sync.RWMutex\n}\n", 0, "0 breaking"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			oldPath, newPath := filepath.Join(tmp, "old.json"), filepath.Join(tmp, "new.json")
			for src, out := range map[string]string{tc.old: oldPath, tc.new: newPath} {
				dir := testutil.WriteModule(t, map[string]string{"a/a.go": src})
				if code, _, errb := run(t, "graph", "--level", "type", "--dir", dir, "--out", out); code != 0 {
					t.Fatalf("graph failed: %d %s", code, errb)
				}
			}
			code, out, _ := run(t, "diff", oldPath, newPath, "--strict")
			if code != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("diff --strict = %d, want %d with %q:\n%s", code, tc.code, tc.want, out)
			}
		})
	}
}

// TestDeadRTAKeepsMethodRoots는 문서 루트인 메서드(keep 표지)가 --algo rta에서도
// 루트로 잡히고 그 피호출자까지 도달하는지 확인한다 — SSA 패키지 Members에는 메서드가
// 없어, 루트이면서 unreachable로 보고되는 모순이 있었다. 제네릭 본문은 x/tools rta가
// 타입 파라미터에서 패닉해 루트로 넣지 않는다 — 크래시 없이 루트 자신만 살아 있다.
func TestDeadRTAKeepsMethodRoots(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

type T struct{}

//gartograph:keep
func (T) Kept() { viaValue() }

type P struct{}

//gartograph:keep
func (*P) PtrKept() { viaPtr() }

type G[X any] struct{ x X }

//gartograph:keep
func (g *G[X]) Generic() any { return any(&g.x) }

//gartograph:keep
func Box[X any](x X) any { return any(&x) }

func viaValue() {}
func viaPtr()   {}

func main() {}
`,
	})
	code, out, errb := run(t, "dead", "--dir", dir, "--algo", "rta", "--format", "json")
	if code != 0 {
		t.Fatalf("dead --algo rta must not crash on generic roots: %d %s", code, errb)
	}
	var rep struct {
		Unreachable []struct{ ID string } `json:"unreachable"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("dead output is not JSON: %v", err)
	}
	if len(rep.Unreachable) != 0 {
		t.Fatalf("root methods (generic ones included) and their callees must be reachable: %s", out)
	}
	_, out, _ = run(t, "dead", "--dir", dir, "--algo", "rta", "--explain", "example.com/fixture.viaPtr")
	if !strings.Contains(out, "root: example.com/fixture.(P).PtrKept") {
		t.Fatalf("rta explain must route from the root method: %s", out)
	}
}

// TestDeadOldDocumentWithoutInitRoots는 초기화 루트 수확 이전 문서로 dead를 돌리면
// 재수확을 권하는지 확인한다 — 그 문서에는 pkg._가 없어 초기화식에서만 쓰는 심볼이
// 거짓으로 dead일 수 있다.
func TestDeadOldDocumentWithoutInitRoots(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n\nfunc unused() {}\n",
	})
	code, out, errb := run(t, "graph", "--level", "symbol", "--format", "json", "--dir", dir)
	if code != 0 {
		t.Fatalf("graph failed: %d %s", code, errb)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["initializerRoots"] != true {
		t.Fatalf("symbol harvest must mark initializer roots: %v", doc["initializerRoots"])
	}
	fresh := filepath.Join(t.TempDir(), "new.json")
	raw, _ := json.Marshal(doc)
	if err := os.WriteFile(fresh, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := run(t, "dead", "--graph", fresh); strings.Contains(out, "lacks the initializerRoots marker") {
		t.Fatalf("a current document must not get the re-harvest note: %s", out)
	}
	delete(doc, "initializerRoots")
	old := filepath.Join(t.TempDir(), "old.json")
	raw, _ = json.Marshal(doc)
	if err := os.WriteFile(old, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := run(t, "dead", "--graph", old); !strings.Contains(out, "lacks the initializerRoots marker") {
		t.Fatalf("an old document must get the re-harvest note: %s", out)
	}
}

// TestDeadRTAInterfaceMethods는 인터페이스 메서드 정점을 RTA가 판정하는지 확인한다 —
// 추상 메서드는 SSA 함수가 없어 늘 unreachable로 나왔다. RTA가 도달한 함수가 그 메서드를
// 호출(또는 참조)하면 도달이고, 아무도 부르지 않는 인터페이스 메서드는 여전히 보고된다.
func TestDeadRTAInterfaceMethods(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import (
	"example.com/fixture/x"
	_ "example.com/fixture/x.y"
)

type I interface {
	Called()
	Never()
}

type T struct{}

func (T) Called() {}
func (T) Never()  {}

type unusedCaller struct{}

func (unusedCaller) run(i I) { i.Never() }

type Base interface{ Emb() }
type Wide interface{ Base }
type Valued interface{ Val() }
type Inited interface{ Init() int }
type Tabled interface{ Tab() }

func (T) Emb()      {}
func (T) Val()      {}
func (T) Init() int { return 0 }
func (T) Tab()      {}

var _ = Inited(T{}).Init()

var table = map[string]func(Tabled){"a": func(t Tabled) { t.Tab() }}

func main() {
	var i I = T{}
	i.Called()
	var w Wide = T{}
	w.Emb()
	f := Valued(T{}).Val
	f()
	table["a"](T{})
	x.Use()
}
`,
		"x/x.go":    "package x\n\ntype y interface{ Clash() }\n\ntype impl struct{}\n\nfunc (impl) Clash() {}\n\nfunc Use() { var v y = impl{}; v.Clash() }\n",
		"x.y/xy.go": "package xy\n",
	})
	code, out, errb := run(t, "dead", "--dir", dir, "--algo", "rta", "--format", "json")
	if code != 0 {
		t.Fatalf("dead --algo rta failed: %d %s", code, errb)
	}
	var rep struct {
		Unreachable []struct{ ID string } `json:"unreachable"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("dead output is not JSON: %v", err)
	}
	got := map[string]bool{}
	for _, f := range rep.Unreachable {
		got[f.ID] = true
	}
	if got["example.com/fixture.(I).Called"] {
		t.Fatalf("an interface method called from rta-reachable code must be reachable: %s", out)
	}
	// Never는 rta가 도달하지 않는 함수에서만 불린다.
	if !got["example.com/fixture.(I).Never"] {
		t.Fatalf("an interface method called only from unreachable code must be reported: %s", out)
	}
	for _, alive := range []string{
		"example.com/fixture.(Base).Emb",         // 임베드 인터페이스로 호출 — 선언 쪽 정점에 붙는다
		"example.com/fixture.(Valued).Val",       // 메서드 값(references)
		"example.com/fixture.(Inited).Init",      // 초기화 루트(pkg._)에서 호출
		"example.com/fixture.(Tabled).Tab",       // 패키지 변수에 담긴 함수 표 안에서만 호출
		"example.com/fixture/x.(y#symbol).Clash", // 소유자가 점 경로 패키지와 겹침
	} {
		if got[alive] {
			t.Fatalf("%s is used from live code but rta reported it: %s", alive, out)
		}
	}
	_, out, _ = run(t, "dead", "--dir", dir, "--algo", "rta", "--explain", "example.com/fixture.(I).Called")
	if !strings.Contains(out, "root: example.com/fixture.main") || !strings.Contains(out, "-> example.com/fixture.(I).Called") {
		t.Fatalf("rta explain must show the call into the interface method: %s", out)
	}
}

// TestJSONEmptyListsAreArrays는 결과가 없는 목록 필드가 null이 아니라 []로 나가는지
// 확인한다 — 소비자가 null과 "없음"을 따로 다루게 하면 안 되고, SARIF는 results가
// 배열이어야 유효하다. 선택 필드(omitempty)는 여전히 키가 빠진다.
func TestJSONEmptyListsAreArrays(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"dead", "--format", "json"}, `"unreachable": []`},
		{[]string{"dead", "--format", "sarif"}, `"results": []`},
		{[]string{"cycles", "--format", "json"}, `"cycles": []`},
		{[]string{"query", "example.com/fixture.main"}, `"dependsOn": []`},
	} {
		code, out, errb := run(t, append(c.args, "--dir", dir)...)
		if code != 0 || !strings.Contains(out, c.want) || strings.Contains(out, ": null") {
			t.Fatalf("%v: expected %s and no null lists: %d %s %s", c.args, c.want, code, out, errb)
		}
	}
}

// TestDeadCHAEmbeddedAndGenericInterfaceDispatch는 CHA 팬아웃이 struct에 임베드한 인터페이스로
// 부르는 호출(s.SEM())과 제네릭 인터페이스 인스턴스 경유 호출(g.Get() — g G[int])에서도
// 구현 메서드로 퍼지는지 확인한다 — 둘 다 구현이 거짓으로 dead였다.
func TestDeadCHAEmbeddedAndGenericInterfaceDispatch(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "io"

type SE interface{ SEM() }

type Holder struct{ SE }

type G[X any] interface{ Get() X }

type T struct{}

func (T) SEM()     {}
func (T) Get() int { return 0 }

// Unused는 모듈 밖 인터페이스(io.Closer)를 만족하지만 쓰이지 않는다 — 모듈 밖 인터페이스
// 호출은 모든 모듈 구현자로 퍼뜨리지 않는다(satisfies 규칙이 리시버 도달성으로 다룬다).
type Unused struct{}

func (Unused) Close() error { return nil }

type Closing struct{ io.Closer }

func main() {
	h := Holder{SE: T{}}
	h.SEM()
	var g G[int] = T{}
	_ = g.Get()
	f := g.Get
	_ = f
	c := Closing{}
	if c.Closer != nil {
		_ = c.Close()
	}
}
`,
	})
	code, out, errb := run(t, "dead", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("dead failed: %d %s", code, errb)
	}
	for _, alive := range []string{"example.com/fixture.(T).SEM", "example.com/fixture.(T).Get"} {
		if strings.Contains(out, `"id": "`+alive+`"`) {
			t.Fatalf("%s is reached through interface dispatch but was reported: %s", alive, out)
		}
	}
	if !strings.Contains(out, `"id": "example.com/fixture.(Unused).Close"`) {
		t.Fatalf("a call through an external interface must not keep every module implementer alive: %s", out)
	}
}

// TestDeadCHADispatchEdgeCases는 CHA 팬아웃 보강의 경계를 경로별로 고정한다 — 비공개
// 제네릭 인터페이스 호출(패닉 회귀), 외부 제네릭 인터페이스를 임베드한 모듈 인터페이스
// (미리 계산한 impls 사용), 제네릭 구체 타입·제네릭 함수 안 호출(이름 기반 과대 근사),
// 메서드 값·메서드 표현식으로만 쓰는 인터페이스 메서드.
func TestDeadCHADispatchEdgeCases(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod":     "module example.com/fixture\n\ngo 1.27\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => ./dep\n",
		"dep/go.mod": "module example.com/dep\n\ngo 1.27\n",
		"dep/dep.go": "package dep\n\ntype G[X any] interface{ Get() X }\n",
		"main.go": `package main

import "example.com/dep"

type g[X any] interface{ get() X }
type Priv struct{}

func (Priv) get() int { return 0 }

type MyG interface{ dep.G[int] }
type Ext struct{}

func (Ext) Get() int { return 0 }

type Gen[X any] interface{ Val() X }
type Box[X any] struct{ x X }

func (b Box[X]) Val() X { return b.x }

// Plain은 비제네릭 구현자(Flat)와 제네릭 구현자(Cell[X])가 함께 있다 — 미리 계산한 impls가
// 제네릭 원형을 빠뜨리면 Cell이 거짓 dead다.
type Plain interface{ Num() int }
type Flat struct{}

func (Flat) Num() int { return 0 }

type Cell[X any] struct{ x X }

func (c Cell[X]) Num() X { return c.x }

// Mismatch는 이름만 같고 모양(결과 개수)이 다른 제네릭 메서드 — 구현자가 아니다.
type Want interface{ Pair() (int, int) }
type Shape[X any] struct{}

func (Shape[X]) Pair() int { return 0 }

type InFunc struct{}

func (InFunc) Val() string { return "" }

func use[X any](v Gen[X]) X { return v.Val() }

type MV interface{ Run() }
type RunOnly struct{}

func (RunOnly) Run() {}

type ME interface{ Do() }
type DoOnly struct{}

func (DoOnly) Do() {}

func main() {
	var p g[int] = Priv{}
	_ = p.get()
	var m MyG = Ext{}
	_ = m.Get()
	var b Gen[int] = Box[int]{}
	_ = b.Val()
	_ = use[string](InFunc{})
	var pl Plain = Cell[int]{}
	_ = pl.Num()
	_ = Flat{}
	var w Want
	if w != nil {
		_, _ = w.Pair()
	}
	_ = Shape[int]{}
	var mv MV = RunOnly{}
	f := mv.Run
	f()
	e := ME.Do
	e(DoOnly{})
}
`,
	})
	code, out, errb := run(t, "dead", "--dir", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("dead must not crash: %d %s", code, errb)
	}
	for _, alive := range []string{"(Priv).get", "(Ext).Get", "(Box).Val", "(InFunc).Val", "(RunOnly).Run",
		"(DoOnly).Do", "(Cell).Num"} {
		if strings.Contains(out, `"id": "example.com/fixture.`+alive+`"`) {
			t.Fatalf("%s is reached through interface dispatch but was reported: %s", alive, out)
		}
	}
	if !strings.Contains(out, `"id": "example.com/fixture.(Shape).Pair"`) {
		t.Fatalf("a generic method with a different shape is not an implementer: %s", out)
	}
}

// TestDiffConstraintTypeSetHarvested는 실제 수확 문서로 제약 인터페이스를 좁히면 breaking이고,
// 항 순서·별칭만 바꾸면 아닌지 확인한다.
func TestDiffConstraintTypeSetHarvested(t *testing.T) {
	tmp := t.TempDir()
	harvest := func(src, name string) string {
		dir := testutil.WriteModule(t, map[string]string{"a/a.go": src})
		out := filepath.Join(tmp, name)
		if code, _, errb := run(t, "graph", "--level", "symbol", "--dir", dir, "--out", out); code != 0 {
			t.Fatalf("graph failed: %d %s", code, errb)
		}
		return out
	}
	base := harvest("package a\n\ntype N interface{ ~int | ~int64 | ~byte }\n", "base.json")
	same := harvest("package a\n\ntype N interface{ ~uint8 | ~int64 | ~int }\n", "same.json")
	narrow := harvest("package a\n\ntype N interface{ ~int }\n", "narrow.json")
	if code, out, _ := run(t, "diff", base, same, "--strict"); code != 0 {
		t.Fatalf("reordering terms and alias spelling is not a change: %d %s", code, out)
	}
	if code, out, _ := run(t, "diff", base, narrow, "--strict"); code != 1 || !strings.Contains(out, "changed type set") {
		t.Fatalf("narrowing an exported constraint must be breaking: %d %s", code, out)
	}
}
