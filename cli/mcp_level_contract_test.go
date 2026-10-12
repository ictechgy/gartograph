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
func TestMcpAnalysisLevelParity(t *testing.T) {
	for _, level := range []graph.Level{graph.LevelModule, graph.LevelPackage, graph.LevelType, graph.LevelSymbol} {
		for _, command := range []string{"dead", "cycles"} {
			t.Run(string(level)+"/"+command, func(t *testing.T) {
				doc := &graph.Document{Version: graph.Version, Tool: graph.Tool, Level: level}
				path := filepath.Join(t.TempDir(), "graph.json")
				if err := export.SaveFile(doc, path); err != nil {
					t.Fatal(err)
				}
				args := []string{command, "--graph", path}
				arguments := `{}`
				if command == "cycles" {
					args = append(args, "--level", "symbol")
					arguments = `{"level":"symbol"}`
				}
				code, _, stderr := run(t, args...)
				responses := mcpSession(t, &mcpServer{doc: doc},
					`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gartograph_`+command+`","arguments":`+arguments+`}}`)
				result := responses[0]["result"].(map[string]any)
				wantError := level.Rank() < graph.LevelSymbol.Rank()
				if (code == 2) != wantError || (result["isError"] == true) != wantError {
					t.Fatalf("CLI/MCP must agree on insufficient level: code=%d stderr=%s result=%v", code, stderr, result)
				}
				if wantError {
					text := result["content"].([]any)[0].(map[string]any)["text"].(string)
					if !strings.Contains(text, "re-harvest with --level symbol") || !strings.Contains(stderr, text) {
						t.Fatalf("CLI/MCP must share actionable level error: stderr=%q MCP=%q", stderr, text)
					}
				}
			})
		}
	}
}

// TestMcpCyclesAllowsCoarserLevel은 충분한 문서의 기본·동일·낮은 해상도 질의를 계속 허용한다.
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
}
