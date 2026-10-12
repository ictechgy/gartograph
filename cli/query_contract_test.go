package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ictechgy/gartograph/export"
	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
	"github.com/ictechgy/gartograph/source"
)

// TestQueryLimitationsParity는 실제 수확의 한계가 CLI와 MCP에서 빠지지 않게 한다.
func TestQueryLimitationsParity(t *testing.T) {
	for _, external := range []bool{false, true} {
		name, content := "complete", "package main\nfunc main() {}\n"
		if external {
			name, content = "incomplete", "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hello\") }\n"
		}
		t.Run(name, func(t *testing.T) {
			dir := testutil.WriteModule(t, map[string]string{"main.go": content})
			doc, err := source.Load(source.Options{Dir: dir, Level: graph.LevelSymbol})
			if err != nil {
				t.Fatal(err)
			}
			if external && len(doc.Limitations) == 0 {
				t.Fatal("external reference fixture must have observed limitations")
			}
			path := filepath.Join(t.TempDir(), "graph.json")
			if err := export.SaveFile(doc, path); err != nil {
				t.Fatal(err)
			}
			code, output, stderr := run(t, "query", "example.com/fixture.main", "--graph", path)
			if code != 0 {
				t.Fatalf("query failed: %d %s", code, stderr)
			}
			responses := mcpSession(t, &mcpServer{doc: doc},
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gartograph_query","arguments":{"id":"example.com/fixture.main"}}}`)
			mcpResult := responses[0]["result"].(map[string]any)
			mcpOutput := mcpResult["content"].([]any)[0].(map[string]any)["text"].(string)
			var cliJSON, mcpJSON map[string]any
			if err := json.Unmarshal([]byte(output), &cliJSON); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(mcpOutput), &mcpJSON); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cliJSON, mcpJSON) {
				t.Fatalf("CLI/MCP query mismatch:\n%s\n%s", output, mcpOutput)
			}
			var result struct {
				Limitations []string `json:"limitations"`
			}
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Limitations, doc.Limitations) {
				t.Fatalf("query lost observed limitations: got %v, want %v", result.Limitations, doc.Limitations)
			}
			if len(doc.Limitations) == 0 {
				if _, exists := cliJSON["limitations"]; exists {
					t.Fatalf("empty optional limitations must be omitted: %s", output)
				}
			}
		})
	}
}
