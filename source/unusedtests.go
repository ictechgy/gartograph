package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"golang.org/x/tools/go/packages"
)

// 의존 패키지 테스트의 require 판정.
//
// go mod tidy는 "all"(주 모듈 패키지와 그 테스트가 import하는 패키지 전체)의 테스트도 적재한다. 그 테스트만 import하는
// 패키지의 모듈이 늘 go.mod에 남는 것은 아니다 — tidy는 "all"의 패키지가 속한 모듈을 루트로 모듈 그래프를 만들고, 그
// 그래프가 그 모듈을 고른 버전 이상으로 제공하지 못할 때만 루트로 더한다(go1.27.1 cmd/go/internal/modload
// buildlist.go tidyPrunedRoots의 "upgraded or otherwise-missing test dependencies"). 그래서 의존 모듈의 go.mod가 그
// 모듈을 같은 버전 이상으로 require하면 지워지고, require하지 않거나 더 낮은 버전이면 남는다 — pgx v5.11.0의
// gopkg.in/check.v1(yaml.v3 v3.0.1의 go.mod는 2016년 판을 require하고 pgx는 2020년 판을 고른다)이 남는 이유다.

// dependencyTestScan은 의존 패키지의 테스트 파일 import를 tidy 규칙으로 읽은 결과다.
type dependencyTestScan struct {
	// used는 tidy가 테스트 의존으로 go.mod에 남기는 require 모듈 경로다.
	used map[string]bool
	// unseenFiles는 이번 로드가 보지 못한 패키지를 import하는 의존 패키지 테스트 파일 수다 — 그 패키지 자신의
	// import(그리고 tidy가 그 모듈을 루트로 더하며 늘어나는 그래프)는 보지 못했다.
	unseenFiles int
	// unreadable은 읽지 못한 의존 패키지 디렉터리·테스트 파일·go.mod 수다.
	unreadable int
}

// tidyTestInput은 scanDependencyTests가 받는 주 모듈 쪽 사실이다.
type tidyTestInput struct {
	requires []requireEntry    // 주 go.mod의 require(버전 포함)
	modules  map[string]bool   // require 모듈 경로 집합(owningRequire 색인)
	used     map[string]bool   // 이미 사용으로 판정한 require
	unseen   func(string) bool // import가 이번 로드가 보지 못한 패키지인지(unseenImport)
}

// scanDependencyTests는 도달한 의존 패키지(주 모듈 밖, std 제외)의 테스트 파일을 읽어, tidy가 테스트 의존으로 남기는
// require를 고른다. 테스트 파일은 tidy처럼 모든 태그·플랫폼 파일을 읽고 //go:build ignore만 뺀다(tidyBuildsFile).
// import한 패키지가 이번 로드에 있으면 그 모듈은 이미 "all"의 루트라 사용이고, 없으면 그 경로를 require에 귀속해
// tidyGraphVersions가 그 모듈을 주 go.mod의 버전 이상으로 제공하지 못할 때만 사용으로 센다.
func scanDependencyTests(reachable []*packages.Package, in tidyTestInput) dependencyTestScan {
	scan := dependencyTestScan{used: map[string]bool{}}
	graph, unreadable := tidyGraphVersions(reachable)
	scan.unreadable += unreadable
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
// 읽고, 정규 파일만 고른다(읽기는 parseImportHeader가 막히지 않게 한다). 읽지 못한 디렉터리는 scan.unreadable로 센다.
func dependencyTestFiles(reachable []*packages.Package, scan *dependencyTestScan) []string {
	var files []string
	seen := map[string]bool{}
	for _, p := range reachable {
		if p.Module == nil || p.Module.Main || p.Dir == "" || seen[p.Dir] {
			continue
		}
		seen[p.Dir] = true
		entries, err := os.ReadDir(p.Dir)
		if err != nil {
			scan.unreadable++
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
				files = append(files, filepath.Join(p.Dir, e.Name()))
			}
		}
	}
	return files
}

// tidyGraphVersions는 tidy가 테스트 의존을 판정할 때 쓰는 모듈 그래프의 근사다 — "all"의 패키지가 속한 의존 모듈
// (루트)과 각 루트 go.mod의 require를 경로별 최고 버전으로 모은다. tidy의 그래프는 여기에 가지치기 안 된(go 1.17
// 미만) 모듈의 전이 require와, 테스트 의존을 루트로 더하며 늘어나는 require도 담는다. 근사는 그만큼 작아 "제공하지
// 못한다"를 더 자주 답한다 — 판정이 "사용" 쪽으로만 기운다. 둘째 값은 읽지 못한 go.mod 수다(그 루트의 require가
// 빠져 같은 쪽으로 기운다).
func tidyGraphVersions(reachable []*packages.Package) (map[string]string, int) {
	graph := map[string]string{}
	read := map[string]bool{}
	unreadable := 0
	for _, p := range reachable {
		m := p.Module
		if m == nil || m.Main {
			continue
		}
		raiseVersion(graph, m.Path, m.Version)
		if m.GoMod == "" || read[m.GoMod] {
			continue
		}
		read[m.GoMod] = true
		requires, err := dependencyRequires(m.GoMod)
		if err != nil {
			unreadable++
			continue
		}
		for _, r := range requires {
			raiseVersion(graph, r.Mod.Path, r.Mod.Version)
		}
	}
	return graph, unreadable
}

// dependencyRequires는 의존 모듈 go.mod의 require 목록이다. 모르는 지시문은 무시한다(ParseLax) — 의존 모듈의
// go.mod는 더 새 Go의 지시문을 쓸 수 있고, replace·exclude는 주 모듈의 것만 효력이 있어 읽지 않는다.
func dependencyRequires(path string) ([]*modfile.Require, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	f, err := modfile.ParseLax(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return f.Require, nil
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

// semverLess는 모듈 버전 a가 b보다 낮은지 본다. 빈 문자열은 "없음"이라 어떤 버전보다도 낮다(tidy의 "none"과 같다).
func semverLess(a, b string) bool {
	if a == "" {
		return b != ""
	}
	return semver.Compare(a, b) < 0
}
