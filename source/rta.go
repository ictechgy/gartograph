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
	"sort"

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
	res, _, err := analyzeRTA(opts, roots)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(res.Reachable))
	for fn := range res.Reachable {
		if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
			out[objectID(obj)] = true
		}
	}
	return out, nil
}

// RTAAdjacency는 RTA 호출 그래프의 정점 ID 인접 맵과 도달 집합을 돌려준다.
// 도달성만으로는 "왜 살아 있다고 봤나"에 답할 수 없다 — dead --explain
// --algo rta가 CHA 수확 그래프가 아니라 실제 판정을 내린 그래프 위의
// 경로를 보여주기 위한 장치다.
func RTAAdjacency(opts Options, roots map[string]bool) (map[string][]string, map[string]bool, error) {
	res, pkgPaths, err := analyzeRTA(opts, roots)
	if err != nil {
		return nil, nil, err
	}
	reach := make(map[string]bool, len(res.Reachable))
	for fn := range res.Reachable {
		if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
			reach[objectID(obj)] = true
		}
	}
	adj := map[string][]string{}
	for fn, node := range res.CallGraph.Nodes {
		if fn == nil || node == nil {
			continue
		}
		fromObj := fn.Object()
		if fromObj == nil || fromObj.Pkg() == nil {
			continue
		}
		from := objectID(fromObj)
		// 점 경로 패키지와 ID가 겹치는 함수는 문서에서 그 ID가 패키지 정점이다 —
		// 경로에 실으면 "함수가 패키지를 호출한다"가 된다(수확기와 같은 규칙).
		if pkgPaths[from] {
			continue
		}
		seen := map[string]bool{}
		for _, e := range node.Out {
			if e.Callee == nil || e.Callee.Func == nil {
				continue
			}
			toObj := e.Callee.Func.Object()
			if toObj == nil || toObj.Pkg() == nil {
				continue
			}
			to := objectID(toObj)
			if !seen[to] && !pkgPaths[to] {
				seen[to] = true
				adj[from] = append(adj[from], to)
			}
		}
	}
	// 결정성 — 맵 순회에 출력 순서를 맡기면 같은 입력이 다른 경로를 낸다.
	for _, l := range adj {
		sort.Strings(l)
	}
	return adj, reach, nil
}

// analyzeRTA는 SSA를 만들고 주어진 루트에서 RTA를 실행한다.
// 도달 집합과 인접 맵의 두 소비자가 같은 분석 결과를 나누기 위한 단위다.
// 두 번째 값은 프로그램의 패키지 경로 집합이다 — 심볼 ID와 겹치는 경로를 거르는 재료.
func analyzeRTA(opts Options, roots map[string]bool) (*rta.Result, map[string]bool, error) {
	pkgs, err := loadSSA(opts)
	if err != nil {
		return nil, nil, err
	}
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	var rootFns []*ssa.Function
	for _, sp := range ssaPkgs {
		if sp != nil && sp.Pkg != nil {
			rootFns = append(rootFns, packageRootFns(sp, roots)...)
		}
	}
	pkgPaths := map[string]bool{}
	for _, sp := range prog.AllPackages() {
		pkgPaths[sp.Pkg.Path()] = true
	}
	return rta.Analyze(rootFns, true), pkgPaths, nil
}

// packageRootFns는 SSA 패키지에서 루트 정점에 해당하는 함수를 고른다.
// 빈 식별자 루트(pkg._)가 있으면 합성 init을 더한다 — 패키지 변수 초기화식은
// 합성 init이 실행하고, 그 함수는 Object가 없어 ID로 짝지을 수 없다.
func packageRootFns(sp *ssa.Package, roots map[string]bool) []*ssa.Function {
	var out []*ssa.Function
	for _, member := range sp.Members {
		fn, ok := member.(*ssa.Function)
		if ok && fn.Object() != nil && roots[objectID(fn.Object())] {
			out = append(out, fn)
		}
	}
	if init := sp.Func("init"); init != nil && roots[sp.Pkg.Path()+"._"] {
		out = append(out, init)
	}
	return out
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
	cfg.Env = platformEnv(opts)
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("loading packages for rta: %w — check go.mod and build tags", err)
	}
	return pkgs, nil
}
