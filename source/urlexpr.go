// route-call URL 식 평가 — Go 식을 URL 조각(urlPart)으로 푼다.
//
// 푸는 것: 문자열 상수(파일 밖·패키지 밖 상수 포함 — 컴파일러가 접은 값), `+` 연결, fmt.Sprintf
// (상수 형식의 %s·%v·%d), 한 번만 대입된 지역 변수와 비공개 패키지 변수(모든 대입이 `?`로 시작하거나
// 빈 값이면 query 꼬리), url.URL 리터럴, url.Parse·ParseRequestURI, (*URL).String·JoinPath·
// ResolveReference·Parse, url.JoinPath, path.Join, strings.TrimSuffix·TrimRight(`/`).
// 그 밖(필드, 파라미터, 함수 결과, 여러 번 대입된 변수)은 값 조각이다. 필드와 패키지 변수는 base
// 식일 때 baseRef가 되도록 정점 ID를 싣는다.
//
// 평가는 재귀 대신 명시적 작업 목록으로 한다: 식마다 노드를 만들고(부모가 자식보다 앞 번호), 뒤 번호부터
// 결과를 계산한다. 심볼 순환 자기 분석(cycles --level symbol --strict)에 걸리지 않게 하기 위해서다.
package source

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// maxEvalNodes는 식 하나를 풀 때 만들 노드 수 상한이다 — 넘으면 나머지는 값 조각이다.
const maxEvalNodes = 512

// maxObjectExpansions는 한 평가에서 같은 변수를 펼치는 횟수 상한이다.
const maxObjectExpansions = 4

// assignedValue는 변수에 들어간 값 하나다. expr이 nil이면 선언의 영값("")이다.
type assignedValue struct {
	expr ast.Expr
	// index는 다중 결과 호출(`u, err := url.Parse(s)`)의 결과 번호다(단일 값이면 -1).
	index int
	pkg   *packages.Package
}

// valueIndex는 모듈 전체의 변수 대입 색인이다. 흐름에 둔감하다 — 어느 대입이 먼저인지 보지 않고,
// 대입이 하나뿐일 때만 그 값을 믿는다.
type valueIndex struct {
	assigns map[types.Object][]assignedValue
	// unknown은 값을 추적하지 않는 쓰기(주소 꺼냄, ++, +=, range, url.URL 필드 쓰기, 이름 있는 결과)가 있는 변수다.
	unknown map[types.Object]bool
	params  map[types.Object]bool
	// fields는 모듈 struct 필드 → 정점 ID다(baseRef).
	fields map[*types.Var]string
	ids    symbolIDs
}

// newValueIndex는 패키지들의 대입을 색인한다.
func newValueIndex(pkgs []*packages.Package, ids symbolIDs) *valueIndex {
	idx := &valueIndex{assigns: map[types.Object][]assignedValue{}, unknown: map[types.Object]bool{},
		params: map[types.Object]bool{}, fields: map[*types.Var]string{}, ids: ids}
	for _, p := range pkgs {
		if p.TypesInfo == nil {
			continue
		}
		idx.indexFields(p)
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				idx.observe(p, n)
				return true
			})
		}
	}
	return idx
}

// indexFields는 패키지의 명명 struct 필드에 정점 ID를 단다(symbols.go fieldID와 같은 형식).
func (idx *valueIndex) indexFields(p *packages.Package) {
	scope := p.Types.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		st, ok := tn.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := 0; i < st.NumFields(); i++ {
			idx.fields[st.Field(i)] = fieldID(tn, st.Field(i))
		}
	}
}

// observe는 노드 하나의 대입·쓰기·파라미터를 기록한다.
func (idx *valueIndex) observe(p *packages.Package, n ast.Node) {
	info := p.TypesInfo
	switch node := n.(type) {
	case *ast.AssignStmt:
		idx.observeAssign(p, node)
	case *ast.ValueSpec:
		idx.observeValueSpec(p, node)
	case *ast.IncDecStmt:
		idx.markUnknown(info, node.X)
	case *ast.UnaryExpr:
		if node.Op == token.AND {
			idx.markUnknown(info, node.X)
		}
	case *ast.RangeStmt:
		idx.markUnknown(info, node.Key)
		idx.markUnknown(info, node.Value)
	case *ast.FuncType:
		idx.markParams(info, node.Params, true)
		idx.markParams(info, node.Results, false)
	case *ast.FuncDecl:
		idx.markParams(info, node.Recv, true)
	}
}

// observeAssign은 대입문을 기록한다. `+=` 같은 복합 대입은 값을 모르는 쓰기다.
func (idx *valueIndex) observeAssign(p *packages.Package, s *ast.AssignStmt) {
	info := p.TypesInfo
	if s.Tok != token.ASSIGN && s.Tok != token.DEFINE {
		for _, lhs := range s.Lhs {
			idx.markUnknown(info, lhs)
		}
		return
	}
	if len(s.Lhs) == len(s.Rhs) {
		for i, lhs := range s.Lhs {
			idx.record(p, lhs, s.Rhs[i], -1)
		}
		return
	}
	if len(s.Rhs) == 1 {
		for i, lhs := range s.Lhs {
			idx.record(p, lhs, s.Rhs[0], i)
		}
	}
}

// observeValueSpec은 var·const 선언을 기록한다. 값 없는 var는 영값("")이다.
func (idx *valueIndex) observeValueSpec(p *packages.Package, s *ast.ValueSpec) {
	switch {
	case len(s.Values) == 0:
		for _, name := range s.Names {
			idx.record(p, name, nil, -1)
		}
	case len(s.Values) == len(s.Names):
		for i, name := range s.Names {
			idx.record(p, name, s.Values[i], -1)
		}
	case len(s.Values) == 1:
		for i, name := range s.Names {
			idx.record(p, name, s.Values[0], i)
		}
	}
}

// record는 lhs ← rhs를 기록한다. 필드 쓰기는 url.URL 변수의 필드면 그 변수를 모르는 쓰기로 본다.
func (idx *valueIndex) record(p *packages.Package, lhs, rhs ast.Expr, index int) {
	info := p.TypesInfo
	switch e := unparen(lhs).(type) {
	case *ast.Ident:
		obj := info.Defs[e]
		if obj == nil {
			obj = info.Uses[e]
		}
		if v, ok := obj.(*types.Var); ok {
			idx.assigns[v] = append(idx.assigns[v], assignedValue{expr: rhs, index: index, pkg: p})
		}
	case *ast.SelectorExpr:
		idx.markURLOwner(info, e)
	case *ast.StarExpr:
		idx.markUnknown(info, e.X)
	}
}

// markURLOwner는 `u.Path = …`처럼 url.URL 값의 필드를 쓰면 그 변수를 모르는 쓰기로 본다.
func (idx *valueIndex) markURLOwner(info *types.Info, sel *ast.SelectorExpr) {
	if typeKey(info.TypeOf(sel.X)) == "net/url.URL" {
		idx.markUnknown(info, sel.X)
	}
}

// markUnknown은 식이 가리키는 변수(선택식이면 그 뿌리 변수)를 모르는 쓰기로 본다.
func (idx *valueIndex) markUnknown(info *types.Info, expr ast.Expr) {
	for expr != nil {
		switch e := unparen(expr).(type) {
		case *ast.Ident:
			if obj := info.Defs[e]; obj != nil {
				idx.unknown[obj] = true
			}
			if obj := info.Uses[e]; obj != nil {
				idx.unknown[obj] = true
			}
			return
		case *ast.SelectorExpr:
			if s := info.Selections[e]; s != nil && s.Kind() == types.FieldVal {
				expr = e.X
				continue
			}
			if obj := info.Uses[e.Sel]; obj != nil {
				idx.unknown[obj] = true
			}
			return
		case *ast.IndexExpr:
			expr = e.X
		case *ast.StarExpr:
			expr = e.X
		default:
			return
		}
	}
}

// markParams는 파라미터(와 리시버)를 기록하고, 이름 있는 결과는 모르는 쓰기로 본다.
func (idx *valueIndex) markParams(info *types.Info, fields *ast.FieldList, param bool) {
	if fields == nil {
		return
	}
	for _, f := range fields.List {
		for _, name := range f.Names {
			obj := info.Defs[name]
			if obj == nil {
				continue
			}
			if param {
				idx.params[obj] = true
			} else {
				idx.unknown[obj] = true
			}
		}
	}
}

// evalOp은 평가 노드의 연산이다.
type evalOp int

const (
	opLeaf      evalOp = iota // parts가 결과다
	opConcat                  // 자식 결과를 잇는다
	opJoinPath                // kids[0] base + kids[1:] 원소 — url.JoinPath
	opResolve                 // kids[0] base, kids[1] ref — ResolveReference
	opPathJoin                // path.Join
	opTrimSlash               // 끝 `/` 떼기(trimAll이면 모두)
	opURL                     // url.URL 리터럴: kids[0] host, kids[1] path(없으면 -1)
	opQueryTail               // 모든 자식이 `?`로 시작하거나 비면 query 꼬리, 아니면 fallback
)

// evalMode는 원문 조각을 만드는 자리다.
type evalMode int

const (
	modeString evalMode = iota // URL 문자열 — 원문 그대로
	modePath                   // url.URL.Path — 디코드된 경로라 세그먼트마다 인코딩한다
)

// evalNode는 평가 노드 하나다.
type evalNode struct {
	op       evalOp
	parts    []urlPart
	kids     []int
	scheme   string
	trimAll  bool
	fallback urlPart
}

// evalTask는 아직 펼치지 않은 식이다.
type evalTask struct {
	node int
	expr ast.Expr
	mode evalMode
	pkg  *packages.Package
	// index는 다중 결과 호출의 결과 번호다(-1이면 단일 값).
	index int
}

// urlEval은 식 하나의 평가 상태다.
type urlEval struct {
	idx      *valueIndex
	nodes    []evalNode
	queue    []evalTask
	expanded map[types.Object]int
}

// literalValue는 식이 문자열 하나로 풀리면 그 값이다(상수, 한 번 대입된 변수 등 evalURLParts 규칙).
// 동사 인자처럼 URL이 아닌 문자열에도 같은 추적을 쓴다.
func (idx *valueIndex) literalValue(p *packages.Package, expr ast.Expr) (string, bool) {
	if s, ok := constantString(p.TypesInfo, expr); ok {
		return s, true
	}
	if !isStringType(p.TypesInfo.TypeOf(expr)) {
		return "", false
	}
	parts := appendParts(nil, idx.evalURLParts(p, expr)...)
	switch {
	case len(parts) == 0:
		return "", true
	case len(parts) == 1 && parts[0].kind == partLiteral:
		return parts[0].text, true
	}
	return "", false
}

// evalURLParts는 식을 URL 조각으로 푼다.
func (idx *valueIndex) evalURLParts(p *packages.Package, expr ast.Expr) []urlPart {
	e := &urlEval{idx: idx, expanded: map[types.Object]int{}}
	e.push(p, expr, modeString, -1)
	for len(e.queue) > 0 {
		task := e.queue[0]
		e.queue = e.queue[1:]
		e.expand(task)
	}
	for i := len(e.nodes) - 1; i >= 0; i-- {
		e.compute(i)
	}
	return e.nodes[0].parts
}

// push는 식 하나를 새 노드로 예약한다. 상한을 넘으면 값 조각 노드가 된다.
func (e *urlEval) push(p *packages.Package, expr ast.Expr, mode evalMode, index int) int {
	id := len(e.nodes)
	e.nodes = append(e.nodes, evalNode{op: opLeaf})
	if id >= maxEvalNodes {
		e.nodes[id].parts = []urlPart{valuePart()}
		return id
	}
	e.queue = append(e.queue, evalTask{node: id, expr: expr, mode: mode, pkg: p, index: index})
	return id
}

// leaf는 노드를 결과가 정해진 잎으로 만든다.
func (e *urlEval) leaf(node int, parts ...urlPart) {
	e.nodes[node].op = opLeaf
	e.nodes[node].parts = parts
}

// literal은 자리에 맞게 인코딩한 원문 잎이다.
func (e *urlEval) literal(node int, text string, mode evalMode) {
	if mode == modePath {
		text = encodeDecodedPath(text)
	}
	e.leaf(node, literalPart(text))
}

// encodeDecodedPath는 디코드된 경로(url.URL.Path)를 EscapedPath처럼 세그먼트마다 인코딩한다.
func encodeDecodedPath(path string) string {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		segments[i] = encodeSegmentValue(s)
	}
	return strings.Join(segments, "/")
}

// expand는 식 하나를 노드 연산과 자식 식으로 펼친다.
func (e *urlEval) expand(t evalTask) {
	info := t.pkg.TypesInfo
	if t.expr == nil {
		e.literal(t.node, "", t.mode)
		return
	}
	if t.index < 0 {
		if text, ok := constantText(info, t.expr); ok {
			e.literal(t.node, text, t.mode)
			return
		}
	}
	switch x := unparen(t.expr).(type) {
	case *ast.BinaryExpr:
		if x.Op == token.ADD && t.index < 0 {
			e.children(t, opConcat, x.X, x.Y)
			return
		}
	case *ast.Ident:
		e.expandObject(t, info.Uses[x])
		return
	case *ast.SelectorExpr:
		e.expandSelector(t, x)
		return
	case *ast.CallExpr:
		e.expandCall(t, x)
		return
	case *ast.UnaryExpr:
		if x.Op == token.AND && t.index < 0 {
			e.children(t, opConcat, x.X)
			return
		}
	case *ast.StarExpr:
		if t.index < 0 {
			e.children(t, opConcat, x.X)
			return
		}
	case *ast.CompositeLit:
		if t.index < 0 && typeKey(info.TypeOf(x)) == "net/url.URL" {
			e.expandURLLiteral(t, x)
			return
		}
	}
	e.leaf(t.node, valuePart())
}

// children은 노드를 연산으로 두고 식들을 자식으로 예약한다(자리는 물려준다).
func (e *urlEval) children(t evalTask, op evalOp, exprs ...ast.Expr) {
	kids := make([]int, len(exprs))
	for i, x := range exprs {
		kids[i] = e.push(t.pkg, x, t.mode, -1)
	}
	e.nodes[t.node].op = op
	e.nodes[t.node].kids = kids
}

// expandObject는 이름이 가리키는 변수를 펼친다.
func (e *urlEval) expandObject(t evalTask, obj types.Object) {
	v, ok := obj.(*types.Var)
	if !ok || t.index >= 0 {
		e.leaf(t.node, valuePart())
		return
	}
	value := urlPart{kind: partValue, param: e.idx.params[v], ref: e.idx.varRef(v)}
	if v.IsField() || e.idx.params[v] || e.idx.unknown[v] || exportedPackageVar(v) {
		e.leaf(t.node, value)
		return
	}
	values := e.idx.assigns[v]
	if len(values) == 0 || e.expanded[v] >= maxObjectExpansions {
		e.leaf(t.node, value)
		return
	}
	e.expanded[v]++
	if len(values) == 1 {
		e.nodes[t.node].op = opConcat
		e.nodes[t.node].kids = []int{e.push(values[0].pkg, values[0].expr, t.mode, values[0].index)}
		return
	}
	kids := make([]int, len(values))
	for i, a := range values {
		kids[i] = e.push(a.pkg, a.expr, t.mode, a.index)
	}
	e.nodes[t.node] = evalNode{op: opQueryTail, kids: kids, fallback: value}
}

// exportedPackageVar는 main이 아닌 패키지의 공개 패키지 변수인지 본다 — 다른 모듈의 import가
// 바꿀 수 있어 저장소 안에서 대입이 하나뿐이어도 값을 믿지 않는다.
func exportedPackageVar(v *types.Var) bool {
	if v.Pkg() == nil || v.Parent() != v.Pkg().Scope() {
		return false
	}
	return v.Exported() && v.Pkg().Name() != "main"
}

// varRef는 패키지 변수·필드의 정점 ID다(지역 변수는 빈 문자열).
func (idx *valueIndex) varRef(v *types.Var) string {
	if v.IsField() {
		if id, ok := idx.fields[v]; ok {
			return id
		}
		return idx.fields[v.Origin()]
	}
	if v.Pkg() != nil && v.Parent() == v.Pkg().Scope() {
		return idx.ids.objectVertexID(v)
	}
	return ""
}

// expandSelector는 패키지 한정 이름과 필드 선택을 펼친다.
func (e *urlEval) expandSelector(t evalTask, sel *ast.SelectorExpr) {
	info := t.pkg.TypesInfo
	if s := info.Selections[sel]; s != nil {
		if v, ok := s.Obj().(*types.Var); ok && s.Kind() == types.FieldVal {
			e.leaf(t.node, urlPart{kind: partValue, ref: e.idx.varRef(v)})
			return
		}
		e.leaf(t.node, valuePart())
		return
	}
	e.expandObject(t, info.Uses[sel.Sel])
}

// expandCall은 호출 식을 펼친다(변환·fmt·net/url·path·strings).
func (e *urlEval) expandCall(t evalTask, call *ast.CallExpr) {
	info := t.pkg.TypesInfo
	if tv, ok := info.Types[call.Fun]; ok && tv.IsType() && len(call.Args) == 1 && t.index < 0 {
		e.children(t, opConcat, call.Args[0])
		return
	}
	fn := calleeFunc(info, call)
	if fn == nil || call.Ellipsis.IsValid() {
		e.leaf(t.node, valuePart())
		return
	}
	key := funcKey(fn)
	if !tupleResultAllowed(key, t.index) {
		e.leaf(t.node, valuePart())
		return
	}
	recv := receiverExpr(call)
	switch key {
	case "fmt..Sprintf":
		e.expandSprintf(t, call)
	case "net/url..Parse", "net/url..ParseRequestURI":
		e.children(t, opConcat, call.Args[0])
	case "net/url.URL.String":
		e.children(t, opConcat, recv)
	case "net/url..JoinPath":
		e.children(t, opJoinPath, call.Args...)
	case "net/url.URL.JoinPath":
		e.children(t, opJoinPath, append([]ast.Expr{recv}, call.Args...)...)
	case "net/url.URL.ResolveReference", "net/url.URL.Parse":
		e.children(t, opResolve, recv, call.Args[0])
	case "path..Join":
		e.children(t, opPathJoin, call.Args...)
	case "strings..TrimSuffix", "strings..TrimRight":
		if cut, ok := constantString(info, call.Args[1]); ok && cut == "/" {
			e.children(t, opTrimSlash, call.Args[0])
			e.nodes[t.node].trimAll = key == "strings..TrimRight"
			return
		}
		e.leaf(t.node, valuePart())
	default:
		e.leaf(t.node, valuePart())
	}
}

// tupleResultAllowed는 다중 결과 호출의 결과 번호가 URL 값인지 본다 — url.Parse·JoinPath의 첫
// 결과만 URL이고 나머지는 error다. 단일 값(-1)은 항상 된다.
func tupleResultAllowed(key string, index int) bool {
	if index < 0 {
		return true
	}
	switch key {
	case "net/url..Parse", "net/url..ParseRequestURI", "net/url..JoinPath", "net/url.URL.Parse":
		return index == 0
	}
	return false
}

// receiverExpr는 메서드 호출의 리시버 식이다(아니면 nil).
func receiverExpr(call *ast.CallExpr) ast.Expr {
	if sel, ok := unparen(call.Fun).(*ast.SelectorExpr); ok {
		return sel.X
	}
	return nil
}

// expandURLLiteral은 url.URL{Scheme, Host, Path} 리터럴을 펼친다. Opaque·RawPath는 풀지 않는다.
func (e *urlEval) expandURLLiteral(t evalTask, lit *ast.CompositeLit) {
	info := t.pkg.TypesInfo
	fields := map[string]ast.Expr{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			e.leaf(t.node, valuePart())
			return
		}
		if key, ok := kv.Key.(*ast.Ident); ok {
			fields[key.Name] = kv.Value
		}
	}
	if fields["Opaque"] != nil || fields["RawPath"] != nil {
		e.leaf(t.node, valuePart())
		return
	}
	node := evalNode{op: opURL, kids: []int{-1, -1}}
	if s, ok := fields["Scheme"]; ok {
		scheme, isConst := constantString(info, s)
		node.scheme = scheme
		if !isConst {
			node.scheme = "?"
		}
	}
	if h, ok := fields["Host"]; ok {
		node.kids[0] = e.push(t.pkg, h, modeString, -1)
	}
	if p, ok := fields["Path"]; ok {
		node.kids[1] = e.push(t.pkg, p, modePath, -1)
	}
	e.nodes[t.node] = node
}

// expandSprintf는 상수 형식의 fmt.Sprintf를 조각으로 푼다. %s·%v는 인자를 펼치고, 그 밖의 동사와
// 폭·정밀도가 붙은 동사는 값이다. 인자 번호 지정(%[1]s)·`*`는 풀지 않는다.
func (e *urlEval) expandSprintf(t evalTask, call *ast.CallExpr) {
	info := t.pkg.TypesInfo
	format, ok := constantString(info, call.Args[0])
	if !ok {
		e.leaf(t.node, valuePart())
		return
	}
	pieces, ok := parseFormat(format)
	if !ok || countVerbs(pieces) != len(call.Args)-1 {
		e.leaf(t.node, valuePart())
		return
	}
	var kids []int
	arg := 1
	for _, piece := range pieces {
		id := len(e.nodes)
		e.nodes = append(e.nodes, evalNode{op: opLeaf})
		kids = append(kids, id)
		if !piece.verb {
			e.literal(id, piece.text, t.mode)
			continue
		}
		e.formatArg(t, id, piece, call.Args[arg])
		arg++
	}
	e.nodes[t.node].op = opConcat
	e.nodes[t.node].kids = kids
}

// formatArg는 형식 동사 하나의 인자를 노드로 만든다.
func (e *urlEval) formatArg(t evalTask, id int, piece formatPiece, arg ast.Expr) {
	info := t.pkg.TypesInfo
	if text, ok := constantText(info, arg); ok && piece.plain && verbPrintsConstant(piece.letter, info.Types[arg]) {
		e.literal(id, text, t.mode)
		return
	}
	if piece.plain && (piece.letter == 's' || piece.letter == 'v') && isStringType(info.TypeOf(arg)) {
		e.queue = append(e.queue, evalTask{node: id, expr: arg, mode: t.mode, pkg: t.pkg, index: -1})
		return
	}
	e.leaf(id, urlPart{kind: partValue, param: e.isParamExpr(info, arg)})
}

// verbPrintsConstant는 동사가 상수를 constantText 표기 그대로 찍는지 본다: 문자열은 %s·%v, 정수는 %d·%v,
// 불리언은 %v(%d면 fmt가 "%!d(bool=…)"를 찍는다). 그 밖의 짝은 값으로 둔다.
func verbPrintsConstant(verb rune, tv types.TypeAndValue) bool {
	switch {
	case verb == 'v':
		return true
	case tv.Value.Kind() == constant.String:
		return verb == 's'
	case tv.Value.Kind() == constant.Int:
		return verb == 'd'
	}
	return false
}

// isParamExpr는 식이 감싸는 함수의 파라미터 이름인지 본다.
func (e *urlEval) isParamExpr(info *types.Info, expr ast.Expr) bool {
	id, ok := unparen(expr).(*ast.Ident)
	return ok && e.idx.params[info.Uses[id]]
}

// isStringType은 밑 타입이 string인지 본다.
func isStringType(t types.Type) bool {
	if t == nil {
		return false
	}
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

// constantText는 상수 식의 문자열 표기다(문자열·정수·불리언). 형식 %d·%v·%s가 같은 표기를 쓴다.
func constantText(info *types.Info, expr ast.Expr) (string, bool) {
	tv, ok := info.Types[expr]
	if !ok || tv.Value == nil {
		return "", false
	}
	switch tv.Value.Kind() {
	case constant.String:
		return constant.StringVal(tv.Value), true
	case constant.Int:
		if isStringType(tv.Type) {
			return "", false
		}
		return tv.Value.ExactString(), true
	case constant.Bool:
		return tv.Value.ExactString(), true
	}
	return "", false
}

// formatPiece는 형식 문자열 조각 하나다(원문이나 동사).
type formatPiece struct {
	text   string
	verb   bool
	letter rune
	// plain은 플래그·폭·정밀도가 없는 동사다.
	plain bool
}

// parseFormat은 fmt 형식을 조각으로 나눈다. 인자 번호 지정·`*`는 풀지 않는다(false).
func parseFormat(format string) ([]formatPiece, bool) {
	var out []formatPiece
	var text strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			text.WriteByte(c)
			continue
		}
		if i+1 < len(format) && format[i+1] == '%' {
			text.WriteByte('%')
			i++
			continue
		}
		j := i + 1
		for j < len(format) && strings.IndexByte("+-# 0123456789.", format[j]) >= 0 {
			j++
		}
		if j >= len(format) || format[j] == '[' || format[j] == '*' {
			return nil, false
		}
		if text.Len() > 0 {
			out = append(out, formatPiece{text: text.String()})
			text.Reset()
		}
		out = append(out, formatPiece{verb: true, letter: rune(format[j]), plain: j == i+1})
		i = j
	}
	if text.Len() > 0 {
		out = append(out, formatPiece{text: text.String()})
	}
	return out, true
}

// countVerbs는 동사 조각 수다.
func countVerbs(pieces []formatPiece) int {
	n := 0
	for _, p := range pieces {
		if p.verb {
			n++
		}
	}
	return n
}

// compute는 노드 하나의 결과를 자식 결과로 계산한다(자식은 이미 계산됐다).
func (e *urlEval) compute(i int) {
	n := &e.nodes[i]
	switch n.op {
	case opConcat:
		var parts []urlPart
		for _, k := range n.kids {
			parts = appendParts(parts, e.nodes[k].parts...)
		}
		n.parts = parts
	case opJoinPath:
		n.parts = joinPathParts(e.nodes[n.kids[0]].parts, e.kidParts(n.kids[1:]))
	case opResolve:
		n.parts = resolveReferenceParts(e.nodes[n.kids[0]].parts, e.nodes[n.kids[1]].parts)
	case opPathJoin:
		n.parts = pathJoinParts(e.kidParts(n.kids))
	case opTrimSlash:
		n.parts = trimTrailingSlash(e.nodes[n.kids[0]].parts, n.trimAll)
	case opURL:
		n.parts = e.urlLiteralParts(n)
	case opQueryTail:
		n.parts = e.queryTailParts(n)
	}
}

// kidParts는 자식들의 결과 목록이다.
func (e *urlEval) kidParts(kids []int) [][]urlPart {
	out := make([][]urlPart, len(kids))
	for i, k := range kids {
		out[i] = e.nodes[k].parts
	}
	return out
}

// trimTrailingSlash는 strings.TrimSuffix(x, "/")·TrimRight(x, "/")다. 끝이 값이면 그대로 둔다.
func trimTrailingSlash(parts []urlPart, all bool) []urlPart {
	parts = appendParts(nil, parts...)
	n := len(parts)
	if n == 0 || parts[n-1].kind != partLiteral {
		return parts
	}
	last := parts[n-1].text
	if all {
		last = strings.TrimRight(last, "/")
	} else {
		last = strings.TrimSuffix(last, "/")
	}
	out := append([]urlPart(nil), parts[:n-1]...)
	return appendParts(out, literalPart(last))
}

// urlLiteralParts는 url.URL 리터럴의 String() 조각이다. host가 상수가 아니면 origin 값이고(Host는
// 경로를 담을 수 없다 — String()이 `/`를 이스케이프한다), host가 있으면 `/` 없는 경로 앞에 `/`를 넣는다.
func (e *urlEval) urlLiteralParts(n *evalNode) []urlPart {
	var out []urlPart
	hasHost := n.kids[0] >= 0
	if hasHost {
		host := appendParts(nil, e.nodes[n.kids[0]].parts...)
		if n.scheme != "?" && n.scheme != "" && len(host) == 1 && host[0].kind == partLiteral {
			out = appendParts(out, literalPart(n.scheme+"://"+host[0].text))
		} else {
			out = append(out, urlPart{kind: partOrigin})
		}
	}
	if n.kids[1] < 0 {
		return out
	}
	path := appendParts(nil, e.nodes[n.kids[1]].parts...)
	if hasHost && len(path) > 0 && (path[0].kind != partLiteral || !strings.HasPrefix(path[0].text, "/")) {
		path = appendParts([]urlPart{literalPart("/")}, path...)
	}
	return appendParts(out, path...)
}

// queryTailParts는 여러 번 대입된 변수의 결과다: 모든 값이 비었거나 `?`로 시작하면 query 꼬리다.
func (e *urlEval) queryTailParts(n *evalNode) []urlPart {
	for _, k := range n.kids {
		parts := appendParts(nil, e.nodes[k].parts...)
		if len(parts) == 0 {
			continue
		}
		if parts[0].kind != partLiteral || !strings.HasPrefix(parts[0].text, "?") {
			return []urlPart{n.fallback}
		}
	}
	return []urlPart{{kind: partQueryTail, param: n.fallback.param, ref: n.fallback.ref}}
}
