package source

import (
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/tools/go/packages"
)

// tidyGraph는 go mod tidy가 테스트 의존을 판정할 때 쓰는 모듈 그래프의 버전표 근사다(scanDependencyTests).
//
// tidy의 그래프는 "all"의 패키지가 속한 의존 모듈(루트)에서 시작한다(go1.27.1 modload buildlist.go
// readModGraph). 루트의 go.mod require는 늘 그래프에 들고, 그 require의 go.mod는 가지치기 안 된(go 1.17 미만)
// 모듈을 거쳐 닿을 때만 읽혀 전이 require가 더해진다. 주 go.mod의 exclude에 걸린 require는 무시된다(Go 1.16+).
// 여기서는 그 셋을 따른다. 테스트 의존을 루트로 더하며 늘어나는 require는 담지 않는다 — 그만큼 그래프가 작아
// "제공하지 못한다"를 더 자주 답하고, 판정은 "사용" 쪽으로 기운다. 읽지 못한 go.mod도 같은 쪽으로 기울며
// unreadable로 센다.
type tidyGraph struct {
	main     goModFacts // 주 go.mod의 exclude·replace
	modDir   string     // 디렉터리 replace의 기준
	modCache string     // 모듈 캐시 루트(GOMODCACHE) — 가지치기 안 된 모듈의 require go.mod를 읽는다
	// versions는 모듈 경로별로 그래프에 든 최고 버전이다.
	versions map[string]string
	// visited는 go.mod를 읽은 "경로@버전"이다.
	visited map[string]bool
	// unreadable은 읽지 못한 go.mod 수다.
	unreadable int
	// vendored는 go.mod 없이 적재된 의존 모듈 경로다(vendor/).
	vendored map[string]bool
}

// buildTidyGraph는 도달한 패키지의 의존 모듈(루트)과 그 go.mod로 tidyGraph를 만든다.
func buildTidyGraph(reachable []*packages.Package, main goModFacts, modDir string) *tidyGraph {
	g := &tidyGraph{main: main, modDir: modDir, modCache: moduleCacheRoot(reachable),
		versions: map[string]string{}, visited: map[string]bool{}, vendored: map[string]bool{}}
	for _, p := range reachable {
		m := p.Module
		if m == nil || m.Main {
			continue
		}
		raiseVersion(g.versions, m.Path, m.Version)
		if m.GoMod == "" {
			g.vendored[m.Path] = true
			continue
		}
		key := module.Version{Path: m.Path, Version: m.Version}.String()
		if g.visited[key] {
			continue
		}
		g.visited[key] = true
		g.addRoot(m.GoMod, prunedGoVersion(m.GoVersion))
	}
	return g
}

// addRoot는 루트 go.mod의 require를 그래프에 더한다. 루트가 가지치기 안 된 모듈이면 그 require의 go.mod를 따라가
// 전이 require도 더한다(followUnpruned).
func (g *tidyGraph) addRoot(goMod string, pruned bool) {
	requires, err := dependencyRequires(goMod)
	if err != nil {
		g.unreadable++
		return
	}
	next := g.addRequires(requires)
	if !pruned {
		g.followUnpruned(next)
	}
}

// addRequires는 exclude에 걸리지 않은 require를 그래프 버전표에 더하고 그 목록을 돌려준다.
func (g *tidyGraph) addRequires(requires []*modfile.Require) []module.Version {
	var out []module.Version
	for _, r := range requires {
		if g.main.excludes[r.Mod.String()] {
			continue
		}
		raiseVersion(g.versions, r.Mod.Path, r.Mod.Version)
		out = append(out, r.Mod)
	}
	return out
}

// followUnpruned는 가지치기 안 된 루트에서 닿는 require의 go.mod를 너비 우선으로 읽어 전이 require를 더한다. 가지치기
// 안 된 모듈을 거친 경로는 끝까지 가지치기 없이 읽힌다(readModGraph의 unpruned 전파와 같다).
func (g *tidyGraph) followUnpruned(queue []module.Version) {
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		if g.visited[m.String()] {
			continue
		}
		g.visited[m.String()] = true
		goMod, err := g.goModFile(m)
		if err != nil {
			g.unreadable++
			continue
		}
		requires, err := dependencyRequires(goMod)
		if err != nil {
			g.unreadable++
			continue
		}
		queue = append(queue, g.addRequires(requires)...)
	}
}

// goModFile은 모듈 버전 m의 go.mod 경로다. 주 go.mod의 replace(버전을 적은 것이 먼저)가 디렉터리면 그 go.mod를, 모듈이면
// 대상 모듈의 캐시 go.mod를 쓴다. 나머지는 모듈 캐시의 cache/download/…/@v/<버전>.mod다 — go 명령이 그래프를 만들며
// 받아 둔 파일이다.
func (g *tidyGraph) goModFile(m module.Version) (string, error) {
	if r, ok := g.replacement(m); ok {
		if modfile.IsDirectoryPath(r.Path) {
			dir := r.Path
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(g.modDir, dir)
			}
			return filepath.Join(dir, "go.mod"), nil
		}
		m = r
	}
	if g.modCache == "" {
		return "", fmt.Errorf("module cache of %s is unknown", m)
	}
	path, err := module.EscapePath(m.Path)
	if err != nil {
		return "", err
	}
	version, err := module.EscapeVersion(m.Version)
	if err != nil {
		return "", err
	}
	return filepath.Join(g.modCache, "cache", "download", path, "@v", version+".mod"), nil
}

// replacement는 주 go.mod에서 m에 걸리는 replace 대상이다. go 명령처럼 버전을 적은 replace가 버전 없는 것보다 먼저다.
func (g *tidyGraph) replacement(m module.Version) (module.Version, bool) {
	var fallback *modfile.Replace
	for _, r := range g.main.replaces {
		if r.Old.Path != m.Path {
			continue
		}
		if r.Old.Version == m.Version {
			return r.New, true
		}
		if r.Old.Version == "" {
			fallback = r
		}
	}
	if fallback != nil {
		return fallback.New, true
	}
	return module.Version{}, false
}

// moduleCacheRoot는 모듈 캐시 루트다. 루트 모듈의 go.mod가 캐시(…/cache/download/…)에 있으면 그 앞부분을 쓴다 — go
// 명령이 실제로 쓴 캐시다. 없으면(모든 의존이 디렉터리 replace) GOMODCACHE, 그다음 GOPATH의 첫 항목/pkg/mod다. go env를
// 분석 대상 디렉터리에서 돌리지 않는다(loaderEnv의 GOTOOLCHAIN 이유와 같다).
func moduleCacheRoot(reachable []*packages.Package) string {
	marker := string(filepath.Separator) + filepath.Join("cache", "download") + string(filepath.Separator)
	for _, p := range reachable {
		if p.Module == nil || p.Module.Main {
			continue
		}
		if i := strings.Index(p.Module.GoMod, marker); i > 0 {
			return p.Module.GoMod[:i]
		}
	}
	if dir := os.Getenv("GOMODCACHE"); dir != "" {
		return dir
	}
	if gopath := filepath.SplitList(build.Default.GOPATH); len(gopath) > 0 && gopath[0] != "" {
		return filepath.Join(gopath[0], "pkg", "mod")
	}
	return ""
}

// prunedGoVersion은 go 지시문 버전이 모듈 그래프 가지치기(go 1.17 이상)를 켜는지 본다. 지시문이 없는 모듈은 go 1.16으로
// 다뤄져 가지치기되지 않는다.
func prunedGoVersion(version string) bool {
	major, rest, _ := strings.Cut(version, ".")
	maj, err := strconv.Atoi(major)
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(leadingDigits(rest))
	return maj > 1 || (err == nil && minor >= 17)
}

// leadingDigits는 s 앞의 숫자 부분이다 — 프리릴리스 버전(`1.21rc1`)의 부 버전을 읽는다.
func leadingDigits(s string) string {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	return s[:end]
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
