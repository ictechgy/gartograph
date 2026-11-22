package source

import (
	"bytes"
	"fmt"
	"go/build"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/tools/go/packages"
)

// tidyGraph는 go mod tidy가 테스트 의존을 판정할 때 쓰는 모듈 그래프의 버전표 근사다(scanDependencyTests).
//
// tidy의 그래프는 "all"의 패키지가 속한 의존 모듈(루트)에서 시작한다(go1.27.1 modload buildlist.go
// readModGraph). 루트의 go.mod require는 늘 그래프에 들고, 그 require의 go.mod는 가지치기 안 된(go 1.17 미만)
// 모듈을 거쳐 닿을 때만 읽혀 전이 require가 더해진다. 같은 모듈 버전이라도 루트로 읽기와 가지치기 없는 걸음으로
// 읽기는 따로 센다(readModGraph의 dedupKey가 (모듈, 가지치기)다) — 가지치기된 루트가 가지치기 안 된 루트에서도 닿으면
// 그 걸음은 그 루트의 require를 따라 계속 내려간다. 주 go.mod의 exclude에 걸린 require는 무시된다(Go 1.16+). go.mod는
// 주 go.mod의 replace만 적용해 고른다 — tidy는 go.work를 보지 않는다. 테스트 의존을 루트로 더하며 늘어나는 require는
// 담지 않는다 — 그만큼 그래프가 작아 "제공하지 못한다"를 더 자주 답하고, 판정은 "사용" 쪽으로 기운다. 읽지 못한
// go.mod도 같은 쪽으로 기울며 unreadable로 센다.
type tidyGraph struct {
	main     goModFacts // 주 go.mod의 exclude·replace
	modDir   string     // 디렉터리 replace의 기준
	modCache string     // 모듈 캐시 루트(GOMODCACHE) — 루트와 가지치기 안 된 모듈의 go.mod를 읽는다
	// versions는 모듈 경로별로 그래프에 든 최고 버전이다.
	versions map[string]string
	// walked는 가지치기 없는 걸음으로 go.mod를 읽은 "경로@버전"이다(루트 읽기와 따로 센다).
	walked map[string]bool
	// unreadable은 읽지 못한 go.mod 수다.
	unreadable int
	// vendored는 go.mod 없이 적재된 의존 모듈 경로다(vendor/).
	vendored map[string]bool
	// workspaceReplaced는 주 go.mod의 replace로 고른 go.mod와 다른 파일로 적재된 의존 모듈 경로다 — go.work의
	// replace가 바꾼 모듈이다. 그래프는 tidy처럼 주 go.mod 기준 go.mod로 만들지만, 테스트 파일은 적재된(바뀐)
	// 디렉터리에서 읽는다.
	workspaceReplaced map[string]bool
}

// buildTidyGraph는 도달한 패키지의 의존 모듈(루트)과 그 go.mod로 tidyGraph를 만든다. 루트는 경로·버전순으로
// 처리한다 — 도달 목록의 순서(import 맵 순회)가 결과에 닿지 않게 한다.
func buildTidyGraph(reachable []*packages.Package, main goModFacts, modDir string) *tidyGraph {
	g := &tidyGraph{main: main, modDir: modDir, modCache: moduleCacheRoot(reachable),
		versions: map[string]string{}, walked: map[string]bool{}, vendored: map[string]bool{},
		workspaceReplaced: map[string]bool{}}
	for _, root := range dependencyRoots(reachable, g) {
		g.addRoot(root)
	}
	return g
}

// dependencyRoots는 도달한 패키지가 속한 의존 모듈 버전을 정렬해 돌려준다. 모든 루트 버전을 버전표에 올리고, go.mod
// 없이 적재된(vendor/) 모듈은 루트로 읽지 않고 g.vendored에, 주 go.mod 기준과 다른 go.mod로 적재된 모듈은
// g.workspaceReplaced에 센다.
func dependencyRoots(reachable []*packages.Package, g *tidyGraph) []module.Version {
	seen := map[module.Version]bool{}
	var roots []module.Version
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
		v := module.Version{Path: m.Path, Version: m.Version}
		if !seen[v] {
			seen[v] = true
			roots = append(roots, v)
			if resolved, err := g.goModFile(v); err == nil && differentFiles(m.GoMod, resolved) {
				g.workspaceReplaced[m.Path] = true
			}
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].String() < roots[j].String() })
	return roots
}

// addRoot는 루트 go.mod의 require를 그래프에 더한다. 루트가 가지치기 안 된 모듈이면(go.mod의 go 지시문으로 판정)
// 그 require의 go.mod를 따라가 전이 require도 더한다(followUnpruned).
func (g *tidyGraph) addRoot(root module.Version) {
	f, err := g.readGoMod(root)
	if err != nil {
		g.unreadable++
		return
	}
	next := g.addRequires(f.Require)
	if !prunedGoVersion(goDirective(f)) {
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
// 안 된 모듈을 거친 경로는 끝까지 가지치기 없이 읽힌다(readModGraph의 unpruned 전파와 같다). 같은 모듈 버전을 두 번
// 걷지 않아 순환에서 멈춘다. 버전표는 닿은 노드 전체의 최댓값이라 걷는 순서와 무관하다.
func (g *tidyGraph) followUnpruned(queue []module.Version) {
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		if g.walked[m.String()] {
			continue
		}
		g.walked[m.String()] = true
		f, err := g.readGoMod(m)
		if err != nil {
			g.unreadable++
			continue
		}
		queue = append(queue, g.addRequires(f.Require)...)
	}
}

// readGoMod는 모듈 버전 m의 go.mod를 읽는다(goModFile).
func (g *tidyGraph) readGoMod(m module.Version) (*modfile.File, error) {
	path, err := g.goModFile(m)
	if err != nil {
		return nil, err
	}
	return dependencyGoMod(path)
}

// goModFile은 모듈 버전 m의 go.mod 경로다. 주 go.mod의 replace(버전을 적은 것이 먼저)가 디렉터리면 그 go.mod를, 모듈이면
// 대상 모듈의 캐시 go.mod를 쓴다. 나머지는 모듈 캐시의 cache/download/…/@v/<버전>.mod다 — go 명령이 그래프를 만들며
// 받아 둔 파일이다. go.work의 replace는 적용하지 않는다(tidy가 보지 않는다).
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
	rel, err := cachedGoModPath(m)
	if err != nil {
		return "", err
	}
	return filepath.Join(g.modCache, rel), nil
}

// differentFiles는 두 경로가 모두 있고 서로 다른 파일인지 본다 — 심링크·경로 표기가 달라도(macOS의 /var와
// /private/var) 같은 파일이면 같다. 어느 쪽이든 없으면 판단하지 않는다(false) — 읽지 못한 go.mod는 unreadable이 센다.
func differentFiles(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && !os.SameFile(fa, fb)
}

// cachedGoModPath는 모듈 캐시 루트 아래 m의 go.mod 상대 경로(cache/download/<escape 경로>/@v/<escape 버전>.mod)다.
func cachedGoModPath(m module.Version) (string, error) {
	path, err := module.EscapePath(m.Path)
	if err != nil {
		return "", err
	}
	ver, err := module.EscapeVersion(m.Version)
	if err != nil {
		return "", err
	}
	return filepath.Join("cache", "download", path, "@v", ver+".mod"), nil
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

// moduleCacheRoot는 모듈 캐시 루트다. 의존 모듈의 go.mod가 캐시에 있으면 그 경로에서 정확한 꼬리
// (cache/download/<escape 경로>/@v/<escape 버전>.mod)를 떼어 쓴다 — go 명령이 실제로 쓴 캐시이고, 캐시 경로 자체에
// cache/download가 들어 있어도 맞다. 그런 모듈이 없으면(모든 의존이 디렉터리 replace) goEnvModCache로 묻는다.
func moduleCacheRoot(reachable []*packages.Package) string {
	for _, p := range reachable {
		if p.Module == nil || p.Module.Main || p.Module.GoMod == "" {
			continue
		}
		candidates := []module.Version{{Path: p.Module.Path, Version: p.Module.Version}}
		if r := p.Module.Replace; r != nil {
			candidates = append(candidates, module.Version{Path: r.Path, Version: r.Version})
		}
		for _, m := range candidates {
			rel, err := cachedGoModPath(m)
			if err != nil {
				continue
			}
			if root, ok := strings.CutSuffix(p.Module.GoMod, string(filepath.Separator)+rel); ok {
				return root
			}
		}
	}
	return goEnvModCache()
}

// goEnvModCache는 `go env GOMODCACHE`의 값이다 — `go env -w`로 저장한 값도 읽는다. 분석 대상 디렉터리가 아니라 임시
// 디렉터리에서 GOTOOLCHAIN=local(환경 변수가 없을 때)로 돌린다 — 분석 대상의 go.mod·go.work가 툴체인을 바꾸게 두지 않는다
// (loaderEnv와 같은 이유). go를 찾지 못하면 GOMODCACHE 환경 변수, 그다음 GOPATH의 첫 항목/pkg/mod다.
func goEnvModCache() string {
	cmd := exec.Command("go", "env", "GOMODCACHE")
	cmd.Dir = os.TempDir()
	cmd.Env = os.Environ()
	if os.Getenv("GOTOOLCHAIN") == "" {
		cmd.Env = append(cmd.Env, "GOTOOLCHAIN=local")
	}
	if out, err := cmd.Output(); err == nil {
		if dir := string(bytes.TrimSpace(out)); dir != "" {
			return dir
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

// goDirective는 go.mod의 go 지시문 버전이다(없으면 빈 문자열).
func goDirective(f *modfile.File) string {
	if f.Go == nil {
		return ""
	}
	return f.Go.Version
}

// prunedGoVersion은 go 지시문 버전이 모듈 그래프 가지치기를 켜는지 본다 — go 명령과 같이 1.17과 비교한다(1.17rc1은
// 1.17보다 앞이라 꺼져 있다). 지시문이 없거나 읽을 수 없는 버전은 go 1.16으로 다뤄져 가지치기되지 않는다.
func prunedGoVersion(v string) bool {
	return version.Compare("go"+v, "go1.17") >= 0
}

// dependencyGoMod는 의존 모듈 go.mod를 읽는다. 모르는 지시문은 무시한다(ParseLax) — 의존 모듈의 go.mod는 더 새 Go의
// 지시문을 쓸 수 있고, replace·exclude는 주 모듈의 것만 효력이 있어 쓰지 않는다.
func dependencyGoMod(path string) (*modfile.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	f, err := modfile.ParseLax(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return f, nil
}
