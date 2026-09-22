// RTA(Rapid Type Analysis) 도달성 — 정밀도가 필요한 소비자를 위한 opt-in이다.
//
// 기본 dead는 수확된 그래프의 CHA 간선을 따라 도달성을 본다 — 인터페이스
// 호출이 모든 구현으로 팬아웃해 "살아 있다" 쪽으로만 기울어진다. RTA는
// 프로그램에서 실제로 생성되는 타입만 디스패치 대상으로 보므로 더 좁은
// 도달 집합을 낸다 — deadcode -algo rta와 같은 알고리즘이다.
//
// 단, RTA는 과소 근사다 — 보지 못한 구체 타입의 메서드는 unreachable로
// 나온다. 그래서 기본값이 아니라 opt-in이고, 보고에 그 한계를 남긴다.
package source

import (
	"fmt"

	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// RTAReachable은 roots로 주어진 정점 ID 집합에서 RTA로 도달 가능한
// 함수·메서드의 정점 ID를 돌려준다.
// 반환 집합에는 루트 자신도 포함된다 — 도달성 집합의 의미와 같다.
// var·const·type 정점은 호출 그래프의 노드가 아니므로 집합에 나타나지
// 않는다 — 비호출 심볼의 판정은 그래프 도달성이 담당한다.
func RTAReachable(opts Options, roots map[string]bool) (map[string]bool, error) {
	pkgs, err := loadSSA(opts)
	if err != nil {
		return nil, err
	}
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	var rootFns []*ssa.Function
	for _, sp := range ssaPkgs {
		if sp == nil || sp.Pkg == nil {
			continue
		}
		for _, member := range sp.Members {
			fn, ok := member.(*ssa.Function)
			if !ok || fn.Object() == nil {
				continue
			}
			if roots[objectID(fn.Object())] {
				rootFns = append(rootFns, fn)
			}
		}
	}
	res := rta.Analyze(rootFns, true)
	out := make(map[string]bool, len(res.Reachable))
	for fn := range res.Reachable {
		if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
			out[objectID(obj)] = true
		}
	}
	return out, nil
}

// loadSSA는 SSA 구축에 필요한 로드 모드로 패키지를 읽는다.
// RTA는 호출 그래프를 만들므로 syntax·타입·사이즈 정보가 모두 필요하다.
func loadSSA(opts Options) ([]*packages.Package, error) {
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	cfg := &packages.Config{
		Dir:   opts.Dir,
		Tests: opts.Tests,
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps |
			packages.NeedModule | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedTypesSizes,
	}
	if opts.Tags != "" {
		cfg.BuildFlags = []string{"-tags=" + opts.Tags}
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("loading packages for rta: %w — check go.mod and build tags", err)
	}
	return pkgs, nil
}
