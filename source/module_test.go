package source

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// writeWorkspaceApp은 go.work 워크스페이스 fixture를 만든다 — a/app은 b/util·b/gen과 의존 모듈
// example.com/dep(워크스페이스 밖, a의 go.mod가 로컬 디렉터리로 replace)의 x를, b/util은 a/lib를 import한다.
// 루트는 심링크를 푼 경로다(go가 알리는 모듈 디렉터리와 같은 표기로 비교한다).
func writeWorkspaceApp(t *testing.T) (root string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.work": "go 1.27\n\nuse ./a\nuse ./b\n",
		"a/go.mod": "module example.com/a\n\ngo 1.27\n\nrequire example.com/dep v0.0.0\n\n" +
			"replace example.com/dep => ../dep\n",
		"b/go.mod":   "module example.com/b\n\ngo 1.27\n",
		"dep/go.mod": "module example.com/dep\n\ngo 1.27\n",
		"a/app/app.go": "package app\n\nimport (\n\t_ \"example.com/b/gen\"\n\t_ \"example.com/b/util\"\n" +
			"\t_ \"example.com/dep/x\"\n)\n",
		"a/lib/lib.go":   "package lib\n",
		"b/util/util.go": "package util\n\nimport _ \"example.com/a/lib\"\n",
		"b/gen/gen.go":   "package gen\n",
		"dep/x/x.go":     "package x\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestWorkspaceModuleFacts는 go.work 수확이 패키지 정점의 모듈 소속을 문서에 싣는지 본다(DECISIONS gartograph
// "go.work의 exclude·components"). 문서에는 워크스페이스 모듈마다 go.mod 디렉터리(workspaceModules)를, 문서
// module이 아닌 모듈(b)의 패키지 정점에는 그 모듈 경로를 싣고, 형제 모듈 패키지는 외부가 아니다. 레벨이 달라도
// 같은 사실이다. --deps로 들어온 의존 모듈(dep)은 워크스페이스 모듈이 아니라 외부이고 모듈 사실이 없다.
// exclude는 지금처럼 패키지마다 자기 모듈 기준이다 — "gen"은 모듈 b의 gen을 빼고, 의존 모듈의 x는 전체 import
// 경로로만 맞아 "x"에 빠지지 않는다.
func TestWorkspaceModuleFacts(t *testing.T) {
	root := writeWorkspaceApp(t)
	want := []graph.WorkspaceModule{
		{Path: "example.com/a", Dir: filepath.Join(root, "a")},
		{Path: "example.com/b", Dir: filepath.Join(root, "b")},
	}
	for _, c := range []struct {
		level graph.Level
		deps  bool
	}{{graph.LevelPackage, false}, {graph.LevelSymbol, false}, {graph.LevelPackage, true}} {
		doc, err := Load(Options{Dir: filepath.Join(root, "a"), Level: c.level, IncludeDeps: c.deps,
			Exclude: []string{"gen", "x"}})
		if err != nil {
			t.Fatal(err)
		}
		if doc.Module != "example.com/a" || !reflect.DeepEqual(doc.WorkspaceModules, want) {
			t.Fatalf("%s deps=%v: module %q, workspace modules %+v", c.level, c.deps, doc.Module, doc.WorkspaceModules)
		}
		facts := map[string]string{}
		for _, v := range doc.Vertices {
			if v.Kind == graph.KindPackage {
				facts[v.ID] = fmt.Sprintf("module=%q external=%v", v.Module, v.External)
			}
		}
		wantFacts := map[string]string{
			"example.com/a/app":  `module="" external=false`,
			"example.com/a/lib":  `module="" external=false`,
			"example.com/b/util": `module="example.com/b" external=false`,
		}
		if c.deps {
			wantFacts["example.com/dep/x"] = `module="" external=true`
		}
		if !reflect.DeepEqual(facts, wantFacts) {
			t.Fatalf("%s deps=%v: package facts %v, want %v (b/gen excluded)", c.level, c.deps, facts, wantFacts)
		}
	}
}

// TestSingleModuleCarriesNoWorkspaceFacts는 주 모듈이 하나인 수확이 모듈 소속 사실을 싣지 않는지 본다 —
// 단일 모듈 문서의 바이트는 그대로여야 한다. go.work 안이라도 그래프에 한 모듈의 패키지만 있으면 같다(b/gen만
// 수확한다).
func TestSingleModuleCarriesNoWorkspaceFacts(t *testing.T) {
	genOnly := filepath.Join(writeWorkspaceApp(t), "b", "gen")
	for _, dir := range []string{fixture(t), genOnly} {
		doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if doc.WorkspaceModules != nil || strings.Contains(string(data), `"workspaceModules"`) {
			t.Fatalf("%s: a single-module document must not carry workspace modules: %s", dir, data)
		}
		for _, v := range doc.Vertices {
			if v.Module != "" {
				t.Fatalf("%s: a single-module vertex must not carry a module: %+v", dir, v)
			}
		}
	}
}
