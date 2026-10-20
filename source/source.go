// Package source는 go/packages로 Go 워크스페이스를 읽어 graph.Document를 만든다.
//
// golang.org/x/tools는 이 패키지 안에서만 import한다 — 수확 기술이 새 나가면
// 같은 저장소가 어디서 스캔됐냐에 따라 다른 그래프가 된다.
// 수확은 원문을 옮기기만 하고, 판정·의미론은 analysis에 둔다.
package source

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ictechgy/gartograph/graph"
	"golang.org/x/tools/go/packages"
)

// Options는 수확 범위를 결정한다.
type Options struct {
	// Dir은 분석 루트다. 빈 문자열은 현재 디렉터리다.
	Dir string
	// Patterns은 packages.Load에 넘길 패턴이다. 빈 슬라이스는 "./..."다.
	Patterns []string
	// Level은 수확할 가장 세밀한 레벨이다. 빈 값은 package다.
	Level graph.Level
	// Tests는 테스트 변형 패키지 포함 여부다.
	// 외부 테스트 패키지(x_test)는 원 패키지를 import하므로, 켜면
	// 패키지 레벨에서 가짜 순환처럼 보일 수 있다는 점을 기억한다.
	Tests bool
	// IncludeDeps는 모듈 밖 의존 패키지를 정점으로 포함할지다.
	// 기본은 모듈 내부만 — 외부 의존까지 넣으면 정점 수가 폭증해
	// 저장소 구조 질의가 흐려진다. 생략한 개수는 limitation으로 남긴다.
	IncludeDeps bool
	// Tags는 go list -tags에 넘길 빌드 태그 목록(쉼표 구분)이다.
	// 태그가 없으면 제약에 걸린 파일이 조용히 빠진다 — 빠진 수는
	// limitation으로 센다.
	Tags string
	// GOOS·GOARCH는 go/build의 타깃 플랫폼을 덮어쓴다 — 다른 OS의
	// 조건부 파일(_linux.go 등)을 수확하려면 호스트 기본값을 바꿔야 한다.
	// 빈 값은 호스트 기본값이다. 둘 다 지정되면 문서에 limitation으로
	// 남긴다 — 같은 저장소가 어느 플랫폼으로 스캔됐는지 알아야 한다.
	GOOS   string
	GOARCH string
	// Exclude는 그래프에서 뺄 패키지 경로 패턴 목록이다 — 컴포넌트
	// 패턴과 같은 의미론(정확 일치, `x/**`, 세그먼트 글롭)으로
	// 주 모듈 패키지는 모듈 상대 경로에, 외부 패키지는 전체 경로에 맞춘다.
	// 제외된 패키지는 정점이 아니고, 그쪽으로의 import는 limitation으로
	// 센다 — 조용히 빼면 "없는 것"과 "뺀 것"을 구분할 수 없다.
	Exclude []string
}

// Load는 opts.Level에 맞는 가장 세밀한 그래프를 수확한다.
// package는 import 간선만, type은 타입 정점과 embeds/implements/references,
// symbol은 함수·변수·상수 정점과 call/references까지 담는다.
func Load(opts Options) (*graph.Document, error) {
	pkgs, err := load(opts)
	if err != nil {
		return nil, err
	}
	return documentFrom(pkgs, opts)
}

// documentFrom은 이미 로드한 패키지로 opts.Level의 문서를 만든다. Load와 schema가
// 같은 로드 결과로 같은 그래프를 얻도록 로드와 문서화를 나눴다 — schema 사실의
// symbol.usr가 impact 그래프의 정점 ID와 같은 규칙(충돌 접미사·exclude 포함)을 따라야
// 하기 때문이다.
func documentFrom(pkgs []*packages.Package, opts Options) (*graph.Document, error) {
	return documentWith(pkgs, opts, false)
}

// usrDocument는 schema·routes(server·client)가 usr를 확인할 symbol 문서다. 세 생산자는 이 문서에서 정점 ID·종류
// (newSymbolIDs)만 읽는다. 정점 집합은 같은 적재·opts의 documentFrom과 같다 — 같은 정점 생성 코드를 지나고, 심볼
// 수확의 간선 전용 단계만 건너뛴다(harvestSymbols의 vertexOnly). 그 단계(구체 타입 × 인터페이스 implements 판정,
// 외부 디스패치 사실, CHA 팬아웃, 간선 기록)가 큰 모듈에서 수확 시간·메모리의 큰 몫이다.
func usrDocument(pkgs []*packages.Package, opts Options) (*graph.Document, error) {
	return documentWith(pkgs, opts, true)
}

// documentWith는 documentFrom·usrDocument의 본체다. vertexOnly면 심볼 수확이 정점만 만든다.
func documentWith(pkgs []*packages.Package, opts Options, vertexOnly bool) (*graph.Document, error) {
	// Root는 절대 경로로 남긴다 — Position.File이 절대 경로라, 이후
	// 파일→정점 해석이 어느 cwd에서든 같은 결과를 내야 한다.
	root, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("resolving --dir %s: %w", opts.Dir, err)
	}
	reachable := walkImports(pkgs)
	// exclude는 정점을 만드는 단계 전에 걸러야 두 레벨 모두에서
	// 같은 범위가 된다 — 패키지와 모듈 문서가 다른 저장소를 보는 걸 막는다.
	reachable, excluded := dropExcluded(reachable, opts.Exclude)
	if opts.Level == graph.LevelModule {
		mod := buildModuleDocument(root, reachable, opts.IncludeDeps)
		if len(excluded) > 0 {
			mod.Limitation(fmt.Sprintf(
				"%d packages matched exclude patterns; they have no vertices and their imports are not module edges",
				len(excluded)))
		}
		if opts.GOOS != "" || opts.GOARCH != "" {
			mod.Limitation(platformNote(opts))
		}
		mod.Sort()
		return mod, nil
	}
	doc, kept := buildDocument(root, reachable, opts.IncludeDeps, excluded)
	if opts.Level.Rank() >= graph.LevelType.Rank() {
		internal := internalPackages(pkgs, kept)
		harvestSymbols(doc, internal, opts.Level, vertexOnly)
	}
	markGenerated(doc, kept)
	if len(excluded) > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d packages matched exclude patterns; they have no vertices", len(excluded)))
	}
	if opts.GOOS != "" || opts.GOARCH != "" {
		doc.Limitation(platformNote(opts))
	}
	doc.Sort()
	return doc, nil
}

// dropExcluded는 exclude 패턴에 맞는 패키지를 도달 집합에서 뺀다.
// 반환하는 제외 집합은 "kept에 없는 것"과 "의도적으로 뺀 것"을 구분하는
// 재료다 — 제외 패키지로 향하는 import는 외부 생략과 다른 사실이다.
// 테스트 변형은 PkgPath가 원 패키지와 같아 함께 빠진다 — 같은 디렉터리다.
func dropExcluded(pkgs []*packages.Package, patterns []string) ([]*packages.Package, map[string]bool) {
	if len(patterns) == 0 {
		return pkgs, nil
	}
	excluded := map[string]bool{}
	var kept []*packages.Package
	for _, p := range pkgs {
		if excludeMatch(p, patterns) {
			excluded[p.PkgPath] = true
			continue
		}
		kept = append(kept, p)
	}
	return kept, excluded
}

// excludeMatch는 패키지가 exclude 패턴 중 하나에 맞는지 본다.
// 주 모듈 패키지(go.work 워크스페이스면 각 워크스페이스 모듈의 패키지)는 그 패키지가 속한 모듈 기준 상대
// 경로로 맞춘다 — 컴포넌트 패턴과 같은 함수(graph.ModuleRelative)로 같은 기준을 써야 설정이 이식 가능하다.
// 외부·모듈 없는 패키지는 전체 경로로 맞춘다.
func excludeMatch(p *packages.Package, patterns []string) bool {
	rel := graph.ModuleRelative(p.PkgPath, mainModulePath(p))
	for _, pat := range patterns {
		if graph.MatchPath(pat, rel) {
			return true
		}
	}
	return false
}

// mainModulePath는 패키지가 주 모듈(go.work 워크스페이스면 워크스페이스 모듈 중 하나) 소속이면 그 모듈
// 경로를, 외부·모듈 없는 패키지면 ""를 돌려준다 — graph.ModuleRelative가 상대 경로의 기준으로 받는 값이다.
func mainModulePath(p *packages.Package) string {
	if p == nil || p.Module == nil || !p.Module.Main {
		return ""
	}
	return p.Module.Path
}

// LoadPackageGraph는 패키지 레벨 의존 그래프를 수확한다.
// 하위 호환용 얇은 래퍼다 — 새 코드는 Load에 Level을 넘기는 쪽을 쓴다.
func LoadPackageGraph(opts Options) (*graph.Document, error) {
	opts.Level = graph.LevelPackage
	return Load(opts)
}

// load는 go/packages를 감싸는 유일한 호출점이다.
// 수확 모드를 한 곳에 두면 레벨 확장 때 reader 정책이 갈라지지 않는다.
func load(opts Options) ([]*packages.Package, error) {
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	// NeedFiles는 파일 목록(GoFiles·IgnoredFiles)을 채운다 — 빌드 제약으로
	// 빠진 파일 수와 생성 파일 표시에 둘 다 필요하다.
	mode := packages.NeedName | packages.NeedImports | packages.NeedDeps |
		packages.NeedModule | packages.NeedFiles
	if opts.Level.Rank() >= graph.LevelType.Rank() {
		// 심볼 수확에는 AST와 타입 정보가 필요하다.
		mode |= packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo
	}
	cfg := &packages.Config{Dir: opts.Dir, Tests: opts.Tests, Mode: mode}
	if opts.Tags != "" {
		cfg.BuildFlags = []string{"-tags=" + opts.Tags}
	}
	cfg.Env = loaderEnv(opts)
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, loaderError("loading packages", err, "check go.mod and build tags")
	}
	return pkgs, nil
}

// loaderEnv는 go/packages가 띄우는 go 명령의 환경이다. go list를 띄우는 두 호출(load·patternRoots)이 함께 써서
// 같은 플랫폼·툴체인 규칙으로 돈다 — dead --algo rta의 SSA도 load가 적재한 패키지로 만든다(LoadSession).
//
// GOOS/GOARCH 덮어쓰기가 있으면 더한다. GOTOOLCHAIN 환경 변수가 없거나 비었으면(go 명령도 빈 값을
// 미지정으로 본다) GOTOOLCHAIN=local을 더한다 — 분석 대상(신뢰하지 않는 입력)의 go.mod·go.work에 있는
// go·toolchain 줄이 다른 Go 툴체인을 내려받아 실행하게 두지 않기 위해서다(DECISIONS gartograph GOTOOLCHAIN).
// 사용자가 환경 변수로 준 값(auto 포함)은 그대로 둔다. `go env -w`로 저장한 값은 이 환경 변수에 덮인다 —
// 그 값을 읽으려고 분석 대상 디렉터리에서 go env를 돌리면 그 실행이 툴체인 전환을 일으킬 수 있어 읽지 않는다.
// 늘 os.Environ()에서 시작하고(Env가 nil일 때 go/packages가 쓰는 값과 같다), 같은 키는 뒤 값이 이긴다(os/exec).
func loaderEnv(opts Options) []string {
	env := os.Environ()
	if opts.GOOS != "" {
		env = append(env, "GOOS="+opts.GOOS)
	}
	if opts.GOARCH != "" {
		env = append(env, "GOARCH="+opts.GOARCH)
	}
	if os.Getenv("GOTOOLCHAIN") == "" {
		env = append(env, "GOTOOLCHAIN=local")
	}
	return env
}

// toolchainAdvice는 go 명령이 GOTOOLCHAIN=local이라 더 새 Go를 요구하는 저장소를 거부했을 때의 원인과 해결
// 방향이다. 기본값이 막았다는 사실과 되돌리는 방법은 go 명령의 문구만으로는 알 수 없다.
const toolchainAdvice = "this repository needs a newer Go than the installed one, and GOTOOLCHAIN=local " +
	"(gartograph's default when the GOTOOLCHAIN environment variable is not set) keeps the go command from " +
	"downloading it: install that Go release, or rerun with GOTOOLCHAIN=auto if you trust this repository"

// loaderError는 packages.Load 실패를 what 단계의 오류로 감싸고 해결 방향 advice를 붙인다. go 명령이
// GOTOOLCHAIN=local 아래에서 더 새 Go 요구를 거부한 실패면 advice 대신 toolchainAdvice를 붙인다 —
// "check go.mod" 같은 일반 안내로는 Go 설치와 GOTOOLCHAIN=auto라는 두 길이 보이지 않는다.
func loaderError(what string, err error, advice string) error {
	if toolchainRefused(err) {
		advice = toolchainAdvice
	}
	return fmt.Errorf("%s: %w — %s", what, err, advice)
}

// toolchainRefused는 go 명령이 GOTOOLCHAIN=local이라 툴체인을 바꾸지 않고 더 새 Go 요구를 거부했는지 오류
// 문구로 본다. 문구는 cmd/go gover.TooNewError의 "X requires go >= V (running go L; GOTOOLCHAIN=local)"이다
// (X는 go.mod·go.work·의존 모듈). 문구가 바뀌면 일반 안내로 물러날 뿐 go 명령의 원인 문구는 그대로 남는다.
// local+path 같은 다른 값은 맞추지 않는다 — 그 값은 사용자가 고른 전환 규칙이다.
func toolchainRefused(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, " requires go >= ") && strings.Contains(msg, "; GOTOOLCHAIN=local)")
}

// platformNote는 플랫폼 덮어쓰기를 limitation 문장으로 만든다.
// 같은 저장소가 linux로 수확됐는지 darwin으로 수확됐는지가 결과를
// 갈라서, 덮어쓴 사실은 문서에 남겨야 한다.
func platformNote(opts Options) string {
	var parts []string
	if opts.GOOS != "" {
		parts = append(parts, "GOOS="+opts.GOOS)
	}
	if opts.GOARCH != "" {
		parts = append(parts, "GOARCH="+opts.GOARCH)
	}
	return "harvested with " + strings.Join(parts, " ") +
		" — platform-conditional files for other targets were not loaded"
}

// buildDocument는 로드된 패키지 목록을 Document로 정규화한다.
// 정점은 모듈 내부 패키지가 기본이고, 외부 의존은 IncludeDeps일 때만 담는다.
// 패키지 목록은 패턴에 맞은 루트뿐이라, 정점 후보는 import 그래프를 BFS로 넓힌다.
// excluded는 exclude 패턴으로 의도적으로 뺀 패키지 집합이다 — 그쪽으로의
// import는 외부 생략이 아니라 "설정이 뺀 것"이라 따로 센다.
// 두 번째 반환값은 그래프에 남은 패키지 — 심볼 수확이 순회할 범위다.
func buildDocument(root string, reachable []*packages.Package,
	includeDeps bool, excluded map[string]bool) (*graph.Document, map[string]*packages.Package) {
	doc := &graph.Document{
		Version: graph.Version,
		Tool:    graph.Tool,
		Level:   graph.LevelPackage,
		Root:    root,
	}
	kept := make(map[string]*packages.Package)
	// 생략 수는 (패키지, 대상) 집합으로 센다 — 테스트 변형("p [p.test]")은 원 패키지의 import와
	// 오류를 다시 싣는다. 정점이 PkgPath로 합쳐지듯 수치도 합쳐야 --tests 문서가 실제의 두 배를
	// 말하지 않는다. 로드 오류는 문구("N packages reported load errors")대로 패키지(PkgPath) 수다 —
	// 오류 문장 수가 아니고, unused-deps의 같은 문구와도 같은 것을 센다.
	extImports, exclImports, loadErrors := map[[2]string]bool{}, map[[2]string]bool{}, map[string]bool{}

	// 먼저 정점을 확정한다 — 간선은 양쪽 정점이 살아 있어야 만든다.
	// 끝이 없는 간선은 유령 정점이 되어 소비자를 헷갈리게 한다.
	// 테스트 변형은 PkgPath가 원 패키지와 같으므로 정점은 하나다.
	for _, p := range reachable {
		if !keep(p, includeDeps) || kept[p.PkgPath] != nil {
			continue
		}
		kept[p.PkgPath] = p
		doc.Vertices = append(doc.Vertices, vertexFor(p))
		if p.Module != nil && p.Module.Main {
			if doc.Module == "" {
				doc.Module = p.Module.Path
				doc.ModuleDir = p.Module.Dir
			}
			// main 패키지는 패키지 레벨에서도 보존 루트다 — 심볼 수확 없이도
			// orphan 판정 같은 소비자가 진입점을 알 수 있게 문서에 남긴다.
			if p.Name == "main" {
				doc.Roots = append(doc.Roots, p.PkgPath)
			}
		}
	}
	recordWorkspaceModules(doc, kept)

	var merged int
	for _, p := range reachable {
		if kept[p.PkgPath] == nil {
			continue
		}
		if len(p.Errors) > 0 {
			loadErrors[p.PkgPath] = true
		}
		if p.ID != p.PkgPath {
			// 테스트 변형("p [p.test]")은 PkgPath가 원 패키지와 같아
			// 정점이 합쳐진다 — 변형의 import는 _test.go의 의존이므로
			// 원 패키지의 간선으로 합치지 않으면 그 의존이 그래프에서
			// 통째로 빠진다.
			sites := importSites(p)
			for _, imp := range p.Imports {
				if kept[imp.PkgPath] != nil {
					doc.Edges = append(doc.Edges, graph.Edge{
						From: p.PkgPath, To: imp.PkgPath, Kind: graph.EdgeImport,
						Positions: sites[imp.PkgPath],
					})
				} else if excluded[imp.PkgPath] {
					exclImports[[2]string{p.PkgPath, imp.PkgPath}] = true
				} else {
					extImports[[2]string{p.PkgPath, imp.PkgPath}] = true
				}
			}
			merged++
			continue
		}
		// import 간선의 사용 지점은 import 선언이다 — 파일 스코프 규칙과
		// 위반 위치 보고가 이 지점을 근거로 삼는다.
		sites := importSites(p)
		for _, imp := range p.Imports {
			if kept[imp.PkgPath] != nil {
				doc.Edges = append(doc.Edges, graph.Edge{
					From: p.PkgPath, To: imp.PkgPath, Kind: graph.EdgeImport,
					Positions: sites[imp.PkgPath],
				})
			} else if excluded[imp.PkgPath] {
				exclImports[[2]string{p.PkgPath, imp.PkgPath}] = true
			} else {
				extImports[[2]string{p.PkgPath, imp.PkgPath}] = true
			}
		}
	}

	// limitation은 실제로 세어서 만든다 — 알릴 것이 없으면 붙이지 않는다.
	if ignored := countIgnored(reachable, kept); ignored > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d source files were excluded by build constraints (use --tags to include)", ignored))
	}
	if len(extImports) > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d imports of packages outside the module were omitted (use --deps to include)", len(extImports)))
	}
	if len(exclImports) > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d imports to excluded packages were omitted (matched by exclude patterns)", len(exclImports)))
	}
	if len(loadErrors) > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d packages reported load errors; their import edges may be incomplete", len(loadErrors)))
	}
	if merged > 0 {
		// _test.go 의존이 원 패키지로 귀속된다는 사실을 남긴다 —
		// 테스트만의 의존이 프로덕션 의존처럼 읽히는 것을 막는다.
		doc.Limitation(fmt.Sprintf(
			"%d test-variant packages merged into their base package (edges from _test.go files are attributed to the base package)", merged))
	}
	doc.Sort()
	return doc, kept
}

// recordWorkspaceModules는 go.work 워크스페이스 수확에서 패키지 정점의 모듈 소속을 문서에 싣는다.
// 그래프에 패키지가 있는 주 모듈이 둘 이상일 때만 싣는다 — 단일 모듈 문서는 Module 하나로 모든 패키지를
// 해석할 수 있어 바이트가 그대로여야 한다. 문서에는 모듈마다 go.mod 디렉터리(WorkspaceModules)를, 문서
// Module이 아닌 모듈의 패키지 정점에는 그 모듈 경로(Module)를 싣는다. 분석이 컴포넌트·fileRules 패턴과
// 파일→패키지 해석을 패키지마다 그 모듈 기준으로 하는 재료다 — exclude는 수확 때 이미 그렇게 맞췄다.
// doc.Vertices는 아직 kept의 패키지 정점뿐이다(심볼 수확 전).
func recordWorkspaceModules(doc *graph.Document, kept map[string]*packages.Package) {
	dirs := map[string]string{}
	for _, p := range kept {
		if path := mainModulePath(p); path != "" {
			dirs[path] = p.Module.Dir
		}
	}
	if len(dirs) < 2 {
		return
	}
	for path, dir := range dirs {
		doc.WorkspaceModules = append(doc.WorkspaceModules, graph.WorkspaceModule{Path: path, Dir: dir})
	}
	sort.Slice(doc.WorkspaceModules, func(i, j int) bool {
		return doc.WorkspaceModules[i].Path < doc.WorkspaceModules[j].Path
	})
	for i := range doc.Vertices {
		if path := mainModulePath(kept[doc.Vertices[i].ID]); path != "" && path != doc.Module {
			doc.Vertices[i].Module = path
		}
	}
}

// internalPackages는 로드된 루트 중 모듈 내부이고 그래프에 남은 것만 골라낸다.
// 심볼 수확은 syntax가 있는 패키지에서만 의미가 있다.
func internalPackages(pkgs []*packages.Package, kept map[string]*packages.Package) []*packages.Package {
	var out []*packages.Package
	for _, p := range pkgs {
		if kept[p.PkgPath] == nil || p.Module == nil || !p.Module.Main {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PkgPath < out[j].PkgPath })
	return out
}

// walkImports는 루트 패키지들에서 import 그래프를 BFS로 넓혀
// 도달 가능한 모든 패키지를 ID 중복 없이 돌려준다.
// --deps 없이도 순회는 한다 — 모듈 내부 패키지가 루트 패턴 밖에 있어도
// import로 도달되면 정점이 되어야 하기 때문이다.
// 테스트 변형("p [p.test]")도 돌려준다 — 정점 dedup은 PkgPath를 보는
// buildDocument의 일이고, 변형의 _test.go import를 순회에서 빼면
// 테스트에서만 쓰는 내부 패키지가 도달 집합에서 통째로 빠진다.
func walkImports(roots []*packages.Package) []*packages.Package {
	seen := make(map[string]bool)
	var out []*packages.Package
	queue := roots
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if p == nil || p.PkgPath == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		out = append(out, p)
		for _, imp := range p.Imports {
			queue = append(queue, imp)
		}
	}
	return out
}

// generatedMarker는 go.dev/s/generatedcode의 표지 정규식이다.
// 마커는 패키지 절보다 앞에 있어야 생성 표시로 인정된다.
var generatedMarker = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// maxGeneratedHeader는 생성 표지를 찾으며 읽는 파일 앞부분의 상한(바이트)이다. 표지는 패키지
// 절보다 앞에 있어야 해서 판정은 보통 첫 몇 줄에서 끝난다 — 상한은 패키지 절이 끝내 나오지
// 않는 입력에서 읽기를 멈추게 하는 방어다.
const maxGeneratedHeader = 1 << 20

// isGeneratedFile은 파일의 패키지 절 이전에 생성 표지가 있는지 본다.
// 정규 파일만 읽는다 — 쓰는 쪽이 없는 FIFO는 open에서 영원히 막히고 장치는 끝없이 읽히므로,
// 막히지 않게 연 뒤(readFlags) 연 파일의 종류를 fstat로 본다. 경로를 Lstat하지 않는 이유는
// 링크를 따라간 정상 .go 파일의 판정을 그대로 두기 위해서다.
func isGeneratedFile(path string) bool {
	f, err := os.OpenFile(path, readFlags, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return false
	}
	sc := bufio.NewScanner(io.LimitReader(f, maxGeneratedHeader))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "package ") {
			break
		}
		if generatedMarker.MatchString(line) {
			return true
		}
	}
	return false
}

// markGenerated는 생성 마커가 있는 파일에서 온 정점에 generated를 표시한다.
// 패키지 정점은 파일 전부가 생성일 때만 표시한다 — 일부만 생성인 패키지를
// 생성이라 하면 손으로 쓴 절반이 숨는다.
func markGenerated(doc *graph.Document, kept map[string]*packages.Package) {
	fileGen := make(map[string]bool)
	pkgAllGen := make(map[string]bool)
	for _, p := range kept {
		files := p.CompiledGoFiles
		if len(files) == 0 {
			files = p.GoFiles
		}
		all := len(files) > 0
		for _, f := range files {
			if _, ok := fileGen[f]; !ok {
				fileGen[f] = isGeneratedFile(f)
			}
			all = all && fileGen[f]
		}
		pkgAllGen[p.PkgPath] = all
	}
	for i := range doc.Vertices {
		v := &doc.Vertices[i]
		if v.Position != nil {
			v.Generated = fileGen[v.Position.File]
		} else if v.Kind == graph.KindPackage {
			v.Generated = pkgAllGen[v.ID]
		}
	}
}

// importSites는 패키지의 import 선언 위치를 import 경로별로 모은다.
// 패키지 레벨 수확은 Syntax를 로드하지 않으므로 ImportsOnly 파싱으로
// 선언부만 읽는다 — 전체 AST를 만들 이유가 없고, 파싱이 안 되는 파일은
// 건너뛰되 지점이 비는 사실은 그대로 남는다.
func importSites(p *packages.Package) map[string][]graph.Position {
	files := p.CompiledGoFiles
	if len(files) == 0 {
		files = p.GoFiles
	}
	if len(files) == 0 {
		return nil
	}
	fset := token.NewFileSet()
	out := make(map[string][]graph.Position)
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			pos := fset.Position(spec.Pos())
			out[path] = append(out[path], graph.Position{
				File: pos.Filename, Line: pos.Line, Column: pos.Column})
		}
	}
	return out
}

// countIgnored는 그래프에 남은 패키지의 빌드 제약 제외 파일을 센다.
// IgnoredFiles는 go list가 알고 있는데 현재 태그로는 빌드되지 않은
// 파일들이다 — 세지 않으면 소비자가 빠진 파일의 존재 자체를 모른다.
// 테스트 변형은 원 패키지와 같은 디렉터리라 같은 파일을 다시 싣는다 — 경로로 한 번만 센다.
func countIgnored(reachable []*packages.Package, kept map[string]*packages.Package) int {
	files := map[string]bool{}
	for _, p := range reachable {
		if kept[p.PkgPath] == nil {
			continue
		}
		for _, f := range p.IgnoredFiles {
			files[f] = true
		}
	}
	return len(files)
}

// keep은 패키지를 그래프에 담을지 결정한다.
// 모듈이 없는(std 등) 패키지와 비주 모듈은 IncludeDeps가 켜질 때만 담는다.
func keep(p *packages.Package, includeDeps bool) bool {
	if includeDeps {
		return true
	}
	return p.Module != nil && p.Module.Main
}

// vertexFor는 패키지 정점을 만든다.
// 정점 ID는 패키지 경로다 — Go에서 경로는 모듈 안에서 유일하다.
func vertexFor(p *packages.Package) graph.Vertex {
	return graph.Vertex{
		ID:   p.PkgPath,
		Kind: graph.KindPackage,
		Name: p.Name,
		// 외부 표시는 수확 시점에만 알 수 있다 — 나중에 경로 접두사로
		// 추론하면 주 모듈 안의 중첩 모듈을 내부로 오분한다.
		External: p.Module == nil || !p.Module.Main,
	}
}

// SortedPackagePaths는 테스트와 디버깅용으로 로드된 경로를 정렬해 돌려준다.
func SortedPackagePaths(pkgs []*packages.Package) []string {
	paths := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		paths = append(paths, p.PkgPath)
	}
	sort.Strings(paths)
	return paths
}
