package source

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
	"golang.org/x/tools/go/packages"
)

// 의존 패키지 테스트의 require 판정.
//
// go mod tidy는 "all"(주 모듈 패키지와 그 테스트가 import하는 패키지 전체)의 테스트도 적재한다. 그 테스트만 import하는
// 패키지의 모듈이 늘 go.mod에 남는 것은 아니다 — tidy는 "all"의 패키지가 속한 모듈을 루트로 모듈 그래프를 만들고, 그
// 그래프가 그 모듈을 고른 버전 이상으로 제공하지 못할 때만 루트로 더한다(go1.27.1 cmd/go/internal/modload
// buildlist.go tidyPrunedRoots의 "upgraded or otherwise-missing test dependencies"). 그래서 어느 루트의 go.mod(go 1.17
// 미만 루트면 그 전이 require까지)가 그 모듈을 같은 버전 이상으로 require하면 지워지고, 아니면 남는다 — pgx
// v5.11.0의 gopkg.in/check.v1(yaml.v3 v3.0.1의 go.mod는 2016년 판을 require하고 pgx는 2020년 판을 고른다)이 남는
// 이유다. 그래프의 근사는 tidyGraph에 있다.

// dependencyTestScan은 의존 패키지의 테스트 파일 import를 tidy 규칙으로 읽은 결과다.
type dependencyTestScan struct {
	// used는 tidy가 테스트 의존으로 go.mod에 남기는 require 모듈 경로다.
	used map[string]bool
	// unseenFiles는 이번 로드가 보지 못한 패키지를 import하는 의존 패키지 테스트 파일 수다 — 그 패키지 자신의
	// import(그리고 tidy가 그 모듈을 루트로 더하며 늘어나는 그래프)는 보지 못했다.
	unseenFiles int
	// unreadable은 읽지 못한 의존 패키지 디렉터리·테스트 파일·go.mod 수다.
	unreadable int
	// vendored는 go.mod 없이 적재된 의존 모듈 수다(vendor/ — go mod vendor는 의존 모듈의 go.mod와 테스트 파일을
	// 옮기지 않는다). tidy는 vendor/를 보지 않고 모듈 캐시를 읽으므로, 그 테스트 의존은 판정하지 못한다.
	vendored int
	// workspaceReplaced는 go.work의 replace로 바뀐 의존 모듈 수다 — tidy는 go.work를 보지 않아 원래 모듈의 테스트
	// 파일을 읽지만, 여기서는 적재된(바뀐) 디렉터리의 테스트 파일을 읽는다.
	workspaceReplaced int
}

// tidyTestInput은 scanDependencyTests가 받는 주 모듈 쪽 사실이다.
type tidyTestInput struct {
	requires []requireEntry    // 주 go.mod의 require(버전 포함)
	modules  map[string]bool   // require 모듈 경로 집합(owningRequire 색인)
	used     map[string]bool   // 이미 사용으로 판정한 require
	unseen   func(string) bool // import가 이번 로드가 보지 못한 패키지인지(unseenImport)
	mainMod  goModFacts        // 주 go.mod의 exclude·replace(tidyGraph)
	modDir   string            // 주 모듈 디렉터리(디렉터리 replace의 기준)
}

// scanDependencyTests는 도달한 의존 패키지(주 모듈 밖, std 제외)의 테스트 파일을 읽어, tidy가 테스트 의존으로 남기는
// require를 고른다. 테스트 파일은 tidy처럼 모든 태그·플랫폼 파일을 읽고 //go:build ignore만 뺀다(tidyBuildsFile).
// import한 패키지가 이번 로드에 있으면 그 모듈은 이미 "all"의 루트라 사용이고, 없으면 그 경로를 require에 귀속해
// tidyGraph가 그 모듈을 주 go.mod의 버전 이상으로 제공하지 못할 때만 사용으로 센다.
func scanDependencyTests(reachable []*packages.Package, in tidyTestInput) dependencyTestScan {
	scan := dependencyTestScan{used: map[string]bool{}}
	tg := buildTidyGraph(reachable, in.mainMod, in.modDir)
	graph := tg.versions
	scan.unreadable += tg.unreadable
	scan.vendored = len(tg.vendored)
	scan.workspaceReplaced = len(tg.workspaceReplaced)
	versions := requireVersions(in.requires)
	for _, file := range dependencyTestFiles(reachable, &scan) {
		f, err := parseImportHeader(file)
		if err != nil {
			scan.unreadable++
			continue
		}
		if !tidyBuildsFile(f) {
			continue
		}
		blind := false
		for _, spec := range f.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil || !in.unseen(imp) {
				// 로드된 패키지(모듈이 이미 루트)와 std는 판정에 더할 것이 없다.
				continue
			}
			blind = true
			m, ok := owningRequire(imp, in.modules)
			if ok && !in.used[m] && semverLess(graph[m], versions[m]) {
				scan.used[m] = true
			}
		}
		if blind {
			scan.unseenFiles++
		}
	}
	return scan
}

// dependencyTestFiles는 도달한 의존 패키지 디렉터리의 _test.go 파일 경로를 모은다. 디렉터리는 패키지마다 한 번만
// 읽는다(파일 읽기는 parseImportHeader가 막히지 않게 한다). go 명령이 무시하는 `_`·`.`로 시작하는 이름은 고르지
// 않는다 — tidy도 그 import를 세지 않는다. go.mod 없이 적재된(vendor/) 모듈의 디렉터리는 테스트 파일이 없어 읽지
// 않는다(scan.vendored가 그 공백을 센다). 읽지 못한 디렉터리는 scan.unreadable로 센다.
func dependencyTestFiles(reachable []*packages.Package, scan *dependencyTestScan) []string {
	var files []string
	seen := map[string]bool{}
	for _, p := range reachable {
		if p.Module == nil || p.Module.Main || p.Module.GoMod == "" || p.Dir == "" || seen[p.Dir] {
			continue
		}
		seen[p.Dir] = true
		entries, err := os.ReadDir(p.Dir)
		if err != nil {
			scan.unreadable++
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && goTestFileName(e.Name()) {
				files = append(files, filepath.Join(p.Dir, e.Name()))
			}
		}
	}
	// 도달 목록의 순서(import 맵 순회)가 읽는 순서에 닿지 않게 한다 — 판정은 집합·개수라 순서와 무관하지만 고정해 둔다.
	sort.Strings(files)
	return files
}

// goTestFileName은 go 명령이 패키지의 테스트 파일로 읽는 이름인지 본다 — `_test.go`로 끝나고 `_`·`.`로 시작하지
// 않는다(go/build는 그런 이름을 무시한다).
func goTestFileName(name string) bool {
	return strings.HasSuffix(name, "_test.go") && !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".")
}

// requireVersions는 주 go.mod require의 경로별 버전이다(같은 경로가 여럿이면 높은 쪽). tidy가 비교하는 "고른 버전"의
// 근사다 — tidy를 거친 go.mod는 고른 버전을 그대로 적는다.
func requireVersions(requires []requireEntry) map[string]string {
	out := map[string]string{}
	for _, r := range requires {
		raiseVersion(out, r.path, r.version)
	}
	return out
}

// raiseVersion은 versions[path]를 version과 비교해 높은 쪽으로 둔다.
func raiseVersion(versions map[string]string, path, version string) {
	if semverLess(versions[path], version) {
		versions[path] = version
	}
}

// semverLess는 모듈 버전 a가 b보다 낮은지 본다. 빈 문자열은 "없음"이라 어떤 버전보다도 낮다(tidy의 "none"과 같다) —
// semver.Compare는 올바르지 않은 문자열(빈 문자열 포함)을 올바른 버전보다 낮게, 서로는 같게 본다.
func semverLess(a, b string) bool {
	return semver.Compare(a, b) < 0
}
