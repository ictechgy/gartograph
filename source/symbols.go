// 심볼·타입 레벨 수확.
//
// go/packages의 타입 정보와 AST를 순회해 심볼 정점과 call/implements/
// embeds/references/contains 간선을 만든다. 인터페이스 메서드 호출은
// CHA(Class Hierarchy Analysis)로 모든 구현체에 간선을 긋는다 —
// 과대 근사는 "살아 있다" 쪽으로만 기울어져 dead 오탐을 막는다.
package source

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/ictechgy/gartograph/graph"
	"golang.org/x/tools/go/packages"
)

// harvester는 한 번의 심볼 수확 동안 공유되는 상태다.
type harvester struct {
	doc      *graph.Document
	vertices map[string]bool     // 존재 확인 — 없는 정점으로의 간선은 금지
	edgeIdx  map[string]int      // (from,to,kind) → doc.Edges 인덱스 — 지점 병합용
	roots    map[string]bool     // 보존 루트 중복 억제
	impls    map[string][]string // 인터페이스 메서드 ID → 구현 메서드 ID들
	extRefs  int                 // 모듈 밖 심볼 참조 수
	noTypes  int                 // 타입 정보 없는 패키지 수
	reflectN int                 // reflect를 import하는 패키지 수
	linkname int                 // //go:linkname 지시문 수
}

// harvestSymbols는 in-module 패키지의 선언을 순회해 심볼/타입 정점과
// 간선을 doc에 추가한다. level이 type이면 타입과 타입 간 관계만,
// symbol이면 함수·변수·상수와 호출·참조까지 수확한다.
func harvestSymbols(doc *graph.Document, internal []*packages.Package, level graph.Level) {
	h := &harvester{
		doc:      doc,
		vertices: make(map[string]bool),
		edgeIdx:  make(map[string]int),
		roots:    make(map[string]bool),
		impls:    make(map[string][]string),
	}
	for _, v := range doc.Vertices {
		h.vertices[v.ID] = true
	}
	wantSymbols := level == graph.LevelSymbol

	h.addSymbolVertices(internal, wantSymbols)
	h.addStructuralEdges(internal)
	h.addBodyEdges(internal, wantSymbols)

	// limitation은 실제로 세어서 만든다.
	if h.noTypes > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d packages had no type information; their symbol edges are missing", h.noTypes))
	}
	if h.extRefs > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d references to symbols outside the module were omitted", h.extRefs))
	}
	if h.reflectN > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d packages import reflect; reflection-based dispatch is not resolved", h.reflectN))
	}
	if h.linkname > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d //go:linkname directives found; their targets may appear unreachable", h.linkname))
	}
	doc.Level = level
}

// addSymbolVertices는 패키지 스코프의 선언을 정점으로 만든다.
// init은 스코프에 들어가지 않아 별도로 decl 순회에서 만든다.
func (h *harvester) addSymbolVertices(internal []*packages.Package, wantSymbols bool) {
	for _, p := range internal {
		if p.Types == nil {
			h.noTypes++
			continue
		}
		if p.Imports["reflect"] != nil {
			h.reflectN++
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			kind, ok := kindOf(obj)
			if !ok {
				continue
			}
			if !wantSymbols && kind != graph.KindType {
				continue
			}
			id := objectID(obj)
			v := graph.Vertex{
				ID:       id,
				Kind:     kind,
				Name:     name,
				Package:  p.PkgPath,
				Position: position(p, obj.Pos()),
				Exported: obj.Exported(),
			}
			if tn, isType := obj.(*types.TypeName); isType {
				fillTypeShape(&v, tn)
			}
			if cn, isConst := obj.(*types.Const); isConst {
				v.Value = cn.Val().ExactString()
			}
			h.vertex(v)
			// contains는 소유 관계라 사용 지점이 없다 — 정점 자체의
			// position이 그 사실을 이미 담는다.
			h.edge(p.PkgPath, id, graph.EdgeContains, nil)
			if _, isFunc := obj.(*types.Func); isFunc && name == "main" && p.Name == "main" {
				h.root(id)
			}
			if tn, isType := obj.(*types.TypeName); isType && wantSymbols {
				h.methodVertices(tn, p)
			}
		}
	}
}

// fillTypeShape는 타입 정점에 형태 정보를 채운다 — 인터페이스 여부와
// struct 필드 목록은 diff의 breaking 분류 재료다.
// 모듈 안에 구현체가 없는 인터페이스도 표시해야 "메서드 추가 = 구현자
// 파괴"를 소비자가 알 수 있다.
func fillTypeShape(v *graph.Vertex, tn *types.TypeName) {
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return
	}
	switch u := named.Underlying().(type) {
	case *types.Interface:
		v.Interface = true
	case *types.Struct:
		v.Fields = structFields(u)
	}
}

// structFields는 struct의 필드를 "name:Type" 형태로 선언 순서대로 적는다.
// unkeyed composite literal은 필드 목록·순서·타입이 계약이라 이 셋이
// 곧 호환성 판정의 단위다. 타입 문자열은 패키지 경로 정규화를 써서
// 같은 이름의 다른 타입을 섞지 않는다.
func structFields(st *types.Struct) []string {
	out := make([]string, st.NumFields())
	for i := range out {
		f := st.Field(i)
		out[i] = f.Name() + ":" + types.TypeString(f.Type(), nil)
	}
	return out
}

// methodVertices는 명명 타입의 메서드를 정점으로 만든다.
// 인터페이스는 선언된 메서드만, 구체 타입은 포인터 메서드 집합(값+포인터
// 리시버 통합)을 쓴다 — 둘이 다른 메서드 집합을 보면 implements가 갈라진다.
func (h *harvester) methodVertices(tn *types.TypeName, p *packages.Package) {
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return
	}
	if iface, ok := named.Underlying().(*types.Interface); ok {
		for i := 0; i < iface.NumExplicitMethods(); i++ {
			h.methodVertex(iface.ExplicitMethod(i), p.Fset)
		}
		return
	}
	ms := types.NewMethodSet(types.NewPointer(named))
	for i := 0; i < ms.Len(); i++ {
		if f, ok := ms.At(i).Obj().(*types.Func); ok {
			h.methodVertex(f, p.Fset)
		}
	}
}

// methodVertex는 메서드 정점 하나를 만든다.
// 승격 메서드는 선언한 타입 아래의 ID를 갖는다 — "pkg.(Embedded).M"이 진짜 선언이다.
// 모듈 밖 타입의 메서드는 정점으로 만들지 않는다 — 외부 심볼은 정점이 없다.
func (h *harvester) methodVertex(f *types.Func, fset *token.FileSet) {
	if f.Pkg() == nil || !h.vertices[f.Pkg().Path()] {
		return
	}
	id := objectID(f)
	h.vertex(graph.Vertex{
		ID:       id,
		Kind:     graph.KindMethod,
		Name:     f.Name(),
		Package:  f.Pkg().Path(),
		Position: positionAt(fset, f.Pos()),
		Exported: f.Exported(),
	})
	h.edge(f.Pkg().Path(), id, graph.EdgeContains, nil)
}

// addStructuralEdges는 타입 간 구조적 관계(embeds, implements)를 긋는다.
// implements는 in-module 명명 타입 × 인터페이스의 전체 조합을 검사한다 —
// 누락된 만족 관계가 인터페이스 디스패치의 도달성을 끊기 때문이다.
func (h *harvester) addStructuralEdges(internal []*packages.Package) {
	var concrete, ifaces []*types.Named
	for _, p := range internal {
		if p.Types == nil {
			continue
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			if _, isIface := named.Underlying().(*types.Interface); isIface {
				ifaces = append(ifaces, named)
			} else {
				concrete = append(concrete, named)
			}
			h.embedEdges(tn, named, p.Fset)
		}
	}
	h.implementsEdges(concrete, ifaces)
}

// embedEdges는 구조체 임베드 필드와 인터페이스 임베드에서 embeds 간선을 긋는다.
// 구조체 필드는 선언 위치가 있고, 인터페이스 임베드는 타입 표현식이
// 하나의 지점을 이루지 않아 위치를 비워 둔다.
func (h *harvester) embedEdges(tn *types.TypeName, named *types.Named,
	fset *token.FileSet) {
	from := objectID(tn)
	switch u := named.Underlying().(type) {
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			f := u.Field(i)
			if !f.Embedded() {
				continue
			}
			if target := namedOf(f.Type()); target != nil {
				h.edge(from, objectID(target), graph.EdgeEmbeds,
					positionAt(fset, f.Pos()))
			}
		}
	case *types.Interface:
		for i := 0; i < u.NumEmbeddeds(); i++ {
			if target := namedOf(u.EmbeddedType(i)); target != nil {
				h.edge(from, objectID(target), graph.EdgeEmbeds, nil)
			}
		}
	}
}

// implementsEdges는 구체 타입이 인터페이스를 만족하면 implements 간선을 긋고,
// 인터페이스 메서드별 구현 메서드 목록을 impls에 쌓는다.
// 포인터 메서드 집합으로 검사해 리시버 종류와 무관한 만족을 잡는다.
func (h *harvester) implementsEdges(concrete, ifaces []*types.Named) {
	for _, t := range concrete {
		tset := types.NewMethodSet(types.NewPointer(t))
		for _, i := range ifaces {
			iface, ok := i.Underlying().(*types.Interface)
			if !ok || iface.NumMethods() == 0 {
				continue
			}
			if !types.Implements(t, iface) && !types.Implements(types.NewPointer(t), iface) {
				continue
			}
			// implements는 선언 위치가 아니라 타입 집합 계산의 산물이다 —
			// 지어낸 위치를 싣지 않는다.
			h.edge(objectID(t.Obj()), objectID(i.Obj()), graph.EdgeImplements, nil)
			for j := 0; j < iface.NumMethods(); j++ {
				m := iface.Method(j)
				sel := tset.Lookup(m.Pkg(), m.Name())
				if sel == nil {
					continue
				}
				if fn, ok := sel.Obj().(*types.Func); ok {
					h.impls[objectID(m)] = append(h.impls[objectID(m)], objectID(fn))
				}
			}
		}
	}
}

// addBodyEdges는 선언 몸체를 순회해 call·references 간선을 긋는다.
// 간선의 출발점은 감싸는 선언이다 — 몸체가 없는 선언(타입 등)은 시그니처만 본다.
func (h *harvester) addBodyEdges(internal []*packages.Package, wantSymbols bool) {
	for _, p := range internal {
		if p.TypesInfo == nil {
			continue
		}
		for _, file := range p.Syntax {
			h.linkname += countLinknames(file)
			for _, decl := range file.Decls {
				h.declEdges(p, decl, wantSymbols)
			}
		}
	}
}

// declEdges는 선언 하나의 몸체를 해당 정점의 간선으로 번역한다.
// ValueSpec은 여러 이름이 한 식을 공유할 수 있어 이름마다 같은 간선을 두되
// 중복 제거가 흡수한다 — 과대 근사가 누락보다 안전하다.
func (h *harvester) declEdges(p *packages.Package, decl ast.Decl, wantSymbols bool) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if !wantSymbols {
			return
		}
		obj, ok := p.TypesInfo.Defs[d.Name].(*types.Func)
		if !ok {
			return
		}
		from := objectID(obj)
		// init은 스코프에 없어 정점이 아직 없다 — 여기서 만들고 루트로 둔다.
		if d.Name.Name == "init" {
			h.vertex(graph.Vertex{
				ID:       from,
				Kind:     graph.KindFunc,
				Name:     "init",
				Package:  p.PkgPath,
				Position: position(p, d.Pos()),
			})
			h.edge(p.PkgPath, from, graph.EdgeContains, nil)
			h.root(from)
		}
		// Test*/Benchmark*/Example*/Fuzz*는 go test 러너만 호출한다 —
		// 그래프에 호출자가 없으므로 루트로 잡지 않으면 --tests 수확에서
		// 테스트 전부가 unreachable로 보고된다.
		if d.Recv == nil && isTestEntry(p, d) {
			h.root(from)
		}
		h.sigTypeEdges(p, d.Type, from)
		h.inspect(p, d, from)
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			h.specEdges(p, spec, wantSymbols)
		}
	}
}

// specEdges는 선언 스펙을 순회한다.
// type 레벨에서는 TypeSpec만 처리해 타입 간 관계만 남긴다.
func (h *harvester) specEdges(p *packages.Package, spec ast.Spec, wantSymbols bool) {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		obj, ok := p.TypesInfo.Defs[s.Name].(*types.TypeName)
		if !ok {
			return
		}
		id := objectID(obj)
		h.sigTypeEdges(p, s.Type, id)
		h.inspect(p, s, id)
	case *ast.ValueSpec:
		if !wantSymbols {
			return
		}
		for _, name := range s.Names {
			obj := p.TypesInfo.Defs[name]
			if obj == nil {
				continue
			}
			id := objectID(obj)
			if s.Type != nil {
				h.sigTypeEdges(p, s.Type, id)
			}
			h.inspect(p, s, id)
		}
	}
}

// inspect는 노드 아래의 호출·참조를 from 정점의 간선으로 기록한다.
func (h *harvester) inspect(p *packages.Package, node ast.Node, from string) {
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			h.callEdge(p, x, from)
		case *ast.SelectorExpr:
			h.selectorEdge(p, x, from)
		case *ast.Ident:
			h.identEdge(p, x, from)
		}
		return true
	})
}

// callEdge는 호출식의 피호출자를 해석해 call 간선을 긋는다.
// 인터페이스 디스패치는 인터페이스 메서드와 모든 구현 메서드로 팬아웃한다(CHA).
func (h *harvester) callEdge(p *packages.Package, ce *ast.CallExpr, from string) {
	fun := unwrapCallee(ce.Fun)
	switch f := fun.(type) {
	case *ast.Ident:
		// Pkg()가 nil인 객체(universe 스코프의 error.Error 등)는 정점이
		// 될 수 없다 — 그대로 objectID에 넣으면 nil 역참조로 죽는다.
		if fn, ok := p.TypesInfo.Uses[f].(*types.Func); ok && fn.Pkg() != nil {
			h.edge(from, objectID(fn), graph.EdgeCall, position(p, ce.Pos()))
		}
	case *ast.SelectorExpr:
		if sel, ok := p.TypesInfo.Selections[f]; ok {
			fn, ok := sel.Obj().(*types.Func)
			if !ok || fn.Pkg() == nil {
				return
			}
			id := objectID(fn)
			pos := position(p, ce.Pos())
			h.edge(from, id, graph.EdgeCall, pos)
			if isInterface(sel.Recv()) {
				for _, impl := range h.impls[id] {
					h.edge(from, impl, graph.EdgeCall, pos)
				}
			}
		} else if fn, ok := p.TypesInfo.Uses[f.Sel].(*types.Func); ok && fn.Pkg() != nil {
			// pkg.F() — 패키지 한정 선택자는 Selections가 아니라 Uses로 해석된다.
			h.edge(from, objectID(fn), graph.EdgeCall, position(p, ce.Pos()))
		}
	}
}

// sigTypeEdges는 선언의 타입 표현식 안 타입 참조를 signature 간선으로 긋는다.
// 같은 참조는 inspect가 references로도 남긴다 — 종류가 다른 두 사실이고,
// 중복 제거는 간선 키의 kind가 갈라 주므로 둘 다 살아남는다.
func (h *harvester) sigTypeEdges(p *packages.Package, expr ast.Expr, from string) {
	ast.Inspect(expr, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.Ident:
			h.sigRef(p.TypesInfo.Uses[t], from, position(p, t.Pos()))
		case *ast.SelectorExpr:
			h.sigRef(p.TypesInfo.Uses[t.Sel], from, position(p, t.Pos()))
		}
		return true
	})
}

// sigRef는 시그니처 타입 참조 하나를 간선으로 긴다. 모듈 밖 참조는
// inspect의 references 패스에서 이미 extRefs로 셌으므로 여기서는 건너뛴다 —
// 같은 참조를 두 번 세면 limitation 수치가 부푼다.
func (h *harvester) sigRef(obj types.Object, from string, pos *graph.Position) {
	if obj == nil || obj.Pkg() == nil || !isPackageLevel(obj) {
		return
	}
	id := objectID(obj)
	if !h.vertices[id] {
		return
	}
	h.edge(from, id, graph.EdgeSignature, pos)
}

// selectorEdge는 비호출 위치의 선택자(메서드 값, 필드 접근)를
// references 간선으로 기록한다. 호출 위치는 callEdge가 call로 이미 남긴다.
func (h *harvester) selectorEdge(p *packages.Package, sel *ast.SelectorExpr, from string) {
	if s, ok := p.TypesInfo.Selections[sel]; ok {
		h.refObject(s.Obj(), from, position(p, sel.Pos()))
		return
	}
	h.refObject(p.TypesInfo.Uses[sel.Sel], from, position(p, sel.Pos()))
}

// identEdge는 식별자 참조를 references 간선으로 기록한다.
// 지역 변수는 패키지 스코프 선언이 아니라 걸러진다 — 같은 이름의 지역 변수가
// 패키지 수준 심볼을 가리키는 가짜 간선을 막는다.
func (h *harvester) identEdge(p *packages.Package, id *ast.Ident, from string) {
	h.refObject(p.TypesInfo.Uses[id], from, position(p, id.Pos()))
}

// refObject는 패키지 수준의 모듈 안 객체를 가리키는 references 간선을 긋는다.
// 지역 선언·모듈 밖 심볼은 정점이 없으므로 외부 참조는 개수만 세어 둔다.
func (h *harvester) refObject(obj types.Object, from string, pos *graph.Position) {
	if obj == nil || obj.Pkg() == nil || !isPackageLevel(obj) {
		return
	}
	id := objectID(obj)
	if !h.vertices[id] {
		h.extRefs++
		return
	}
	h.edge(from, id, graph.EdgeReferences, pos)
}

// isPackageLevel은 객체가 패키지 스코프 선언인지 확인한다.
// 메서드는 패키지 스코프에 들어가지 않아 리시버 유무로 판별한다.
func isPackageLevel(obj types.Object) bool {
	if obj.Parent() == obj.Pkg().Scope() {
		return true
	}
	if f, ok := obj.(*types.Func); ok && f.Signature() != nil {
		return f.Signature().Recv() != nil
	}
	return false
}

// unwrapCallee는 제네릭 인스턴스화와 괄호를 벗겨 진짜 피호출자를 꺼낸다.
func unwrapCallee(e ast.Expr) ast.Expr {
	for {
		switch w := e.(type) {
		case *ast.ParenExpr:
			e = w.X
		case *ast.IndexExpr:
			e = w.X
		case *ast.IndexListExpr:
			e = w.X
		default:
			return e
		}
	}
}

// vertex는 정점을 중복 없이 추가한다.
func (h *harvester) vertex(v graph.Vertex) {
	if h.vertices[v.ID] {
		return
	}
	h.vertices[v.ID] = true
	h.doc.Vertices = append(h.doc.Vertices, v)
}

// edge는 양 끝 정점이 있을 때만 간선을 추가한다.
// 의존 간선의 목적지가 없으면 모듈 밖 참조로 센다 — 없는 정점을 가리키는
// 간선은 유령이라 만들지 않고, 그 사실은 limitation으로 남긴다.
// pos는 이 관계가 성립하는 소스 지점이다 — 같은 관계가 지점마다 반복되면
// 간선은 하나인 채 positions에 지점만 쌓인다.
func (h *harvester) edge(from, to string, kind graph.EdgeKind, pos *graph.Position) {
	if !h.vertices[from] || !h.vertices[to] {
		if h.vertices[from] && kind != graph.EdgeContains {
			h.extRefs++
		}
		return
	}
	key := from + "\x00" + to + "\x00" + string(kind)
	if i, ok := h.edgeIdx[key]; ok {
		if pos != nil {
			h.doc.Edges[i].Positions = append(h.doc.Edges[i].Positions, *pos)
		}
		return
	}
	e := graph.Edge{From: from, To: to, Kind: kind}
	if pos != nil {
		e.Positions = []graph.Position{*pos}
	}
	h.edgeIdx[key] = len(h.doc.Edges)
	h.doc.Edges = append(h.doc.Edges, e)
}

// isTestEntry는 go test 러너가 진입하는 패키지 레벨 함수인지 본다.
// _test.go 파일 안의 Test/Benchmark/Example/Fuzz 접두 함수가 대상이다.
func isTestEntry(p *packages.Package, d *ast.FuncDecl) bool {
	file := p.Fset.Position(d.Pos()).Filename
	if !strings.HasSuffix(file, "_test.go") {
		return false
	}
	for _, prefix := range []string{"Test", "Benchmark", "Example", "Fuzz"} {
		if strings.HasPrefix(d.Name.Name, prefix) {
			return true
		}
	}
	return false
}

// root는 보존 루트를 중복 없이 기록한다.
func (h *harvester) root(id string) {
	if h.roots[id] {
		return
	}
	h.roots[id] = true
	h.doc.Roots = append(h.doc.Roots, id)
}

// objectID는 심볼의 정점 ID를 만든다.
// 패키지 레벨 심볼은 "pkgpath.Name", 메서드는 "pkgpath.(Recv).Name"이다.
func objectID(obj types.Object) string {
	if f, ok := obj.(*types.Func); ok {
		if recv := recvName(f); recv != "" {
			return f.Pkg().Path() + ".(" + recv + ")." + f.Name()
		}
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

// recvName은 메서드의 리시버 타입 이름을 돌려준다.
// 포인터 리시버는 벗겨 같은 타입 아래로 모은다 — "(T).M"과 "(*T).M"을
// 두 정점으로 갈라 내면 같은 선언이 둘이 된다.
func recvName(f *types.Func) string {
	sig := f.Signature()
	if sig == nil || sig.Recv() == nil {
		return ""
	}
	t := sig.Recv().Type()
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return t.String()
}

// kindOf는 go/types 객체를 정점 종류로 번역한다.
// 정점이 없는 종류(Builtin·Label·PkgName 등)는 false다.
func kindOf(obj types.Object) (graph.VertexKind, bool) {
	switch obj.(type) {
	case *types.TypeName:
		return graph.KindType, true
	case *types.Func:
		return graph.KindFunc, true
	case *types.Var:
		return graph.KindVar, true
	case *types.Const:
		return graph.KindConst, true
	}
	return "", false
}

// namedOf는 타입에서 명명 타입을 꺼낸다. 포인터와 별칭을 한 겹 벗긴다.
func namedOf(t types.Type) *types.TypeName {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if alias, ok := t.(*types.Alias); ok {
		t = types.Unalias(alias)
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj()
	}
	return nil
}

// isInterface는 타입이 인터페이스로 해석되는지 확인한다.
// 셀렉터 리시버가 인터페이스면 호출은 동적 디스패치다.
func isInterface(t types.Type) bool {
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

// countLinknames는 파일의 //go:linkname 주석 수를 센다.
// linkname은 심볼 그래프 밖에서 함수를 살리므로 limitation 대상이다.
func countLinknames(file *ast.File) int {
	n := 0
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:linkname") {
				n++
			}
		}
	}
	return n
}

// position은 패키지 fset 기준 위치를 만든다.
func position(p *packages.Package, pos token.Pos) *graph.Position {
	return positionAt(p.Fset, pos)
}

// positionAt은 FileSet 기준의 소스 위치를 만든다.
// go/packages는 로드 세션에 하나의 FileSet을 공유하므로 다른 패키지의
// 심볼 위치도 같은 fset으로 해석할 수 있다.
func positionAt(fset *token.FileSet, pos token.Pos) *graph.Position {
	if fset == nil {
		return nil
	}
	p := fset.Position(pos)
	if !p.IsValid() {
		return nil
	}
	return &graph.Position{File: p.Filename, Line: p.Line, Column: p.Column}
}
