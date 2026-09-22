// Package source는 go/packages로 Go 워크스페이스를 읽어 graph.Document를 만든다.
//
// golang.org/x/tools는 이 패키지 안에서만 import한다 — 수확 기술이 새 나가면
// 같은 저장소가 어디서 스캔됐냐에 따라 다른 그래프가 된다.
// 수확은 원문을 옮기기만 하고, 판정·의미론은 analysis에 둔다.
package source

import (
	"fmt"
	"sort"

	"github.com/ictechgy/gartograph/graph"
	"golang.org/x/tools/go/packages"
)

// Options는 수확 범위를 결정한다.
type Options struct {
	// Dir은 분석 루트다. 빈 문자열은 현재 디렉터리다.
	Dir string
	// Patterns은 packages.Load에 넘길 패턴이다. 빈 슬라이스는 "./..."다.
	Patterns []string
	// Tests는 테스트 변형 패키지 포함 여부다.
	// 외부 테스트 패키지(x_test)는 원 패키지를 import하므로, 켜면
	// 패키지 레벨에서 가짜 순환처럼 보일 수 있다는 점을 기억한다.
	Tests bool
	// IncludeDeps는 모듈 밖 의존 패키지를 정점으로 포함할지다.
	// 기본은 모듈 내부만 — 외부 의존까지 넣으면 정점 수가 폭증해
	// 저장소 구조 질의가 흐려진다. 생략한 개수는 limitation으로 남긴다.
	IncludeDeps bool
}

// LoadPackageGraph는 패키지 레벨 의존 그래프를 수확한다.
// 패키지 정점은 모듈 경로 안의 것만 기본으로 담고, 간선은 import다.
func LoadPackageGraph(opts Options) (*graph.Document, error) {
	pkgs, err := load(opts)
	if err != nil {
		return nil, err
	}
	return buildDocument(opts.Dir, pkgs, opts.IncludeDeps), nil
}

// load는 go/packages를 감싸는 유일한 호출점이다.
// 수확 모드를 한 곳에 두면 심볼 레벨로 확장할 때도 reader 정책이 갈라지지 않는다.
func load(opts Options) ([]*packages.Package, error) {
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	cfg := &packages.Config{
		Dir:   opts.Dir,
		Tests: opts.Tests,
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps |
			packages.NeedModule,
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
func buildDocument(root string, pkgs []*packages.Package, includeDeps bool) *graph.Document {
	doc := &graph.Document{
		Version: graph.Version,
		Tool:    graph.Tool,
		Level:   graph.LevelPackage,
		Root:    root,
	}
	reachable := walkImports(pkgs)
	kept := make(map[string]bool)
	var extImports, errCount int

	// 먼저 정점을 확정한다 — 간선은 양쪽 정점이 살아 있어야 만든다.
	// 끝이 없는 간선은 유령 정점이 되어 소비자를 헷갈리게 한다.
	for _, p := range reachable {
		if !keep(p, includeDeps) {
			continue
		}
		kept[p.PkgPath] = true
		doc.Vertices = append(doc.Vertices, vertexFor(p))
	}

	for _, p := range reachable {
		if !kept[p.PkgPath] {
			continue
		}
		for _, imp := range p.Imports {
			if kept[imp.PkgPath] {
				doc.Edges = append(doc.Edges, graph.Edge{
					From: p.PkgPath, To: imp.PkgPath, Kind: graph.EdgeImport,
				})
			} else {
				extImports++
			}
		}
		errCount += len(p.Errors)
	}

	// limitation은 실제로 세어서 만든다 — 알릴 것이 없으면 붙이지 않는다.
	if extImports > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d imports of packages outside the module were omitted (use --deps to include)", extImports))
	}
	if errCount > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d packages reported load errors; their import edges may be incomplete", errCount))
	}
	doc.Sort()
	return doc
}

// walkImports는 루트 패키지들에서 import 그래프를 BFS로 넓혀
// 도달 가능한 모든 패키지를 중복 없이 돌려준다.
// --deps 없이도 순회는 한다 — 모듈 내부 패키지가 루트 패턴 밖에 있어도
// import로 도달되면 정점이 되어야 하기 때문이다.
func walkImports(roots []*packages.Package) []*packages.Package {
	seen := make(map[string]bool)
	var out []*packages.Package
	queue := roots
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if p == nil || p.PkgPath == "" || seen[p.PkgPath] {
			continue
		}
		seen[p.PkgPath] = true
		out = append(out, p)
		for _, imp := range p.Imports {
			queue = append(queue, imp)
		}
	}
	return out
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
