package export

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// TestFileRoundTrip은 Save→Load가 문서를 보존하는지 확인한다.
// 그래프 파일은 영속 산출물이라 왕복에서 정보가 새면 안 된다.
func TestFileRoundTrip(t *testing.T) {
	doc := &graph.Document{
		Version: graph.Version, Tool: graph.Tool,
		Level: graph.LevelSymbol,
		Root:  "/x", Module: "example.com/m",
		Roots: []string{"example.com/m.main"},
		Vertices: []graph.Vertex{
			{ID: "b", Kind: graph.KindFunc},
			{ID: "a", Kind: graph.KindFunc},
		},
		Edges: []graph.Edge{
			{From: "a", To: "b", Kind: graph.EdgeCall},
			{From: "a", To: "b", Kind: graph.EdgeCall}, // dedupe 대상
		},
		Limitations: []string{"z note", "a note"},
	}
	path := filepath.Join(t.TempDir(), "graph.json")
	if err := SaveFile(doc, path); err != nil {
		t.Fatal(err)
	}
	back, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Module != "example.com/m" || back.Level != graph.LevelSymbol ||
		len(back.Roots) != 1 || len(back.Vertices) != 2 || len(back.Edges) != 1 {
		t.Fatalf("round trip lost data: %+v", back)
	}
	// 정렬·중복 제거가 파일에 반영됐는지 확인한다.
	if back.Vertices[0].ID != "a" || back.Limitations[0] != "a note" {
		t.Fatalf("output not normalized: %+v", back)
	}
}

// TestLoadFileRejects는 깨진 파일과 미래 버전 문서를 거부하는지 확인한다.
// 모르는 버전을 읽으면 조용히 정보를 잃는 것보다 거부가 안전하다.
func TestLoadFileRejects(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(bad); err == nil {
		t.Fatal("invalid JSON must be an error")
	}
	future := filepath.Join(dir, "future.json")
	if err := os.WriteFile(future, []byte(`{"version": 999}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(future); err == nil {
		t.Fatal("future version must be rejected")
	}
}
