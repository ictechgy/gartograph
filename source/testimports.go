package source

import (
	"fmt"
	"go/build"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// testOnlyImportLimitation은 --tests 없이 수확한 문서에서 다른 패키지의 _test.go 파일에서만 import되는
// 주 모듈 패키지를 실제로 세어 limitation 문장으로 만든다. 그런 패키지(테스트 헬퍼)는 importer가 전부
// 그래프 밖이라 metrics의 orphan, dead의 unreachable로 보이지만, 사실은 수확 범위가 테스트를 뺀 결과다.
// 기본 보고 범위·사유는 바꾸지 않는다 — 그 몫이 어디서 왔는지만 알린다. 없으면 ""다.
// --pattern이나 모듈 하위 --dir로 수확 범위를 좁히면 범위 밖 제품 importer는 적재되지 않아 셀 수 없다 — 그때는
// 문구가 "적재한 패키지 중"으로 범위를 밝히고, --tests만으로 풀리지 않는다는 것도 말한다.
func testOnlyImportLimitation(loaded []*packages.Package, kept map[string]*packages.Package, opts Options,
	root, moduleDir string) string {
	testOnly := testOnlyImported(loaded, kept, buildContext(opts))
	if len(testOnly) == 0 {
		return ""
	}
	if len(opts.Patterns) > 0 || !sameDir(root, moduleDir) {
		return fmt.Sprintf("%d packages are imported only by _test.go files among the loaded packages (first: %s); "+
			"test files were not harvested and packages outside --dir/--pattern were not loaded, so these packages "+
			"may appear orphaned and their symbols unreachable (use --tests to include test importers)",
			len(testOnly), testOnly[0])
	}
	return fmt.Sprintf("%d packages are imported only by _test.go files of other packages (first: %s); "+
		"test files were not harvested, so these packages may appear orphaned and their symbols unreachable "+
		"(use --tests to include)", len(testOnly), testOnly[0])
}

// sameDir는 두 경로가 같은 디렉터리인지 본다 — go 명령이 돌려준 모듈 디렉터리와 --dir의 절대 경로는 심볼릭
// 링크(macOS의 /var → /private/var 등) 표기가 다를 수 있다. 모듈 디렉터리를 모르면 같지 않다고 본다.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// testOnlyImported는 적재한 어느 패키지(exclude로 뺀 것 포함)의 제품 파일도 import하지 않고, 다른 주 모듈 패키지의 테스트 파일
// (같은 디렉터리의 package p와 package p_test 둘 다)만 import하는 주 모듈 패키지 경로를 정렬해 돌려준다.
// main 패키지는 import될 수 없어 대상이 아니다. 자기 디렉터리의 테스트만 import하는 패키지(외부 테스트를 둔
// 라이브러리)도 세지 않는다 — 테스트 헬퍼가 아니라 자기 테스트다.
// 테스트 파일의 import는 go/build로 읽는다 — 빌드 제약(//go:build, _GOOS 접미사, 태그)을 go list와 같이
// 따라야 빠진 테스트 파일의 import를 세지 않는다(문맥의 범위와 남은 차이는 buildContext).
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
		// go/build는 파일 하나가 오류(헤더 문법, 테스트의 cgo, 정규 파일 아님)여도 그 파일만 InvalidGoFiles로
		// 빼고 나머지 파일의 import를 채운 채 오류를 돌려준다. 오류 종류로 디렉터리를 통째로 버리면 남은
		// 테스트 importer가 사라진다 — 디렉터리를 못 읽으면 import가 비어 있을 뿐이다.
		bp, _ := ctxt.ImportDir(p.Dir, 0)
		if bp == nil {
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

// buildContext는 load가 go list를 띄우는 환경(loaderEnv)에서 읽은 플랫폼·태그·cgo의 go/build 문맥이다.
//   - 플랫폼은 그 환경의 GOOS·GOARCH다(--goos/--goarch 포함).
//   - 태그는 --tags가 있으면 그것이고(명령줄 -tags가 GOFLAGS를 이긴다), 없으면 그 환경 GOFLAGS의 -tags다.
//     캡처 수확은 loaderEnv가 GOFLAGS를 비워 태그가 없다.
//   - cgo는 CGO_ENABLED가 0·1이면 그 값, 아니면 go/build 기본 문맥처럼 호스트 플랫폼에서만 호스트 기본값이고
//     크로스 플랫폼이면 꺼진다.
//
// 남은 차이: `go env -w`로 저장한 GOFLAGS·CGO_ENABLED는 읽지 않는다(loaderEnv와 같은 이유로 분석 대상에서 go env를
// 돌리지 않는다). C 컴파일러가 없어 go 명령이 cgo를 끄는 호스트도 구분하지 않는다. 둘 다 `//go:build cgo`나
// 저장된 태그로만 켜지는 테스트 파일의 import를 세는지만 바꾼다.
// 파일은 readFlags로 열고 정규 파일만 읽는다 — 분석 대상이 _test.go 이름의 FIFO를 두면 보통의 open이 영원히
// 막힌다(isGeneratedFile과 같은 이유).
func buildContext(opts Options) build.Context {
	env := loaderEnv(opts)
	ctxt := build.Default
	if goos := envValue(env, "GOOS"); goos != "" {
		ctxt.GOOS = goos
	}
	if goarch := envValue(env, "GOARCH"); goarch != "" {
		ctxt.GOARCH = goarch
	}
	tags := opts.Tags
	if tags == "" {
		tags = goflagsTags(envValue(env, "GOFLAGS"))
	}
	ctxt.BuildTags = nil
	if tags != "" {
		ctxt.BuildTags = strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == ' ' })
	}
	switch envValue(env, "CGO_ENABLED") {
	case "1":
		ctxt.CgoEnabled = true
	case "0":
		ctxt.CgoEnabled = false
	default:
		ctxt.CgoEnabled = build.Default.CgoEnabled && ctxt.GOOS == build.Default.GOOS && ctxt.GOARCH == build.Default.GOARCH
	}
	ctxt.OpenFile = openRegular
	return ctxt
}

// envValue는 환경 목록에서 key의 값을 돌려준다. 같은 키는 뒤 값이 이긴다(os/exec와 같다).
func envValue(env []string, key string) string {
	value := ""
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, key+"="); ok {
			value = v
		}
	}
	return value
}

// goflagsTags는 GOFLAGS(공백으로 구분한 -flag=value 목록)에서 마지막 -tags 값을 돌려준다.
func goflagsTags(goflags string) string {
	tags := ""
	for _, field := range strings.Fields(goflags) {
		for _, prefix := range []string{"-tags=", "--tags="} {
			if v, ok := strings.CutPrefix(field, prefix); ok {
				tags = v
			}
		}
	}
	return tags
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
