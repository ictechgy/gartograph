// 컴파일러 지시문이 만드는 보존 사실.
//
// //go:wasmexport와 //go:linkname은 그래프 밖(wasm 호스트·링커)에서 함수를 부르거나 두 선언을
// 같은 심볼로 묶는다. 호출 지점이 소스에 없으므로 수확이 지시문을 읽어 루트·간선으로 옮긴다.
// 판정은 "살아 있다" 쪽으로만 기운다 — 해석하지 못한 지시문은 limitation으로 센다.
package source

import (
	"go/ast"
	"go/types"
	"strings"

	"github.com/ictechgy/gartograph/graph"
	"golang.org/x/tools/go/packages"
)

// wasmExported는 함수가 //go:wasmexport <이름>으로 wasm 호스트에 내보내지는지 본다.
// 내보내는 이름은 함수 이름과 달라도 되므로 이름 인자가 있는지만 확인한다. 리시버가 있는
// 메서드는 내보낼 수 없다. 이름 없는 지시자는 컴파일 오류라 루트로 인정하지 않는다.
func wasmExported(function *ast.FuncDecl) bool {
	if function.Recv != nil || function.Doc == nil {
		return false
	}
	for _, comment := range function.Doc.List {
		if rest, ok := strings.CutPrefix(comment.Text, "//go:wasmexport "); ok && strings.TrimSpace(rest) != "" {
			return true
		}
	}
	return false
}

// linkname은 //go:linkname 지시문 하나다. target이 비면 인자 하나짜리(내보내기 표시) 형식이다.
type linkname struct {
	local, target string
}

// parseLinkname은 주석 한 줄을 //go:linkname 지시문으로 읽는다.
func parseLinkname(text string) (linkname, bool) {
	rest, ok := strings.CutPrefix(text, "//go:linkname ")
	if !ok {
		return linkname{}, false
	}
	fields := strings.Fields(rest)
	switch len(fields) {
	case 1:
		return linkname{local: fields[0]}, true
	case 2:
		return linkname{local: fields[0], target: fields[1]}, true
	}
	return linkname{}, false
}

// splitLinkTarget은 "importpath.name"을 컴파일러처럼 마지막 '/' 뒤의 첫 '.'에서 나눈다 —
// 도메인의 점(github.com)은 마지막 '/' 앞에 있다. name에 점이 남으면 메서드 표기(T.m)다.
func splitLinkTarget(target string) (pkgPath, name string, ok bool) {
	slash := strings.LastIndex(target, "/")
	dot := strings.Index(target[slash+1:], ".")
	if dot < 0 {
		return "", "", false
	}
	cut := slash + 1 + dot
	return target[:cut], target[cut+1:], true
}

// linknames는 파일의 //go:linkname 지시문을 보존 사실로 옮기고, 모듈 심볼로 해석하지 못한 수를
// 돌려준다. 양쪽이 모두 모듈 심볼이면 서로를 후보 references 간선으로 잇고(누가 누구와 묶였는지 —
// query·impact 재료, 후보라 확정 순환의 근거가 아니다) 본문 있는 함수 쪽을 루트로 둔다. 본문 없는
// 선언을 부르는 호출은 SSA에서 거기서 끝나므로 RTA는 간선만으로 본문 쪽에 닿지 못한다 — 루트로 두면
// CHA와 RTA가 같은 결론을 낸다(묶인 짝이 죽었어도 살리는 과대 근사, "살아 있다" 쪽).
// 인자 하나짜리 지시문과, 본문 있는 함수를 모듈 밖 이름으로 밀어 넣는 지시문은 밖에서 불릴 수
// 있어 루트다. 본문 없는 선언이 모듈 밖 심볼을 당겨 오는 지시문은 모듈 안에 살릴 대상이 없다.
func (h *harvester) linknames(p *packages.Package, file *ast.File) int {
	unresolved := 0
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if !strings.HasPrefix(c.Text, "//go:linkname") {
				continue
			}
			if l, ok := parseLinkname(c.Text); !ok || !h.applyLinkname(p, file, l) {
				unresolved++
			}
		}
	}
	return unresolved
}

// applyLinkname은 지시문 하나를 루트·간선으로 옮긴다. 해석했으면 true다.
func (h *harvester) applyLinkname(p *packages.Package, file *ast.File, l linkname) bool {
	localID, ok := h.packageSymbolID(p.Types, l.local)
	if !ok {
		return false
	}
	if l.target == "" {
		h.root(localID)
		return true
	}
	pkgPath, name, ok := splitLinkTarget(l.target)
	if !ok {
		return false
	}
	if !h.modulePkgs[pkgPath] {
		if hasFuncBody(file, l.local) {
			h.root(localID) // 모듈 밖으로 밀어 넣은 구현 — 링커를 거쳐 밖에서 불린다
		}
		return true
	}
	targetID, ok := h.packageSymbolID(h.moduleTypes[pkgPath], name)
	if !ok {
		return false // 메서드 표기(T.m)나 없는 이름 — 정점으로 옮길 수 없다
	}
	h.candidateEdge(localID, targetID, graph.EdgeReferences, nil)
	h.candidateEdge(targetID, localID, graph.EdgeReferences, nil)
	if hasFuncBody(file, l.local) {
		h.root(localID) // 모듈 안의 본문 없는 선언으로 밀어 넣은 구현
	} else if _, isFunc := h.moduleTypes[pkgPath].Scope().Lookup(name).(*types.Func); isFunc {
		h.root(targetID) // 본문 없는 선언이 당겨 온 구현
	}
	return true
}

// packageSymbolID는 패키지 스코프의 함수·변수 이름을 정점 ID로 바꾼다. 정점이 없으면 false다.
func (h *harvester) packageSymbolID(pkg *types.Package, name string) (string, bool) {
	if pkg == nil || strings.Contains(name, ".") {
		return "", false
	}
	obj := pkg.Scope().Lookup(name)
	switch obj.(type) {
	case *types.Func, *types.Var:
	default:
		return "", false
	}
	id := h.id(obj)
	return id, h.vertices[id]
}

// hasFuncBody는 파일에 이름이 같은 본문 있는 최상위 함수 선언이 있는지 본다.
func hasFuncBody(file *ast.File, name string) bool {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
			return fn.Body != nil
		}
	}
	return false
}

// moduleTypesByPath는 모듈 패키지의 PkgPath → 타입 패키지다. 테스트 변형("p [p.test]")보다 원
// 패키지를 고른다 — linkname 대상은 원 패키지의 패키지 스코프 선언이다.
func moduleTypesByPath(pkgs []*packages.Package) map[string]*types.Package {
	out := make(map[string]*types.Package, len(pkgs))
	for _, p := range pkgs {
		if p.Types == nil {
			continue
		}
		if _, seen := out[p.PkgPath]; !seen || p.ID == p.PkgPath {
			out[p.PkgPath] = p.Types
		}
	}
	return out
}
