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
	"go/types"
	"sort"

	"github.com/ictechgy/gartograph/graph"
	"golang.org/x/tools/go/callgraph"
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
// doc은 ID 규칙의 기준이다 — 수확 문서와 같은 패키지 ID 집합으로 이름을 지어야
// 접미사가 붙은 충돌 ID(collisionSuffix)가 두 그래프에서 같다.
func RTAReachable(opts Options, doc *graph.Document, roots map[string]bool) (map[string]bool, error) {
	namer := newRTANamer(doc, roots)
	res, err := analyzeRTA(opts, namer)
	if err != nil {
		return nil, err
	}
	return withRoots(namer.reachable(res), roots), nil
}

// isGenericBody는 인스턴스 없는 제네릭 본문(타입 파라미터를 가진 함수)인지 본다.
// 이런 본문을 RTA 루트로 넣으면 x/tools rta가 타입 파라미터를 품은 타입(any(&x) 등)에서
// 패닉한다("ForEachElement called on type containing *types.TypeParam"). 그래서 루트로
// 넣지 않는다 — 루트 자신은 withRoots로 살아 있고, 그 피호출자는 RTA의 과소 근사
// limitation("uninstantiated types")이 말한다.
func isGenericBody(fn *ssa.Function) bool {
	return fn.TypeParams().Len() > 0
}

// withRoots는 도달 집합에 루트 자신을 넣는다 — 루트는 정의상 도달하고, SSA 함수가 없는
// 루트(pkg._ 같은 합성 정점)도 "루트이면서 unreachable"로 보고되면 모순이다.
// RTAReachable과 RTAAdjacency가 같은 답을 내도록 둘 다 쓴다.
func withRoots(reach, roots map[string]bool) map[string]bool {
	for r := range roots {
		reach[r] = true
	}
	return reach
}

// RTAAdjacency는 RTA 호출 그래프의 정점 ID 인접 맵과 도달 집합을 돌려준다.
// 도달성만으로는 "왜 살아 있다고 봤나"에 답할 수 없다 — dead --explain
// --algo rta가 CHA 수확 그래프가 아니라 실제 판정을 내린 그래프 위의
// 경로를 보여주기 위한 장치다.
func RTAAdjacency(opts Options, doc *graph.Document, roots map[string]bool) (map[string][]string, map[string]bool, error) {
	namer := newRTANamer(doc, roots)
	res, err := analyzeRTA(opts, namer)
	if err != nil {
		return nil, nil, err
	}
	adj := map[string][]string{}
	for fn, node := range res.CallGraph.Nodes {
		if from, ok := namer.name(fn); ok && node != nil {
			adj[from] = append(adj[from], namer.callees(node)...)
		}
	}
	// 결정성 — 맵 순회에 출력 순서를 맡기면 같은 입력이 다른 경로를 낸다.
	for from, l := range adj {
		adj[from] = uniqueSorted(l)
	}
	return adj, withRoots(namer.reachable(res), roots), nil
}

// uniqueSorted는 목록을 정렬하고 중복을 없앤다 — 합성 init과 사용자 함수가 같은
// 정점 ID로 모일 수 있다.
func uniqueSorted(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// rtaNamer는 SSA 함수를 문서 정점 ID로 옮긴다 — 수확과 같은 ID 규칙(disambiguate)을
// 써야 두 그래프가 같은 정점을 가리킨다.
type rtaNamer struct {
	packageIDs map[string]bool
	roots      map[string]bool
}

// newRTANamer는 문서의 패키지 정점 ID와 루트로 이름 규칙을 만든다.
func newRTANamer(doc *graph.Document, roots map[string]bool) rtaNamer {
	ids := map[string]bool{}
	for _, v := range doc.Vertices {
		if v.Kind == graph.KindPackage {
			ids[v.ID] = true
		}
	}
	return rtaNamer{packageIDs: ids, roots: roots}
}

// PackageInitSuffix는 RTA 인접 맵에서 합성 패키지 init을 가리키는 순회용 ID의
// 접미사다("pkgpath#init"). 문서 정점이 아니다 — 합성 init은 패키지 변수 초기화식과
// import한 패키지의 init을 실행하므로, 빼면 판정은 살린 함수를 explain이 "no path"라고
// 한다. '#'는 import 경로·식별자에 없어 어떤 정점 ID와도 겹치지 않는다.
const PackageInitSuffix = "#init"

// name은 함수의 정점 ID를 돌려준다. 합성 init은 Object가 없다 — 그 패키지의 빈
// 식별자 루트(pkg._)가 있으면 그 ID로(루트와 짝지어진다), 없으면 순회용 ID로 옮긴다.
func (n rtaNamer) name(fn *ssa.Function) (string, bool) {
	if fn == nil {
		return "", false
	}
	if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
		return disambiguate(objectID(obj), n.packageIDs), true
	}
	if fn.Pkg != nil && fn.Pkg.Func("init") == fn {
		path := fn.Pkg.Pkg.Path()
		if blank := path + "._"; n.roots[blank] {
			return blank, true
		}
		return path + PackageInitSuffix, true
	}
	return "", false
}

// callees는 호출 그래프 노드의 피호출 정점 ID를 돌려준다.
func (n rtaNamer) callees(node *callgraph.Node) []string {
	var out []string
	for _, e := range node.Out {
		if e.Callee == nil {
			continue
		}
		if to, ok := n.name(e.Callee.Func); ok {
			out = append(out, to)
		}
	}
	return out
}

// reachable은 RTA 도달 함수의 정점 ID 집합이다.
func (n rtaNamer) reachable(res *rta.Result) map[string]bool {
	out := make(map[string]bool, len(res.Reachable))
	for fn := range res.Reachable {
		if id, ok := n.name(fn); ok {
			out[id] = true
		}
	}
	return out
}

// analyzeRTA는 SSA를 만들고 주어진 루트에서 RTA를 실행한다.
// 도달 집합과 인접 맵의 두 소비자가 같은 분석 결과를 나누기 위한 단위다.
func analyzeRTA(opts Options, namer rtaNamer) (*rta.Result, error) {
	pkgs, err := loadSSA(opts)
	if err != nil {
		return nil, err
	}
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	var rootFns []*ssa.Function
	for _, sp := range ssaPkgs {
		if sp != nil && sp.Pkg != nil {
			rootFns = append(rootFns, namer.packageRootFns(sp)...)
		}
	}
	return rta.Analyze(rootFns, true), nil
}

// packageRootFns는 SSA 패키지에서 루트 정점에 해당하는 함수와 합성 init을 고른다.
// 합성 init은 늘 루트다 — 패키지 변수 초기화식과 import한 패키지의 init을 실행하고,
// CHA는 모든 사용자 init과 초기화 루트(pkg._)를 루트로 둔다. 같은 기준이어야 두
// 알고리즘이 초기화 때 실행되는 코드를 똑같이 본다.
func (n rtaNamer) packageRootFns(sp *ssa.Package) []*ssa.Function {
	var out []*ssa.Function
	for _, member := range sp.Members {
		switch m := member.(type) {
		case *ssa.Function:
			if id, named := n.name(m); m == sp.Func("init") || (named && n.roots[id] && !isGenericBody(m)) {
				out = append(out, m)
			}
		case *ssa.Type:
			out = append(out, n.methodRootFns(sp.Prog, m.Type())...)
		}
	}
	return out
}

// methodRootFns는 타입이 선언한 메서드 중 문서 루트(keep 표지 등)인 것의 SSA 함수를
// 고른다. 패키지 Members에는 메서드가 없어서, 이것 없이는 루트 메서드가 RTA에서
// unreachable로 보고되는 모순이 생긴다. 메서드 집합(MethodValue)이 아니라 선언
// 메서드(FuncValue)를 쓴다 — 승격·간접 wrapper가 아니라 선언 함수가 루트다.
// 제네릭 타입(isGenericBody)과 인터페이스(추상 메서드)는 건너뛴다.
func (n rtaNamer) methodRootFns(prog *ssa.Program, t types.Type) []*ssa.Function {
	named, ok := t.(*types.Named)
	if !ok || types.IsInterface(named) || named.TypeParams().Len() > 0 {
		return nil
	}
	var out []*ssa.Function
	for i := 0; i < named.NumMethods(); i++ {
		fn := prog.FuncValue(named.Method(i))
		if id, ok := n.name(fn); ok && n.roots[id] {
			out = append(out, fn)
		}
	}
	return out
}

// ExplainRoots는 RTA explain의 출발점이다 — 문서 루트에 패키지마다 합성 init의
// 순회용 ID(pkgpath#init)를 더한다. 합성 init은 RTA 루트지만 문서 정점이 아니라
// 문서 루트 목록에 없다 — 빼면 판정은 살린 초기화식 함수를 explain이 못 찾는다.
func ExplainRoots(doc *graph.Document, roots []string) []string {
	out := append([]string(nil), roots...)
	for _, v := range doc.Vertices {
		if v.Kind == graph.KindPackage {
			out = append(out, v.ID+PackageInitSuffix)
		}
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
