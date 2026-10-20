package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// toolAnswer는 MCP 도구 결과 하나다 — 본문 텍스트와 isError 표시.
type toolAnswer struct {
	text    string
	isError bool
}

// mcpCall은 tools/call 요청 한 줄을 만든다. arguments가 빈 문자열이면 arguments 키를 뺀다 — MCP에서
// tools/call의 arguments는 선택 필드다.
func mcpCall(id int, tool, arguments string) string {
	if arguments == "" {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q}}`, id, tool)
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
		id, tool, arguments)
}

// runMcpTools는 실제 mcp 명령 경로(mcpRun: 플래그 해석·설정·수확·서빙)에 요청을 보내 도구 결과를 차례로
// 돌려준다. CLI와 같은 플래그로 얻은 문서 위의 답을 CLI 출력과 바이트로 견주려고 쓴다.
func runMcpTools(t *testing.T, flags []string, calls ...string) []toolAnswer {
	t.Helper()
	var out, errb bytes.Buffer
	if code := mcpRun(flags, strings.NewReader(strings.Join(calls, "\n")+"\n"), &out, &errb); code != 0 {
		t.Fatalf("mcp %v exited %d: %s", flags, code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(calls) {
		t.Fatalf("expected %d responses, got %d: %s", len(calls), len(lines), out.String())
	}
	answers := make([]toolAnswer, len(lines))
	for i, line := range lines {
		var resp struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &resp); err != nil || len(resp.Result.Content) != 1 {
			t.Fatalf("response %d is not one tool result: %v %q", i+1, err, line)
		}
		answers[i] = toolAnswer{resp.Result.Content[0].Text, resp.Result.IsError}
	}
	return answers
}

// cliReport는 CLI를 실행해 종료 코드 0을 요구하고 표준 출력을 돌려준다.
func cliReport(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errb := run(t, args...)
	if code != 0 {
		t.Fatalf("%v exited %d: %s", args, code, errb)
	}
	return out
}

// requireCLIReport는 MCP 답이 isError가 아니고 CLI JSON 출력과 같은 바이트(CLI는 끝 개행 하나를 더 쓴다)인지 본다.
// 서버에는 baseline이 없어, baseline 없이 돈 CLI 보고와 키까지 같아야 한다.
func requireCLIReport(t *testing.T, label string, answer toolAnswer, cliOut string) {
	t.Helper()
	if answer.isError || answer.text+"\n" != cliOut {
		t.Fatalf("%s: MCP must answer with the CLI report:\nMCP (isError %v) %s\nCLI %s",
			label, answer.isError, answer.text, cliOut)
	}
}

// reportParityFixture는 CLI와 MCP 보고가 갈라지던 모양을 한데 담은 라이브러리 모듈이다. main이 없어 문서
// 루트가 없고, reflect를 import하고(문서 limitation), 모듈 밖 인터페이스(fmt.Stringer)를 만족하는 메서드와
// 이름으로 읽지 않는 필드가 unreachable로 남고, 공개 서명이 다른 컴포넌트 타입을 쓰는 signature 위반이 있다.
func reportParityFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"lib/lib.go": `package lib

import (
	"reflect"

	"example.com/fixture/api"
)

type Name struct {
	value string
	Spare int
}

func (n Name) String() string { return n.value }

func Kind(v any) string { return reflect.TypeOf(v).String() }

func Handle(r api.Req) Name { return Name{value: r.ID} }
`,
		"api/api.go": `package api

type Req struct{ ID string }
`,
		".gartograph.yml": `components:
  lib: [lib]
  api: [api]
deps:
  lib: [api]
signature:
  lib: []
`,
	})
}

// TestMcpDeadReportMatchesCLI는 같은 수확에서 MCP gartograph_dead가 CLI `dead --format json`과 같은 바이트를
// 내는지 본다. 따로 조립하던 때 MCP는 algorithm을 비우고 알 수 없는 루트·main 없음·외부 디스패치·필드
// limitation을 빠뜨려, main 없는 라이브러리의 공개 API 전부가 unreachable인 보고가 그 이유 없이 나갔다.
func TestMcpDeadReportMatchesCLI(t *testing.T) {
	dir := reportParityFixture(t)
	cases := []struct {
		name      string
		flags     []string
		arguments string
	}{
		{"default roots", nil, `{}`},
		{"retain public", []string{"--retain-public"}, `{"retainPublic":true}`},
		{"extra and unknown roots", []string{"--root", "example.com/fixture/lib.Kind", "--root", "ghost"},
			`{"roots":["example.com/fixture/lib.Kind","ghost"]}`},
	}
	calls := make([]string, len(cases))
	for i, c := range cases {
		calls[i] = mcpCall(i+1, "gartograph_dead", c.arguments)
	}
	answers := runMcpTools(t, []string{"--dir", dir}, calls...)
	for i, c := range cases {
		cli := cliReport(t, append([]string{"dead", "--format", "json", "--dir", dir}, c.flags...)...)
		requireCLIReport(t, c.name, answers[i], cli)
	}
	// 같은 바이트가 빈 보고끼리의 일치가 아니게, 갈라지던 사실이 실제로 실렸는지 본다.
	var plain, rooted deadReport
	if err := json.Unmarshal([]byte(answers[0].text), &plain); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(answers[2].text), &rooted); err != nil {
		t.Fatal(err)
	}
	if plain.Algorithm != "cha" {
		t.Fatalf("MCP dead must name its algorithm: %q", plain.Algorithm)
	}
	for _, want := range []string{"no main or init roots found", "packages import reflect",
		"methods implementing interfaces declared outside the module", "field reachability counts named accesses"} {
		if !hasLimitation(plain.Limitations, "", want) {
			t.Fatalf("fixture must carry the %q limitation: %v", want, plain.Limitations)
		}
	}
	if !hasLimitation(rooted.Limitations, `retention root "ghost"`, "is not in the graph") {
		t.Fatalf("an unknown extra root must be a limitation: %v", rooted.Limitations)
	}
}

// TestMcpRulesReportMatchesCLI는 MCP gartograph_rules가 같은 문서·같은 규칙 파일의 CLI `rules --format json`과
// 같은 바이트를 내는지 본다. 따로 조립하던 때 MCP는 문서 limitation만 실어, `mcp --level package`에서
// signature 규칙을 검사하지 못했는데도 위반 0을 아무 말 없이 돌려줬고(CLI `rules --graph`는 signature checks
// skipped를 단다), 위치 없는 import 간선의 fileScope 미검사 수도 빠뜨렸다.
func TestMcpRulesReportMatchesCLI(t *testing.T) {
	dir := reportParityFixture(t)
	packageDoc := filepath.Join(t.TempDir(), "package.json")
	cliReport(t, "graph", "--level", "package", "--dir", dir, "--out", packageDoc)
	packageLevel := runMcpTools(t, []string{"--dir", dir, "--level", "package"}, mcpCall(1, "gartograph_rules", `{}`))
	cliPackage := cliReport(t, "rules", "--format", "json", "--dir", dir, "--graph", packageDoc)
	requireCLIReport(t, "package level", packageLevel[0], cliPackage)
	if !strings.Contains(cliPackage, "signature checks skipped") {
		t.Fatalf("a package-level document must say signature rules were not checked: %s", cliPackage)
	}

	// mcp 기본(symbol) 수확은 CLI 라이브 rules(signature 규칙이 있으면 symbol로 수확)와 같은 위반을 찾는다.
	symbolLevel := runMcpTools(t, []string{"--dir", dir}, mcpCall(1, "gartograph_rules", `{}`))
	cliLive := cliReport(t, "rules", "--format", "json", "--dir", dir)
	requireCLIReport(t, "symbol level", symbolLevel[0], cliLive)
	if !strings.Contains(cliLive, `"rule": "signature"`) {
		t.Fatalf("fixture must violate the signature rule: %s", cliLive)
	}

	// 위치를 싣지 않은 저장 문서의 fileRules 대상 import는 fileScope 미검사로 센다.
	scratch := t.TempDir()
	positionless := filepath.Join(scratch, "positionless.json")
	rules := filepath.Join(scratch, "rules.yml")
	writeTestFile(t, positionless, `{"version":2,"level":"package","module":"example.com/m","vertices":[
{"id":"example.com/m/web","kind":"package"},{"id":"example.com/m/db","kind":"package"}],
"edges":[{"from":"example.com/m/web","to":"example.com/m/db","kind":"import"}]}`, 0o644)
	writeTestFile(t, rules, `components: {web: [web], db: [db]}
deps: {web: [db]}
fileRules:
  - {name: prod-only, from: "!*_test.go", to: db}
`, 0o644)
	fileScope := runMcpTools(t, []string{"--graph", positionless, "--config", rules}, mcpCall(1, "gartograph_rules", `{}`))
	cliFileScope := cliReport(t, "rules", "--format", "json", "--graph", positionless, "--config", rules)
	requireCLIReport(t, "positionless fileRules", fileScope[0], cliFileScope)
	if !strings.Contains(cliFileScope, "file scope could not be checked") {
		t.Fatalf("a positionless import to a fileRules target must be counted: %s", cliFileScope)
	}
}

// TestMcpCyclesReportMatchesCLI는 MCP gartograph_cycles가 CLI `cycles --format json`의 봉투({cycles,
// limitations})를 같은 바이트로 내는지 본다. 예전에는 맨 배열만 돌려줘 문서 limitation(생략한 외부 import
// 등)이 빠졌다 — 순환 0개가 "다 봤다"인지 "못 본 곳이 있다"인지 소비자가 가를 수 없었다.
func TestMcpCyclesReportMatchesCLI(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

import "strings"

func A(s string) string { return B(strings.TrimSpace(s)) }
func B(s string) string { return A(s) }
`,
	})
	symbolDoc := filepath.Join(t.TempDir(), "symbol.json")
	cliReport(t, "graph", "--level", "symbol", "--dir", dir, "--out", symbolDoc)
	answers := runMcpTools(t, []string{"--dir", dir},
		mcpCall(1, "gartograph_cycles", `{}`),
		mcpCall(2, "gartograph_cycles", `{"level":"symbol"}`),
		mcpCall(3, "gartograph_cycles", `{"level":"package"}`))
	live := cliReport(t, "cycles", "--format", "json", "--level", "symbol", "--dir", dir)
	requireCLIReport(t, "document level", answers[0], live)
	requireCLIReport(t, "symbol level", answers[1], live)
	// 더 거친 투영은 같은 symbol 문서로 견준다 — CLI가 package 레벨로 새로 수확하면 문서 limitation이 다르다.
	requireCLIReport(t, "package projection", answers[2],
		cliReport(t, "cycles", "--format", "json", "--level", "package", "--graph", symbolDoc))
	var report cyclesReport
	if err := json.Unmarshal([]byte(answers[0].text), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Cycles) != 1 || len(report.Limitations) == 0 {
		t.Fatalf("fixture must have one cycle and a document limitation: %s", answers[0].text)
	}
}

// TestMcpMetricsAndMappingMatchCLI는 MCP gartograph_metrics·gartograph_mapping이 같은 문서·같은 규칙 파일의
// CLI `metrics`·`mapping --format json`과 같은 바이트를 내는지 본다. metrics는 따로 조립하던 때 패키지 레벨
// 문서에서 abstractness/distance가 빠진 이유를 싣지 않았고 limitation을 정렬하지도 않았다.
func TestMcpMetricsAndMappingMatchCLI(t *testing.T) {
	cases := []struct {
		name  string
		dir   string
		tools []string
	}{
		{"with rules file", reportParityFixture(t), []string{"metrics", "mapping"}},
		{"without rules file", fixture(t), []string{"metrics"}},
	}
	for _, c := range cases {
		packageDoc := filepath.Join(t.TempDir(), "package.json")
		cliReport(t, "graph", "--level", "package", "--dir", c.dir, "--out", packageDoc)
		calls := make([]string, len(c.tools))
		for i, tool := range c.tools {
			calls[i] = mcpCall(i+1, "gartograph_"+tool, `{}`)
		}
		answers := runMcpTools(t, []string{"--dir", c.dir, "--graph", packageDoc}, calls...)
		for i, tool := range c.tools {
			cli := cliReport(t, tool, "--format", "json", "--dir", c.dir, "--graph", packageDoc)
			requireCLIReport(t, c.name+"/"+tool, answers[i], cli)
			if tool == "metrics" && !strings.Contains(cli, "abstractness/distance omitted") {
				t.Fatalf("%s: a package-level document must say why A/D are missing: %s", c.name, cli)
			}
		}
	}
}

// TestMcpRejectsMalformedArguments는 형식이 틀린 도구 인자가 기본값으로 조용히 바뀌지 않고 isError가 되는지
// 본다. 예전에는 cycles·dead가 인자 해석 오류를 버려, {"retainPublic":"true"}가 공개 API를 루트로 삼지 않은
// 보고(roots 0)를 오류 없이 돌려줬다. arguments가 없거나 null인 호출은 MCP에서 정상이라 빈 인자와 같은 답이다.
func TestMcpRejectsMalformedArguments(t *testing.T) {
	for _, c := range []struct{ tool, arguments string }{
		{"gartograph_dead", `{"retainPublic":"true"}`},
		{"gartograph_dead", `{"roots":"pkg.a"}`},
		{"gartograph_dead", `[]`},
		{"gartograph_cycles", `{"level":3}`},
	} {
		_, err := testMcpServer().runTool(c.tool, json.RawMessage(c.arguments))
		if err == nil || !strings.HasPrefix(err.Error(), "invalid arguments: ") {
			t.Fatalf("%s %s must be rejected as invalid arguments, got %v", c.tool, c.arguments, err)
		}
	}
	res := mcpSession(t, testMcpServer(), mcpCall(1, "gartograph_dead", `{"retainPublic":"true"}`))
	result := res[0]["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if result["isError"] != true || !strings.Contains(text, "retainPublic") {
		t.Fatalf("the rejection must be an isError result naming the argument: %v", result)
	}
	for _, tool := range []string{"gartograph_dead", "gartograph_cycles"} {
		empty := mcpSession(t, testMcpServer(), mcpCall(1, tool, `{}`))
		for _, arguments := range []string{"", "null"} {
			if got := mcpSession(t, testMcpServer(), mcpCall(1, tool, arguments)); !reflect.DeepEqual(got, empty) {
				t.Fatalf("%s without arguments (%q) must answer like {}:\ngot  %v\nwant %v", tool, arguments, got, empty)
			}
		}
	}
}
