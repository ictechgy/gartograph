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
}

// Load는 opts.Level에 맞는 가장 세밀한 그래프를 수확한다.
// package는 import 간선만, type은 타입 정점과 embeds/implements/references,
// symbol은 함수·변수·상수 정점과 call/references까지 담는다.
func Load(opts Options) (*graph.Document, error) {
	pkgs, err := load(opts)
	if err != nil {
		return nil, err
	}
	// Root는 절대 경로로 남긴다 — Position.File이 절대 경로라, 이후
	// 파일→정점 해석이 어느 cwd에서든 같은 결과를 내야 한다.
	root, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("resolving --dir %s: %w", opts.Dir, err)
	}
	reachable := walkImports(pkgs)
	if opts.Level == graph.LevelModule {
		return buildModuleDocument(root, reachable, opts.IncludeDeps), nil
	}
	doc, kept := buildDocument(root, reachable, opts.IncludeDeps)
	if opts.Level.Rank() >= graph.LevelType.Rank() {
		internal := internalPackages(pkgs, kept)
		harvestSymbols(doc, internal, opts.Level)
	}
	markGenerated(doc, kept)
	doc.Sort()
	return doc, nil
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
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w — check go.mod and build tags", err)
	}
	return pkgs, nil
}

// buildDocument는 로드된 패키지 목록을 Document로 정규화한다.
// 정점은 모듈 내부 패키지가 기본이고, 외부 의존은 IncludeDeps일 때만 담는다.
// 패키지 목록은 패턴에 맞은 루트뿐이라, 정점 후보는 import 그래프를 BFS로 넓힌다.
// 두 번째 반환값은 그래프에 남은 패키지 — 심볼 수확이 순회할 범위다.
func buildDocument(root string, reachable []*packages.Package,
	includeDeps bool) (*graph.Document, map[string]*packages.Package) {
	doc := &graph.Document{
		Version: graph.Version,
		Tool:    graph.Tool,
		Level:   graph.LevelPackage,
		Root:    root,
	}
	kept := make(map[string]*packages.Package)
	var extImports, errCount int

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

	var merged int
	for _, p := range reachable {
		if kept[p.PkgPath] == nil {
			continue
		}
		errCount += len(p.Errors)
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
				} else {
					extImports++
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
			} else {
				extImports++
			}
		}
	}

	// limitation은 실제로 세어서 만든다 — 알릴 것이 없으면 붙이지 않는다.
	if ignored := countIgnored(reachable, kept); ignored > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d source files were excluded by build constraints (use --tags to include)", ignored))
	}
	if extImports > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d imports of packages outside the module were omitted (use --deps to include)", extImports))
	}
	if errCount > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d packages reported load errors; their import edges may be incomplete", errCount))
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

// isGeneratedFile은 파일의 패키지 절 이전에 생성 표지가 있는지 본다.
func isGeneratedFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
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
func countIgnored(reachable []*packages.Package, kept map[string]*packages.Package) int {
	n := 0
	for _, p := range reachable {
		if kept[p.PkgPath] != nil {
			n += len(p.IgnoredFiles)
		}
	}
	return n
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
