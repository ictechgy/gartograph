package analysis

import (
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// affectedDoc은 파일 위치를 가진 문서다:
// /mod/a/a.go에 m/a.F와 패키지 m/a, /mod/b/b.go에 m/b.G와 패키지 m/b,
// 간선은 m/b.G → m/a.F(call)와 m/b → m/a(import).
func affectedDoc() *graph.Document {
	return &graph.Document{
		Root:   "/mod",
		Module: "m",
		Vertices: []graph.Vertex{
			{ID: "m/a", Kind: graph.KindPackage},
			{ID: "m/b", Kind: graph.KindPackage},
			{ID: "m/a.F", Kind: graph.KindFunc, Package: "m/a",
				Position: &graph.Position{File: "/mod/a/a.go", Line: 3}},
			{ID: "m/b.G", Kind: graph.KindFunc, Package: "m/b",
				Position: &graph.Position{File: "/mod/b/b.go", Line: 3}},
		},
		Edges: []graph.Edge{
			{From: "m/b.G", To: "m/a.F", Kind: graph.EdgeCall},
			{From: "m/b", To: "m/a", Kind: graph.EdgeImport},
		},
	}
}

// TestAffectedByFiles는 파일이 선언 심볼과 패키지 정점 둘 다로 해석되는지,
// 그 루트들의 의존자가 모이는지 확인한다.
func TestAffectedByFiles(t *testing.T) {
	res, err := AffectedByFiles(affectedDoc(), []string{"a/a.go"}, nil, 0, 0)
	if err != nil {
		t.Fatalf("affected failed: %v", err)
	}
	// a/a.go는 m/a.F(선언 위치)와 m/a(디렉터리 패키지)로 해석된다.
	want := map[string]bool{"m/a": true, "m/a.F": true}
	if len(res.Roots) != len(want) {
		t.Fatalf("roots: %+v", res.Roots)
	}
	for _, r := range res.Roots {
		if !want[r] {
			t.Fatalf("unexpected root %s", r)
		}
	}
	// m/a.F와 m/a를 의존하는 것: m/b.G(call)와 m/b(import).
	var gotG, gotB bool
	for _, d := range res.Dependers {
		gotG = gotG || d.ID == "m/b.G"
		gotB = gotB || d.ID == "m/b"
	}
	if !gotG || !gotB {
		t.Fatalf("expected m/b.G and m/b as dependers: %+v", res.Dependers)
	}
}

// TestAffectedSubdirRoot는 --dir이 모듈 하위 디렉터리일 때 파일→패키지
// 해석이 ModuleDir 기준으로 이뤄지는지 확인한다 — Root 기준으로 계산하면
// 패키지 경로가 한 단계 짧아져 루트를 놓친다.
func TestAffectedSubdirRoot(t *testing.T) {
	d := &graph.Document{
		Root:      "/repo/sub", // --dir은 모듈의 하위 디렉터리
		ModuleDir: "/repo",     // go.mod는 모듈 루트에 있다
		Module:    "example.com/repo",
		Vertices: []graph.Vertex{
			{ID: "example.com/repo/sub/child", Kind: graph.KindPackage},
		},
	}
	// git --relative는 --dir 기준 "child/x.go"를 준다 — 절대화는 Root 기준,
	// 패키지 해석은 ModuleDir 기준이어야 맞다.
	roots, unmapped := VerticesForFiles(d, []string{"child/x.go"})
	if len(unmapped) != 0 || len(roots) != 1 ||
		roots[0] != "example.com/repo/sub/child" {
		t.Fatalf("subdir file must resolve to its package: roots=%v unmapped=%v",
			roots, unmapped)
	}
}

// TestAffectedUnmapped는 매칭되지 않는 파일이 unmappedFiles로 보고되는지,
// 없는 추가 루트가 ErrNotFound인지 확인한다.
func TestAffectedUnmapped(t *testing.T) {
	res, err := AffectedByFiles(affectedDoc(),
		[]string{"a/a.go", "README.md", "gone/g.go"}, nil, 0, 0)
	if err != nil {
		t.Fatalf("affected failed: %v", err)
	}
	if len(res.UnmappedFiles) != 2 {
		t.Fatalf("expected README.md and gone/g.go unmapped: %+v", res.UnmappedFiles)
	}
	if _, err := AffectedByFiles(affectedDoc(), nil, []string{"ghost"}, 0, 0); err == nil {
		t.Fatal("missing extra root must be ErrNotFound")
	}
}

// TestAffectedNoModule은 모듈 정보가 없는 문서와 모듈 밖 파일이
// 패키지 해석 없이 unmapped로 떨어지는지 확인한다.
func TestAffectedNoModule(t *testing.T) {
	// Module이 없는 문서 — 패키지 추론 자체가 성립하지 않는다.
	d := &graph.Document{Root: "/x", Vertices: []graph.Vertex{
		{ID: "p", Kind: graph.KindPackage},
	}}
	roots, unmapped := VerticesForFiles(d, []string{"a.go"})
	if len(roots) != 0 || len(unmapped) != 1 {
		t.Fatalf("module-less doc must not guess packages: %v %v", roots, unmapped)
	}
	// 모듈 밖의 파일 — 절대 경로가 ModuleDir 밖이면 unmapped다.
	roots, unmapped = VerticesForFiles(affectedDoc(), []string{"/elsewhere/x.go"})
	if len(roots) != 0 || len(unmapped) != 1 {
		t.Fatalf("file outside the module must be unmapped: %v %v", roots, unmapped)
	}
}
