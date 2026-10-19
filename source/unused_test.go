package source

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"testing"

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
	testutil.FinishWithin(t, testutil.BlockDeadline, func() { scan = scanExcludedFiles(walkImports(pkgs)) })
	if scan.files != 1 || scan.unreadable != 1 || len(scan.imports) != 1 || scan.imports[0] != "example.com/dep" {
		t.Fatalf("want the regular file read and the FIFO counted unreadable: %+v", scan)
	}
	lims := unusedLimitations(nil, scan)
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
