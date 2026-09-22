package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
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
	if len(tools) != 7 {
		t.Fatalf("expected 7 tools, got %v", tools)
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
		".gartograph.yml": `components:
  app: ["."]
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
	)
	texts := make([]string, len(res))
	for i, r := range res {
		result := r["result"].(map[string]any)
		if result["isError"] == true {
			t.Fatalf("tool call %d errored: %v", i, result)
		}
		texts[i] = result["content"].([]any)[0].(map[string]any)["text"].(string)
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
