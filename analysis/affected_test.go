package analysis

import (
	"errors"
	"fmt"
	"math/rand"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
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

// TestAffectedMatchesScan은 무작위 문서·파일 목록에서 VerticesForFiles·AffectedByFiles가 파일과 추가
// 루트마다 정점 목록을 훑던 구현(scanVerticesForFiles)과 같은 roots·unmapped·오류·의존자를 내는지
// 확인한다. 선언 파일, 디렉터리 패키지만 맞는 파일, 문서에 없는 패키지 디렉터리, .go가 아닌 파일,
// 모듈 밖 파일, 상대·절대 경로, 겹친 파일, 문서에 없는 추가 루트를 섞는다. 정점 ID가 겹친 문서(손으로
// 합친 저장 문서)도 섞는다 — 의존자의 정점 종류는 VertexByID처럼 앞의 정점에서 읽어야 한다.
func TestAffectedMatchesScan(t *testing.T) {
	r := rand.New(rand.NewSource(23))
	dirs := []string{"", "a", "b", "a/c"}
	sawMissing, sawPackageOnly := false, false
	for trial := 0; trial < 500; trial++ {
		d := randomDependencyDocument(r)
		d.Root, d.Module = "/mod", "m"
		for _, dir := range dirs {
			if r.Intn(3) > 0 {
				d.Vertices = append(d.Vertices, graph.Vertex{ID: path.Join("m", dir), Kind: graph.KindPackage})
			}
		}
		for i := range d.Vertices {
			if d.Vertices[i].Kind != graph.KindPackage && r.Intn(3) > 0 {
				file := path.Join("/mod", dirs[r.Intn(len(dirs))], fmt.Sprintf("f%d.go", r.Intn(2)))
				d.Vertices[i].Position = &graph.Position{File: file, Line: 1}
			}
		}
		if r.Intn(3) == 0 {
			dup := d.Vertices[r.Intn(len(d.Vertices))]
			dup.Kind = graph.KindVar
			d.Vertices = append(d.Vertices, dup)
		}
		var files []string
		for k := r.Intn(6); k > 0; k-- {
			file := path.Join(dirs[r.Intn(len(dirs))], fmt.Sprintf("f%d.go", r.Intn(3)))
			switch r.Intn(5) {
			case 0:
				file = "/mod/" + file
			case 1:
				file = "/elsewhere/" + file
			case 2:
				file = strings.TrimSuffix(file, ".go") + ".md"
			}
			files = append(files, file)
		}
		var extra []string
		for k := r.Intn(3); k > 0; k-- {
			extra = append(extra, d.Vertices[r.Intn(len(d.Vertices))].ID)
		}
		if r.Intn(4) == 0 {
			extra = append(extra, "m.ghost")
		}

		roots, unmapped := VerticesForFiles(d, files)
		wantRoots, wantUnmapped := scanVerticesForFiles(d, files)
		if !reflect.DeepEqual(roots, wantRoots) || !reflect.DeepEqual(unmapped, wantUnmapped) {
			t.Fatalf("trial %d files %v:\ngot  %v %v\nwant %v %v", trial, files, roots, unmapped, wantRoots, wantUnmapped)
		}
		depth, maxEntries := r.Intn(3), r.Intn(3)
		aff, err := AffectedByFiles(d, files, extra, depth, maxEntries)
		if slices.Contains(extra, "m.ghost") {
			if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "m.ghost") {
				t.Fatalf("trial %d: missing extra root must be ErrNotFound naming it, got %v", trial, err)
			}
			sawMissing = true
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		want := dedupeSorted(append(append([]string(nil), wantRoots...), extra...))
		wantDeps, wantTruncated := edgeScanImpactFrom(d, want, depth, maxEntries)
		if !reflect.DeepEqual(aff.Roots, want) || !reflect.DeepEqual(aff.UnmappedFiles, wantUnmapped) ||
			!reflect.DeepEqual(aff.Dependers, wantDeps) || aff.Truncated != wantTruncated {
			t.Fatalf("trial %d files %v extra %v:\ngot  %+v\nwant roots %v unmapped %v deps %+v",
				trial, files, extra, aff, want, wantUnmapped, wantDeps)
		}
		for _, id := range roots {
			if v, _ := d.VertexByID(id); v != nil && v.Kind == graph.KindPackage && v.Position == nil {
				sawPackageOnly = true
			}
		}
	}
	if !sawMissing || !sawPackageOnly {
		t.Fatalf("random inputs must exercise missing extra roots (%v) and package-directory matches (%v)",
			sawMissing, sawPackageOnly)
	}
}

// scanVerticesForFiles는 색인을 쓰기 전의 VerticesForFiles다 — 파일마다 HasVertex로 정점 목록을
// 훑어 디렉터리 패키지를 확인한다. 느려도 출력 대조 기준으로 그대로 둔다.
func scanVerticesForFiles(d *graph.Document, files []string) (roots, unmapped []string) {
	byFile := map[string][]string{}
	for _, v := range d.Vertices {
		if v.Position != nil {
			abs := filepath.Clean(v.Position.File)
			byFile[abs] = append(byFile[abs], v.ID)
		}
	}
	seen := map[string]bool{}
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") {
			unmapped = append(unmapped, f)
			continue
		}
		abs := f
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(d.Root, f)
		}
		if resolved, err := filepath.Abs(abs); err == nil {
			abs = resolved
		}
		abs = filepath.Clean(abs)
		matched := false
		for _, id := range byFile[abs] {
			if !seen[id] {
				seen[id] = true
				roots = append(roots, id)
			}
			matched = true
		}
		if pkg := packageForFile(d, abs); pkg != "" && d.HasVertex(pkg) {
			if !seen[pkg] {
				seen[pkg] = true
				roots = append(roots, pkg)
			}
			matched = true
		}
		if !matched {
			unmapped = append(unmapped, f)
		}
	}
	sort.Strings(roots)
	sort.Strings(unmapped)
	return roots, unmapped
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
