package source

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/tools/go/packages"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestTidyBuildsFileMatchesGoModTidy는 파일 머리의 빌드 제약을 go mod tidy와 같게 판정하는지
// 확인한다. 기대값은 각 제약의 파일이 따로 import하는 로컬 모듈로 go mod tidy(go1.27.1)를 돌려
// require가 남는지로 얻었다 — tidy는 ignore 외의 태그를 부정 여부와 무관하게 참으로 친다.
func TestTidyBuildsFileMatchesGoModTidy(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   bool
	}{
		{"//go:build ignore", false},
		{"//go:build !ignore", true},
		{"//go:build integration", true},
		{"//go:build !darwin", true},
		{"//go:build linux && !linux", true},
		{"//go:build ignore && linux", false},
		{"//go:build ignore || !linux", true},
		{"//go:build !(ignore || linux)", true},
		{"//go:build !(!ignore)", false},
		{"//go:build !(linux && !ignore)", true},
		{"// +build ignore", false},
		{"// +build linux,!linux", true},
		{"//go:build linux\n// +build ignore", true}, // 두 형식이 함께 있으면 go:build 줄이 판정한다
		{"// +build linux\n// +build ignore", false}, // 여러 줄의 plus-build 제약은 AND다
		{"// Package p는 제약이 없다.", true},
	} {
		src := tc.header + "\n\npackage p\n\nimport _ \"example.com/dep\"\n"
		f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ImportsOnly|parser.ParseComments)
		if err != nil {
			t.Fatalf("%q: %v", tc.header, err)
		}
		if got := tidyBuildsFile(f); got != tc.want {
			t.Errorf("%q: tidyBuildsFile = %v, want %v", tc.header, got, tc.want)
		}
	}
}

// TestScanExcludedFilesReadsOnlyRegularFiles는 빌드 제약 제외 파일을 정규 파일일 때만 읽는지
// 확인한다. 이름의 GOOS 접미사로 빠진 파일은 go list가 열지 않아, 분석 대상이 그 자리에 둔 FIFO는
// 보통의 open에서 영원히 막힌다 — 막히지 않고 "읽을 수 없는 파일"로 세어 limitation에 남아야 한다.
func TestScanExcludedFilesReadsOnlyRegularFiles(t *testing.T) {
	otherOS := "windows"
	if runtime.GOOS == "windows" {
		otherOS = "linux"
	}
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":               "package main\n\nfunc main() {}\n",
		"ok_" + otherOS + ".go": "package main\n\nimport _ \"example.com/dep\"\n",
	})
	testutil.MakeFIFO(t, filepath.Join(dir, "pipe_"+otherOS+".go"))
	pkgs, err := load(Options{Dir: dir, Tests: true})
	if err != nil {
		t.Fatal(err)
	}
	var scan excludedScan
	reachable := walkImports(pkgs)
	loaded := loadedPackagePaths(reachable)
	unseen := func(path string) bool { return unseenImport(path, loaded, "example.com/fixture", nil) }
	testutil.FinishWithin(t, testutil.BlockDeadline, func() { scan = scanExcludedFiles(reachable, unseen) })
	if scan.unseenFiles != 1 || scan.unreadable != 1 || len(scan.imports) != 1 || scan.imports[0] != "example.com/dep" {
		t.Fatalf("want the regular file read and the FIFO counted unreadable: %+v", scan)
	}
	lims := unusedLimitations(0, scan, unloadedScan{}, dependencyTestScan{})
	if len(lims) != 2 {
		t.Fatalf("both the read and the unreadable excluded files must be counted: %v", lims)
	}
}

// TestParseImportHeaderLimit은 제외 파일의 머리를 상한까지만 읽는지 확인한다 — 상한 뒤에 있는
// import 선언은 보지 않고, 큰 파일 전체를 메모리에 올리지 않는다.
func TestParseImportHeaderLimit(t *testing.T) {
	deep := filepath.Join(t.TempDir(), "deep.go")
	padding := make([]byte, maxImportHeader)
	for i := range padding {
		padding[i] = '\n'
	}
	src := append(padding, []byte("package p\n\nimport _ \"example.com/dep\"\n")...)
	if err := os.WriteFile(deep, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if f, err := parseImportHeader(deep); err == nil && len(f.Imports) > 0 {
		t.Fatalf("imports after the first %d bytes must not be read: %v", maxImportHeader, f.Imports)
	}
}

// countingReader는 읽힌 바이트 수를 센다 — parseHeaderFrom이 import 선언 뒤를 읽지 않는지 보는 재료다.
type countingReader struct {
	r io.Reader
	n int
}

// Read는 안의 reader를 읽고 읽은 수를 더한다.
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// headerImports는 파싱한 파일의 import 경로 목록이다.
func headerImports(t *testing.T, f *ast.File) []string {
	t.Helper()
	var out []string
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, path)
	}
	return out
}

// TestParseHeaderFromStopsAfterImports는 import 선언이 첫 덩어리 안에서 끝나면 그 뒤를 읽지 않는지 본다. 예전에는
// 모든 제외 파일을 상한(1 MiB)까지 읽어, 의존 패키지의 큰 생성 파일(modernc.org/libc의 플랫폼별 파일)까지 읽게 되며
// unused-deps가 느려졌다.
func TestParseHeaderFromStopsAfterImports(t *testing.T) {
	header := "//go:build windows\n\npackage p\n\nimport (\n\t\"example.com/a\"\n\t\"os\"\n)\n\n"
	r := &countingReader{r: strings.NewReader(header + strings.Repeat("var x = 1\n", 100_000))}
	f, err := parseHeaderFrom("p.go", r)
	if err != nil {
		t.Fatal(err)
	}
	if got := headerImports(t, f); !slices.Equal(got, []string{"example.com/a", "os"}) {
		t.Fatalf("imports = %v", got)
	}
	if r.n > importHeaderChunk {
		t.Fatalf("read %d bytes of a file whose imports end in the first %d", r.n, importHeaderChunk)
	}
}

// TestParseHeaderFromChunkBoundaries는 읽은 끝이 import 선언 사이, 두 번째 `import` 키워드 가운데, 세미콜론 뒤에
// 떨어져도 import를 빠짐없이 읽는지 본다 — 앞 주석 길이로 두 번째 선언의 시작을 첫 덩어리 끝 앞뒤로 민다. 머리가
// 덩어리 여러 개에 걸치는 긴 주석도 본다. 기대값은 파일 전체를 파싱한 결과다.
func TestParseHeaderFromChunkBoundaries(t *testing.T) {
	body := "\nvar x = 1\n" + strings.Repeat("// filler\n", 10_000)
	var sources []string
	for _, sep := range []string{"\n", ";"} {
		for shift := -12; shift <= 12; shift++ {
			tail := "\npackage p\nimport \"example.com/a\"" + sep
			pad := importHeaderChunk + shift - len("// ") - len(tail)
			sources = append(sources, "// "+strings.Repeat("x", pad)+tail+"import \"example.com/b\""+body)
		}
	}
	sources = append(sources, "/*"+strings.Repeat("license\n", 5_000)+"*/\npackage p\nimport \"example.com/a\"\n"+
		"import \"example.com/b\""+body)
	for i, src := range sources {
		f, err := parseHeaderFrom("p.go", strings.NewReader(src))
		if err != nil {
			t.Fatalf("source %d: %v", i, err)
		}
		whole, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := headerImports(t, f), headerImports(t, whole); !slices.Equal(got, want) || len(want) != 2 {
			t.Errorf("source %d: imports = %v, want %v", i, got, want)
		}
	}
}

// TestScanDependencyTestsCountsUnreadable는 읽지 못한 의존 패키지 디렉터리와 go.mod를 판정 공백으로 세는지 본다 —
// 디렉터리를 못 읽으면 그 테스트 import를, go.mod를 못 읽으면 그 루트의 require를 모른다. 둘 다 조용히 넘기면
// limitation 없이 판정이 기운다.
func TestScanDependencyTestsCountsUnreadable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	dep := &packages.Package{PkgPath: "example.com/dep", Dir: missing,
		Module: &packages.Module{Path: "example.com/dep", Version: "v1.0.0", GoMod: filepath.Join(missing, "go.mod")}}
	scan := scanDependencyTests([]*packages.Package{dep}, tidyTestInput{used: map[string]bool{},
		unseen: func(string) bool { return true }})
	if scan.unreadable != 2 || scan.unseenFiles != 0 || len(scan.used) != 0 {
		t.Fatalf("want the unreadable directory and go.mod counted, got %+v", scan)
	}
	if lims := scan.limitations(); len(lims) != 1 || !strings.HasPrefix(lims[0], "2 dependency test files, package directories, or go.mod files could not be read;") {
		t.Fatalf("limitations = %v", lims)
	}
}

// TestScanDependencyTestsCountsUnparsableTestFile은 파싱하지 못한 의존 패키지 테스트 파일을 판정 공백으로 세는지 본다 —
// 그 import를 모르면 tidy가 남기는 require를 미사용으로 낼 수 있다.
func TestScanDependencyTestsCountsUnparsableTestFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken_test.go"), []byte("package dep\n\nimport (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goMod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/dep\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dep := &packages.Package{PkgPath: "example.com/dep", Dir: dir,
		Module: &packages.Module{Path: "example.com/dep", Version: "v1.0.0", GoMod: goMod, GoVersion: "1.27"}}
	scan := scanDependencyTests([]*packages.Package{dep}, tidyTestInput{used: map[string]bool{},
		unseen: func(string) bool { return true }})
	if scan.unreadable != 1 || scan.unseenFiles != 0 {
		t.Fatalf("the unparsable test file must be counted as unreadable, got %+v", scan)
	}
}

// writeModFile은 path에 go.mod 내용을 쓴다(디렉터리를 만든다).
func writeModFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTidyGraphFollowsUnprunedRequirements는 go 1.17 미만 루트에서 모듈 캐시의 go.mod를 따라 전이 require를 더하고,
// go 1.17 이상 루트에서는 루트 go.mod의 require만 더하며, 주 go.mod의 exclude에 걸린 require는 빼는지 본다 —
// tidy의 readModGraph와 같다. 캐시 경로는 대문자를 `!`로 바꾼 escape 경로다.
func TestTidyGraphFollowsUnprunedRequirements(t *testing.T) {
	cache := t.TempDir()
	download := filepath.Join(cache, "cache", "download")
	writeModFile(t, filepath.Join(download, "example.com", "!e", "@v", "v1.0.0.mod"),
		"module example.com/E\n\nrequire example.com/x v1.1.0\n")
	writeModFile(t, filepath.Join(download, "example.com", "x", "@v", "v1.1.0.mod"), "module example.com/x\n")
	rootMod := filepath.Join(download, "example.com", "dep", "@v", "v1.0.0.mod")
	writeModFile(t, rootMod, "module example.com/dep\n\nrequire example.com/E v1.0.0\n")
	root := func(goVersion string) []*packages.Package {
		return []*packages.Package{{PkgPath: "example.com/dep", Module: &packages.Module{
			Path: "example.com/dep", Version: "v1.0.0", GoMod: rootMod, GoVersion: goVersion}}}
	}
	if g := buildTidyGraph(root("1.16"), goModFacts{}, t.TempDir()); g.versions["example.com/x"] != "v1.1.0" ||
		g.modCache != cache || g.unreadable != 0 {
		t.Fatalf("an unpruned root must reach x through the cached go.mod of E: %+v", g)
	}
	if g := buildTidyGraph(root(""), goModFacts{}, t.TempDir()); g.versions["example.com/x"] != "v1.1.0" {
		t.Fatalf("a root without a go directive is unpruned: %+v", g.versions)
	}
	if g := buildTidyGraph(root("1.27"), goModFacts{}, t.TempDir()); g.versions["example.com/x"] != "" ||
		g.versions["example.com/E"] != "v1.0.0" {
		t.Fatalf("a pruned root contributes only its own requirements: %+v", g.versions)
	}
	excluded := goModFacts{excludes: map[string]bool{"example.com/E@v1.0.0": true}}
	if g := buildTidyGraph(root("1.16"), excluded, t.TempDir()); g.versions["example.com/E"] != "" ||
		g.versions["example.com/x"] != "" {
		t.Fatalf("an excluded requirement and what it brings must be left out: %+v", g.versions)
	}
}

// TestTidyGraphGoModFile은 주 go.mod의 replace를 go 명령의 우선순위(버전을 적은 것이 먼저)로 적용해 go.mod 위치를
// 정하는지 본다 — 디렉터리 대상은 주 모듈 기준 go.mod, 모듈 대상은 대상 모듈의 캐시 go.mod다.
func TestTidyGraphGoModFile(t *testing.T) {
	main := goModFacts{replaces: []*modfile.Replace{
		{Old: module.Version{Path: "example.com/a"}, New: module.Version{Path: "./local-a"}},
		{Old: module.Version{Path: "example.com/a", Version: "v1.2.0"}, New: module.Version{Path: "example.com/fork", Version: "v0.1.0"}},
	}}
	g := &tidyGraph{main: main, modDir: "/work", modCache: "/cache"}
	for _, c := range []struct {
		m    module.Version
		want string
	}{
		{module.Version{Path: "example.com/a", Version: "v1.0.0"}, filepath.Join("/work", "local-a", "go.mod")},
		{module.Version{Path: "example.com/a", Version: "v1.2.0"}, filepath.Join("/cache", "cache", "download", "example.com", "fork", "@v", "v0.1.0.mod")},
		{module.Version{Path: "example.com/B", Version: "v2.0.0+incompatible"}, filepath.Join("/cache", "cache", "download", "example.com", "!b", "@v", "v2.0.0+incompatible.mod")},
	} {
		if got, err := g.goModFile(c.m); err != nil || got != c.want {
			t.Errorf("goModFile(%v) = %q, %v; want %q", c.m, got, err, c.want)
		}
	}
}

// TestPrunedGoVersion은 go 지시문이 모듈 그래프 가지치기를 켜는지(1.17 이상) 본다. 지시문이 없으면 꺼져 있다.
func TestPrunedGoVersion(t *testing.T) {
	for v, want := range map[string]bool{"": false, "1.16": false, "1.17": true, "1.21rc1": true, "1.27.1": true, "2": true} {
		if got := prunedGoVersion(v); got != want {
			t.Errorf("prunedGoVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

// TestSemverLess는 "없음"(빈 문자열)이 모든 버전보다 낮고 의사 버전도 semver 순서로 비교하는지 본다 — tidy가 그래프에
// 없는 모듈을 "none"으로 비교하는 것과 같다.
func TestSemverLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"", "v0.0.0", true},
		{"", "", false},
		{"v0.0.0-20161208181325-20d25e280405", "v1.0.0-20201130134442-10cb98267c6c", true},
		{"v0.2.0", "v0.1.0", false},
		{"v0.2.0", "v0.2.0", false},
	} {
		if got := semverLess(c.a, c.b); got != c.want {
			t.Errorf("semverLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestUnseenImport는 제외 파일의 import 중 판정 공백을 남기는 것만 고르는지 확인한다 — 이번 로드가
// 보지 못한 비 std 패키지다. 로드된 패키지는 의존까지 셌고 std는 require에 기대지 않는다. std는 첫 경로
// 요소에 점이 없는 경로지만, 점 없는 주 모듈 경로와 require 경로는 std가 아니다.
func TestUnseenImport(t *testing.T) {
	loaded := map[string]bool{"os": true, "fixture/lib": true, "example.com/dep": true}
	requires := map[string]bool{"example.com/dep": true, "localdep": true}
	for path, want := range map[string]bool{
		"os":                       false, // 로드된 std
		"syscall/js":               false, // 로드되지 않은 std
		"C":                        false, // cgo 의사 패키지
		"fixture/lib":              false, // 로드된 주 모듈 패키지
		"example.com/dep":          false, // 로드된 require 패키지
		"fixture":                  true,  // 점 없는 주 모듈 경로 자체
		"fixture/winonly":          true,  // 이 플랫폼 파일이 없어 로드되지 않은 주 모듈 패키지
		"fixtures/x":               false, // 주 모듈과 경로 요소 경계가 다르다 — std 모양
		"localdep/sub":             true,  // 점 없는 require(로컬 replace)
		"example.com/x/y":          true,  // 로드되지 않은 비 std
		"golang.org/x/sys/windows": true,  // require 목록 밖이어도 점 있는 경로는 비 std
	} {
		if got := unseenImport(path, loaded, "fixture", requires); got != want {
			t.Errorf("unseenImport(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestOwningRequire는 import 경로를 가장 긴 require 모듈 경로에 귀속하는지 확인한다 — 경로
// 세그먼트 경계에서만 맞추므로 example.com/ab는 example.com/a의 패키지가 아니다.
func TestOwningRequire(t *testing.T) {
	requires := map[string]bool{"example.com/a": true, "example.com/a/b": true}
	for path, want := range map[string]string{
		"example.com/a":       "example.com/a",
		"example.com/a/x":     "example.com/a",
		"example.com/a/b/y":   "example.com/a/b",
		"example.com/ab":      "",
		"fmt":                 "",
		"example.com/a/b/c/d": "example.com/a/b",
	} {
		got, ok := owningRequire(path, requires)
		if got != want || ok != (want != "") {
			t.Errorf("owningRequire(%q) = %q, %v; want %q", path, got, ok, want)
		}
	}
}

// otherGOOS는 호스트가 아닌 OS다 — 그 OS 접미사 파일(x_windows.go)만 있는 디렉터리는 호스트에서 패키지가 되지 않는다.
func otherGOOS() string {
	if runtime.GOOS == "windows" {
		return "linux"
	}
	return "windows"
}

// platformGapNote는 이 플랫폼 파일이 없는 디렉터리 n개를 알리는 limitation 문구다.
func platformGapNote(n int) string {
	return fmt.Sprintf("%d directories have Go files but none for this platform or build tags, so go list loaded "+
		"no package from them; requires imported only there may be reported as unused "+
		"(load them with --goos, --goarch, or --tags)", n)
}

// platformGapModule은 require를 이 플랫폼 파일이 없는 디렉터리(svc)에서만 import하는 모듈이다. require는 모듈 안의 로컬
// replace 대상(winreq)이라 오프라인으로 로드되고, winreq는 go.mod가 있는 하위 모듈이라 순회에서 빠져야 한다. 나머지도 세면
// 안 되는 디렉터리다: tidy도 읽지 않는 //go:build ignore 파일만 있는 gen, go list가 들어가지 않는 testdata·_·. 디렉터리.
func platformGapModule(other string) map[string]string {
	return map[string]string{
		"go.mod": "module example.com/fixture\n\ngo 1.27\n\nrequire example.com/winreq v0.0.0\n\n" +
			"replace example.com/winreq => ./winreq\n",
		"main.go":                  "package main\n\nfunc main() {}\n",
		"svc/svc_" + other + ".go": "package svc\n\nimport \"example.com/winreq\"\n\nfunc Run() { winreq.Do() }\n",
		"winreq/go.mod":            "module example.com/winreq\n\ngo 1.27\n",
		"winreq/winreq.go":         "package winreq\n\nfunc Do() {}\n",
		"gen/gen.go": "//go:build ignore\n\npackage main\n\nimport \"example.com/winreq\"\n\n" +
			"func main() { winreq.Do() }\n",
		"testdata/t/t_" + other + ".go": "package t\n",
		"_tools/x_" + other + ".go":     "package x\n",
		".cache/c_" + other + ".go":     "package c\n",
	}
}

// TestUnusedRequiresCountsDirectoriesWithoutPlatformFiles는 이 플랫폼·태그로 빌드할 파일이 없어 go list가 패키지로 내지
// 않는 디렉터리(windows 전용 svc)를 limitation으로 세는지 확인한다. 그 디렉터리의 import는 읽지 않으므로 거기서만 쓰는
// require는 여전히 unused다(판정은 그대로) — limitation이 그 공백을 알린다. 그 OS로 수확하면 svc가 패키지가 되어 require가
// 쓰이고 알릴 것이 없다. go.mod의 ignore 지시문이 뺀 디렉터리는 go list도 tidy도 보지 않으니 세지 않는다(tidy도 그
// require를 지운다).
func TestUnusedRequiresCountsDirectoriesWithoutPlatformFiles(t *testing.T) {
	other := otherGOOS()
	dir := testutil.WriteModule(t, platformGapModule(other))
	rep, err := UnusedRequires(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Unused, []string{"example.com/winreq"}) ||
		!slices.Equal(rep.Limitations, []string{platformGapNote(1)}) {
		t.Fatalf("only the %s-only directory must be counted: %+v", other, rep)
	}
	rep, err = UnusedRequires(Options{Dir: dir, GOOS: other})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unused) != 0 || rep.UsedRequires != 1 || len(rep.Limitations) != 0 {
		t.Fatalf("harvested for %s the directory is a package and nothing is left to report: %+v", other, rep)
	}
	files := platformGapModule(other)
	files["go.mod"] += "\nignore ./svc\n"
	rep, err = UnusedRequires(Options{Dir: testutil.WriteModule(t, files)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Unused, []string{"example.com/winreq"}) || len(rep.Limitations) != 0 {
		t.Fatalf("a directory the go.mod ignores is outside go list and tidy alike: %+v", rep)
	}
}

// TestUnusedRequiresDirectoryScanFollowsPatterns는 디렉터리 순회가 이번 실행의 패턴이 덮는 트리만 걷는지 확인한다 — 패턴
// 밖의 디렉터리는 플랫폼 탓이 아니라 범위 밖이라, 세면 거짓 이유를 붙인다. import 경로 패턴과 all도 주 모듈 트리를 덮는다.
// 와일드카드 없는 패턴(./svc)은 걷지 않는다 — go list가 파일이 없어도 오류 패키지로 실어 로드 오류 limitation이 세고, 그
// 패키지의 제외 파일 import도 읽힌다. 겹치는 패턴은 디렉터리를 한 번만 센다.
func TestUnusedRequiresDirectoryScanFollowsPatterns(t *testing.T) {
	files := platformGapModule(otherGOOS())
	files["lib/lib.go"] = "package lib\n"
	files["svc/core/core.go"] = "package core\n"
	dir := testutil.WriteModule(t, files)
	literal := []string{
		"1 packages reported load errors; requires needed only through them may be reported as unused",
		"1 source files excluded by build constraints import packages that were not loaded; requires needed only by " +
			"those packages' dependencies may be reported as unused (load them with --goos or --tags)",
	}
	for _, tc := range []struct {
		patterns []string
		want     []string
	}{
		{nil, []string{platformGapNote(1)}},
		{[]string{"./svc/..."}, []string{platformGapNote(1)}},
		{[]string{"example.com/fixture/svc/..."}, []string{platformGapNote(1)}},
		{[]string{"all"}, []string{platformGapNote(1)}},
		{[]string{"./svc/...", "./..."}, []string{platformGapNote(1)}},
		{[]string{"./lib/..."}, nil},
		{[]string{"example.com/fixture/lib/..."}, nil},
		{[]string{"./svc"}, literal},
	} {
		rep, err := UnusedRequires(Options{Dir: dir, Patterns: tc.patterns})
		if err != nil {
			t.Fatalf("%v: %v", tc.patterns, err)
		}
		if !slices.Equal(rep.Limitations, tc.want) {
			t.Errorf("%v: limitations = %q, want %q", tc.patterns, rep.Limitations, tc.want)
		}
	}
}

// TestUnusedRequiresDirectoryScanMatchesGoListPaths는 go list가 보고한 패키지 디렉터리와 순회한 디렉터리를 같은 디렉터리로
// 맞추는지 확인한다. 심링크 체크아웃을 상대 --dir로 주면 go list는 심링크를 푼 경로를, 심링크 경로로 주면 링크 경로를
// 보고하고(macOS의 /var도 심링크다), 패키지 디렉터리를 가리키는 링크는 링크 이름의 패키지가 된다. 경로 문자열만 견주면
// 로드된 디렉터리를 "패키지가 없는 디렉터리"로 세고, 뿌리 링크를 따라가지 않으면 아무것도 세지 못한다.
func TestUnusedRequiresDirectoryScanMatchesGoListPaths(t *testing.T) {
	other := otherGOOS()
	files := platformGapModule(other)
	files["lib/lib.go"] = "package lib\n"
	files["lib/win/w_"+other+".go"] = "package win\n"
	dir := testutil.WriteModule(t, files)
	modLink := filepath.Join(t.TempDir(), "mod")
	if err := os.Symlink(dir, modLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	libLink := filepath.Join(dir, "liblink")
	if err := os.Symlink(filepath.Join(dir, "lib"), libLink); err != nil {
		t.Fatal(err)
	}
	countGaps := func(name, dir string) {
		t.Helper()
		rep, err := UnusedRequires(Options{Dir: dir})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := 2 // svc, lib/win
		if dir == libLink {
			want = 1 // lib/win — lib 자신은 liblink라는 이름으로 로드됐다
		}
		if !slices.Equal(rep.Limitations, []string{platformGapNote(want)}) {
			t.Errorf("%s: limitations = %q, want %d directories", name, rep.Limitations, want)
		}
	}
	countGaps("symlinked checkout", modLink)
	countGaps("symlink to a package directory", libLink)
	t.Chdir(modLink)
	countGaps("relative --dir in a symlinked checkout", ".")
}

// scanTree는 go list 없이 dir 모듈을 패턴으로 걸어 로드되지 않은 디렉터리를 센다. loaded는 이번 로드가 import를 읽었다고
// 둘 모듈 상대 디렉터리다.
func scanTree(t *testing.T, dir string, patterns, ignores []string, loaded ...string) unloadedScan {
	t.Helper()
	scan, err := scanUnloadedDirs(Options{Dir: dir, Patterns: patterns}, "example.com/fixture", dir, ignores,
		loadedAt(dir, loaded...))
	if err != nil {
		t.Fatal(err)
	}
	return scan
}

// loadedAt은 모듈 상대 디렉터리마다 그 디렉터리에서 로드된 패키지가 있는 집합을 만든다.
func loadedAt(dir string, rels ...string) *loadedDirs {
	var pkgs []*packages.Package
	for _, rel := range rels {
		pkgs = append(pkgs, &packages.Package{PkgPath: "example.com/fixture/" + rel, Dir: filepath.Join(dir, rel)})
	}
	return newLoadedDirs(pkgs)
}

// TestScanUnloadedDirsFollowsGoListWalk는 디렉터리 순회가 go list ./...와 go mod tidy의 규칙을 따르는지 확인한다 — 걸러야 할
// 디렉터리를 세면 알릴 것이 없는 저장소에 거짓 limitation이 붙는다. 세는 것: 이 플랫폼 파일이 없는 디렉터리, 테스트 파일만
// 있는 디렉터리(tidy는 테스트 import도 읽는다), ignore 아닌 항이 있는 제약 파일, vendor 디렉터리 자신. 세지 않는 것: 로드된
// 디렉터리, //go:build ignore 파일만 있는 디렉터리, 이름이 _·.로 시작하는 파일만 있는 디렉터리, testdata·_·. 디렉터리,
// 하위 모듈, vendor 밑, go.mod ignore 지시문("./rooted"는 모듈 루트의 rooted만, "anywhere"는 어디서나 — 세그먼트
// 경계에서만이라 anywherex는 아니다)이 가리키는 디렉터리.
func TestScanUnloadedDirsFollowsGoListWalk(t *testing.T) {
	other := otherGOOS()
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":                            "package main\n\nfunc main() {}\n",
		"loaded/l.go":                        "package loaded\n",
		"gap/g_" + other + ".go":             "package gap\n",
		"gap/deeper/d_" + other + "_test.go": "package deeper\n",
		"either/e.go":                        "//go:build ignore || " + other + "\n\npackage either\n",
		"ignoreonly/i.go":                    "//go:build ignore\n\npackage main\n",
		"names/_x.go":                        "package names\n",
		"names/.y.go":                        "package names\n",
		"docs/notes.txt":                     "no Go here\n",
		"testdata/t/t_" + other + ".go":      "package t\n",
		"_tools/u_" + other + ".go":          "package u\n",
		".cache/h_" + other + ".go":          "package h\n",
		"nested/go.mod":                      "module example.com/nested\n\ngo 1.27\n",
		"nested/n_" + other + ".go":          "package nested\n",
		"vendor/v_" + other + ".go":          "package vendor\n",
		"vendor/sub/s_" + other + ".go":      "package sub\n",
		"rooted/r_" + other + ".go":          "package rooted\n",
		"deep/rooted/r_" + other + ".go":     "package rooted\n",
		"deep/anywhere/a_" + other + ".go":   "package anywhere\n",
		"deep/anywherex/a_" + other + ".go":  "package anywherex\n",
	})
	scan := scanTree(t, dir, nil, []string{"./rooted", "anywhere"}, ".", "loaded")
	want := []string{"deep/anywherex", "deep/rooted", "either", "gap", "gap/deeper", "vendor"}
	if !slices.Equal(scan.dirs, want) || scan.unreadable != 0 {
		t.Fatalf("counted %v (unreadable %d), want %v", scan.dirs, scan.unreadable, want)
	}
}

// TestScanUnloadedDirsPatternRoots는 패턴마다 걷는 뿌리를 go 명령의 패턴 해석과 같게 정하는지 확인한다. 디렉터리 패턴은
// 걷기 시작하는 디렉터리 자신의 이름만 .·_·testdata 규칙을 받고(./a/_b/c/...는 c를 걷는다), 주 모듈을 덮는 import 경로
// 패턴은 모듈 루트 밑 모든 세그먼트가 그 규칙을 받는다. 와일드카드 없는 패턴, 중간의 ..., std, 모듈 밖 경로, 하위 모듈 안,
// 없는 디렉터리, 파일을 가리킨 패턴은 걷지 않는다 — 없는 디렉터리나 파일은 읽지 못한 디렉터리가 아니다.
func TestScanUnloadedDirsPatternRoots(t *testing.T) {
	other := otherGOOS()
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":                      "package main\n\nfunc main() {}\n",
		"svc/s_" + other + ".go":       "package svc\n",
		"a/_b/c/c_" + other + ".go":    "package c\n",
		"x/_y/z/z_" + other + ".go":    "package z\n",
		"_tools/t_" + other + ".go":    "package t\n",
		"testdata/d_" + other + ".go":  "package d\n",
		"nested/go.mod":                "module example.com/fixture/nested\n\ngo 1.27\n",
		"nested/in/n_" + other + ".go": "package in\n",
	})
	up := "../" + filepath.Base(dir) + "/svc/..."
	for _, tc := range []struct {
		patterns []string
		want     []string
	}{
		{nil, []string{"svc"}},
		{[]string{"./a/_b/c/..."}, []string{"a/_b/c"}},
		{[]string{"./x/_y/z/..."}, []string{"x/_y/z"}},
		{[]string{"example.com/fixture/x/_y/z/..."}, nil},
		{[]string{"./_tools/..."}, nil},
		{[]string{"./testdata/..."}, nil},
		{[]string{"./svc/.../x"}, nil},
		{[]string{"./svc"}, nil},
		{[]string{"example.com/..."}, []string{"svc"}},
		{[]string{"..."}, []string{"svc"}},
		{[]string{"work"}, []string{"svc"}},
		{[]string{"std"}, nil},
		{[]string{"example.com/other/..."}, nil},
		{[]string{"./nested/in/..."}, nil},
		{[]string{"example.com/fixture/nested/..."}, nil},
		{[]string{filepath.Join(dir, "svc") + string(filepath.Separator) + "..."}, []string{"svc"}},
		{[]string{up}, []string{"svc"}},
		{[]string{"../..."}, nil},
		{[]string{"./nope/..."}, nil},
		{[]string{"example.com/fixture/nope/..."}, nil},
		{[]string{"./main.go/..."}, nil},
		{[]string{"example.com/fixture/main.go/..."}, nil},
	} {
		scan := scanTree(t, dir, tc.patterns, nil, ".")
		if !slices.Equal(scan.dirs, tc.want) || scan.unreadable != 0 {
			t.Errorf("%q: counted %v (unreadable %d), want %v", tc.patterns, scan.dirs, scan.unreadable, tc.want)
		}
	}
}

// TestScanUnloadedDirsSpecialFiles는 tidy(imports.ScanDir)처럼 정규 파일만 Go 파일로 보는지 확인한다: FIFO는 열지 않고
// (막히지 않는다) 세지 않으며, 정규 파일을 가리키는 .go 심링크는 Go 파일이고, 깨진 심링크는 아니다. 디렉터리 심링크는 go
// list처럼 따라가지 않는다. 읽을 수 없는 .go 파일은 제약을 모르므로 센다 — tidy도 그 디렉터리를 오류 난 패키지로 다룬다.
func TestScanUnloadedDirsSpecialFiles(t *testing.T) {
	other := otherGOOS()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "o_"+other+".go"), []byte("package o\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":                   "package main\n\nfunc main() {}\n",
		"target/t_" + other + ".go": "package target\n",
		"locked/l.go":               "//go:build ignore\n\npackage locked\n",
		"symfile/keep.txt":          "",
		"broken/keep.txt":           "",
		"fifo/keep.txt":             "",
	})
	testutil.MakeFIFO(t, filepath.Join(dir, "fifo", "p_"+other+".go"))
	for link, target := range map[string]string{
		filepath.Join(dir, "symfile", "s.go"): filepath.Join(dir, "target", "t_"+other+".go"),
		filepath.Join(dir, "broken", "b.go"):  filepath.Join(dir, "missing.go"),
		filepath.Join(dir, "symdir"):          outside,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"symfile", "target"}
	if os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked", "l.go")
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		want = []string{"locked", "symfile", "target"}
	}
	var scan unloadedScan
	var err error
	testutil.FinishWithin(t, testutil.BlockDeadline, func() {
		scan, err = scanUnloadedDirs(Options{Dir: dir}, "example.com/fixture", dir, nil, loadedAt(dir, "."))
	})
	if err != nil || !slices.Equal(scan.dirs, want) || scan.unreadable != 0 {
		t.Fatalf("counted %v (unreadable %d, %v), want %v", scan.dirs, scan.unreadable, err, want)
	}
}

// TestScanUnloadedDirsStopsAtUnreadableDirectory는 목록을 읽지 못한 디렉터리에서 그 패턴의 순회를 멈추고 unreadable로 세는지
// 확인한다 — go list도 같은 자리에서 순회를 멈추고 패턴 오류를 내므로 그 뒤의 디렉터리(z)는 go list가 보지 않았다. 이어
// 걸어 세면 "이 플랫폼 파일이 없다"는 거짓 이유를 붙인다. 겹치는 패턴은 같은 디렉터리를 두 번 세지 않고, 뿌리까지 가지
// 못한 패턴도 센다.
func TestScanUnloadedDirsStopsAtUnreadableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions that bind the current user")
	}
	other := otherGOOS()
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":                    "package main\n\nfunc main() {}\n",
		"a/a_" + other + ".go":       "package a\n",
		"m/inner/i_" + other + ".go": "package inner\n",
		"z/z_" + other + ".go":       "package z\n",
	})
	locked := filepath.Join(dir, "m")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o755); err != nil {
			t.Errorf("restoring %s for cleanup: %v", locked, err)
		}
	})
	if scan := scanTree(t, dir, nil, nil, "."); !slices.Equal(scan.dirs, []string{"a"}) || scan.unreadable != 1 {
		t.Fatalf("the walk must stop at the unreadable directory: %+v", scan)
	}
	if scan := scanTree(t, dir, []string{"./...", "./..."}, nil, "."); !slices.Equal(scan.dirs, []string{"a"}) ||
		scan.unreadable != 1 {
		t.Fatalf("overlapping patterns walk a tree once and count the unreadable directory once: %+v", scan)
	}
	if scan := scanTree(t, dir, []string{"./m/inner/..."}, nil, "."); len(scan.dirs) != 0 || scan.unreadable != 1 {
		t.Fatalf("a pattern whose root cannot be reached must be counted: %+v", scan)
	}
	lims := unusedLimitations(0, excludedScan{}, unloadedScan{dirs: []string{"a"}, unreadable: 1}, dependencyTestScan{})
	if !slices.Equal(lims, []string{platformGapNote(1), "1 directories could not be read, so the directories " +
		"under and after them were not checked for Go files of other platforms; requires imported only there " +
		"may be reported as unused"}) {
		t.Fatalf("both counts must be reported: %q", lims)
	}
}
