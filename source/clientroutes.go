// isthmus http route-call 생산자 — Go 클라이언트가 보내는 요청을 (method, 정규 경로 템플릿) 사실로
// 낸다(`gartograph routes --role client`).
//
// 계약은 ../isthmus의 docs/GRAPH-EXCHANGE.md "HTTP 경계"와 docs/HTTP-WRAPPERS.md가 정본이다. 지원 범위:
//   - net/http: http.Get·Head·Post·PostForm, (*http.Client)의 같은 메서드, http.NewRequest(WithContext)
//     (요청을 만든 자리가 사실이다 — client.Do가 보내는 URL은 만든 요청의 URL이다)
//   - resty v2: Client.R()·NewRequest() 체인의 Get…Patch·Execute, SetBaseURL·SetHostURL·BaseURL 필드,
//     SetPathParam(s)·SetRawPathParam(s)의 `{name}` 치환
//   - http-wrappers v1로 선언한 Go 래퍼(httpwrappers.go)
//
// symbol.usr는 호출을 감싸는 선언의 정점 ID다 — schema relation-use와 같은 귀속(declParts)이고,
// `impact --format language-traversal --roots-from`이 그대로 root로 쓴다.
package source

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ictechgy/gartograph/graph"
	"golang.org/x/tools/go/packages"
)

// ClientRouteDocument는 isthmus bridge-facts v1의 http 문서(클라이언트 역할)다.
type ClientRouteDocument struct {
	Format           string            `json:"format"`
	Version          int               `json:"version"`
	Tool             BridgeFactsTool   `json:"tool"`
	GeneratedAt      string            `json:"generatedAt"`
	SourceModifiedAt string            `json:"sourceModifiedAt,omitempty"`
	Platform         string            `json:"platform"`
	Target           string            `json:"target"`
	Project          string            `json:"project"`
	Service          string            `json:"service,omitempty"`
	Roles            []string          `json:"roles"`
	SourceSets       map[string]string `json:"sourceSets"`
	Facts            []RouteCallFact   `json:"facts"`
	// 계약상 항상 배열이다.
	Limitations []string `json:"limitations"`
}

// RouteCallFact는 route-call 사실 하나다. dynamic이면 channel은 null이다 — 원문 식은 userinfo·query를
// 담을 수 있어 싣지 않고, 증명한 리터럴 접두사만 마스킹한 channelPrefix로 싣는다.
type RouteCallFact struct {
	Kind              string          `json:"kind"`
	Method            string          `json:"method,omitempty"`
	MethodDynamic     bool            `json:"methodDynamic,omitempty"`
	Channel           *string         `json:"channel"`
	Dynamic           bool            `json:"dynamic"`
	PathAnchor        string          `json:"pathAnchor"`
	Authority         string          `json:"authority,omitempty"`
	BaseRef           string          `json:"baseRef,omitempty"`
	Service           string          `json:"service,omitempty"`
	QueryTailStripped bool            `json:"queryTailStripped,omitempty"`
	ChannelPrefix     string          `json:"channelPrefix,omitempty"`
	MaskedSegments    int             `json:"maskedSegments,omitempty"`
	Location          *BridgeLocation `json:"location"`
	// Symbol은 호출을 감싸는 선언이다 — usr는 reach·impact와 같은 심볼 정점 ID다.
	Symbol *FactSymbol `json:"symbol,omitempty"`
}

// ClientRouteOptions는 routes --role client의 입력이다.
type ClientRouteOptions struct {
	Harvest     Options
	Service     string
	GeneratedAt time.Time
	// Wrappers는 http-wrappers v1 선언이다(없으면 nil).
	Wrappers *WrapperFile
}

// ClientRouteFacts는 opts.Harvest.Dir 아래 Go 클라이언트의 요청을 http route-call 문서로 낸다.
func ClientRouteFacts(opts ClientRouteOptions, toolVersion string) (*ClientRouteDocument, error) {
	root, err := realPath(opts.Harvest.Dir)
	if err != nil {
		return nil, err
	}
	// usr 확인 그래프는 impact 기본 수확과 같아야 한다(RouteFacts와 같은 이유).
	loadOpts := Options{Dir: root, Level: graph.LevelSymbol, Exclude: opts.Harvest.Exclude, Tags: opts.Harvest.Tags}
	pkgs, err := load(loadOpts)
	if err != nil {
		return nil, err
	}
	symbolDoc, err := documentFrom(pkgs, loadOpts)
	if err != nil {
		return nil, err
	}
	roots, err := patternRoots(pkgs, root, opts.Harvest)
	if err != nil {
		return nil, err
	}
	wrappers := goWrappers(opts.Wrappers)
	if err := checkWrapperServices(wrappers, opts.Service); err != nil {
		return nil, err
	}
	scan := newClientScan(root, newSymbolIDs(symbolDoc), wrappers)
	main := scan.mainPackages(roots)
	resolveDeclarations(wrappers, pkgs, scan.ids)
	scan.harvest(main)
	doc := scan.document(opts, toolVersion)
	if err := selfCheckClient(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// checkWrapperServices는 선언의 service가 문서 service와 다르지 않은지 본다 — 계약상 사실의 유효
// service와 문서 service가 다르면 입력 오류다.
func checkWrapperServices(wrappers []*goWrapper, service string) error {
	for _, w := range wrappers {
		if service != "" && w.decl.Service != "" && w.decl.Service != service {
			return fmt.Errorf("wrappers[%d] declares service %q but --service is %q; drop one of them (a fact's service must match the document service)",
				w.index, w.decl.Service, service)
		}
	}
	return nil
}

// selfCheckClient는 문서가 isthmus 입력 검증을 통과할 모양인지 내기 전에 확인한다.
func selfCheckClient(doc *ClientRouteDocument) error {
	for i, f := range doc.Facts {
		if problem := routeCallProblem(f); problem != "" {
			return fmt.Errorf("internal error: route-call fact %d fails the isthmus contract self-check (%s); please report it with the call site", i, problem)
		}
	}
	return nil
}

// routeCallProblem은 route-call 사실 하나의 거부 사유다(isthmus parse.ts의 route-call 규칙).
func routeCallProblem(f RouteCallFact) string {
	switch {
	case f.MethodDynamic == (f.Method != ""):
		return "a route call needs exactly one of method or methodDynamic"
	case f.Method != "" && !routeMethods[f.Method]:
		return "invalid route method"
	case f.PathAnchor != "root" && f.PathAnchor != "base":
		return "invalid pathAnchor"
	case f.Location == nil:
		return "missing location"
	case f.Authority != "" && !authorityPattern.MatchString(f.Authority):
		return "invalid authority"
	}
	if f.Dynamic {
		if f.Channel != nil {
			return "dynamic route calls carry a null channel"
		}
		if f.ChannelPrefix != "" && (templateProblem(f.ChannelPrefix) != "" || strings.Contains(f.ChannelPrefix, "{**}")) {
			return "channelPrefix is not a canonical template"
		}
		return ""
	}
	if f.Channel == nil || templateProblem(*f.Channel) != "" || strings.Contains(*f.Channel, "{**}") {
		return "route channel is not a canonical call template"
	}
	if f.ChannelPrefix != "" {
		return "static route calls carry no channelPrefix"
	}
	if f.MaskedSegments > strings.Count(*f.Channel, "{}") {
		return "maskedSegments exceeds the template's {} segments"
	}
	return ""
}

// clientScan은 route-call 수확의 중간 상태다.
type clientScan struct {
	root     string
	ids      symbolIDs
	idx      *valueIndex
	flow     *routeFlow
	wrappers []*goWrapper
	// resty는 resty 클라이언트 노드별 base URL·path param 설정이다.
	resty map[*routerNode]*restyConfig
	facts []pendingCall
	seen  map[string]bool
	// 계수(한계 문구의 근거).
	latest       time.Time
	loadErrors   int
	unsupported  map[string]int
	unmodelled   map[string]int
	rewrites     int
	undeclared   map[string]bool
	missingUsrs  int
	unresolved   int
	ambiguous    int
	finalFacts   []RouteCallFact
	wrapperOwner map[string]bool
}

// pendingCall은 억제·usr 확인 전의 사실이다.
type pendingCall struct {
	fact  RouteCallFact
	owner string
	res   composedURL
	// methodParam은 동사가 감싸는 함수의 파라미터에서 왔다는 표시다.
	methodParam bool
	// fromWrapper는 선언된 래퍼 호출에서 온 사실이다(래퍼 본문 억제 대상이 아니다).
	fromWrapper bool
}

// restyConfig는 resty 클라이언트 하나의 설정이다.
type restyConfig struct {
	bases []restyBase
	// keys는 SetPathParam(s)로 설정한 키, rawKeys는 SetRawPathParam(s)의 키다.
	keys, rawKeys map[string]bool
	// unknownKeys·unknownRawKeys는 키를 상수로 읽지 못한 설정이 있다는 표시다.
	unknownKeys, unknownRawKeys bool
}

// restyBase는 base URL 후보 하나다.
type restyBase struct {
	parts   []urlPart
	trimmed bool
}

// newClientScan은 빈 수확 상태를 만든다.
func newClientScan(root string, ids symbolIDs, wrappers []*goWrapper) *clientScan {
	return &clientScan{root: root, ids: ids, wrappers: wrappers, resty: map[*routerNode]*restyConfig{},
		seen: map[string]bool{}, unsupported: map[string]int{}, unmodelled: map[string]int{},
		undeclared: map[string]bool{}, wrapperOwner: map[string]bool{}}
}

// mainPackages는 패턴 루트에서 import로 닿는 주 모듈 패키지를 고르고 로드 오류·mtime·지원하지 않는
// 클라이언트 import를 센다. 경로 순으로 정렬해 흐름 분석의 노드 번호를 고정한다.
func (s *clientScan) mainPackages(roots []*packages.Package) []*packages.Package {
	var out []*packages.Package
	for _, p := range walkImports(roots) {
		if !keep(p, false) {
			continue
		}
		s.loadErrors += len(p.Errors)
		s.observePackage(p)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// observePackage는 패키지의 mtime과 지원하지 않는 클라이언트 import를 관찰한다.
func (s *clientScan) observePackage(p *packages.Package) {
	for _, f := range p.Syntax {
		if p.Fset == nil {
			continue
		}
		if info, err := os.Stat(p.Fset.Position(f.Pos()).Filename); err == nil && info.ModTime().After(s.latest) {
			s.latest = info.ModTime()
		}
	}
	for _, path := range unsupportedClients {
		if _, ok := p.Imports[path]; ok {
			s.unsupported[path]++
		}
	}
}

// harvest는 흐름 분석(resty 클라이언트)과 대입 색인 뒤 호출마다 사실을 만든다.
func (s *clientScan) harvest(pkgs []*packages.Package) {
	s.flow = newRouteFlow(s.ids)
	s.flow.analyze(pkgs)
	s.idx = newValueIndex(pkgs, s.ids)
	s.collectResty()
	for _, w := range s.wrappers {
		for id := range w.bodies {
			s.wrapperOwner[id] = true
		}
	}
	for _, p := range pkgs {
		if p.TypesInfo == nil {
			continue
		}
		for _, file := range p.Syntax {
			for _, decl := range file.Decls {
				for _, part := range declParts(s.ids, p, decl) {
					s.scanPart(p, part)
				}
			}
		}
	}
	s.finish()
}

// collectResty는 resty 설정 호출을 클라이언트 노드에 붙인다(고정점 뒤라 수신 식을 평가할 수 있다).
func (s *clientScan) collectResty() {
	for _, c := range s.flow.restyCalls {
		if c.recv == nil {
			continue
		}
		for _, n := range s.flow.eval(c.pkg.TypesInfo, c.pkg, c.recv).sorted() {
			s.restyConfigOf(n).apply(s.idx, c)
		}
	}
}

// restyConfigOf는 노드의 설정이다(없으면 만든다).
func (s *clientScan) restyConfigOf(n *routerNode) *restyConfig {
	cfg, ok := s.resty[n]
	if !ok {
		cfg = &restyConfig{keys: map[string]bool{}, rawKeys: map[string]bool{}}
		s.resty[n] = cfg
	}
	return cfg
}

// apply는 설정 호출 하나를 더한다.
func (cfg *restyConfig) apply(idx *valueIndex, c restyCall) {
	info := c.pkg.TypesInfo
	switch c.kind {
	case setBaseURL:
		cfg.bases = append(cfg.bases, restyBase{parts: idx.evalURLParts(c.pkg, c.args[0]), trimmed: c.trimmed})
	case setPathParam, setRawPathParam:
		key, ok := constantString(info, c.args[0])
		raw := c.kind == setRawPathParam
		switch {
		case !ok && raw:
			cfg.unknownRawKeys = true
		case !ok:
			cfg.unknownKeys = true
		case raw:
			cfg.rawKeys[key] = true
		default:
			cfg.keys[key] = true
		}
	case setPathParams, setRawPathParams:
		cfg.addMapKeys(info, c.args[0], c.kind == setRawPathParams)
	}
}

// addMapKeys는 map 리터럴의 상수 키를 더한다. 리터럴이 아니거나 키가 상수가 아니면 모르는 키다.
func (cfg *restyConfig) addMapKeys(info *types.Info, expr ast.Expr, raw bool) {
	lit, ok := unparen(expr).(*ast.CompositeLit)
	known := ok
	if ok {
		for _, elt := range lit.Elts {
			kv, isKV := elt.(*ast.KeyValueExpr)
			key, isConst := "", false
			if isKV {
				key, isConst = constantString(info, kv.Key)
			}
			if !isConst {
				known = false
				continue
			}
			if raw {
				cfg.rawKeys[key] = true
			} else {
				cfg.keys[key] = true
			}
		}
	}
	if !known && raw {
		cfg.unknownRawKeys = true
	} else if !known {
		cfg.unknownKeys = true
	}
}

// scanPart는 선언 귀속 단위 하나에서 요청 호출·래퍼 호출·요청 재작성을 찾는다.
func (s *clientScan) scanPart(p *packages.Package, part declPart) {
	ast.Inspect(part.node, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			s.scanCall(p, part.owner, node)
		case *ast.CompositeLit:
			s.scanComposite(p, part.owner, node)
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if rewritesRequest(p.TypesInfo, lhs) {
					s.rewrites++
				}
			}
		}
		return true
	})
}

// scanCall은 호출 하나를 라이브러리 요청·래퍼 호출·모델링하지 않은 요청으로 분류한다.
func (s *clientScan) scanCall(p *packages.Package, owner string, call *ast.CallExpr) {
	info := p.TypesInfo
	fn := calleeFunc(info, call)
	if fn == nil {
		return
	}
	key := funcKey(fn)
	// 메서드 식 호출(`(*http.Client).Get(c, u)`)은 리시버가 첫 인자다 — 인자 위치를 한 칸 민다.
	args, recv := call.Args, receiverExpr(call)
	if isMethodExpression(info, call) {
		if len(args) == 0 {
			return
		}
		recv, args = args[0], args[1:]
	}
	if spec, ok := clientAPI[key]; ok {
		s.libraryCall(p, owner, call, spec, args, recv)
		return
	}
	if what, ok := unmodelledRequests[key]; ok {
		s.unmodelled[what]++
		return
	}
	for _, w := range s.wrappers {
		if w.decl.Kind == "function" && w.keys[key] {
			s.wrapperCall(p, owner, call.Pos(), w, callArgs(info, fn.Signature(), args))
		}
	}
}

// isMethodExpression은 `T.M(recv, …)`처럼 리시버가 첫 인자인 호출인지 본다.
func isMethodExpression(info *types.Info, call *ast.CallExpr) bool {
	sel, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	s := info.Selections[sel]
	return s != nil && s.Kind() == types.MethodExpr
}

// scanComposite는 생성자 래퍼 리터럴과 net/http Request 리터럴(모델링하지 않은 요청)을 찾는다.
func (s *clientScan) scanComposite(p *packages.Package, owner string, lit *ast.CompositeLit) {
	info := p.TypesInfo
	key := typeKey(info.TypeOf(lit))
	if key == "net/http.Request" {
		s.unmodelled["net/http Request literals"]++
		return
	}
	st, ok := derefStruct(info.TypeOf(lit))
	if !ok {
		return
	}
	for _, w := range s.wrappers {
		if w.decl.Kind == "constructor" && w.keys[key] {
			s.wrapperCall(p, owner, lit.Pos(), w, literalArgs(info, st, lit))
		}
	}
}

// rewritesRequest는 대입 대상이 만든 요청의 URL·동사를 바꾸는지 본다: net/http Request의 URL·Method,
// Request.URL의 경로·host 필드, resty Request의 URL·Method.
func rewritesRequest(info *types.Info, lhs ast.Expr) bool {
	sel, ok := unparen(lhs).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	s := info.Selections[sel]
	if s == nil || s.Kind() != types.FieldVal {
		return false
	}
	switch typeKey(s.Recv()) {
	case "net/http.Request", restyPath + ".Request":
		return sel.Sel.Name == "URL" || sel.Sel.Name == "Method"
	case "net/url.URL":
		inner, ok := unparen(sel.X).(*ast.SelectorExpr)
		if !ok {
			return false
		}
		is := info.Selections[inner]
		return is != nil && is.Kind() == types.FieldVal && inner.Sel.Name == "URL" &&
			typeKey(is.Recv()) == "net/http.Request"
	}
	return false
}

// libraryCall은 net/http·resty 요청 하나의 사실을 만든다. args·recv는 메서드 식을 보정한 인자와 리시버다.
func (s *clientScan) libraryCall(p *packages.Package, owner string, call *ast.CallExpr, spec callSpec,
	args []ast.Expr, recv ast.Expr) {
	if spec.urlArg >= len(args) || spec.methodArg >= len(args) {
		return
	}
	method, methodParam := s.callMethod(p, args, spec)
	path := s.idx.evalURLParts(p, args[spec.urlArg])
	loc := locateIn(s.root, p.Fset, call.Pos())
	if spec.lib == libNetHTTP {
		res := composeURL(path, anchorRule{pathOnly: "base"})
		s.add(pendingCall{fact: callFact(method, res, loc), owner: owner, res: res, methodParam: methodParam})
		return
	}
	for _, res := range s.restyResults(p, recv, path) {
		s.add(pendingCall{fact: callFact(method, res, loc), owner: owner, res: res, methodParam: methodParam})
	}
}

// callMethod는 요청의 동사다. 인자로 받으면 값이 문자열 하나로 풀려야 하고(상수나 한 번 대입된 변수),
// 빈 문자열은 GET(http.NewRequest 규칙 — resty Execute도 같은 함수로 간다), 계약 동사와 정확히 같아야
// 동사다. 아니면 빈 문자열(methodDynamic)과 그 인자가 파라미터인지를 돌려준다.
func (s *clientScan) callMethod(p *packages.Package, args []ast.Expr, spec callSpec) (string, bool) {
	if spec.method != "" {
		return spec.method, false
	}
	arg := args[spec.methodArg]
	m, ok := s.idx.literalValue(p, arg)
	switch {
	case ok && m == "":
		return "GET", false
	case ok && routeMethods[m]:
		return m, false
	}
	id, isIdent := unparen(arg).(*ast.Ident)
	return "", isIdent && s.idx.params[p.TypesInfo.Uses[id]]
}

// restyResults는 resty 요청의 base 결합 결과다. 수신 클라이언트 노드마다 그 노드의 설정(base 후보·path
// param)으로 따로 결합한다 — 노드 사이에 path param을 섞으면 raw 키를 가진 클라이언트의 요청이 escape
// 키를 가진 다른 클라이언트 규칙으로 풀린다. 노드를 추적하지 못했거나 base 설정이 없으면 base를 모르는
// 값이다. 추적하지 못한 클라이언트의 path param은 모르므로 `{name}`을 값으로 본다(원문으로 두면 치환된
// 요청이 거짓 route-call-without-decl error가 된다 — 값이면 최악이 거짓 match다).
func (s *clientScan) restyResults(p *packages.Package, recv ast.Expr, path []urlPart) []composedURL {
	configs := []*restyConfig{{unknownKeys: true}}
	if nodes := s.flow.eval(p.TypesInfo, p, recv).sorted(); len(nodes) > 0 {
		configs = configs[:0]
		for _, n := range nodes {
			cfg := s.resty[n]
			if cfg == nil {
				cfg = &restyConfig{}
			}
			configs = append(configs, cfg)
		}
	}
	var out []composedURL
	for _, cfg := range configs {
		resolved := restyPlaceholders(path, cfg)
		bases := cfg.bases
		if len(bases) == 0 {
			bases = []restyBase{{parts: []urlPart{valuePart()}}}
		}
		for _, b := range bases {
			joined := slashJoinParts(b.parts, b.trimmed, resolved)
			out = append(out, composeURL(joined, anchorRule{pathOnly: "base", relativeBase: true}))
		}
	}
	return out
}

// restyPlaceholders는 resty path param 자리표시(`{name}`)를 값으로 바꾼다(parseRequestURL). 설정이
// 하나도 없으면 resty가 치환하지 않아 원문 그대로 나간다. raw 값은 `/`를 담을 수 있어 여러 세그먼트
// 값이다. 이름 없는 `{}`는 resty도 그대로 둔다.
func restyPlaceholders(path []urlPart, cfg *restyConfig) []urlPart {
	if len(cfg.keys) == 0 && len(cfg.rawKeys) == 0 && !cfg.unknownKeys && !cfg.unknownRawKeys {
		return path
	}
	var out []urlPart
	for _, p := range path {
		if p.kind != partLiteral {
			out = appendParts(out, p)
			continue
		}
		out = appendParts(out, splitPlaceholders(p.text, cfg)...)
	}
	return out
}

// splitPlaceholders는 원문 하나의 `{name}`을 값 조각으로 나눈다. resty는 escape 키(요청 → 클라이언트)를
// 먼저 채우고 raw 키는 비어 있는 이름에만 넣으므로, 알려진 escape 키는 모르는 raw 키가 있어도 한 세그먼트다.
func splitPlaceholders(text string, cfg *restyConfig) []urlPart {
	var out []urlPart
	for {
		open := strings.IndexByte(text, '{')
		if open < 0 {
			return appendParts(out, literalPart(text))
		}
		closeAt := strings.IndexByte(text[open:], '}')
		if closeAt < 0 {
			return appendParts(out, literalPart(text))
		}
		closeAt += open
		key := text[open+1 : closeAt]
		out = appendParts(out, literalPart(text[:open]))
		switch {
		case key == "":
			out = appendParts(out, literalPart("{}"))
		case cfg.keys[key]:
			out = appendParts(out, valuePart())
		case cfg.rawKeys[key] || cfg.unknownRawKeys:
			out = appendParts(out, urlPart{kind: partValue, multi: true})
		case cfg.unknownKeys:
			out = appendParts(out, valuePart())
		default:
			out = appendParts(out, literalPart(text[open:closeAt+1]))
		}
		text = text[closeAt+1:]
	}
}

// wrapperCall은 선언된 래퍼 호출 하나의 사실을 만든다.
func (s *clientScan) wrapperCall(p *packages.Package, owner string, pos token.Pos, w *goWrapper, args []wrapperArg) {
	w.calls++
	pathArg, ok := bindWrapperArg(w.decl.PathArg, args)
	if !ok {
		// 조용히 버리면 낡은 선언이 호출 0건을 낸다 — http-wrapper-unresolved로 센다.
		w.unbound++
		return
	}
	method := wrapperMethod(w.decl, args)
	methodParam := false
	if arg, bound := bindWrapperArg(w.decl.MethodArg, args); bound && method == "" {
		if m, isString := s.idx.literalValue(p, arg.expr); isString && routeMethods[m] {
			method = m
		}
		id, isIdent := unparen(arg.expr).(*ast.Ident)
		methodParam = method == "" && isIdent && s.idx.params[p.TypesInfo.Uses[id]]
	}
	rule := anchorRule{pathOnly: w.decl.PathAnchor, relativeBase: w.decl.PathAnchor == "base"}
	parts := appendParts(nil, s.idx.evalURLParts(p, pathArg.expr)...)
	res := composeURL(parts, rule)
	if len(parts) > 0 && parts[0].kind == partLiteral {
		// 선언이 base라고 밝힌 경로다 — 풀지 못한 base 식이 아니라 선언의 앵커라 unresolved-base-url로 세지 않는다.
		res.unresolvedBase = false
	}
	fact := callFact(method, res, locateIn(s.root, p.Fset, pos))
	fact.Service = w.decl.Service
	s.add(pendingCall{fact: fact, owner: owner, res: res, methodParam: methodParam, fromWrapper: true})
}

// callFact는 조립 결과로 사실의 공통 필드를 채운다.
func callFact(method string, res composedURL, loc *BridgeLocation) RouteCallFact {
	fact := RouteCallFact{Kind: "route-call", Method: method, MethodDynamic: method == "", Dynamic: res.dynamic,
		PathAnchor: res.anchor, Authority: res.authority, BaseRef: res.baseRef,
		QueryTailStripped: res.queryTailStripped, MaskedSegments: res.maskedSegments, Location: loc}
	if res.dynamic {
		fact.ChannelPrefix = res.channelPrefix
	} else {
		template := res.template
		fact.Channel = &template
	}
	return fact
}

// add는 사실 후보를 담는다(위치를 모르면 버린다 — 계약상 location은 필수다).
func (s *clientScan) add(pc pendingCall) {
	if pc.fact.Location == nil {
		return
	}
	s.facts = append(s.facts, pc)
}

// finish는 래퍼 본문의 dynamic 요청을 억제하고, usr를 확인하고, 계수를 센다.
func (s *clientScan) finish() {
	for _, pc := range s.facts {
		dynamicish := pc.fact.Dynamic || pc.fact.MethodDynamic
		if !pc.fromWrapper && dynamicish && s.wrapperOwner[pc.owner] {
			continue
		}
		fact := pc.fact
		if s.ids.symbols[pc.owner] {
			fact.Symbol = &FactSymbol{QualifiedName: graph.ShortName(pc.owner), Usr: pc.owner}
		}
		if !s.push(fact) {
			continue
		}
		if fact.Symbol == nil {
			s.missingUsrs++
		}
		if pc.res.unresolvedBase && !fact.Dynamic {
			s.unresolved++
		}
		if pc.res.ambiguousJoin {
			s.ambiguous++
		}
		if dynamicish && !pc.fromWrapper && (pc.res.param || pc.methodParam) {
			s.undeclared[pc.owner] = true
		}
	}
}

// push는 같은 사실을 한 번만 담는다. 새로 담았으면 참이다.
func (s *clientScan) push(fact RouteCallFact) bool {
	data, err := json.Marshal(fact)
	if err != nil {
		return false
	}
	key := string(data)
	if s.seen[key] {
		return false
	}
	s.seen[key] = true
	s.finalFacts = append(s.finalFacts, fact)
	return true
}

// document는 문서를 조립한다. 사실은 위치·동사·템플릿 순으로 정렬한다.
func (s *clientScan) document(opts ClientRouteOptions, toolVersion string) *ClientRouteDocument {
	sortRouteCalls(s.finalFacts)
	generated := opts.GeneratedAt
	if generated.IsZero() {
		generated = time.Now()
	}
	doc := &ClientRouteDocument{
		Format: "bridge-facts", Version: 1, Tool: BridgeFactsTool{Name: "gartograph", Version: toolVersion},
		GeneratedAt: bridgeTimestamp(generated), Platform: "go", Target: "http", Project: s.root,
		Service: opts.Service, Roles: []string{"client"}, SourceSets: map[string]string{"tests": "excluded"},
		Facts: s.finalFacts,
	}
	if doc.Facts == nil {
		doc.Facts = []RouteCallFact{}
	}
	if !s.latest.IsZero() {
		doc.SourceModifiedAt = bridgeTimestamp(s.latest)
	}
	doc.Limitations = s.limitations()
	return doc
}

// sortRouteCalls는 결정적 순서로 정렬한다.
func sortRouteCalls(facts []RouteCallFact) {
	sort.SliceStable(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
		if a.Location.Path != b.Location.Path {
			return a.Location.Path < b.Location.Path
		}
		if a.Location.Line != b.Location.Line {
			return a.Location.Line < b.Location.Line
		}
		if a.Location.Column != b.Location.Column {
			return a.Location.Column < b.Location.Column
		}
		ka, _ := json.Marshal(a)
		kb, _ := json.Marshal(b)
		return string(ka) < string(kb)
	})
}

// limitations는 실제로 센 호출 측 공백만 결정적 순서로 낸다. 스코프는 내지 않는다 — 호출 측 공백이
// 가릴 수 있는 요청의 상한을 증명할 수 없어서다(스코프 없는 한계는 문서 전체 효과).
func (s *clientScan) limitations() []string {
	out := []string{}
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	if s.loadErrors > 0 {
		add("route-call-coverage: %d package load or parse error(s); HTTP calls there are not reported", s.loadErrors)
	}
	if len(s.unsupported) > 0 {
		paths := sortedKeys(s.unsupported)
		n := 0
		for _, p := range paths {
			n += s.unsupported[p]
		}
		add("route-call-coverage: %d package import(s) of HTTP client libraries gartograph does not harvest (%s); their calls are not reported",
			n, strings.Join(paths, ", "))
	}
	if len(s.unmodelled) > 0 {
		kinds := sortedKeys(s.unmodelled)
		n := 0
		for _, k := range kinds {
			n += s.unmodelled[k]
		}
		add("route-call-coverage: %d request(s) built in ways gartograph does not model (%s); they are not reported",
			n, strings.Join(kinds, ", "))
	}
	if s.unresolved > 0 {
		add("unresolved-base-url: %d route-call fact(s) join a base URL that could not be resolved statically; they carry pathAnchor \"base\"", s.unresolved)
	}
	if s.ambiguous > 0 {
		add("ambiguous-base-join: %d call(s) append a path without a leading \"/\" to an unresolved base URL; they are dynamic", s.ambiguous)
	}
	if s.rewrites > 0 {
		add("url-rewrite-interceptors: %d assignment(s) rewrite a built request's URL or method (net/http Request.URL/Method, resty Request.URL/Method); those requests may reach other routes", s.rewrites)
	}
	for _, w := range s.wrappers {
		switch {
		case !w.found:
			add("http-wrapper-unresolved: wrappers[%d] (%s %s) matches no Go declaration; check owner and name", w.index, w.decl.Owner, w.decl.Name)
		case w.calls == 0:
			add("http-wrapper-unresolved: wrappers[%d] (%s %s) has no call site in the scanned packages", w.index, w.decl.Owner, w.decl.Name)
		case w.unbound > 0:
			add("http-wrapper-unresolved: wrappers[%d] (%s %s) has %d call site(s) where pathArg could not be bound; check its index or label (Go labels are parameter names)",
				w.index, w.decl.Owner, w.decl.Name, w.unbound)
		}
	}
	if len(s.undeclared) > 0 {
		add("http-wrapper-undeclared: %d function(s) pass a parameter through to a request URL or method; declare them in an http-wrappers file (language \"go\") to report their call sites", len(s.undeclared))
	}
	if s.missingUsrs > 0 {
		add("missing-route-usrs: %d route-call fact(s) have no enclosing symbol in the impact graph; they carry no symbol", s.missingUsrs)
	}
	return out
}
