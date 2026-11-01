package source

import (
	"errors"
	"fmt"
	"go/build"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// testOnlyImportLimitation은 --tests 없이 수확한 문서에서 다른 패키지의 _test.go 파일에서만 import되는
// 주 모듈 패키지를 실제로 세어 limitation 문장으로 만든다. 그런 패키지(테스트 헬퍼)는 importer가 전부
// 그래프 밖이라 metrics의 orphan, dead의 unreachable로 보이지만, 사실은 수확 범위가 테스트를 뺀 결과다.
// 기본 보고 범위·사유는 바꾸지 않는다 — 그 몫이 어디서 왔는지만 알린다. 없으면 ""다.
func testOnlyImportLimitation(loaded []*packages.Package, kept map[string]*packages.Package, opts Options) string {
	testOnly := testOnlyImported(loaded, kept, buildContext(opts))
	if len(testOnly) == 0 {
		return ""
	}
	return fmt.Sprintf("%d packages are imported only by _test.go files of other packages (first: %s); "+
		"test files were not harvested, so these packages may appear orphaned and their symbols unreachable "+
		"(use --tests to include)", len(testOnly), testOnly[0])
}

// testOnlyImported는 적재한 어느 패키지(exclude로 뺀 것 포함)의 제품 파일도 import하지 않고, 다른 주 모듈 패키지의 테스트 파일
// (같은 디렉터리의 package p와 package p_test 둘 다)만 import하는 주 모듈 패키지 경로를 정렬해 돌려준다.
// main 패키지는 import될 수 없어 대상이 아니다. 자기 디렉터리의 테스트만 import하는 패키지(외부 테스트를 둔
// 라이브러리)도 세지 않는다 — 테스트 헬퍼가 아니라 자기 테스트다.
// 테스트 파일의 import는 go/build로 읽는다 — go list와 같은 빌드 제약(//go:build, _GOOS 접미사, --tags)을
// 따라야 빠진 테스트 파일의 import를 세지 않는다.
func testOnlyImported(loaded []*packages.Package, kept map[string]*packages.Package, ctxt build.Context) []string {
	imported := map[string]bool{}
	for _, p := range loaded {
		for path := range p.Imports {
			if path != p.PkgPath {
				imported[path] = true
			}
		}
	}
	candidates := map[string]bool{}
	for path, p := range kept {
		if mainModulePath(p) != "" && p.Name != "main" && !imported[path] {
			candidates[path] = true
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	// 제품 파일의 import는 이미 문서 간선이다 — 디렉터리에서 테스트 파일만 읽는다.
	ctxt.ReadDir = testFilesOnly
	found := map[string]bool{}
	for path, p := range kept {
		if mainModulePath(p) == "" || p.Dir == "" {
			continue
		}
		bp, err := ctxt.ImportDir(p.Dir, 0)
		if bp == nil || err != nil && !partialImport(err) {
			continue
		}
		for _, imp := range slices.Concat(bp.TestImports, bp.XTestImports) {
			if candidates[imp] && imp != path {
				found[imp] = true
			}
		}
	}
	out := make([]string, 0, len(found))
	for path := range found {
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}

// testFilesOnly는 디렉터리 항목 중 _test.go 파일만 돌려준다. go/build는 테스트 파일만 있는 디렉터리도
// TestImports·XTestImports를 채운다.
func testFilesOnly(dir string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []fs.FileInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

// partialImport는 go/build가 패키지 정보를 채운 채 돌려주는 오류인지 본다 — 테스트 파일이 없거나(NoGoError)
// 패키지 이름이 섞여도(MultiplePackageError) 읽은 테스트 import는 남아 있다. 그 밖의 오류(디렉터리 읽기
// 실패)는 그 디렉터리를 건너뛴다.
func partialImport(err error) bool {
	var noGo *build.NoGoError
	var multiple *build.MultiplePackageError
	return errors.As(err, &noGo) || errors.As(err, &multiple)
}

// buildContext는 load가 go list에 넘기는 플랫폼·태그와 같은 go/build 문맥이다. 파일은 readFlags로 열고
// 정규 파일만 읽는다 — 분석 대상이 _test.go 이름의 FIFO를 두면 보통의 open이 영원히 막힌다(isGeneratedFile과
// 같은 이유).
func buildContext(opts Options) build.Context {
	ctxt := build.Default
	if opts.GOOS != "" {
		ctxt.GOOS = opts.GOOS
	}
	if opts.GOARCH != "" {
		ctxt.GOARCH = opts.GOARCH
	}
	if opts.Tags != "" {
		ctxt.BuildTags = strings.FieldsFunc(opts.Tags, func(r rune) bool { return r == ',' || r == ' ' })
	}
	ctxt.OpenFile = openRegular
	return ctxt
}

// openRegular는 path를 막히지 않는 플래그로 열고 정규 파일이 아니면 거부한다.
func openRegular(path string) (io.ReadCloser, error) {
	f, err := os.OpenFile(path, readFlags, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	return f, nil
}
