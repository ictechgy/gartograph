// 이름 없는 외부 인터페이스 디스패치 수확.
//
// errors.Is/As/Unwrap은 `interface{ Unwrap() error }` 같은 이름 없는
// 인터페이스로 타입 단언해 메서드를 부른다. 이런 표기는 의존 패키지의 함수
// 본문·인자 타입에만 있어 패키지 스코프 명명 인터페이스 목록에 없다.
// 심볼 레벨 로드는 의존 패키지도 소스에서 타입체크하므로(NeedDeps +
// NeedSyntax/NeedTypesInfo) 그 AST와 타입 정보에서 표기를 그대로 읽는다 —
// 다시 파싱·해석하면 가려진 이름이나 봉인 메서드가 다른 객체로 풀린다.
package source

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// anonScan은 의존 소스 스캔의 누적 결과다.
// noInfo는 limitation 재료다 — 타입 정보 없는 의존 패키지의 표기를 조용히
// 버리면 그 인터페이스로만 불리는 메서드에 사실이 없는 이유를 알 수 없다.
type anonScan struct {
	ifaces []externalIface
	seen   map[string]bool
	noInfo int // AST·타입 정보가 없어 훑지 못한 의존 패키지 수
}

// scanAnonymousInterfaces는 모듈 밖 패키지 소스의 이름 없는 인터페이스 표기를
// 모은다. 결과는 이름 순이다 — 순회 순서가 사실 순서를 흔들지 않게.
func scanAnonymousInterfaces(internal []*packages.Package) *anonScan {
	scan := &anonScan{seen: map[string]bool{}}
	for _, p := range dependencyPackages(internal) {
		scan.scanPackage(p)
	}
	sort.Slice(scan.ifaces, func(i, j int) bool { return scan.ifaces[i].name < scan.ifaces[j].name })
	return scan
}

// limitations는 스캔이 실제로 세어 둔 못 본 영역을 문장으로 돌려준다.
// "unreachable로 보일 수 있다" 같은 판정 결과가 아니라 문서 사실(사실이 빠졌다)로
// 쓴다 — RTA는 의존 코드를 SSA로 직접 봐서 같은 메서드를 살리므로, 결과를
// 말하는 문구는 알고리즘에 따라 거짓이 된다.
func (s *anonScan) limitations() []string {
	if s.noInfo == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"%d dependency packages had no syntax or type information; their anonymous interfaces produce no satisfies facts",
		s.noInfo)}
}

// dependencyPackages는 모듈 패키지에서 import로 닿는 모듈 밖 패키지를
// 경로 순으로 모은다. unsafe는 소스가 없는 의사 패키지라 뺀다.
func dependencyPackages(internal []*packages.Package) []*packages.Package {
	inModule := make(map[string]bool, len(internal))
	for _, p := range internal {
		inModule[p.PkgPath] = true
	}
	seen := map[string]*packages.Package{}
	queue := append([]*packages.Package(nil), internal...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, imp := range cur.Imports {
			if _, ok := seen[imp.PkgPath]; ok || inModule[imp.PkgPath] || imp.PkgPath == "unsafe" {
				continue
			}
			seen[imp.PkgPath] = imp
			queue = append(queue, imp)
		}
	}
	return sortedPackages(seen)
}

// sortedPackages는 패키지를 경로 순으로 돌려준다.
func sortedPackages(pkgs map[string]*packages.Package) []*packages.Package {
	out := make([]*packages.Package, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PkgPath < out[j].PkgPath })
	return out
}

// scanPackage는 의존 패키지 하나의 모든 파일에서 표기를 누적한다.
func (s *anonScan) scanPackage(p *packages.Package) {
	if len(p.Syntax) == 0 || p.TypesInfo == nil {
		s.noInfo++
		return
	}
	for _, file := range p.Syntax {
		for _, lit := range interfaceLiterals(file) {
			s.add(p.TypesInfo.Types[lit].Type)
		}
	}
}

// add는 표기 하나의 해석된 타입을 이름으로 중복 제거해 누적한다.
// 타입 원소(~int, 유니언, 비인터페이스 임베드)가 있는 인터페이스는 제약 전용이라
// 값으로 디스패치되지 않고, 메서드가 없으면 부를 것이 없어 뺀다.
// 타입 파라미터를 쓰는 표기는 그대로 담는다 — 모듈 메서드가 T를 적을 수 없어
// Implements가 거짓이 될 뿐이고, 그 한계는 제네릭 limitation 문구가 이미 말한다.
func (s *anonScan) add(t types.Type) {
	iface, ok := t.(*types.Interface)
	if !ok || !iface.IsMethodSet() || iface.NumMethods() == 0 {
		return
	}
	name := methodSetName(iface)
	if s.seen[name] {
		return
	}
	s.seen[name] = true
	s.ifaces = append(s.ifaces, newExternalIface(name, iface))
}

// methodSetName은 이름 없는 인터페이스의 결정적 이름을 만든다 — 임베드를 펼친
// 메서드 집합을 파라미터 이름 없이 적는다("interface{Unwrap() error}").
// types.TypeString은 파라미터 이름과 임베드 형태를 그대로 옮겨, 같은 인터페이스가
// 표기마다 다른 이름이 되고 upstream의 이름 변경이 문서 diff가 된다.
// 비공개 메서드는 패키지 경로로 한정한다 — 다른 패키지의 같은 이름은 다른 메서드다.
func methodSetName(iface *types.Interface) string {
	methods := make([]string, iface.NumMethods())
	for i := range methods {
		m := iface.Method(i)
		name := m.Name()
		if !m.Exported() && m.Pkg() != nil {
			name = m.Pkg().Path() + "." + name
		}
		sig := types.TypeString(unnamedSignature(m.Signature()), nil)
		methods[i] = name + strings.TrimPrefix(sig, "func")
	}
	return "interface{" + strings.Join(methods, "; ") + "}"
}

// unnamedSignature는 파라미터·결과 이름을 뺀 같은 서명을 만든다.
func unnamedSignature(sig *types.Signature) *types.Signature {
	return types.NewSignatureType(nil, nil, nil,
		unnamedTuple(sig.Params()), unnamedTuple(sig.Results()), sig.Variadic())
}

// unnamedTuple은 튜플의 각 변수를 이름 없는 변수로 바꾼다.
func unnamedTuple(t *types.Tuple) *types.Tuple {
	vars := make([]*types.Var, t.Len())
	for i := range vars {
		vars[i] = types.NewParam(token.NoPos, nil, "", t.At(i).Type())
	}
	return types.NewTuple(vars...)
}

// interfaceLiterals는 파일에서 메서드·임베드 요소가 있는 인터페이스 표기를 모은다.
// 패키지 스코프의 명명 type 선언은 뺀다 — externalInterfaces가 명명 인터페이스로
// 이미 모았다. 별칭(type X = interface{…})은 명명 목록이 건너뛰므로 여기서 잡고,
// 함수 안 type 선언은 패키지 스코프에 없어 여기서 잡는다.
func interfaceLiterals(file *ast.File) []*ast.InterfaceType {
	named := packageScopeNamedInterfaces(file)
	var out []*ast.InterfaceType
	ast.Inspect(file, func(n ast.Node) bool {
		it, ok := n.(*ast.InterfaceType)
		if ok && !named[it] && it.Methods != nil && len(it.Methods.List) > 0 {
			out = append(out, it)
		}
		return true
	})
	return out
}

// packageScopeNamedInterfaces는 파일의 패키지 스코프 명명(별칭 아닌) type
// 선언이 직접 가진 인터페이스다.
func packageScopeNamedInterfaces(file *ast.File) map[*ast.InterfaceType]bool {
	out := map[*ast.InterfaceType]bool{}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts := spec.(*ast.TypeSpec)
			if it, ok := ts.Type.(*ast.InterfaceType); ok && !ts.Assign.IsValid() {
				out[it] = true
			}
		}
	}
	return out
}
