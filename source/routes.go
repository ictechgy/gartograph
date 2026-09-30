// isthmus http route-decl 생산자 — Go 서버가 선언한 라우트를 (method, 정규 경로 템플릿)
// 사실로 낸다(`gartograph routes --role server`).
//
// 계약은 ../isthmus의 docs/GRAPH-EXCHANGE.md "HTTP 경계" 절이 정본이다. 지원 범위:
//   - net/http ServeMux(Go 1.22+ 패턴과 GODEBUG httpmuxgo121 이전 패턴), DefaultServeMux
//   - chi v5(Get…·Handle·Method·Route·Mount·Group·With), gin v1(GET…·Handle·Any·Match·Group·
//     Static), echo v4(GET…·Add·Any·Match·Group·Host·Static·File)
//
// 디스패치 모델은 넷 모두 specificity다. ServeMux는 등록 시 충돌(어느 쪽도 더 구체적이지 않은
// 두 패턴)을 패닉으로 막아, 실행되는 등록 집합에서는 트리 탐색 순서(리터럴 > 단일 와일드카드 >
// 다중 와일드카드, 왼쪽부터, 역추적)가 곧 가장 구체적인 패턴이다(routing_tree.go matchPath).
// chi·gin·echo의 기수 트리도 정적 > 파라미터(chi는 정규식 먼저) > catch-all 순으로 왼쪽부터
// 역추적하며 찾는다 — isthmus 구체성(리터럴 > 부분 세그먼트 > 제약 {} > {} > {**}, 왼쪽부터)과 같은
// 방향이다. 차이(chi 정규식이 부분 세그먼트보다 먼저, echo 끝 파라미터의 다중 세그먼트)는
// README에 적었다.
package source

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ictechgy/gartograph/graph"
	"golang.org/x/tools/go/packages"
)

// RouteFactsDocument는 isthmus bridge-facts v1의 http 문서(서버 역할)다.
// 키 순서는 계약 문서의 나열 순서를 따라 diff 가능하게 유지한다.
type RouteFactsDocument struct {
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
	Dispatch         string            `json:"dispatch"`
	SourceSets       map[string]string `json:"sourceSets"`
	Facts            []RouteFact       `json:"facts"`
	// 계약상 항상 배열이다 — 비어 있어도 생략하면 isthmus 파서가 거부한다.
	Limitations      []string          `json:"limitations"`
	LimitationScopes []LimitationScope `json:"limitationScopes,omitempty"`
}

// RouteFact는 route-decl 사실 하나다. 존재 자체가 증거인 표식(narrowed·catchAllPrefix)은 참일 때만 싣는다.
type RouteFact struct {
	Kind             string            `json:"kind"`
	Method           string            `json:"method"`
	Channel          string            `json:"channel"`
	Dynamic          bool              `json:"dynamic"`
	PathAnchor       string            `json:"pathAnchor"`
	TrailingSlash    string            `json:"trailingSlash,omitempty"`
	Narrowed         bool              `json:"narrowed,omitempty"`
	ParamConstraints []ParamConstraint `json:"paramConstraints,omitempty"`
	CatchAllPrefix   bool              `json:"catchAllPrefix,omitempty"`
	Location         *BridgeLocation   `json:"location"`
	// Symbol은 핸들러 정점이다 — usr는 reach·impact와 같은 심볼 정점 ID다.
	Symbol *FactSymbol `json:"symbol,omitempty"`
}

// ParamConstraint는 계약의 paramConstraints 항목이다.
type ParamConstraint = paramConstraint

// LimitationScope는 한계 하나가 가릴 수 있는 요청의 상한이다(계약 "http limitation 스코프").
type LimitationScope struct {
	LimitationIndex  int      `json:"limitationIndex"`
	Templates        []string `json:"templates,omitempty"`
	TemplatePrefixes []string `json:"templatePrefixes,omitempty"`
	TemplateSuffixes []string `json:"templateSuffixes,omitempty"`
	Methods          []string `json:"methods,omitempty"`
}

// RouteOptions는 routes 명령의 입력이다.
type RouteOptions struct {
	Harvest     Options
	Service     string
	GeneratedAt time.Time
}

// RouteFacts는 opts.Harvest.Dir 아래 Go 서버의 라우트 선언을 http route-decl 문서로 낸다.
func RouteFacts(opts RouteOptions, toolVersion string) (*RouteFactsDocument, error) {
	root, err := realPath(opts.Harvest.Dir)
	if err != nil {
		return nil, err
	}
	loadOpts := Options{Dir: root, Level: graph.LevelSymbol, Exclude: opts.Harvest.Exclude,
		Patterns: opts.Harvest.Patterns, Tags: opts.Harvest.Tags}
	pkgs, err := load(loadOpts)
	if err != nil {
		return nil, err
	}
	symbolDoc, err := documentFrom(pkgs, loadOpts)
	if err != nil {
		return nil, err
	}
	scan := newRouteScan(root, newSymbolIDs(symbolDoc))
	scan.harvest(mainPackages(scan, pkgs))
	doc := scan.document(opts, toolVersion)
	if err := selfCheck(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// selfCheck는 문서가 isthmus 입력 검증을 통과할 모양인지 내기 전에 확인한다. 실패는 이 생산자의
// 결함이다 — 거부될 문서를 조용히 내면 소비자 쪽에서 원인을 찾기 어렵다.
func selfCheck(doc *RouteFactsDocument) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encoding route document: %w", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		return fmt.Errorf("decoding route document: %w", err)
	}
	if problem := routeDocumentProblem(generic); problem != "" {
		return fmt.Errorf("internal error: the route document fails the isthmus contract self-check (%s); please report it with the route registration", problem)
	}
	return nil
}

// mainPackages는 주 모듈 패키지를 고르고 로드 오류·mtime·지원하지 않는 라우터 import를 센다.
func mainPackages(scan *routeScan, pkgs []*packages.Package) []*packages.Package {
	var out []*packages.Package
	for _, p := range pkgs {
		if !keep(p, false) {
			continue
		}
		scan.loadErrors += len(p.Errors)
		scan.observePackage(p)
		out = append(out, p)
	}
	return out
}

// routeScan은 route 수확의 중간 상태다.
type routeScan struct {
	root       string
	ids        symbolIDs
	flow       *routeFlow
	legacyMux  bool
	facts      []RouteFact
	seen       map[string]bool
	latest     time.Time
	loadErrors int
	// unsupported는 수확하지 않는 라우터 패키지 경로 → import한 패키지 수다.
	unsupported map[string]int
	gaps        map[string]*routeGap
	missingUsrs int
	anonymous   int
	// explicit은 인스턴스별 명시적 (method, 템플릿)이다 — 빈 값 변형을 뺄 근거.
	explicit map[string]bool
	// echoRoutes는 echo 인스턴스별 leaf 판정 재료다.
	echoRoutes map[string][]echoLeaf
	// echoDynamic은 경로가 상수가 아닌 echo 등록이 있는지다(leaf 판정을 흔든다).
	echoDynamic bool
}

// routeGap은 같은 종류의 공백 한계 하나다(개수와 스코프 원소).
type routeGap struct {
	prefix, message string
	count           int
	templates       map[string]bool
	prefixes        map[string]bool
	suffixes        map[string]bool
	methods         map[string]bool
	unscoped        bool
}

// echoLeaf는 echo 끝 파라미터 판정 재료 하나다.
type echoLeaf struct {
	tokens   string
	prefix   string // 끝 파라미터 템플릿(스코프 접두사), 끝이 파라미터가 아니면 빈 문자열
	method   string
	anchored bool // root 앵커
}

// newRouteScan은 빈 수확 상태를 만든다.
func newRouteScan(root string, ids symbolIDs) *routeScan {
	return &routeScan{root: root, ids: ids, seen: map[string]bool{}, unsupported: map[string]int{},
		gaps: map[string]*routeGap{}, explicit: map[string]bool{}, echoRoutes: map[string][]echoLeaf{}}
}

// observePackage는 패키지의 mtime·지원하지 않는 라우터 import·ServeMux 모드를 관찰한다.
func (s *routeScan) observePackage(p *packages.Package) {
	for _, f := range p.Syntax {
		if p.Fset == nil {
			continue
		}
		if info, err := os.Stat(p.Fset.Position(f.Pos()).Filename); err == nil && info.ModTime().After(s.latest) {
			s.latest = info.ModTime()
		}
		if hasGoDebugMux121(f) {
			s.legacyMux = true
		}
	}
	for _, path := range unsupportedRouters {
		if _, ok := p.Imports[path]; ok {
			s.unsupported[path]++
		}
	}
	if p.Module != nil && legacyServeMuxModule(p.Module) {
		s.legacyMux = true
	}
}

// muxGoDebug는 go.mod·//go:debug의 httpmuxgo121=1 설정이다.
var muxGoDebug = regexp.MustCompile(`httpmuxgo121\s*=\s*1`)

// legacyServeMuxModule은 주 모듈이 Go 1.22 이전 ServeMux 의미론을 쓰는지 본다: go 지시어가
// 1.22 미만이면 GODEBUG 기본값이 httpmuxgo121=1이고, go.mod의 godebug 지시어로도 켤 수 있다.
func legacyServeMuxModule(m *packages.Module) bool {
	if m.GoVersion != "" && goVersionLess(m.GoVersion, 1, 22) {
		return true
	}
	if m.GoMod == "" {
		return false
	}
	data, err := os.ReadFile(m.GoMod)
	return err == nil && muxGoDebug.Match(godebugLines(string(data)))
}

// godebugLines는 go.mod의 godebug 지시어 줄만 모은다(주석의 우연한 일치를 막는다).
func godebugLines(gomod string) []byte {
	var b strings.Builder
	inBlock := false
	for _, line := range strings.Split(gomod, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "godebug ("):
			inBlock = true
		case inBlock && trimmed == ")":
			inBlock = false
		case inBlock || strings.HasPrefix(trimmed, "godebug "):
			b.WriteString(trimmed + "\n")
		}
	}
	return []byte(b.String())
}

// goVersionLess는 "1.21"·"1.21.3" 같은 go 지시어가 major.minor보다 낮은지 본다.
func goVersionLess(version string, major, minor int) bool {
	parts := strings.SplitN(version, ".", 3)
	ma, err1 := strconv.Atoi(parts[0])
	mi := 0
	var err2 error
	if len(parts) > 1 {
		mi, err2 = strconv.Atoi(strings.TrimRightFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' }))
	}
	if err1 != nil || err2 != nil {
		return false
	}
	return ma < major || (ma == major && mi < minor)
}

// hasGoDebugMux121은 파일의 `//go:debug httpmuxgo121=1` 지시어를 찾는다.
func hasGoDebugMux121(f *ast.File) bool {
	for _, group := range f.Comments {
		for _, c := range group.List {
			if strings.HasPrefix(c.Text, "//go:debug ") && muxGoDebug.MatchString(c.Text) {
				return true
			}
		}
	}
	return false
}

// harvest는 흐름 분석 뒤 등록마다 사실을 만든다.
func (s *routeScan) harvest(pkgs []*packages.Package) {
	s.flow = newRouteFlow(s.ids)
	s.flow.analyze(pkgs)
	regs := append([]registration(nil), s.flow.regs...)
	sort.SliceStable(regs, func(i, j int) bool { return regs[i].call.Pos() < regs[j].call.Pos() })
	var pending []pendingFact
	for _, r := range regs {
		pending = append(pending, s.registrationFacts(r)...)
	}
	s.finish(pending)
}

// pendingFact는 변형 정리·usr 확인 전의 사실이다.
type pendingFact struct {
	fact     RouteFact
	instance string
	variant  bool
	usr      string
	anon     bool
	regPos   token.Pos
}

// routeContext는 등록이 닿는 라우터 사슬 하나를 푼 결과다.
type routeContext struct {
	fw       routeFramework
	patterns []mountedPattern
	strips   []string
	host     string
	anchor   string
	top      *routerNode
	// ginBase는 gin 그룹 사슬로 합친 기준 경로다(gin만).
	ginBase string
}

// registrationFacts는 등록 하나의 사실을 만든다.
func (s *routeScan) registrationFacts(r registration) []pendingFact {
	if s.isRouterMount(r) {
		return nil
	}
	pattern, method, ok := s.registrationPattern(r)
	if !ok {
		return s.dynamicFacts(r)
	}
	methods, known := s.registrationMethods(r, method)
	res := s.handlerOf(r)
	var out []pendingFact
	for _, ctx := range s.contexts(r, initialPatterns(r, pattern)) {
		for _, shape := range s.contextShapes(r, ctx) {
			out = append(out, s.shapeFacts(r, ctx, shape, methods, known, res)...)
		}
	}
	return out
}

// isRouterMount는 핸들러가 추적된 라우터(마운트)라서 이 등록 자체는 사실이 아닌 경우다.
// 하위 라우터의 경로가 사실이 된다(부모 간선 또는 절대 경로 루트).
func (s *routeScan) isRouterMount(r registration) bool {
	handler := mountHandlerArg(r)
	if handler == nil {
		return false
	}
	info := r.pkg.TypesInfo
	if _, inner, ok := stripPrefixCall(info, handler); ok {
		handler = inner
	}
	return len(s.flow.eval(info, r.pkg, handler)) > 0
}

// registrationPattern은 등록의 경로 패턴 원문과 패턴 안 동사(ServeMux·chi Handle)를 읽는다.
// 경로가 상수가 아니면 ok가 거짓이다.
func (s *routeScan) registrationPattern(r registration) (string, string, bool) {
	if r.spec.pathArg < 0 || r.spec.pathArg >= len(r.call.Args) {
		return "", "", false
	}
	raw, ok := constantString(r.pkg.TypesInfo, r.call.Args[r.spec.pathArg])
	if !ok {
		return "", "", false
	}
	switch {
	case r.spec.kind == regMount:
		return raw, "ANY", true
	case r.spec.kind == regStatic && r.spec.fw == fwGin:
		return path.Join(raw, "/*filepath"), "", true
	case r.spec.kind == regStatic:
		return raw + "*", "", true
	case r.spec.fw == fwChi && r.spec.method == "PATTERN":
		if i := strings.IndexAny(raw, " \t"); i >= 0 {
			return strings.TrimLeft(raw[i+1:], " \t"), raw[:i], true
		}
		return raw, "ANY", true
	}
	return raw, "", true
}

// registrationMethods는 등록의 동사 목록이다. 동사를 증명하지 못하면 known이 거짓이다.
// 계약 밖 동사(CONNECT·PROPFIND 등)는 모델링된 호출이 보낼 수 없어 사실도 공백도 만들지 않는다.
func (s *routeScan) registrationMethods(r registration, patternMethod string) ([]string, bool) {
	switch {
	case len(r.spec.staticMethods) > 0:
		return r.spec.staticMethods, true
	case patternMethod != "":
		return filterMethods([]string{strings.ToUpper(patternMethod)}), true
	case r.spec.method == "PATTERN":
		return nil, true // ServeMux — 패턴 해석이 동사를 정한다
	case r.spec.method != "":
		return filterMethods([]string{r.spec.method}), true
	case r.spec.methodArg >= 0 && r.spec.methodArg < len(r.call.Args):
		m, ok := constantString(r.pkg.TypesInfo, r.call.Args[r.spec.methodArg])
		return filterMethods([]string{strings.ToUpper(m)}), ok
	case r.spec.methodsArg >= 0 && r.spec.methodsArg < len(r.call.Args):
		return constantMethodList(r, r.call.Args[r.spec.methodsArg])
	}
	return nil, false
}

// filterMethods는 계약의 동사(와 ANY)만 남긴다.
func filterMethods(in []string) []string {
	var out []string
	for _, m := range in {
		if m == "ANY" || routeMethods[m] {
			out = append(out, m)
		}
	}
	return out
}

// constantMethodList는 `[]string{"GET", http.MethodPost}` 리터럴의 상수 동사 목록이다.
func constantMethodList(r registration, expr ast.Expr) ([]string, bool) {
	lit, ok := unparen(expr).(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	var out []string
	for _, elt := range lit.Elts {
		m, ok := constantString(r.pkg.TypesInfo, elt)
		if !ok {
			return nil, false
		}
		out = append(out, strings.ToUpper(m))
	}
	return filterMethods(out), true
}

// handlerOf는 등록의 핸들러 usr 후보다(정적 파일 등록은 프레임워크 핸들러라 없음).
func (s *routeScan) handlerOf(r registration) handlerResolution {
	idx := r.spec.handlerArg
	if idx == handlerLast {
		idx = len(r.call.Args) - 1
		if r.call.Ellipsis.IsValid() || idx <= r.spec.pathArg {
			return handlerResolution{}
		}
	}
	if idx < 0 || idx >= len(r.call.Args) {
		return handlerResolution{}
	}
	return resolveHandler(s.ids, r.pkg, r.call.Args[idx], r.owner)
}

// initialPatterns는 등록 자체가 만드는 패턴이다. chi Mount(P, 불투명 핸들러)는 P·P/·P/*를 모두
// 그 핸들러에 묶으므로(mux.go Mount) 하위 "/*"를 P에 마운트한 것과 같다.
func initialPatterns(r registration, pattern string) []mountedPattern {
	if r.spec.kind == regMount {
		return chiMountExpand(pattern, "/*")
	}
	return []mountedPattern{{pattern: pattern}}
}

// contexts는 등록이 닿는 라우터 사슬마다 패턴 합성 결과를 만든다. 수신 라우터를 추적하지
// 못하면 사슬 없는 base 앵커 문맥 하나다.
func (s *routeScan) contexts(r registration, patterns []mountedPattern) []routeContext {
	receivers := s.flow.receiverNodes(r)
	if len(receivers) == 0 {
		return []routeContext{s.applyChain(r.spec.fw, patterns, nil, nil)}
	}
	var out []routeContext
	for _, n := range receivers {
		for _, chain := range chainsOf(n, s.flow.nodes) {
			out = append(out, s.applyChain(r.spec.fw, patterns, chain.edges, chain.top))
		}
	}
	return out
}

// nodeChain은 노드에서 루트(또는 풀지 못한 꼭대기)까지의 부모 간선 사슬이다(안쪽부터).
type nodeChain struct {
	edges []parentEdge
	top   *routerNode
}

// maxChains는 한 노드가 펼칠 사슬 수 상한이다 — 넘으면 나머지는 버리고 공백으로 센다.
const maxChains = 64

// chainsOf는 노드의 모든 부모 사슬이다. 순환은 명시적 스택의 방문 표시로 끊는다.
func chainsOf(start *routerNode, nodes []*routerNode) []nodeChain {
	type frame struct {
		node  *routerNode
		edges []parentEdge
		seen  map[*routerNode]bool
	}
	var out []nodeChain
	stack := []frame{{node: start, seen: map[*routerNode]bool{start: true}}}
	for len(stack) > 0 && len(out) < maxChains {
		fr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(fr.node.parents) == 0 {
			out = append(out, nodeChain{edges: fr.edges, top: fr.node})
			continue
		}
		for i := len(fr.node.parents) - 1; i >= 0; i-- {
			e := fr.node.parents[i]
			if e.parent == noParent {
				out = append(out, nodeChain{edges: append(append([]parentEdge(nil), fr.edges...), e)})
				continue
			}
			parent := nodes[e.parent]
			if fr.seen[parent] {
				continue
			}
			seen := map[*routerNode]bool{parent: true}
			for k := range fr.seen {
				seen[k] = true
			}
			edges := append(append([]parentEdge(nil), fr.edges...), e)
			stack = append(stack, frame{node: parent, edges: edges, seen: seen})
		}
	}
	return out
}

// applyChain은 패턴에 사슬의 접두사를 안쪽부터 합성한다. 같은 프레임워크의 하위 라우터·chi
// 마운트를 먼저, 그 뒤 StripPrefix 접두사를 템플릿 수준에서 붙인다. 상수가 아닌 접두사나 그 밖의
// 조합을 만나면 거기서 멈추고 base 앵커다.
func (s *routeScan) applyChain(fw routeFramework, patterns []mountedPattern, edges []parentEdge, top *routerNode) routeContext {
	ctx := routeContext{fw: fw, patterns: append([]mountedPattern(nil), patterns...), anchor: "root", top: top}
	var ginRels, echoPrefixes []string
	stripping := false
	for _, e := range edges {
		if e.dynamic || (stripping && e.kind != edgeStrip) {
			ctx.anchor = "base"
			break
		}
		switch e.kind {
		case edgeStrip:
			stripping = true
			ctx.strips = append(ctx.strips, e.prefix)
		case edgeChiMount:
			ctx.patterns = chiMountAll(e.prefix, ctx.patterns)
		default:
			ginRels, echoPrefixes = s.applyDerive(&ctx, e, ginRels, echoPrefixes)
		}
	}
	if top == nil || top.kind == nodeDerived {
		ctx.anchor = "base"
	}
	s.finishChain(&ctx, ginRels, echoPrefixes)
	return ctx
}

// applyDerive는 하위 라우터 간선 하나를 합성한다(gin·echo는 모아 두었다가 바깥부터 합친다).
func (s *routeScan) applyDerive(ctx *routeContext, e parentEdge, ginRels, echoPrefixes []string) ([]string, []string) {
	switch e.derive {
	case deriveChiRoute:
		ctx.patterns = chiMountAll(e.prefix, ctx.patterns)
	case deriveGinGroup:
		ginRels = append(ginRels, e.prefix)
	case deriveEchoGroup:
		echoPrefixes = append(echoPrefixes, e.prefix)
	case deriveEchoHost:
		ctx.host = e.prefix
	}
	return ginRels, echoPrefixes
}

// finishChain은 gin 그룹(joinPaths, 바깥부터)과 echo 그룹(문자열 연결)을 패턴에 합친다.
func (s *routeScan) finishChain(ctx *routeContext, ginRels, echoPrefixes []string) {
	switch ctx.fw {
	case fwGin:
		base := "/"
		for i := len(ginRels) - 1; i >= 0; i-- {
			base = ginJoinPaths(base, ginRels[i])
		}
		ctx.ginBase = base
		for i := range ctx.patterns {
			ctx.patterns[i].pattern = ginJoinPaths(base, ctx.patterns[i].pattern)
		}
	case fwEcho:
		prefix := ""
		for i := len(echoPrefixes) - 1; i >= 0; i-- {
			prefix += echoPrefixes[i]
		}
		for i := range ctx.patterns {
			ctx.patterns[i].pattern = normalizeEchoPath(prefix + ctx.patterns[i].pattern)
		}
	}
}

// chiMountAll은 패턴 목록 전체에 chi 마운트 접두사를 합성한다.
func chiMountAll(prefix string, patterns []mountedPattern) []mountedPattern {
	var out []mountedPattern
	for _, p := range patterns {
		for _, m := range chiMountExpand(prefix, p.pattern) {
			m.catchAllPrefix = m.catchAllPrefix || p.catchAllPrefix
			out = append(out, m)
		}
	}
	return out
}

// contextShapes는 문맥의 패턴들을 템플릿으로 펼치고 StripPrefix 접두사를 붙인다. 템플릿으로
// 쓸 수 없는 패턴은 공백으로 센다.
func (s *routeScan) contextShapes(r registration, ctx routeContext) []contextShape {
	var out []contextShape
	for _, mp := range ctx.patterns {
		parsed, host, method, ok := s.parsePattern(ctx.fw, mp.pattern)
		if !ok {
			continue
		}
		if parsed.dynamic {
			s.gap("route-coverage:", "route registration(s) use path patterns that are not canonical templates (several parameters in one segment or a mid-path wildcard); they are not declared", true).count++
			if parsed.capped {
				s.gap("route-template-expansion-capped:", "route registration(s) expand to more than 16 templates; they are not declared", true).count++
			}
			continue
		}
		for i, shape := range parsed.shapes {
			shape = withStrips(shape, ctx.strips)
			shape.catchAllPrefix = shape.catchAllPrefix || (mp.catchAllPrefix && !shape.variant)
			cs := contextShape{shape: shape, host: firstNonEmpty(host, ctx.host),
				patternMethod: method, leafTokens: parsed.leafTokens, leafPrefix: leafPrefixOf(parsed, shape)}
			if i == 0 {
				cs.anySuffixUnder = parsed.anySuffixUnder
			}
			out = append(out, cs)
		}
	}
	return out
}

// contextShape는 문맥 안 템플릿 하나와 그 부가 정보다.
type contextShape struct {
	shape         routeShape
	host          string
	patternMethod string // ServeMux 패턴의 동사("" = 모든 동사)
	leafTokens    string
	leafPrefix    string
	// anySuffixUnder는 부분 catch-all 공백의 스코프 부모 템플릿이다(주 템플릿에만 싣는다).
	anySuffixUnder string
}

// leafPrefixOf는 echo 끝 파라미터 템플릿이다(주 템플릿이 파라미터·부분 세그먼트로 끝날 때만).
func leafPrefixOf(parsed parsedRoute, shape routeShape) string {
	if parsed.leafTokens == "" || shape.variant || shape.catchAllPrefix {
		return ""
	}
	last := shape.segments[len(shape.segments)-1]
	if last.kind != segParam && last.kind != segPartial {
		return ""
	}
	return shape.template()
}

// parsePattern은 프레임워크 패턴 하나를 해석한다. 등록되지 않는(패닉·절대 맞지 않는) 패턴은 ok가 거짓이다.
func (s *routeScan) parsePattern(fw routeFramework, pattern string) (parsedRoute, string, string, bool) {
	switch fw {
	case fwServeMux:
		mux := parseServeMuxPattern(pattern)
		if s.legacyMux {
			mux = parseLegacyServeMuxPattern(pattern)
		}
		if !mux.valid {
			return parsedRoute{}, "", "", false
		}
		method := mux.method
		if method == "" {
			method = "ANY"
		}
		return buildShapes(mux.raw), mux.host, method, true
	case fwChi:
		raw, dynamic, ok := parseChiPattern(pattern)
		if dynamic {
			return parsedRoute{dynamic: true}, "", "", true
		}
		return buildShapes(raw), "", "", ok
	case fwGin:
		raw, ok := parseGinPath(pattern)
		return buildShapes(raw), "", "", ok
	}
	raw, dynamic, tokens := parseEchoPath(pattern)
	if dynamic {
		return parsedRoute{dynamic: true}, "", "", true
	}
	parsed := buildShapes(raw)
	parsed.leafTokens = tokens
	return parsed, "", "", true
}

// withStrips는 StripPrefix 접두사(안쪽부터)를 템플릿 앞에 붙인다. 제약의 세그먼트 번호도 민다.
func withStrips(shape routeShape, strips []string) routeShape {
	for _, strip := range strips {
		var head []templateSegment
		for _, part := range strings.Split(strings.TrimPrefix(strip, "/"), "/") {
			head = append(head, templateSegment{kind: segLiteral, value: encodeSegmentValue(part)})
		}
		segments := append(head, shape.segments...)
		constraints := make([]paramConstraint, len(shape.constraints))
		for i, c := range shape.constraints {
			c.Segment += len(head)
			constraints[i] = c
		}
		shape = routeShape{segments: segments, constraints: constraints,
			catchAllPrefix: shape.catchAllPrefix, variant: shape.variant}
	}
	return shape
}

// noteAnySuffix는 부분 catch-all(`/static*`)이 세그먼트 안 나머지도 받는다는 공백을 스코프와 함께 센다.
func (s *routeScan) noteAnySuffix(ctx routeContext, cs contextShape, methods []string) {
	if cs.anySuffixUnder == "" {
		return
	}
	g := s.gap("route-coverage:", "catch-all route(s) end inside a path segment (for example /static*) and also serve paths that extend that segment; those paths are not declared", false)
	g.count++
	if ctx.anchor != "root" || len(ctx.strips) > 0 {
		g.unscoped = true
		return
	}
	g.prefixes[cs.anySuffixUnder] = true
	for _, m := range methods {
		g.methods[m] = true
	}
}

// shapeFacts는 템플릿 하나 × 동사마다 사실을 만든다.
func (s *routeScan) shapeFacts(r registration, ctx routeContext, cs contextShape, methods []string,
	known bool, res handlerResolution) []pendingFact {
	if r.spec.fw == fwServeMux {
		methods = filterMethods([]string{cs.patternMethod})
	}
	if !known {
		s.noteUnknownMethod(ctx, cs)
		return nil
	}
	s.noteAnySuffix(ctx, cs, methods)
	instance := instanceKey(ctx.top, r, cs.host)
	var out []pendingFact
	for _, method := range methods {
		fact := s.baseFact(r, ctx, cs, method)
		out = append(out, pendingFact{fact: fact, instance: instance, variant: cs.shape.variant,
			usr: res.usr, anon: res.anonymous, regPos: r.call.Pos()})
		if !cs.shape.variant {
			s.explicit[instance+"|"+method+"|"+fact.Channel] = true
		}
		if ctx.fw == fwEcho {
			s.echoRoutes[instance] = append(s.echoRoutes[instance], echoLeaf{tokens: cs.leafTokens,
				prefix: cs.leafPrefix, method: method, anchored: ctx.anchor == "root" && len(ctx.strips) == 0})
		}
	}
	return out
}

// baseFact는 사실 하나의 공통 필드를 채운다.
func (s *routeScan) baseFact(r registration, ctx routeContext, cs contextShape, method string) RouteFact {
	fact := RouteFact{
		Kind: "route-decl", Method: method, Channel: cs.shape.template(), PathAnchor: ctx.anchor,
		TrailingSlash: s.trailingSlash(ctx, cs.shape), Narrowed: cs.host != "",
		ParamConstraints: cs.shape.constraints, CatchAllPrefix: cs.shape.catchAllPrefix,
		Location: s.registrationLocation(r),
	}
	if ctx.anchor == "base" {
		s.noteUnresolvedPrefix(fact.Channel)
	}
	return fact
}

// trailingSlash는 프레임워크별 끝 슬래시 정책이다(생략 = 모름).
//   - `{**}`로 끝나는 템플릿: 해당 없음(생략)
//   - ServeMux: `/`로 끝나는 템플릿은 슬래시 없는 요청을 301로 보내므로 모름, 그 밖은 strict
//   - chi·echo: 기본 strict, 끝 슬래시 미들웨어를 쓰면 모름
//   - gin: RedirectTrailingSlash 기본 true라 optional, 상수 false면 strict(ginTrailingSlash)
func (s *routeScan) trailingSlash(ctx routeContext, shape routeShape) string {
	if shape.endsWithCatchAll() {
		return ""
	}
	switch ctx.fw {
	case fwServeMux:
		if shape.endsWithSlash() {
			return ""
		}
		return "strict"
	case fwGin:
		if ctx.top == nil || ctx.top.kind == nodeDerived {
			return ""
		}
		return s.flow.ginTrailingSlash(ctx.top)
	}
	if s.flow.slashMiddleware[ctx.fw] {
		return ""
	}
	return "strict"
}

// instanceKey는 라우터 인스턴스(꼭대기 노드와 host)다. 꼭대기를 모르면 등록마다 따로다.
func instanceKey(top *routerNode, r registration, host string) string {
	if top == nil {
		return fmt.Sprintf("reg:%d|%s", r.call.Pos(), host)
	}
	return fmt.Sprintf("node:%d|%s", top.id, host)
}

// noteUnknownMethod는 동사를 증명하지 못한 등록을 그 템플릿 스코프의 공백으로 센다.
func (s *routeScan) noteUnknownMethod(ctx routeContext, cs contextShape) {
	g := s.gap("route-coverage:", "route registration(s) take their method from a non-constant value; they are not declared", false)
	g.count++
	s.scopeTemplate(g, cs.shape.template(), ctx.anchor)
}

// noteUnresolvedPrefix는 base 앵커 사실을 unresolved-route-prefix 공백으로 센다.
func (s *routeScan) noteUnresolvedPrefix(template string) {
	g := s.gap("unresolved-route-prefix:", "route declaration(s) are on routers whose mount prefix could not be resolved statically; they carry pathAnchor \"base\"", false)
	g.count++
	s.scopeTemplate(g, template, "base")
}

// scopeTemplate은 템플릿 하나를 공백 스코프에 더한다. root 앵커는 정확 템플릿, base 앵커는
// 접미사다(`{**}`·`/`는 접미사로 쓸 수 없어 스코프 없는 공백으로 넓힌다).
func (s *routeScan) scopeTemplate(g *routeGap, template, anchor string) {
	if anchor == "root" {
		g.templates[template] = true
		return
	}
	if template == "/" || strings.Contains(template, "{**}") {
		g.unscoped = true
		return
	}
	g.suffixes[template] = true
}

// registrationLocation은 등록의 위치다(경로 인자, 없으면 호출).
func (s *routeScan) registrationLocation(r registration) *BridgeLocation {
	pos := r.call.Pos()
	if r.spec.pathArg >= 0 && r.spec.pathArg < len(r.call.Args) {
		pos = r.call.Args[r.spec.pathArg].Pos()
	}
	return locateIn(s.root, r.pkg.Fset, pos)
}

// locateIn은 토큰 위치를 root 상대의 계약 위치로 바꾼다(root 밖이면 nil).
func locateIn(root string, fset *token.FileSet, pos token.Pos) *BridgeLocation {
	if fset == nil {
		return nil
	}
	p := fset.Position(pos)
	if !p.IsValid() {
		return nil
	}
	rel, err := filepath.Rel(root, p.Filename)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	return &BridgeLocation{Path: filepath.ToSlash(rel), Line: p.Line, Column: p.Column}
}

// dynamicFacts는 경로가 상수가 아닌 등록을 dynamic 사실로 보존한다(원문 식, 길이 상한).
func (s *routeScan) dynamicFacts(r registration) []pendingFact {
	if r.spec.pathArg < 0 || r.spec.pathArg >= len(r.call.Args) {
		return nil
	}
	methods, known := s.registrationMethods(r, "")
	if r.spec.fw == fwServeMux || !known || len(methods) == 0 {
		methods = []string{"ANY"}
	}
	var buf strings.Builder
	if err := printer.Fprint(&buf, r.pkg.Fset, r.call.Args[r.spec.pathArg]); err != nil {
		return nil
	}
	text := buf.String()
	if len(text) > 200 {
		text = text[:197] + "..."
	}
	s.gap("route-coverage:", "route registration(s) have non-constant path patterns; they are declared as dynamic", true).count++
	s.echoDynamic = s.echoDynamic || r.spec.fw == fwEcho
	res := s.handlerOf(r)
	anchor := "root"
	if len(s.flow.receiverNodes(r)) == 0 {
		anchor = "base"
	}
	var out []pendingFact
	for _, method := range methods {
		fact := RouteFact{Kind: "route-decl", Method: method, Channel: text, Dynamic: true,
			PathAnchor: anchor, Location: s.registrationLocation(r)}
		out = append(out, pendingFact{fact: fact, instance: instanceKey(nil, r, ""), usr: res.usr,
			anon: res.anonymous, regPos: r.call.Pos()})
	}
	return out
}

// gap은 (접두사, 문구) 공백을 돌려준다(처음이면 만든다). unscoped면 스코프 없이 센다.
func (s *routeScan) gap(prefix, message string, unscoped bool) *routeGap {
	key := prefix + message
	g, ok := s.gaps[key]
	if !ok {
		g = &routeGap{prefix: prefix, message: message, templates: map[string]bool{},
			prefixes: map[string]bool{}, suffixes: map[string]bool{}, methods: map[string]bool{}}
		s.gaps[key] = g
	}
	g.unscoped = g.unscoped || unscoped
	return g
}

// finish는 빈 값 변형을 정리하고 usr를 확인해 사실을 확정한다. echo 끝 파라미터 공백도 센다.
func (s *routeScan) finish(pending []pendingFact) {
	leaves := s.noteEchoLeaves()
	anonRegs := map[token.Pos]bool{}
	for _, pf := range pending {
		if pf.variant && s.explicitCovers(pf) {
			continue
		}
		fact := pf.fact
		if fact.Location == nil {
			continue
		}
		if leaves[pf.instance+"|"+fact.Channel] {
			fact.TrailingSlash = s.echoLeafTrailingSlash()
		}
		if s.ids.symbols[pf.usr] {
			fact.Symbol = &FactSymbol{QualifiedName: graph.ShortName(pf.usr), Usr: pf.usr}
			if pf.anon {
				anonRegs[pf.regPos] = true
			}
		} else {
			fact.CatchAllPrefix = false
		}
		s.push(fact)
	}
	s.anonymous = len(anonRegs)
}

// echoLeafTrailingSlash는 echo 끝 파라미터 decl의 끝 슬래시 정책이다. 자식 없는 파라미터 노드는
// 나머지 전부(끝 슬래시 포함)를 값으로 받으므로 optional이다. 경로가 상수가 아닌 echo 등록이 있으면
// 그 등록이 자식을 만들 수 있어 모른다(생략).
func (s *routeScan) echoLeafTrailingSlash() string {
	if s.echoDynamic {
		return ""
	}
	return "optional"
}

// explicitCovers는 같은 인스턴스에 같은 템플릿의 명시적 decl(같은 동사나 ANY)이 있는지 본다.
func (s *routeScan) explicitCovers(pf pendingFact) bool {
	return s.explicit[pf.instance+"|"+pf.fact.Method+"|"+pf.fact.Channel] ||
		s.explicit[pf.instance+"|ANY|"+pf.fact.Channel]
}

// push는 같은 사실을 한 번만 담고 usr 없는 사실을 센다.
func (s *routeScan) push(fact RouteFact) {
	key := fmt.Sprintf("%s|%s|%v|%s|%s|%v|%v|%v|%s:%d:%d|%v", fact.Method, fact.Channel, fact.Dynamic,
		fact.PathAnchor, fact.TrailingSlash, fact.Narrowed, fact.CatchAllPrefix, fact.ParamConstraints,
		fact.Location.Path, fact.Location.Line, fact.Location.Column, fact.Symbol)
	if s.seen[key] {
		return
	}
	s.seen[key] = true
	if fact.Symbol == nil {
		s.missingUsrs++
	}
	s.facts = append(s.facts, fact)
}

// noteEchoLeaves는 echo 끝 파라미터가 자식 없는 노드라 나머지 경로 전부를 받는 경우를 공백으로 세고,
// 그런 (인스턴스, 템플릿) 집합을 돌려준다. 같은 인스턴스에 그 파라미터 뒤로 이어지는 경로가 하나라도
// 있으면 자식이 있어 한 세그먼트만 받는다.
func (s *routeScan) noteEchoLeaves() map[string]bool {
	leaves := map[string]bool{}
	for _, instance := range sortedKeys(s.echoRoutes) {
		routes := s.echoRoutes[instance]
		for _, leaf := range routes {
			if leaf.prefix == "" || hasDeeperRoute(routes, leaf.tokens) {
				continue
			}
			leaves[instance+"|"+leaf.prefix] = true
			g := s.gap("route-coverage:", "echo route(s) end in a path parameter whose node has no children; echo also routes longer paths (across \"/\") to them, and those paths are not declared", false)
			g.count++
			if !leaf.anchored {
				g.unscoped = true
				continue
			}
			g.prefixes[leaf.prefix] = true
			g.methods[leaf.method] = true
		}
	}
	return leaves
}

// hasDeeperRoute는 토큰열 뒤로 더 이어지는 경로가 있는지 본다.
func hasDeeperRoute(routes []echoLeaf, tokens string) bool {
	for _, other := range routes {
		if strings.HasPrefix(other.tokens, tokens+"/") {
			return true
		}
	}
	return false
}

// sortedKeys는 맵 키를 정렬해 돌려준다.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// document는 문서를 조립한다. 사실은 위치·동사·템플릿 순으로 정렬한다.
func (s *routeScan) document(opts RouteOptions, toolVersion string) *RouteFactsDocument {
	sortRouteFacts(s.facts)
	generated := opts.GeneratedAt
	if generated.IsZero() {
		generated = time.Now()
	}
	doc := &RouteFactsDocument{
		Format: "bridge-facts", Version: 1, Tool: BridgeFactsTool{Name: "gartograph", Version: toolVersion},
		GeneratedAt: bridgeTimestamp(generated), Platform: "go", Target: "http", Project: s.root,
		Service: opts.Service, Roles: []string{"server"}, Dispatch: "specificity",
		SourceSets: map[string]string{"tests": "excluded"}, Facts: s.facts,
	}
	if doc.Facts == nil {
		doc.Facts = []RouteFact{}
	}
	if !s.latest.IsZero() {
		doc.SourceModifiedAt = bridgeTimestamp(s.latest)
	}
	doc.Limitations, doc.LimitationScopes = s.limitations()
	return doc
}

// sortRouteFacts는 결정적 순서로 정렬한다.
func sortRouteFacts(facts []RouteFact) {
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
		if a.Channel != b.Channel {
			return a.Channel < b.Channel
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.PathAnchor < b.PathAnchor
	})
}
