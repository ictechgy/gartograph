package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/analysis"
	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
	"github.com/ictechgy/gartograph/source"
)

// mcpSession은 NDJSON 요청을 서버에 밀어 넣고 응답 줄들을 돌려준다.
func mcpSession(t *testing.T, srv *mcpServer, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if code := srv.serve(in, &out, io.Discard); code != 0 {
		t.Fatalf("serve exited %d", code)
	}
	var res []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("response is not JSON: %q", l)
		}
		res = append(res, m)
	}
	return res
}

// testMcpServer는 두 정점·한 호출 간선의 최소 문서를 서빙한다.
func testMcpServer() *mcpServer {
	return &mcpServer{protocol: "2024-11-05", doc: &graph.Document{
		Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "pkg.a", Kind: graph.KindFunc},
			{ID: "pkg.b", Kind: graph.KindFunc},
		},
		Edges: []graph.Edge{
			{From: "pkg.a", To: "pkg.b", Kind: graph.EdgeCall},
		},
	}}
}

// TestMcpHandshake는 initialize·tools/list·알림 무시를 확인한다.
func TestMcpHandshake(t *testing.T) {
	res := mcpSession(t, testMcpServer(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(res) != 2 {
		t.Fatalf("notification must not produce a response, got %d", len(res))
	}
	init := res[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocol echo failed: %v", init)
	}
	tools := res[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 10 {
		t.Fatalf("expected 10 tools, got %v", tools)
	}
}

// TestMcpToolCall은 query 도구가 실제 그래프 위에서 답하는지 확인한다.
func TestMcpToolCall(t *testing.T) {
	res := mcpSession(t, testMcpServer(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gartograph_query","arguments":{"id":"pkg.b","depth":1}}}`,
	)
	result := res[0]["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	var n analysis.Neighbors
	if err := json.Unmarshal([]byte(text), &n); err != nil {
		t.Fatalf("tool result is not the query JSON: %v", err)
	}
	if len(n.DependedBy) != 1 || n.DependedBy[0].ID != "pkg.a" {
		t.Fatalf("query over MCP returned wrong neighbors: %+v", n)
	}
}

// TestMcpToolError는 없는 도구·없는 정점이 isError 결과로 나가는지 확인한다.
// 프로토콜 오류가 아니라 도구 결과여야 클라이언트가 본문을 읽는다.
func TestMcpToolError(t *testing.T) {
	res := mcpSession(t, testMcpServer(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gartograph_query","arguments":{"id":"missing"}}}`,
	)
	for _, r := range res {
		result := r["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("expected isError result, got %v", r)
		}
	}
}

// TestMcpToolsCovered는 나머지 도구들이 문서 위에서 응답하는지 확인한다.
// summary·cycles·dead·rules는 각각 다른 분석 경로를 탄다.
func TestMcpToolsCovered(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() { run() }
func run() {}
func unused() {}
`,
		// ghost는 어느 패키지와도 맞지 않는다 — mapping의 빈 목록이 null이면 안 된다.
		".gartograph.yml": `components:
  app: ["."]
  ghost: ["nothing/**"]
deps: {}
`,
	})
	srv := &mcpServer{dir: dir, cfgPath: filepath.Join(dir, ".gartograph.yml"),
		protocol: "2024-11-05"}
	doc, err := source.Load(source.Options{Dir: dir, Level: graph.LevelSymbol})
	if err != nil {
		t.Fatal(err)
	}
	srv.doc = doc

	res := mcpSession(t, srv,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gartograph_summary","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gartograph_cycles","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"gartograph_dead","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"gartograph_rules","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"gartograph_impact","arguments":{"id":"example.com/fixture.run"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"gartograph_path","arguments":{"from":"example.com/fixture.main","to":"example.com/fixture.run"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"gartograph_metrics","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"gartograph_mapping","arguments":{}}}`,
	)
	texts := make([]string, len(res))
	for i, r := range res {
		result := r["result"].(map[string]any)
		if result["isError"] == true {
			t.Fatalf("tool call %d errored: %v", i, result)
		}
		texts[i] = result["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	// 결과가 없는 목록(순환 0개, limitation 없음, 빈 컴포넌트)도 null이 아니라 []다.
	for i, text := range texts {
		if strings.Contains(text, "null") {
			t.Fatalf("tool call %d must not emit null lists: %s", i, text)
		}
	}
	if !strings.Contains(texts[0], `"vertices"`) {
		t.Fatalf("summary missing counts: %s", texts[0])
	}
	if !strings.Contains(texts[2], "fixture.unused") {
		t.Fatalf("dead must report unused over MCP: %s", texts[2])
	}
	if !strings.Contains(texts[4], `"dependers"`) {
		t.Fatalf("impact missing dependers: %s", texts[4])
	}
	if !strings.Contains(texts[5], `"found": true`) {
		t.Fatalf("path must find main->run over MCP: %s", texts[5])
	}
	if !strings.Contains(texts[6], `"afferent"`) {
		t.Fatalf("metrics missing couplings over MCP: %s", texts[6])
	}
	if !strings.Contains(texts[7], `"app"`) {
		t.Fatalf("mapping missing components over MCP: %s", texts[7])
	}
}

// TestMcpReusesQueryIndex는 MCP 서버가 첫 도구 호출에서 만든 질의 색인(ID 색인·인접 목록)을 다음
// 호출에 다시 쓰는지 본다. 문서는 기동 때 한 번 수확하고 바뀌지 않으므로(README) 호출마다 색인을 다시
// 만들 이유가 없다 — 다시 만들면 답이 몇 줄이어도 호출마다 문서 크기만큼 기다린다. 재사용을 밖에서
// 보려고 첫 호출들 뒤 문서에 간선(c→b)을 더한다: 색인을 다시 쓰면 두 번째 답이 첫 답과 같고, 호출마다
// 만들면 새 간선이 답에 나타난다. 실제 서버의 문서는 바뀌지 않는다.
func TestMcpReusesQueryIndex(t *testing.T) {
	srv := &mcpServer{protocol: "2024-11-05", doc: &graph.Document{
		Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "pkg.a", Kind: graph.KindFunc},
			{ID: "pkg.b", Kind: graph.KindFunc},
			{ID: "pkg.c", Kind: graph.KindFunc},
		},
		Edges: []graph.Edge{{From: "pkg.a", To: "pkg.b", Kind: graph.EdgeCall}},
	}}
	calls := []struct{ tool, args string }{
		{"gartograph_query", `{"id":"pkg.b"}`},
		{"gartograph_impact", `{"id":"pkg.b"}`},
		{"gartograph_path", `{"from":"pkg.c","to":"pkg.b"}`},
		{"gartograph_shared", `{"roots":["pkg.a","pkg.c"]}`},
		{"gartograph_dead", `{"roots":["pkg.c"]}`},
	}
	first := make([]string, len(calls))
	for i, c := range calls {
		text, err := srv.runTool(c.tool, json.RawMessage(c.args))
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		first[i] = text
	}
	srv.doc.Edges = append(srv.doc.Edges, graph.Edge{From: "pkg.c", To: "pkg.b", Kind: graph.EdgeCall})
	for i, c := range calls {
		text, err := srv.runTool(c.tool, json.RawMessage(c.args))
		if err != nil || text != first[i] {
			t.Fatalf("%s must answer from the index built on the first call, not rebuild it per call:\n"+
				"first  %s\nsecond %s (err %v)", c.tool, first[i], text, err)
		}
	}
}

// TestMcpIndexMatchesFreshServer는 한 서버가 질의 색인을 나눠 쓰며 답한 세션과, 요청마다 새 서버(새
// 색인)로 답한 결과가 같은지 본다. 도구 순서를 섞어 한 도구가 만든 색인을 다른 도구가 그대로 쓰게
// 하고, 외부 인터페이스 디스패치·인터페이스 팬아웃·빈 식별자 루트가 있는 실제 수확 문서와 없는
// 정점·없는 루트의 답도 넣는다 — 색인을 나눠 쓰는 것은 비용만 줄여야 한다.
func TestMcpIndexMatchesFreshServer(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Run(lib.New()) }

func init() { helper() }

func helper() {}

func unused() {}
`,
		"lib/lib.go": `package lib

type Store interface{ Get() string }

type mem struct{ v string }

func (m *mem) Get() string { return m.v }

func (m *mem) Error() string { return m.v }

func New() Store { return &mem{} }

func Run(s Store) { _ = s.Get() }

var _ Store = (*mem)(nil)

func Orphan() {}
`,
	})
	doc, err := source.Load(source.Options{Dir: dir, Level: graph.LevelSymbol})
	if err != nil {
		t.Fatal(err)
	}
	const m, lib = "example.com/fixture.", "example.com/fixture/lib."
	for _, id := range []string{m + "main", m + "helper", m + "unused", lib + "Run", lib + "Store",
		lib + "(mem).Get", lib + "Orphan"} {
		if !doc.HasVertex(id) {
			t.Fatalf("fixture lacks vertex %s", id)
		}
	}
	call := func(id int, tool, args string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
			id, tool, args)
	}
	lines := []string{
		call(1, "gartograph_query", `{"id":"`+m+`main"}`),
		call(2, "gartograph_impact", `{"id":"`+lib+`(mem).Get"}`),
		call(3, "gartograph_path", `{"from":"`+m+`main","to":"`+lib+`(mem).Get"}`),
		call(4, "gartograph_shared", `{"roots":["`+m+`main","`+lib+`Run"]}`),
		call(5, "gartograph_dead", `{}`),
		call(6, "gartograph_dead", `{"retainPublic":true,"roots":["`+m+`unused","ghost"]}`),
		call(7, "gartograph_query", `{"id":"`+lib+`Store","depth":2,"max":1}`),
		call(8, "gartograph_impact", `{"id":"ghost"}`),
		call(9, "gartograph_path", `{"from":"`+m+`main","to":"ghost"}`),
		call(10, "gartograph_shared", `{"roots":["`+m+`helper","ghost"]}`),
		call(11, "gartograph_impact", `{"id":"`+lib+`(mem).Get","depth":1,"max":1}`),
		call(12, "gartograph_query", `{"id":"`+lib+`Run","depth":3}`),
		call(13, "gartograph_dead", `{"roots":["`+lib+`Orphan"]}`),
		call(14, "gartograph_path", `{"from":"`+lib+`Orphan","to":"`+m+`main"}`),
		call(15, "gartograph_shared", `{"roots":["`+m+`main","`+m+`helper","`+lib+`Store"]}`),
		call(16, "gartograph_cycles", `{}`),
	}
	reused := mcpSession(t, &mcpServer{doc: doc, protocol: "2024-11-05"}, lines...)
	if len(reused) != len(lines) {
		t.Fatalf("expected %d responses, got %d", len(lines), len(reused))
	}
	sawError := false
	for i, line := range lines {
		fresh := mcpSession(t, &mcpServer{doc: doc, protocol: "2024-11-05"}, line)
		if !reflect.DeepEqual(reused[i], fresh[0]) {
			t.Fatalf("call %d answered differently with a reused index:\nreused %v\nfresh  %v", i+1, reused[i], fresh[0])
		}
		sawError = sawError || reused[i]["result"].(map[string]any)["isError"] == true
	}
	if !sawError {
		t.Fatal("the session must include error answers for missing vertices")
	}
}

// benchmarkMcpDocument는 반복 질의 벤치마크용 합성 심볼 문서다(패키지 200개 × 함수 100개, 함수마다
// 호출 3개). 대형 testdata 파일 대신 고정 시드로 매번 같은 문서를 만든다.
func benchmarkMcpDocument() *graph.Document {
	r := rand.New(rand.NewSource(1))
	d := &graph.Document{Version: graph.Version, Tool: graph.Tool, Level: graph.LevelSymbol,
		Module: "example.com/big"}
	fn := func(p, s int) string { return fmt.Sprintf("example.com/big/p%03d.Fn%03d", p, s) }
	for p := 0; p < 200; p++ {
		pkg := fmt.Sprintf("example.com/big/p%03d", p)
		d.Vertices = append(d.Vertices, graph.Vertex{ID: pkg, Kind: graph.KindPackage})
		for s := 0; s < 100; s++ {
			d.Vertices = append(d.Vertices, graph.Vertex{ID: fn(p, s), Kind: graph.KindFunc, Package: pkg})
			d.Edges = append(d.Edges, graph.Edge{From: pkg, To: fn(p, s), Kind: graph.EdgeContains})
			for c := 0; c < 3; c++ {
				d.Edges = append(d.Edges, graph.Edge{From: fn(p, s), To: fn(r.Intn(p+1), r.Intn(100)),
					Kind: graph.EdgeCall})
			}
		}
	}
	d.Sort()
	return d
}

// BenchmarkMcpQuery는 한 MCP 서버에 depth 1 query를 거듭 묻는다(약 2만 정점). 호출마다 이웃 목록을
// 다시 만들면 호출당 비용이 문서 크기를 따르고, 첫 호출의 색인을 다시 쓰면 답의 크기를 따른다.
func BenchmarkMcpQuery(b *testing.B) {
	srv := &mcpServer{doc: benchmarkMcpDocument()}
	args := json.RawMessage(`{"id":"example.com/big/p100.Fn050","depth":1}`)
	for b.Loop() {
		if _, err := srv.runTool("gartograph_query", args); err != nil {
			b.Fatal(err)
		}
	}
}

// TestMcpRun은 플래그 파싱·수확·서빙이 이어진 명령 경로를 검증한다.
// 빈 입력은 즉시 EOF라 서버가 0으로 끝나야 하고, 나쁜 레벨은 2다.
func TestMcpRun(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})
	var out, errb bytes.Buffer
	code := mcpRun([]string{"--dir", dir}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("mcp on empty stdin: expected 0, got %d %s", code, errb.String())
	}
	code = mcpRun([]string{"--dir", dir, "--level", "bogus"},
		strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("mcp bad level: expected 2, got %d", code)
	}
}

// TestMcpConfigReadBeforeHarvest는 mcp가 수확 전에 --config 규칙 파일을 읽어, 없거나 깨진 파일이 기동 때 그
// 파일을 밝힌 오류(종료 2)가 되는지 본다 — 그 파일의 exclude가 수확 범위라, 읽지 못한 채 수확하면 범위가 틀린
// 문서를 서빙한다. 예전에는 서버가 떴고 rules 도구를 부를 때에야 isError였다. 저장 문서(--graph)는 수확하지
// 않으므로 지금처럼 도구 호출 때 읽는다.
func TestMcpConfigReadBeforeHarvest(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	scratch := t.TempDir()
	broken := filepath.Join(scratch, "broken.yml")
	writeTestFile(t, broken, "components: {a: [a]}\ndeps: {a: [ghost]}\n", 0o644)
	missing := filepath.Join(scratch, "missing.yml")
	for _, cfg := range []string{broken, missing} {
		var out, errb bytes.Buffer
		code := mcpRun([]string{"--dir", dir, "--config", cfg}, strings.NewReader(""), &out, &errb)
		if code != 2 || !strings.Contains(errb.String(), cfg) || out.Len() != 0 {
			t.Fatalf("mcp --config %s must stop at startup naming the file: code=%d stderr=%q stdout=%q",
				cfg, code, errb.String(), out.String())
		}
	}
	saved := filepath.Join(scratch, "graph.json")
	cliReport(t, "graph", "--dir", dir, "--out", saved)
	answers := runMcpTools(t, []string{"--graph", saved, "--config", missing}, mcpCall(1, "gartograph_rules", `{}`))
	if !answers[0].isError || !strings.Contains(answers[0].text, missing) {
		t.Fatalf("with --graph the rules file is read when the tool runs: %+v", answers[0])
	}
}

// TestMcpUnknownMethod는 알 수 없는 요청에 -32601을, 알림에는 침묵을 확인한다.
func TestMcpUnknownMethod(t *testing.T) {
	res := mcpSession(t, testMcpServer(),
		`{"jsonrpc":"2.0","id":1,"method":"bogus/method"}`,
		`{"jsonrpc":"2.0","method":"bogus/notify"}`,
	)
	if len(res) != 1 {
		t.Fatalf("notification must stay silent, got %d responses", len(res))
	}
	errObj := res[0]["error"].(map[string]any)
	if errObj["code"].(float64) != -32601 {
		t.Fatalf("expected -32601, got %v", errObj)
	}
}
