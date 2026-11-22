package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// 빈 수확 회귀. --pattern 오타·없는 디렉터리·go.mod 없는 --dir이면 go list는 패턴 오류만 담은 가짜 패키지(이름·
// 파일·모듈 없음)를 돌려준다. 예전에는 keep이 그것을 버린 뒤 로드 오류를 세어 cycles --strict·dead --strict·MCP가
// 아무것도 보지 않고 limitation 없이 성공했고, dead는 엉뚱한 --retain-public 안내를 냈다.

// patternErrorNote는 패턴 오류 limitation의 공통 부분이다.
const patternErrorNote = "package patterns reported errors from go list"

// 패턴 오류 limitation의 뒷부분이다 — 아무 패키지도 적재하지 않았을 때와, 다른 패키지는 수확됐을 때.
const (
	nothingLoadedNote = "no packages were loaded, so nothing was harvested — check --pattern and --dir"
	partialLoadNote   = "packages they could not resolve were not analyzed"
)

// emptyHarvestCase는 패턴 오류 하나를 일으키는 수확 인자와, limitation의 first: 뒤에 나올 go list 문구다.
type emptyHarvestCase struct {
	name  string
	args  []string
	first string
}

// emptyHarvestCases는 세 가지 빈 수확을 만든다 — 패턴 오타, 없는 디렉터리, go.mod 없는 --dir.
func emptyHarvestCases(t *testing.T) (string, []emptyHarvestCase) {
	t.Helper()
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":    "package main\n\nimport \"example.com/fixture/lib\"\n\nfunc main() { lib.Used() }\n",
		"lib/lib.go": "package lib\n\nfunc Used() {}\n\nfunc Exported() {}\n",
	})
	noMod := t.TempDir()
	if err := os.WriteFile(filepath.Join(noMod, "x.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, []emptyHarvestCase{
		{"typo pattern", []string{"--dir", dir, "--pattern", "./interal/..."}, "pattern ./interal/...: "},
		{"missing directory", []string{"--dir", dir, "--pattern", "./nonexistent"}, "directory not found"},
		{"dir without go.mod", []string{"--dir", noMod}, "does not contain main module"},
	}
}

// TestEmptyHarvestCountsPatternErrors는 패턴 오류가 수확 문서의 limitation으로 세어지고, 종료 코드는 그대로이며
// (limitation은 --strict에 들어가지 않는다), 정점 없는 문서에 --retain-public 안내가 붙지 않는지 본다.
func TestEmptyHarvestCountsPatternErrors(t *testing.T) {
	_, cases := emptyHarvestCases(t)
	// 규칙 파일은 모듈 밖에 둔다 — 모듈에 두면 같은 fixture의 init이 "이미 있다"로 먼저 거부된다.
	cfg := filepath.Join(t.TempDir(), "rules.yml")
	rules := "components:\n  app: [\".\"]\n  lib: [\"lib\"]\ndeps:\n  app: [\"lib\"]\n  lib: []\n"
	if err := os.WriteFile(cfg, []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := "1 " + patternErrorNote + " (first: "
			code, out, errb := run(t, append([]string{"cycles", "--strict"}, c.args...)...)
			if code != 0 || !strings.Contains(out, want) || !strings.Contains(out, c.first) ||
				!strings.Contains(out, nothingLoadedNote) || strings.Contains(out, partialLoadNote) {
				t.Errorf("cycles --strict: exit %d, want 0 with the pattern error counted (%q)\n%s%s", code, c.first, out, errb)
			}
			code, out, errb = run(t, append([]string{"dead", "--strict"}, c.args...)...)
			if code != 0 || !strings.Contains(out, want) || strings.Contains(out, "use --retain-public") {
				t.Errorf("dead --strict: exit %d, want 0 with the pattern error and no --retain-public hint\n%s%s", code, out, errb)
			}
			code, out, errb = run(t, append([]string{"rules", "--strict", "--config", cfg}, c.args...)...)
			if code != 0 || !strings.Contains(out, want) {
				t.Errorf("rules --strict: exit %d, want 0 with the pattern error counted\n%s%s", code, out, errb)
			}
			// --deps는 모든 패키지를 정점으로 삼는다 — 패턴 오류 가짜 패키지가 유령 정점이 되면 안 된다.
			code, out, errb = run(t, append([]string{"graph", "--deps"}, c.args...)...)
			if code != 0 || strings.Contains(out, `"name": ""`) || !strings.Contains(out, patternErrorNote) {
				t.Errorf("graph --deps: exit %d, want no ghost vertex and the pattern error counted\n%s%s", code, out, errb)
			}
			// module 레벨은 가짜 패키지를 "모듈 없는(builtin) 패키지"로 세던 곳이다.
			code, out, errb = run(t, append([]string{"graph", "--level", "module"}, c.args...)...)
			if code != 0 || strings.Contains(out, "have no module (builtin)") || !strings.Contains(out, patternErrorNote) {
				t.Errorf("graph --level module: exit %d\n%s%s", code, out, errb)
			}
			var mcpOut strings.Builder
			in := strings.NewReader(mcpCall(1, "gartograph_summary", "") + "\n")
			if code := mcpRun(c.args, in, &mcpOut, &mcpOut); code != 0 || !strings.Contains(mcpOut.String(), patternErrorNote) {
				t.Errorf("mcp summary: exit %d, pattern error not in limitations\n%s", code, mcpOut.String())
			}
		})
	}
}

// TestEmptyHarvestExitTwoCommands는 빈 수확을 원래 2로 거부하던 명령이 원인을 정확히 말하는지 본다.
// unused-deps는 패턴 오타에도 "is there a go.mod?"라고 했고, dead --algo rta는 패턴을 "타입·로드 오류 패키지"로 셌다.
func TestEmptyHarvestExitTwoCommands(t *testing.T) {
	_, cases := emptyHarvestCases(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, errb := run(t, append([]string{"unused-deps"}, c.args...)...)
			if code != 2 || !strings.Contains(errb, "1 "+patternErrorNote) || !strings.Contains(errb, c.first) {
				t.Errorf("unused-deps: exit %d, want 2 naming the pattern error\n%s", code, errb)
			}
			if c.name != "dir without go.mod" && strings.Contains(errb, "is there a go.mod") {
				t.Errorf("unused-deps blames go.mod for a pattern error:\n%s", errb)
			}
			code, _, errb = run(t, append([]string{"dead", "--algo", "rta"}, c.args...)...)
			if code != 2 || !strings.Contains(errb, "1 "+patternErrorNote) || strings.Contains(errb, "loaded packages have type or load errors") {
				t.Errorf("dead --algo rta: exit %d, want 2 naming the pattern error, not a broken package\n%s", code, errb)
			}
			code, _, errb = run(t, append([]string{"init"}, c.args...)...)
			if code != 2 || !strings.Contains(errb, patternErrorNote) {
				t.Errorf("init: exit %d, want 2 with the pattern error shown\n%s", code, errb)
			}
			code, _, errb = run(t, append([]string{"query", "example.com/fixture/lib"}, c.args...)...)
			if code != 2 || !strings.Contains(errb, "vertex not found") || !strings.Contains(errb, patternErrorNote) {
				t.Errorf("query: exit %d, want 2 explaining the empty graph\n%s", code, errb)
			}
			// MCP의 ID 도구도 CLI처럼 빈 그래프의 이유를 붙인다 — 예전에는 `vertex not found: X`만 돌려줬다.
			for _, call := range []string{
				mcpCall(1, "gartograph_query", `{"id":"example.com/fixture/lib"}`),
				mcpCall(2, "gartograph_path", `{"from":"example.com/fixture","to":"example.com/fixture/lib"}`),
			} {
				answer := runMcpTools(t, c.args, call)[0]
				if !answer.isError || !strings.Contains(answer.text, "(the graph has no vertices: 1 "+patternErrorNote) {
					t.Errorf("mcp %s: want an isError result explaining the empty graph, got %+v", call, answer)
				}
			}
		})
	}
}

// TestPatternErrorWithHarvestedPackages는 go list가 패턴 오류와 함께 그 패턴이 고른 실제 패키지도 돌려줄 때
// limitation이 "아무것도 적재하지 못했다"고 단정하지 않는지 본다. go.mod의 replace 대상 디렉터리가 없으면
// `--pattern example.com/fixture/...`는 주 모듈 패키지와 `example.com/...` 패턴 오류를 함께 낸다(네트워크 없이
// 결정적이다). 예전 문구는 정점이 있는 문서에도 "loaded no packages … nothing was harvested"라고 했다. 읽을 수 없는
// 하위 디렉터리(`./...`의 permission denied)도 같은 모양이지만 root로 도는 CI에서는 재현되지 않아 이 사례로 고정한다.
func TestPatternErrorWithHarvestedPackages(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/fixture\n\ngo 1.27\n\nrequire example.com/dep v0.0.0\n\n" +
			"replace example.com/dep => ./missingdep\n",
		"main.go":    "package main\n\nimport \"example.com/fixture/lib\"\n\nfunc main() { lib.Used() }\n",
		"lib/lib.go": "package lib\n\nfunc Used() {}\n",
	})
	code, out, errb := run(t, "graph", "--dir", dir, "--pattern", "example.com/fixture/...")
	var doc struct {
		Vertices []struct {
			ID string `json:"id"`
		} `json:"vertices"`
		Limitations []string `json:"limitations"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil {
		t.Fatalf("graph: exit %d\n%s%s", code, out, errb)
	}
	if len(doc.Vertices) != 2 {
		t.Errorf("the module's packages must still be harvested, got %d vertices:\n%s", len(doc.Vertices), out)
	}
	note := strings.Join(doc.Limitations, "\n")
	if !strings.Contains(note, "1 "+patternErrorNote+" (first: example.com/dep@v0.0.0 (replaced by ./missingdep)") ||
		!strings.Contains(note, partialLoadNote) || strings.Contains(note, "nothing was harvested") ||
		strings.Contains(note, "no packages were loaded") {
		t.Errorf("the limitation must state only what go list reported, not that nothing was loaded:\n%s", note)
	}
}

// loadErrorNote는 수확 문서·unused-deps 보고의 로드 오류 limitation 공통 부분이다.
const loadErrorNote = "packages reported load errors"

// unresolvedImportModule은 주 패키지가 어느 모듈도 제공하지 않는 경로(example.com/missing/pkg)를 import하는
// fixture다. 그 import는 go list가 오류만 담은 가짜 패키지로 돌려준다 — --deps면 이름 없는 정점이 된다.
func unresolvedImportModule(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport _ \"example.com/missing/pkg\"\n\nfunc main() {}\n",
	})
}

// harvestLimitations는 JSON 그래프 문서의 정점 ID와 limitation을 읽는다.
func harvestLimitations(t *testing.T, args ...string) (vertices, limitations []string) {
	t.Helper()
	code, out, errb := run(t, args...)
	var doc struct {
		Vertices []struct {
			ID string `json:"id"`
		} `json:"vertices"`
		Limitations []string `json:"limitations"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil {
		t.Fatalf("%v: exit %d\n%s%s", args, code, out, errb)
	}
	for _, v := range doc.Vertices {
		vertices = append(vertices, v.ID)
	}
	return vertices, doc.Limitations
}

// countWith는 limitation 목록에서 note가 든 항목 수다.
func countWith(list []string, note string) int {
	n := 0
	for _, l := range list {
		if strings.Contains(l, note) {
			n++
		}
	}
	return n
}

// TestPatternErrorImportedCountedOnce는 풀리지 않은 import 경로를 --pattern으로도 주면 같은 패키지가 패턴 오류와
// 로드 오류로 두 번 세이던 문제의 회귀다. go/packages는 그 경로의 가짜 패키지 하나를 패턴 루트와 import 그래프에
// 함께 싣는다 — 예전에는 --deps 문서가 `1 package patterns reported errors …`와 `1 packages reported load errors`를,
// unused-deps가 같은 두 줄을 냈다. 정점 집합은 바꾸지 않는다(--deps의 이름 없는 정점은 그대로 남는다). 같은 경로를
// 패턴으로 주지 않으면 로드 오류로 그대로 센다 — 계수가 사라지지 않았다는 대조군이다.
func TestPatternErrorImportedCountedOnce(t *testing.T) {
	dir := unresolvedImportModule(t)
	withPattern := []string{"--dir", dir, "--pattern", "./...", "--pattern", "example.com/missing/pkg"}

	vertices, lims := harvestLimitations(t, append([]string{"graph", "--deps"}, withPattern...)...)
	if countWith(lims, "1 "+patternErrorNote) != 1 || countWith(lims, loadErrorNote) != 0 {
		t.Errorf("graph --deps: the unresolved package must be counted once, by the pattern error:\n%s",
			strings.Join(lims, "\n"))
	}
	if strings.Join(vertices, " ") != "example.com/fixture example.com/missing/pkg" {
		t.Errorf("graph --deps: the vertex set must not change, got %v", vertices)
	}
	if _, lims := harvestLimitations(t, "graph", "--deps", "--dir", dir); countWith(lims, "1 "+loadErrorNote) != 1 {
		t.Errorf("graph --deps without the pattern must still count the load error:\n%s", strings.Join(lims, "\n"))
	}
	// 심볼 레벨에서는 주 패키지도 타입 오류(import 실패)를 낸다 — 그 하나만 로드 오류로 남는다(예전에는 2).
	if _, lims := harvestLimitations(t, append([]string{"graph", "--level", "symbol", "--deps"}, withPattern...)...); countWith(lims, "1 "+loadErrorNote) != 1 {
		t.Errorf("graph --level symbol --deps: only the importing package's own error is a load error:\n%s",
			strings.Join(lims, "\n"))
	}

	code, out, errb := run(t, append([]string{"unused-deps"}, withPattern...)...)
	if code != 0 || !strings.Contains(out, "1 "+patternErrorNote) || strings.Contains(out, loadErrorNote) {
		t.Errorf("unused-deps: exit %d, the unresolved package must be counted once\n%s%s", code, out, errb)
	}
	code, out, errb = run(t, "unused-deps", "--dir", dir)
	if code != 0 || !strings.Contains(out, "1 "+loadErrorNote) {
		t.Errorf("unused-deps without the pattern: exit %d, the load error must stay counted\n%s%s", code, out, errb)
	}
}

// TestPatternErrorFirstLine는 first: 발췌가 go list 문구의 첫 줄에서 다음 줄을 소개하는 절(`; to add it:`)을 떼는지
// 본다 — 예전에는 "…; to add it"으로 끊긴 채 실렸다.
func TestPatternErrorFirstLine(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	_, out, errb := run(t, "cycles", "--dir", dir, "--pattern", "example.com/missing")
	if !strings.Contains(out, "(first: no required module provides package example.com/missing); ") ||
		strings.Contains(out, "to add it") {
		t.Errorf("first: must end at the error itself:\n%s%s", out, errb)
	}
}

// TestEmptyHarvestIsthmusDocuments는 schema·routes 문서가 패턴 오류를 기존 접두사의 로드 오류 수로 세는지 본다
// (isthmus 접두사 목록은 닫혀 있어 새 접두사를 만들지 않는다).
func TestEmptyHarvestIsthmusDocuments(t *testing.T) {
	dir, cases := emptyHarvestCases(t)
	noMod := cases[2].args[1]
	limitations := func(t *testing.T, args ...string) []string {
		t.Helper()
		code, out, errb := run(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, errb)
		}
		var doc struct {
			Limitations []string `json:"limitations"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return doc.Limitations
	}
	has := func(list []string, prefix string) bool {
		for _, l := range list {
			if strings.HasPrefix(l, prefix) {
				return true
			}
		}
		return false
	}
	if l := limitations(t, "schema", "--dir", noMod); !has(l, "unparsed-sources: 1 package load or parse error(s)") {
		t.Errorf("schema without go.mod: %v", l)
	}
	for _, args := range [][]string{
		{"routes", "--dir", dir, "--pattern", "./interal/..."},
		{"routes", "--dir", noMod},
	} {
		if l := limitations(t, args...); !has(l, "route-coverage: 1 package load or parse error(s)") {
			t.Errorf("%v: %v", args, l)
		}
	}
	if l := limitations(t, "routes", "--role", "client", "--dir", dir, "--pattern", "./interal/..."); !has(l, "route-call-coverage: 1 package load or parse error(s)") {
		t.Errorf("routes --role client: %v", l)
	}
}

// TestEmptyHarvestNoMatch는 오류 없이 아무 패키지도 고르지 않은 패턴(Go 파일이 없는 디렉터리)도 조용히 지나가지
// 않는지 본다 — go list는 경고만 하고 패키지를 하나도 돌려주지 않는다.
func TestEmptyHarvestNoMatch(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":     "package main\n\nfunc main() {}\n",
		"docs/README": "no Go files here\n",
	})
	code, out, errb := run(t, "dead", "--strict", "--dir", dir, "--pattern", "./docs/...")
	if code != 0 || !strings.Contains(out, "no packages matched the patterns (./docs/...)") || strings.Contains(out, "use --retain-public") {
		t.Errorf("dead: exit %d\n%s%s", code, out, errb)
	}
	// 정상 수확에는 두 limitation 모두 없다.
	if _, out, _ := run(t, "cycles", "--dir", dir); strings.Contains(out, patternErrorNote) || strings.Contains(out, "no packages matched") {
		t.Errorf("a clean harvest must stay quiet:\n%s", out)
	}
	// 패턴 하나만 틀리면 나머지 수확은 그대로이고 그 패턴만 센다 — 다른 패키지가 수확됐으니 "아무것도 적재하지
	// 못했다"고 쓰지 않는다.
	code, out, errb = run(t, "dead", "--dir", dir, "--pattern", "./...", "--pattern", "./interal/...")
	if code != 0 || !strings.Contains(out, "1 "+patternErrorNote) || !strings.Contains(out, partialLoadNote) ||
		strings.Contains(out, nothingLoadedNote) || !strings.Contains(out, "retention roots") {
		t.Errorf("mixed patterns: exit %d\n%s%s", code, out, errb)
	}
}

// TestEmptyHarvestRoutesNoMatch는 routes가 아무 패키지도 스캔하지 못한 패턴을 기존 isthmus 접두사로 세는지 본다.
// 예전에는 Go 파일이 없는 디렉터리의 --pattern ./docs/...가 사실도 limitation도 없는 문서를 냈고, isthmus는 그런
// 서버 문서를 "선언한 라우트 없음"으로 읽어 호출을 route-call-without-decl로 냈다. 접두사 목록은 닫혀 있어
// route-coverage:·route-call-coverage:에 싣는다.
func TestEmptyHarvestRoutesNoMatch(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":     "package main\n\nimport \"net/http\"\n\nfunc main() { http.HandleFunc(\"/a\", nil) }\n",
		"docs/README": "no Go files here\n",
	})
	routeLimitations := func(args ...string) []string {
		t.Helper()
		code, out, errb := run(t, append([]string{"routes", "--dir", dir}, args...)...)
		var doc struct {
			Limitations []string `json:"limitations"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &doc) != nil {
			t.Fatalf("routes %v: exit %d\n%s%s", args, code, out, errb)
		}
		return doc.Limitations
	}
	want := map[string]string{
		"server": "route-coverage: no package of the module was scanned for the patterns (./docs/...); route registrations were not harvested",
		"client": "route-call-coverage: no package of the module was scanned for the patterns (./docs/...); HTTP calls were not harvested",
	}
	for _, role := range []string{"server", "client"} {
		if got := routeLimitations("--role", role, "--pattern", "./docs/..."); len(got) != 1 || got[0] != want[role] {
			t.Errorf("routes --role %s --pattern ./docs/...: limitations %q, want [%q]", role, got, want[role])
		}
		// 정상 수확(패턴 없음)과 패키지를 고른 패턴에는 이 limitation이 없다.
		for _, args := range [][]string{{"--role", role}, {"--role", role, "--pattern", "./..."}} {
			for _, l := range routeLimitations(args...) {
				if strings.Contains(l, "no package of the module was scanned") {
					t.Errorf("routes %v must stay quiet about scanning: %q", args, l)
				}
			}
		}
	}
}

// TestInitReportsLimitations는 init이 설정을 쓰고 성공할 때도 수확 limitation을 stderr에 싣는지 본다. 예전에는
// `--pattern ./lib --pattern ./interal/...`이 lib만 담은 설정을 쓰고 아무 표시 없이 0으로 끝났다. 표준 출력과
// 종료 코드는 그대로다.
func TestInitReportsLimitations(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":    "package main\n\nimport \"example.com/fixture/lib\"\n\nfunc main() { lib.Used() }\n",
		"lib/lib.go": "package lib\n\nfunc Used() {}\n",
	})
	code, out, errb := run(t, "init", "--dir", dir, "--pattern", "./lib", "--pattern", "./interal/...")
	if code != 0 || !strings.HasPrefix(out, "wrote ") {
		t.Fatalf("init: exit %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(errb, "limitation: 1 "+patternErrorNote+" (first: pattern ./interal/...") ||
		!strings.Contains(errb, partialLoadNote) {
		t.Errorf("init must report the pattern error on stderr after writing the config:\n%s", errb)
	}
}
