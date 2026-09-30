// 라우터 값 흐름 — 어느 등록 호출이 어느 라우터(와 그 접두사 사슬)에 닿는지.
//
// 흐름에 둔감한(flow-insensitive) 부분집합 분석이다. 라우터 값은 생성 호출(chi.NewRouter,
// gin.Default 등)이나 하위 라우터 호출(Group·Route·With·Host)마다 노드 하나이고, 변수·필드·
// 함수 파라미터·결과를 칸(cell)으로 삼아 대입·호출 인자·return을 따라 고정점까지 전파한다.
// 필드는 객체(types.Var) 단위로 합친다 — 같은 필드에 담긴 라우터는 어느 인스턴스든 같은 칸이다.
// 이 근사는 "등록이 닿을 수 있는 라우터"를 넓게 잡는 쪽이다: 칸이 비면(모듈 밖에서만 불리는
// 함수의 파라미터 등) 접두사를 모른다고 보고 pathAnchor "base"로 낸다.
package source

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/typeutil"
)

// nodeKind는 라우터 노드의 출처다.
type nodeKind int

const (
	nodeRoot    nodeKind = iota // 생성 호출(NewRouter·New·Default·NewServeMux·&ServeMux{})
	nodeDefault                 // http.DefaultServeMux
	nodeDerived                 // Group·Route·With·Host가 만든 하위 라우터
)

// routerNode는 추상 라우터 값 하나다.
type routerNode struct {
	id      int
	fw      routeFramework
	kind    nodeKind
	derive  deriveKind
	pkg     *packages.Package
	call    *ast.CallExpr // 생성·하위 라우터 호출(기본 mux는 nil)
	parents []parentEdge
}

// edgeKind는 하위 노드가 부모 경로를 물려받는 방식이다.
type edgeKind int

const (
	edgeDerive   edgeKind = iota // 같은 프레임워크의 하위 라우터(node.derive 규칙)
	edgeChiMount                 // chi Mount로 붙인 chi 라우터
	edgeStrip                    // http.StripPrefix로 접두사를 떼고 넘긴 라우터
)

// unknownHost는 상수가 아닌 echo Host 인자의 자리표시다(narrowed 판정에만 쓴다).
const unknownHost = "?"

// noParent는 수신 라우터를 추적하지 못한 하위 라우터 간선의 부모 자리다.
const noParent = -1

// parentEdge는 부모 노드(흐름 상태의 노드 번호)와 접두사다. dynamic이면 접두사가 상수가 아니다.
// 포인터 대신 번호를 쓰는 이유: 노드와 간선이 서로를 담으면 타입 순환이 된다.
type parentEdge struct {
	parent  int
	kind    edgeKind
	derive  deriveKind
	prefix  string
	dynamic bool
}

// nodeSet은 노드 집합이다.
type nodeSet map[*routerNode]bool

// sorted는 노드를 id 순으로 돌려준다 — 맵 순회 순서가 출력에 새지 않게 한다.
func (s nodeSet) sorted() []*routerNode {
	out := make([]*routerNode, 0, len(s))
	for n := range s {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// nodeIDs는 노드 번호 목록이다.
func nodeIDs(nodes []*routerNode) []int {
	out := make([]int, len(nodes))
	for i, n := range nodes {
		out[i] = n.id
	}
	return out
}

// addAll은 src를 합치고 바뀌었는지 돌려준다.
func (s nodeSet) addAll(src nodeSet) bool {
	changed := false
	for n := range src {
		if !s[n] {
			s[n] = true
			changed = true
		}
	}
	return changed
}

// ruleKind는 전파 규칙의 모양이다.
type ruleKind int

const (
	ruleExpr   ruleKind = iota // dst ← eval(expr)
	ruleResult                 // dst ← 함수 fn의 결과 index
	ruleReturn                 // 함수 fn의 결과 index ← eval(expr)
)

// flowRule은 고정점 반복에서 다시 적용하는 전파 규칙 하나다. 흐름 상태를 가리키는 함수 값 대신
// 데이터로 둔다 — 상태가 규칙을, 규칙이 상태를 담으면 타입 순환이 된다.
type flowRule struct {
	kind  ruleKind
	dst   types.Object
	fn    *types.Func
	index int
	expr  ast.Expr
	info  *types.Info
	pkg   *packages.Package
}

// registration은 수집한 라우터 호출 하나다(경로 등록·마운트·정적 파일).
type registration struct {
	spec  regSpec
	call  *ast.CallExpr
	pkg   *packages.Package
	owner string // 호출을 감싸는 선언의 정점 ID 후보
	recv  ast.Expr
}

// routeFlow는 흐름 분석 상태다.
type routeFlow struct {
	cells      map[types.Object]nodeSet
	results    map[*types.Func][]nodeSet
	decls      map[*types.Func]bool
	nodes      []*routerNode
	nodeAt     map[token.Pos]*routerNode
	defaultMux *routerNode
	rules      []flowRule
	regs       []registration
	// ginRedirect는 gin 루트 노드별 RedirectTrailingSlash 대입이다(없으면 기본값 true).
	ginRedirect map[*routerNode][]ginAssign
	ginAssigns  []ginAssign
	// slashMiddleware는 끝 슬래시를 바꾸는 미들웨어를 쓰는 프레임워크다.
	slashMiddleware map[routeFramework]bool
	ids             symbolIDs
}

// ginAssign은 gin Engine 설정 필드 대입이다(고정점 뒤에 대상 엔진을 푼다).
type ginAssign struct {
	target ast.Expr
	value  ast.Expr
	pkg    *packages.Package
}

// newRouteFlow는 빈 흐름 상태를 만든다.
func newRouteFlow(ids symbolIDs) *routeFlow {
	return &routeFlow{
		cells: map[types.Object]nodeSet{}, results: map[*types.Func][]nodeSet{},
		decls: map[*types.Func]bool{}, nodeAt: map[token.Pos]*routerNode{},
		ginRedirect: map[*routerNode][]ginAssign{}, slashMiddleware: map[routeFramework]bool{},
		ids: ids,
	}
}

// analyze는 패키지의 제약을 모으고 고정점까지 전파한 뒤 부모 간선을 푼다.
func (f *routeFlow) analyze(pkgs []*packages.Package) {
	for _, p := range pkgs {
		f.indexDecls(p)
	}
	for _, p := range pkgs {
		for _, file := range p.Syntax {
			f.collectFile(p, file)
		}
	}
	for iteration := 0; iteration < 200; iteration++ {
		changed := false
		for _, rule := range f.rules {
			changed = f.apply(rule) || changed
		}
		if !changed {
			break
		}
	}
	f.resolveParents()
	f.resolveGinSettings()
}

// apply는 규칙 하나를 적용하고 칸이 바뀌었는지 돌려준다.
func (f *routeFlow) apply(rule flowRule) bool {
	switch rule.kind {
	case ruleResult:
		return f.cell(rule.dst).addAll(f.result(rule.fn, rule.index))
	case ruleReturn:
		return f.result(rule.fn, rule.index).addAll(f.eval(rule.info, rule.pkg, rule.expr))
	}
	return f.cell(rule.dst).addAll(f.eval(rule.info, rule.pkg, rule.expr))
}

// indexDecls는 본문이 있는 모듈 함수를 기록한다 — 파라미터·결과 칸은 이 함수들에만 둔다.
func (f *routeFlow) indexDecls(p *packages.Package) {
	if p.TypesInfo == nil {
		return
	}
	for _, file := range p.Syntax {
		for _, decl := range file.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil {
				if fn, ok := p.TypesInfo.Defs[fd.Name].(*types.Func); ok {
					f.decls[fn] = true
				}
			}
		}
	}
}

// cell은 객체의 칸이다(없으면 만든다).
func (f *routeFlow) cell(obj types.Object) nodeSet {
	s, ok := f.cells[obj]
	if !ok {
		s = nodeSet{}
		f.cells[obj] = s
	}
	return s
}

// result는 함수 결과 i의 칸이다.
func (f *routeFlow) result(fn *types.Func, i int) nodeSet {
	rs := f.results[fn]
	if rs == nil {
		rs = make([]nodeSet, fn.Signature().Results().Len())
		f.results[fn] = rs
	}
	if rs[i] == nil {
		rs[i] = nodeSet{}
	}
	return rs[i]
}

// node는 위치에 묶인 노드를 돌려준다(처음이면 만든다) — 반복마다 같은 호출이 같은 노드다.
// 위치는 호출의 여는 괄호(리터럴은 여는 중괄호)다 — `chi.NewRouter().With(m)`처럼 사슬 호출은
// 시작 위치(Pos)가 같아 두 노드가 합쳐지기 때문이다.
func (f *routeFlow) node(pos token.Pos, fw routeFramework, kind nodeKind, dk deriveKind,
	p *packages.Package, call *ast.CallExpr) *routerNode {
	if n, ok := f.nodeAt[pos]; ok {
		return n
	}
	n := &routerNode{id: len(f.nodes), fw: fw, kind: kind, derive: dk, pkg: p, call: call}
	f.nodes = append(f.nodes, n)
	f.nodeAt[pos] = n
	return n
}

// defaultServeMux는 http.DefaultServeMux 노드다.
func (f *routeFlow) defaultServeMux() *routerNode {
	if f.defaultMux == nil {
		f.defaultMux = &routerNode{id: len(f.nodes), fw: fwServeMux, kind: nodeDefault}
		f.nodes = append(f.nodes, f.defaultMux)
	}
	return f.defaultMux
}

// flowWalker는 파일 하나를 훑으며 감싸는 함수(return 귀속)와 선언 귀속(usr 후보)을 추적한다.
type flowWalker struct {
	f     *routeFlow
	p     *packages.Package
	fn    []*types.Func // 감싸는 함수 스택(함수 리터럴은 nil)
	path  []ast.Node    // 지금 노드까지의 조상 — ast.Inspect의 나감(nil) 호출에서 함수 리터럴을 닫는다
	owner string
}

// collectFile은 파일의 선언마다 귀속을 정하고 제약·등록을 모은다.
func (f *routeFlow) collectFile(p *packages.Package, file *ast.File) {
	if p.TypesInfo == nil {
		return
	}
	for _, decl := range file.Decls {
		for _, part := range declParts(f.ids, p, decl) {
			w := &flowWalker{f: f, p: p, owner: part.owner}
			if fd, ok := part.node.(*ast.FuncDecl); ok {
				fn, _ := p.TypesInfo.Defs[fd.Name].(*types.Func)
				w.fn = []*types.Func{fn}
				if fd.Body != nil {
					ast.Inspect(fd.Body, w.visit)
				}
				continue
			}
			ast.Inspect(part.node, w.visit)
		}
	}
}

// visit는 노드 하나를 관찰한다. 함수 리터럴 안에서는 return을 감싸는 함수에 귀속하지 않도록
// 함수 스택에 nil을 쌓고, 나갈 때(ast.Inspect의 nil 호출) 걷어 낸다 — 재귀 대신 조상 스택을 쓴다.
func (w *flowWalker) visit(n ast.Node) bool {
	if n == nil {
		last := w.path[len(w.path)-1]
		w.path = w.path[:len(w.path)-1]
		if _, ok := last.(*ast.FuncLit); ok {
			w.fn = w.fn[:len(w.fn)-1]
		}
		return true
	}
	w.path = append(w.path, n)
	switch node := n.(type) {
	case *ast.FuncLit:
		w.fn = append(w.fn, nil)
	case *ast.AssignStmt:
		w.assign(node)
	case *ast.ValueSpec:
		w.valueSpec(node)
	case *ast.ReturnStmt:
		w.ret(node)
	case *ast.CompositeLit:
		w.composite(node)
	case *ast.CallExpr:
		w.call(node)
	case *ast.Ident:
		w.observeMiddleware(node)
	}
	return true
}

// info는 지금 패키지의 타입 정보다.
func (w *flowWalker) info() *types.Info { return w.p.TypesInfo }

// assign은 대입문의 제약을 더한다. 오른쪽이 다중 결과 호출 하나면 결과 칸을 짝짓는다.
func (w *flowWalker) assign(s *ast.AssignStmt) {
	if len(s.Lhs) == len(s.Rhs) {
		for i := range s.Lhs {
			w.flowInto(s.Lhs[i], s.Rhs[i])
			w.observeGinSetting(s.Lhs[i], s.Rhs[i])
		}
		return
	}
	if len(s.Rhs) == 1 {
		w.tupleInto(s.Lhs, s.Rhs[0])
	}
}

// valueSpec은 var 선언의 제약을 더한다.
func (w *flowWalker) valueSpec(s *ast.ValueSpec) {
	if len(s.Names) == len(s.Values) {
		for i := range s.Names {
			w.flowInto(s.Names[i], s.Values[i])
		}
		return
	}
	if len(s.Values) == 1 {
		lhs := make([]ast.Expr, len(s.Names))
		for i, name := range s.Names {
			lhs[i] = name
		}
		w.tupleInto(lhs, s.Values[0])
	}
}

// ret은 감싸는 함수의 결과 칸 제약을 더한다(함수 리터럴 안의 return은 귀속하지 않는다).
func (w *flowWalker) ret(s *ast.ReturnStmt) {
	fn := w.fn[len(w.fn)-1]
	if fn == nil {
		return
	}
	results := fn.Signature().Results()
	if len(s.Results) != results.Len() {
		return
	}
	info := w.info()
	for i, expr := range s.Results {
		if !isRouterType(results.At(i).Type()) {
			continue
		}
		w.f.rules = append(w.f.rules, flowRule{kind: ruleReturn, fn: fn, index: i, expr: expr, info: info, pkg: w.p})
	}
}

// composite는 구조체 리터럴의 필드 초기값을 필드 칸에 흘린다.
func (w *flowWalker) composite(lit *ast.CompositeLit) {
	st, ok := derefStruct(w.info().TypeOf(lit))
	if !ok {
		return
	}
	for i, elt := range lit.Elts {
		var field *types.Var
		value := elt
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			field = fieldByName(st, key.Name)
			value = kv.Value
		} else if i < st.NumFields() {
			field = st.Field(i)
		}
		if field != nil && isRouterType(field.Type()) {
			w.addFlow(field, value)
		}
	}
}

// derefStruct는 (포인터를 벗긴) 구조체 타입을 돌려준다.
func derefStruct(t types.Type) (*types.Struct, bool) {
	if t == nil {
		return nil, false
	}
	if ptr, ok := t.Underlying().(*types.Pointer); ok {
		t = ptr.Elem()
	}
	st, ok := t.Underlying().(*types.Struct)
	return st, ok
}

// fieldByName은 구조체의 이름 있는 필드다.
func fieldByName(st *types.Struct, name string) *types.Var {
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == name {
			return st.Field(i)
		}
	}
	return nil
}

// flowInto는 lhs ← rhs 제약을 더한다(lhs가 라우터 타입의 변수·필드일 때만).
func (w *flowWalker) flowInto(lhs, rhs ast.Expr) {
	obj := w.targetObject(lhs)
	if obj == nil || !isRouterType(obj.Type()) {
		return
	}
	w.addFlow(obj, rhs)
}

// addFlow는 obj ← expr 제약을 더한다.
func (w *flowWalker) addFlow(obj types.Object, expr ast.Expr) {
	w.f.rules = append(w.f.rules, flowRule{kind: ruleExpr, dst: obj, expr: expr, info: w.info(), pkg: w.p})
}

// tupleInto는 다중 결과 호출을 왼쪽 변수들에 짝짓는다.
func (w *flowWalker) tupleInto(lhs []ast.Expr, rhs ast.Expr) {
	call, ok := unparen(rhs).(*ast.CallExpr)
	if !ok {
		return
	}
	fn := calleeFunc(w.info(), call)
	if fn == nil || !w.f.decls[fn] || fn.Signature().Results().Len() != len(lhs) {
		return
	}
	for i, target := range lhs {
		obj := w.targetObject(target)
		if obj == nil || !isRouterType(obj.Type()) {
			continue
		}
		w.f.rules = append(w.f.rules, flowRule{kind: ruleResult, dst: obj, fn: fn, index: i})
	}
}

// targetObject는 대입 대상 식의 칸 객체다(변수 이름이나 필드 선택).
func (w *flowWalker) targetObject(expr ast.Expr) types.Object {
	info := w.info()
	switch e := unparen(expr).(type) {
	case *ast.Ident:
		if obj := info.Defs[e]; obj != nil {
			return obj
		}
		return info.Uses[e]
	case *ast.SelectorExpr:
		if sel := info.Selections[e]; sel != nil && sel.Kind() == types.FieldVal {
			return sel.Obj()
		}
		return info.Uses[e.Sel]
	}
	return nil
}

// call은 호출 하나를 관찰한다: 모듈 함수 인자 → 파라미터 칸, 라우터 호출 → 등록·콜백 칸.
func (w *flowWalker) call(call *ast.CallExpr) {
	info := w.info()
	fn := calleeFunc(info, call)
	if fn == nil {
		return
	}
	if spec, ok := routeAPI[funcKey(fn)]; ok {
		w.registration(call, spec)
	}
	if w.f.decls[fn] {
		w.bindArguments(call, fn)
	}
}

// registration은 라우터 호출을 기록하고, chi Route·Group 콜백의 첫 파라미터에 하위 노드를 흘린다.
func (w *flowWalker) registration(call *ast.CallExpr, spec regSpec) {
	var recv ast.Expr
	if sel, ok := unparen(call.Fun).(*ast.SelectorExpr); ok && !spec.defaultMux {
		recv = sel.X
	}
	if spec.kind != regDerive {
		w.f.regs = append(w.f.regs, registration{spec: spec, call: call, pkg: w.p, owner: w.owner, recv: recv})
		return
	}
	if spec.fnArg < 0 || spec.fnArg >= len(call.Args) {
		return
	}
	param := w.callbackParam(call.Args[spec.fnArg])
	if param == nil {
		return
	}
	w.addFlow(param, call)
}

// callbackParam은 콜백 인자(함수 리터럴이나 모듈 함수·메서드 값)의 첫 파라미터 객체다.
func (w *flowWalker) callbackParam(arg ast.Expr) types.Object {
	info := w.info()
	if lit, ok := unparen(arg).(*ast.FuncLit); ok {
		params := lit.Type.Params
		if params == nil || len(params.List) == 0 || len(params.List[0].Names) == 0 {
			return nil
		}
		return info.Defs[params.List[0].Names[0]]
	}
	fn := referencedFunc(info, arg)
	if fn == nil || fn.Signature().Params().Len() == 0 {
		return nil
	}
	return fn.Signature().Params().At(0)
}

// bindArguments는 모듈 함수 호출의 인자(와 리시버)를 파라미터 칸에 흘린다.
func (w *flowWalker) bindArguments(call *ast.CallExpr, fn *types.Func) {
	sig := fn.Signature()
	if sel, ok := unparen(call.Fun).(*ast.SelectorExpr); ok && sig.Recv() != nil {
		if s := w.info().Selections[sel]; s != nil && s.Kind() == types.MethodVal && isRouterType(sig.Recv().Type()) {
			w.addFlow(sig.Recv(), sel.X)
		}
	}
	params := sig.Params()
	if call.Ellipsis.IsValid() {
		return
	}
	for i, arg := range call.Args {
		at := i
		if sig.Variadic() && at >= params.Len()-1 {
			at = params.Len() - 1
		}
		if at >= params.Len() {
			break
		}
		param := params.At(at)
		t := param.Type()
		if sig.Variadic() && at == params.Len()-1 {
			if slice, ok := t.(*types.Slice); ok {
				t = slice.Elem()
			}
		}
		if isRouterType(t) {
			w.addFlow(param, arg)
		}
	}
}

// observeGinSetting은 gin Engine의 RedirectTrailingSlash 대입을 모은다.
func (w *flowWalker) observeGinSetting(lhs, rhs ast.Expr) {
	sel, ok := unparen(lhs).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "RedirectTrailingSlash" {
		return
	}
	s := w.info().Selections[sel]
	if s == nil || s.Kind() != types.FieldVal || typeKey(s.Recv()) != ginPath+".Engine" {
		return
	}
	w.f.ginAssigns = append(w.f.ginAssigns, ginAssign{target: sel.X, value: rhs, pkg: w.p})
}

// observeMiddleware는 끝 슬래시를 바꾸는 미들웨어 참조를 기록한다.
func (w *flowWalker) observeMiddleware(id *ast.Ident) {
	fn, ok := w.info().Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return
	}
	if fw, ok := trailingSlashMiddleware[fn.Pkg().Path()+"."+fn.Name()]; ok {
		w.f.slashMiddleware[fw] = true
	}
}

// eval은 식이 가리킬 수 있는 라우터 노드 집합이다(지금 칸 값 기준). 괄호·역참조·타입 단언·변환처럼
// 값을 그대로 넘기는 포장은 반복으로 벗긴다(재귀는 심볼 순환 자기 분석에 걸린다).
func (f *routeFlow) eval(info *types.Info, p *packages.Package, expr ast.Expr) nodeSet {
	for {
		inner := passThrough(info, expr)
		if inner == nil {
			break
		}
		expr = inner
	}
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		if lit, ok := e.X.(*ast.CompositeLit); ok && e.Op == token.AND {
			return f.evalComposite(info, p, lit)
		}
	case *ast.CompositeLit:
		return f.evalComposite(info, p, e)
	case *ast.Ident:
		return f.evalObject(info.Uses[e])
	case *ast.SelectorExpr:
		if sel := info.Selections[e]; sel != nil {
			if sel.Kind() == types.FieldVal {
				return f.evalObject(sel.Obj())
			}
			return nil
		}
		return f.evalObject(info.Uses[e.Sel])
	case *ast.CallExpr:
		return f.evalCall(info, p, e)
	}
	return nil
}

// passThrough는 값을 그대로 넘기는 포장 식의 안쪽 식이다(아니면 nil).
func passThrough(info *types.Info, expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return e.X
	case *ast.StarExpr:
		return e.X
	case *ast.TypeAssertExpr:
		return e.X
	case *ast.UnaryExpr:
		if _, lit := e.X.(*ast.CompositeLit); !lit || e.Op != token.AND {
			return e.X
		}
	case *ast.CallExpr:
		if tv, ok := info.Types[e.Fun]; ok && tv.IsType() && len(e.Args) == 1 {
			return e.Args[0]
		}
	}
	return nil
}

// evalObject는 변수·필드 객체의 칸이다. http.DefaultServeMux는 기본 mux 노드다.
func (f *routeFlow) evalObject(obj types.Object) nodeSet {
	v, ok := obj.(*types.Var)
	if !ok {
		return nil
	}
	if v.Pkg() != nil && v.Pkg().Path() == "net/http" && v.Name() == "DefaultServeMux" {
		return nodeSet{f.defaultServeMux(): true}
	}
	return f.cells[v]
}

// evalComposite는 `http.ServeMux{}` 리터럴을 새 ServeMux 노드로 본다.
func (f *routeFlow) evalComposite(info *types.Info, p *packages.Package, lit *ast.CompositeLit) nodeSet {
	if typeKey(info.TypeOf(lit)) != "net/http.ServeMux" {
		return nil
	}
	return nodeSet{f.node(lit.Lbrace, fwServeMux, nodeRoot, 0, p, nil): true}
}

// evalCall은 호출 식의 노드다: 생성·하위 라우터 호출은 그 노드, 모듈 함수는 결과 칸.
func (f *routeFlow) evalCall(info *types.Info, p *packages.Package, call *ast.CallExpr) nodeSet {
	if isNewServeMux(info, call) {
		return nodeSet{f.node(call.Lparen, fwServeMux, nodeRoot, 0, p, call): true}
	}
	fn := calleeFunc(info, call)
	if fn == nil {
		return nil
	}
	key := funcKey(fn)
	if fw, ok := routerConstructors[key]; ok {
		return nodeSet{f.node(call.Lparen, fw, nodeRoot, 0, p, call): true}
	}
	if spec, ok := routeAPI[key]; ok && spec.kind == regDerive {
		return nodeSet{f.node(call.Lparen, spec.fw, nodeDerived, spec.derive, p, call): true}
	}
	if f.decls[fn] && fn.Signature().Results().Len() >= 1 && isRouterType(fn.Signature().Results().At(0).Type()) {
		return f.result(fn, 0)
	}
	return nil
}

// isNewServeMux는 `new(http.ServeMux)`인지 본다.
func isNewServeMux(info *types.Info, call *ast.CallExpr) bool {
	id, ok := unparen(call.Fun).(*ast.Ident)
	if !ok || len(call.Args) != 1 {
		return false
	}
	if _, builtin := info.Uses[id].(*types.Builtin); !builtin || id.Name != "new" {
		return false
	}
	return typeKey(info.TypeOf(call.Args[0])) == "net/http.ServeMux"
}

// resolveParents는 고정점 뒤에 하위 라우터·마운트의 부모 간선을 푼다. 등록 수신 식을 먼저
// 평가해 칸에 담기지 않은 사슬 호출(`r.With(m).Get(…)`)의 노드도 만든다. 부모를 평가하다 새
// 노드가 생길 수 있어(사슬의 사슬) 늘어나는 목록을 끝까지 훑는다.
func (f *routeFlow) resolveParents() {
	for _, r := range f.regs {
		f.receiverNodes(r)
	}
	for i := 0; i < len(f.nodes); i++ {
		n := f.nodes[i]
		if n.kind != nodeDerived {
			continue
		}
		sel, ok := unparen(n.call.Fun).(*ast.SelectorExpr)
		if !ok {
			continue
		}
		prefix, dynamic := "", false
		if spec := routeAPI[funcKey(calleeFunc(n.pkg.TypesInfo, n.call))]; spec.pathArg >= 0 && spec.pathArg < len(n.call.Args) {
			prefix, dynamic = constantString(n.pkg.TypesInfo, n.call.Args[spec.pathArg])
			dynamic = !dynamic
		}
		if n.derive == deriveEchoHost && dynamic {
			// host는 경로가 아니라 요청을 좁히는 조건이다 — 값을 몰라도 접두사는 그대로다.
			prefix, dynamic = unknownHost, false
		}
		parents := nodeIDs(f.eval(n.pkg.TypesInfo, n.pkg, sel.X).sorted())
		if len(parents) == 0 {
			// 수신 라우터를 추적하지 못해도 이 하위 라우터의 접두사는 안다 — 부모 없는 간선으로 남겨
			// 사슬이 접두사를 합성한 뒤 base 앵커로 끝나게 한다.
			parents = []int{noParent}
		}
		for _, parent := range parents {
			n.parents = append(n.parents, parentEdge{parent: parent, kind: edgeDerive,
				derive: n.derive, prefix: prefix, dynamic: dynamic})
		}
	}
	for _, r := range f.regs {
		f.resolveMount(r)
	}
}

// resolveMount는 마운트 호출(chi Mount, ServeMux Handle + StripPrefix)의 하위 라우터에 부모 간선을 단다.
func (f *routeFlow) resolveMount(r registration) {
	info := r.pkg.TypesInfo
	handler := mountHandlerArg(r)
	if handler == nil {
		return
	}
	parents := f.receiverNodes(r)
	if strip, inner, ok := stripPrefixCall(info, handler); ok {
		prefix, isConst := constantString(info, strip)
		dynamic := !isConst || !stripMatchesPattern(r, prefix)
		for _, child := range f.eval(info, r.pkg, inner).sorted() {
			for _, parent := range parents {
				child.parents = append(child.parents, parentEdge{parent: parent.id, kind: edgeStrip,
					prefix: prefix, dynamic: dynamic})
			}
		}
		return
	}
	if r.spec.kind != regMount {
		return
	}
	prefix, isConst := constantString(info, r.call.Args[r.spec.pathArg])
	for _, child := range f.eval(info, r.pkg, handler).sorted() {
		if child.fw != fwChi {
			continue
		}
		for _, parent := range parents {
			child.parents = append(child.parents, parentEdge{parent: parent.id, kind: edgeChiMount,
				prefix: prefix, dynamic: !isConst})
		}
	}
}

// mountHandlerArg는 마운트 후보 호출의 핸들러 인자다(chi Mount·ServeMux Handle만).
func mountHandlerArg(r registration) ast.Expr {
	if r.spec.kind != regMount && !(r.spec.fw == fwServeMux && r.spec.kind == regRoute) {
		return nil
	}
	if r.spec.handlerArg < 0 || r.spec.handlerArg >= len(r.call.Args) {
		return nil
	}
	return r.call.Args[r.spec.handlerArg]
}

// stripMatchesPattern은 ServeMux Handle 패턴이 StripPrefix 접두사의 하위 트리(`S/`, method·host 없음)인지 본다.
// chi Mount는 접두사를 RoutePath에만 반영하므로 StripPrefix(S)의 S가 마운트 접두사와 같아야 한다.
func stripMatchesPattern(r registration, strip string) bool {
	if strip == "" || strip[len(strip)-1] == '/' || r.spec.pathArg >= len(r.call.Args) {
		return false
	}
	pattern, ok := constantString(r.pkg.TypesInfo, r.call.Args[r.spec.pathArg])
	if !ok {
		return false
	}
	if r.spec.fw == fwChi {
		return pattern == strip
	}
	return pattern == strip+"/"
}

// stripPrefixCall은 `http.StripPrefix(S, h)`를 알아본다.
func stripPrefixCall(info *types.Info, expr ast.Expr) (ast.Expr, ast.Expr, bool) {
	call, ok := unparen(expr).(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return nil, nil, false
	}
	fn := calleeFunc(info, call)
	if fn == nil || funcKey(fn) != "net/http..StripPrefix" {
		return nil, nil, false
	}
	return call.Args[0], call.Args[1], true
}

// receiverNodes는 등록 호출의 수신 라우터 노드다(http.Handle 같은 패키지 함수는 기본 mux).
func (f *routeFlow) receiverNodes(r registration) []*routerNode {
	if r.spec.defaultMux {
		return []*routerNode{f.defaultServeMux()}
	}
	if r.recv == nil {
		return nil
	}
	return f.eval(r.pkg.TypesInfo, r.pkg, r.recv).sorted()
}

// resolveGinSettings는 RedirectTrailingSlash 대입을 대상 엔진 노드에 붙인다.
func (f *routeFlow) resolveGinSettings() {
	for _, a := range f.ginAssigns {
		for _, n := range f.eval(a.pkg.TypesInfo, a.pkg, a.target).sorted() {
			f.ginRedirect[n] = append(f.ginRedirect[n], a)
		}
	}
}

// ginTrailingSlash는 gin 루트 엔진의 끝 슬래시 정책이다: 대입이 없으면 기본(RedirectTrailingSlash
// true — 301/307 리다이렉트로 다른 쪽 경로도 핸들러에 닿는다)이라 optional, 상수 false면 strict,
// 그 밖(상수가 아닌 값, 서로 다른 값)은 모름.
func (f *routeFlow) ginTrailingSlash(root *routerNode) string {
	values := f.ginRedirect[root]
	if len(values) == 0 {
		return "optional"
	}
	policy := ""
	for _, a := range values {
		tv, ok := a.pkg.TypesInfo.Types[a.value]
		if !ok || tv.Value == nil || tv.Value.Kind() != constant.Bool {
			return ""
		}
		next := "optional"
		if !constant.BoolVal(tv.Value) {
			next = "strict"
		}
		if policy != "" && policy != next {
			return ""
		}
		policy = next
	}
	return policy
}

// calleeFunc는 호출의 피호출 함수(제네릭 인스턴스는 원형)다.
func calleeFunc(info *types.Info, call *ast.CallExpr) *types.Func {
	fn, ok := typeutil.Callee(info, call).(*types.Func)
	if !ok {
		return nil
	}
	return fn.Origin()
}

// referencedFunc는 함수·메서드를 가리키는 식(이름, 패키지 한정 이름, 메서드 값·식, 제네릭 인스턴스)의 함수다.
func referencedFunc(info *types.Info, expr ast.Expr) *types.Func {
	for {
		switch e := unparen(expr).(type) {
		case *ast.IndexExpr:
			expr = e.X
			continue
		case *ast.IndexListExpr:
			expr = e.X
			continue
		case *ast.Ident:
			fn, _ := info.Uses[e].(*types.Func)
			return originOf(fn)
		case *ast.SelectorExpr:
			return selectedFunc(info, e)
		}
		return nil
	}
}

// selectedFunc는 셀렉터 식이 가리키는 함수·메서드다(필드 선택은 nil).
func selectedFunc(info *types.Info, e *ast.SelectorExpr) *types.Func {
	if sel := info.Selections[e]; sel != nil {
		if sel.Kind() == types.MethodVal || sel.Kind() == types.MethodExpr {
			fn, _ := sel.Obj().(*types.Func)
			return originOf(fn)
		}
		return nil
	}
	fn, _ := info.Uses[e.Sel].(*types.Func)
	return originOf(fn)
}

// originOf는 제네릭 인스턴스의 원형 함수다(nil은 그대로).
func originOf(fn *types.Func) *types.Func {
	if fn == nil {
		return nil
	}
	return fn.Origin()
}

// unparen은 괄호를 벗긴다.
func unparen(expr ast.Expr) ast.Expr {
	for {
		p, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = p.X
	}
}

// constantString은 문자열 상수 식의 값이다(상수 이름·상수 연결 포함).
func constantString(info *types.Info, expr ast.Expr) (string, bool) {
	tv, ok := info.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}
