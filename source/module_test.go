package source

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// writeWorkspace는 go.work 워크스페이스 fixture를 만든다.
// testutil.WriteModule은 루트 go.mod를 자동 생성해 워크스페이스 시나리오에
// 맞지 않아 여기서 직접 쓴다.
func writeWorkspace(t *testing.T) (root, moduleA string) {
	t.Helper()
	root = t.TempDir()
	write := func(name, content string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.work", "go 1.27\n\nuse ./a\nuse ./b\n")
	write("a/go.mod", "module example.com/a\n\ngo 1.27\n\nrequire example.com/b v0.0.0\n")
	write("a/a.go", `package a

import "example.com/b"

var A = b.B
`)
	write("b/go.mod", "module example.com/b\n\ngo 1.27\n")
	write("b/b.go", "package b\n\nvar B = 1\n")
	return root, filepath.Join(root, "a")
}

// TestModuleLevelWorkspace는 워크스페이스에서 모듈 정점과
// 크로스 모듈 import 간선이 만들어지는지 확인한다.
func TestModuleLevelWorkspace(t *testing.T) {
	_, a := writeWorkspace(t)
	doc, err := Load(Options{Dir: a, Level: graph.LevelModule})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Level != graph.LevelModule {
		t.Fatalf("expected module level, got %s", doc.Level)
	}
	if !doc.HasVertex("example.com/a") || !doc.HasVertex("example.com/b") {
		t.Fatalf("missing module vertices: %+v", doc.Vertices)
	}
	if len(doc.Edges) != 1 ||
		doc.Edges[0].From != "example.com/a" || doc.Edges[0].To != "example.com/b" {
		t.Fatalf("expected module edge a→b: %+v", doc.Edges)
	}
	for _, v := range doc.Vertices {
		if v.Kind != graph.KindModule {
			t.Fatalf("non-module vertex leaked: %+v", v)
		}
	}
}

// TestModuleLevelSingle은 단일 모듈에서 정점 하나가 정상 결과인지 확인한다.
// 모듈이 하나라도 거짓으로 간선을 만들면 안 된다.
func TestModuleLevelSingle(t *testing.T) {
	doc, err := Load(Options{Dir: fixture(t), Level: graph.LevelModule})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Vertices) != 1 || doc.Vertices[0].ID != "example.com/fixture" {
		t.Fatalf("expected one module vertex: %+v", doc.Vertices)
	}
	if len(doc.Edges) != 0 {
		t.Fatalf("self-contained module must have no module edges: %+v", doc.Edges)
	}
}
