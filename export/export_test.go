package export

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// TestJSONDeterministic는 같은 입력이 같은 바이트가 되는지 확인한다.
func TestJSONDeterministic(t *testing.T) {
	doc := &graph.Document{
		Version:  graph.Version,
		Tool:     graph.Tool,
		Level:    graph.LevelPackage,
		Vertices: []graph.Vertex{{ID: "b"}, {ID: "a"}},
		Edges:    []graph.Edge{{From: "a", To: "b", Kind: graph.EdgeImport}},
	}
	first, err := JSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	// 순서를 섞은 두 번째 문서도 같은 바이트여야 한다.
	doc2 := &graph.Document{
		Version:  graph.Version,
		Tool:     graph.Tool,
		Level:    graph.LevelPackage,
		Vertices: []graph.Vertex{{ID: "a"}, {ID: "b"}},
		Edges:    []graph.Edge{{From: "a", To: "b", Kind: graph.EdgeImport}},
	}
	second, err := JSON(doc2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("non-deterministic JSON:\n%s\n---\n%s", first, second)
	}
}

// TestMermaid는 mermaid 출력에 노드와 간선이 실리고
// contains 간선은 빠지는지 확인한다.
func TestMermaid(t *testing.T) {
	doc := &graph.Document{
		Vertices: []graph.Vertex{
			{ID: "example.com/a"}, {ID: "example.com/b"},
		},
		Edges: []graph.Edge{
			{From: "example.com/a", To: "example.com/b", Kind: graph.EdgeImport},
			{From: "example.com/a", To: "example.com/b", Kind: graph.EdgeContains},
		},
	}
	out, err := Mermaid(doc)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "flowchart LR") {
		t.Fatalf("missing header:\n%s", s)
	}
	if strings.Count(s, "-->") != 1 {
		t.Fatalf("expected exactly one dependency edge, contains leaked:\n%s", s)
	}
	if !strings.Contains(s, "example.com/a") {
		t.Fatalf("missing vertex label:\n%s", s)
	}
}
