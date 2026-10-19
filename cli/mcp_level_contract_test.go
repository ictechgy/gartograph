package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/export"
	"github.com/ictechgy/gartograph/graph"
)

// TestMcpAnalysisLevelParity는 얕은 저장 문서를 빈 성공 결과로 오독하지 않게 CLI와 같은 오류를 요구한다.
// cycles의 module 투영은 Rank가 아니라 문서 레벨 자체를 본다 — module 정점은 --level module
// 수확만 만들어, 더 세밀한 문서를 module로 투영하면 순환이 있어도 빈 성공이 된다.
func TestMcpAnalysisLevelParity(t *testing.T) {
	cases := []struct {
		command string
		want    graph.Level // cycles가 요청하는 투영 레벨(dead는 늘 symbol)
	}{
		{"dead", graph.LevelSymbol},
		{"cycles", graph.LevelSymbol},
		{"cycles", graph.LevelModule},
	}
	for _, level := range []graph.Level{graph.LevelModule, graph.LevelPackage, graph.LevelType, graph.LevelSymbol} {
		for _, c := range cases {
			t.Run(string(level)+"/"+c.command+"/"+string(c.want), func(t *testing.T) {
				doc := &graph.Document{Version: graph.Version, Tool: graph.Tool, Level: level}
				path := filepath.Join(t.TempDir(), "graph.json")
				if err := export.SaveFile(doc, path); err != nil {
					t.Fatal(err)
				}
				args := []string{c.command, "--graph", path}
				arguments := `{}`
				if c.command == "cycles" {
					args = append(args, "--level", string(c.want))
					arguments = `{"level":"` + string(c.want) + `"}`
				}
				code, _, stderr := run(t, args...)
				responses := mcpSession(t, &mcpServer{doc: doc},
					`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gartograph_`+c.command+`","arguments":`+arguments+`}}`)
				result := responses[0]["result"].(map[string]any)
				wantError := level.Rank() < c.want.Rank()
				if c.want == graph.LevelModule {
					wantError = level != graph.LevelModule
				}
				if (code == 2) != wantError || (result["isError"] == true) != wantError {
					t.Fatalf("CLI/MCP must agree on insufficient level: code=%d stderr=%s result=%v", code, stderr, result)
				}
				if wantError {
					text := result["content"].([]any)[0].(map[string]any)["text"].(string)
					if !strings.Contains(text, "re-harvest with --level "+string(c.want)) || !strings.Contains(stderr, text) {
						t.Fatalf("CLI/MCP must share actionable level error: stderr=%q MCP=%q", stderr, text)
					}
				}
			})
		}
	}
}

// TestMcpCyclesAllowsCoarserLevel은 충분한 문서의 기본·동일·낮은 해상도 질의를 계속 허용한다.
// module 문서의 module 투영은 순환을 그대로 찾아야 한다 — 거부 조건이 module 문서까지 막으면
// go.work 워크스페이스의 모듈 순환 검사가 사라진다.
func TestMcpCyclesAllowsCoarserLevel(t *testing.T) {
	for _, level := range []graph.Level{graph.LevelPackage, graph.LevelType, graph.LevelSymbol} {
		args, err := json.Marshal(map[string]string{"level": string(level)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := testMcpServer().runTool("gartograph_cycles", args); err != nil {
			t.Fatalf("supported %s projection failed: %v", level, err)
		}
	}
	moduleDoc := &graph.Document{Version: graph.Version, Tool: graph.Tool, Level: graph.LevelModule,
		Vertices: []graph.Vertex{
			{ID: "example.com/a", Kind: graph.KindModule, Name: "example.com/a"},
			{ID: "example.com/b", Kind: graph.KindModule, Name: "example.com/b"},
		},
		Edges: []graph.Edge{
			{From: "example.com/a", To: "example.com/b", Kind: graph.EdgeImport},
			{From: "example.com/b", To: "example.com/a", Kind: graph.EdgeImport},
		}}
	for _, arguments := range []string{`{"level":"module"}`, `{}`} {
		text, err := (&mcpServer{doc: moduleDoc}).runTool("gartograph_cycles", json.RawMessage(arguments))
		if err != nil || !strings.Contains(text, `"example.com/a"`) {
			t.Fatalf("module document must keep its module cycle for %s: %q %v", arguments, text, err)
		}
	}
	path := filepath.Join(t.TempDir(), "module.json")
	if err := export.SaveFile(moduleDoc, path); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := run(t, "cycles", "--graph", path, "--level", "module", "--strict")
	if code != 1 || !strings.Contains(out, "cycle: [example.com/a example.com/b]") {
		t.Fatalf("CLI module projection of a module document must find the cycle: code=%d out=%q stderr=%q",
			code, out, stderr)
	}
}
