package source

import (
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// fixture는 a→b import와 b의 stdlib import를 가진 모듈이다.
// stdlib은 모듈이 없어 생략되고 limitation으로 남아야 한다.
func fixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

import _ "example.com/fixture/b"
`,
		"b/b.go": `package b

import "fmt"

var _ = fmt.Sprint
`,
	})
}

// TestLoadPackageGraph는 패키지 그래프 수확의 기본 계약을 확인한다:
// 모듈 내부 정점, import 간선, 외부 생략 limitation.
func TestLoadPackageGraph(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Level != graph.LevelPackage {
		t.Fatalf("expected package level, got %s", doc.Level)
	}
	if !doc.HasVertex("example.com/fixture/a") || !doc.HasVertex("example.com/fixture/b") {
		t.Fatalf("missing package vertices: %+v", doc.Vertices)
	}
	kinds := graph.EdgeKinds(doc, "example.com/fixture/a", "example.com/fixture/b")
	if len(kinds) != 1 || kinds[0] != graph.EdgeImport {
		t.Fatalf("missing import edge a→b: %+v", doc.Edges)
	}
	// fmt는 모듈 밖이라 정점이 되지 않고 limitation으로 남는다.
	if doc.HasVertex("fmt") {
		t.Fatal("stdlib package leaked as vertex")
	}
	if len(doc.Limitations) == 0 ||
		!strings.Contains(doc.Limitations[0], "outside the module") {
		t.Fatalf("expected external-import limitation: %v", doc.Limitations)
	}
}

// TestImportEdgePositions는 import 간선이 선언 위치를 싣는지 확인한다.
// v2 스키마에서 "의존이 어디서 일어나는가"는 간선이 직접 담는 사실이다.
func TestImportEdgePositions(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	var e *graph.Edge
	for i := range doc.Edges {
		if doc.Edges[i].Kind == graph.EdgeImport &&
			doc.Edges[i].From == "example.com/fixture/a" {
			e = &doc.Edges[i]
		}
	}
	if e == nil || len(e.Positions) != 1 {
		t.Fatalf("import edge must carry its decl site: %+v", doc.Edges)
	}
	pos := e.Positions[0]
	if !strings.HasSuffix(pos.File, "a/a.go") || pos.Line != 3 {
		t.Fatalf("position must point at the import spec in a/a.go: %+v", pos)
	}
}

// TestLoadPackageGraphDeps는 --deps가 외부 패키지를 정점으로 담는지 확인한다.
func TestLoadPackageGraphDeps(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t), IncludeDeps: true})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.HasVertex("fmt") {
		t.Fatal("expected stdlib vertex with --deps")
	}
	kinds := graph.EdgeKinds(doc, "example.com/fixture/b", "fmt")
	if len(kinds) != 1 {
		t.Fatalf("missing edge b→fmt: %+v", doc.Edges)
	}
}
