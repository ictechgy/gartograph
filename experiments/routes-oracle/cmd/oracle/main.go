// Command oracle는 gartograph routes 문서를 실제 라우터와 대조해 정밀도·재현율을 잰다.
//
// 사용법(이 모듈 루트에서, 의존은 proxy.golang.org에서 받는다):
//
//	gartograph routes --dir . --pattern ./fixtures/... --generated-at 2026-01-01T00:00:00Z --out /tmp/routes.json
//	go run ./cmd/oracle /tmp/routes.json > recorded/report.json
//
// 정답(route table)은 라우터가 스스로 밝힌 것만 쓴다: chi.Walk, gin Engine.Routes(), echo
// Routes()·Routers(), ServeMux는 공개 목록이 없어 등록 색인(routingIndex)을 reflect로 읽고 실제
// 요청(httptest)으로 탐침한다. 핸들러 신원은 요청을 실행해 fixture 핸들러가 싣는 X-Handler
// 헤더(runtime.Caller 이름)로 안다.
//
//   - 정밀도 = 검증된 정적 root 사실 / 정적 root 사실. 사실 템플릿에서 만든 표본 요청이 그
//     사실의 핸들러(usr가 없으면 fixture 밖 핸들러)에 닿고, ANY는 계약의 여덟 동사 모두, 선언한
//     trailingSlash(strict는 뒤집은 경로가 닿지 않음, optional은 닿거나 원래 경로로 리다이렉트)와
//     int 제약(정수 아닌 값은 닿지 않음)까지 맞아야 검증이다.
//   - 재현율 = 덮인 정답 항목 / 정답 항목. 항목은 (동사, 라우터 패턴, 핸들러)이고, 계약 밖 동사
//     (CONNECT·PROPFIND·REPORT)와 마운트 등록 자체는 뺀다. 항목의 표본 요청 경로에 isthmus 세그먼트
//     매칭으로 맞고 동사(또는 ANY)와 핸들러가 같은 사실이 있으면 덮였다.
//
// 정밀도나 재현율이 1 미만이면 종료 코드 1이다.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/labstack/echo/v4"

	"example.com/routesoracle/fixtures/chiapp"
	"example.com/routesoracle/fixtures/echoapp"
	"example.com/routesoracle/fixtures/ginapp"
	"example.com/routesoracle/fixtures/mark"
	"example.com/routesoracle/fixtures/muxapp"
)

// contractMethods는 isthmus 계약의 동사다.
var contractMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE"}

// defaultHost는 host 없는 탐침의 Host 헤더다.
const defaultHost = "example.test"

// fact는 gartograph route-decl 사실이다(필요한 필드만).
type fact struct {
	Method           string `json:"method"`
	Channel          string `json:"channel"`
	Dynamic          bool   `json:"dynamic"`
	PathAnchor       string `json:"pathAnchor"`
	TrailingSlash    string `json:"trailingSlash"`
	Narrowed         bool   `json:"narrowed"`
	CatchAllPrefix   bool   `json:"catchAllPrefix"`
	ParamConstraints []struct {
		Segment int    `json:"segment"`
		Kind    string `json:"kind"`
		Pattern string `json:"pattern"`
	} `json:"paramConstraints"`
	Location struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	} `json:"location"`
	Symbol *struct {
		Usr string `json:"usr"`
	} `json:"symbol"`
}

// usr는 사실의 핸들러 신원이다(없으면 빈 문자열).
func (f fact) usr() string {
	if f.Symbol == nil {
		return ""
	}
	return f.Symbol.Usr
}

// entry는 정답 route table 항목 하나다.
type entry struct {
	Method   string `json:"method"`
	Pattern  string `json:"pattern"`
	Sample   string `json:"sample"`
	Host     string `json:"host,omitempty"`
	Identity string `json:"identity"`
	// key는 표본 요청이 닿아야 하는 라우터 패턴 키다(matchKey 결과와 비교한다).
	key    string
	server http.Handler
	// match는 항목 전용 매처다(없으면 fixture의 matchKey) — 한 fixture에 서버가 여럿일 때 쓴다.
	match func(method, host, path string) string
}

// target은 fixture 하나다.
type target struct {
	name    string
	dir     string
	servers []http.Handler
	hosts   []string
	// matchKey는 요청이 닿는 라우터 패턴(없으면 빈 문자열)이다 — 실행하지 않고 라우터에 묻는다.
	matchKey func(method, host, path string) string
	entries  []entry
	// facts는 이 fixture의 사실이다 — strict 판정에서 뒤집은 경로를 다른 사실이 선언했는지 본다.
	facts []fact
}

// frameworkReport는 fixture 하나의 결과다.
type frameworkReport struct {
	Framework     string   `json:"framework"`
	Facts         int      `json:"staticRootFacts"`
	Verified      int      `json:"verifiedFacts"`
	DynamicFacts  int      `json:"dynamicFacts"`
	BaseFacts     int      `json:"baseAnchorFacts"`
	Entries       int      `json:"tableEntries"`
	Covered       int      `json:"coveredEntries"`
	Precision     float64  `json:"precision"`
	Recall        float64  `json:"recall"`
	Unverified    []string `json:"unverifiedFacts,omitempty"`
	Uncovered     []string `json:"uncoveredEntries,omitempty"`
	TableSample   []entry  `json:"table"`
	ExcludedTable []string `json:"excludedEntries,omitempty"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: oracle <routes.json>")
		os.Exit(2)
	}
	facts, err := readFacts(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	var reports []frameworkReport
	ok := true
	for _, t := range targets() {
		r := evaluate(t, facts)
		ok = ok && r.Precision == 1 && r.Recall == 1
		reports = append(reports, r)
	}
	data, _ := json.MarshalIndent(reports, "", "  ")
	fmt.Println(string(data))
	if !ok {
		os.Exit(1)
	}
}

// readFacts는 문서의 사실을 읽는다.
func readFacts(path string) ([]fact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Facts []fact `json:"facts"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Facts, nil
}

// targets는 네 fixture의 라우터와 정답 표를 만든다.
func targets() []target {
	return []target{muxTarget(), chiTarget(), ginTarget(), echoTarget()}
}

// muxTarget은 ServeMux fixture다. 최상위 mux, StripPrefix("/admin")로 붙인 AdminMux, DefaultServeMux.
func muxTarget() target {
	top := muxapp.NewHandler().(*http.ServeMux)
	admin := muxapp.AdminMux()
	muxapp.Register()
	t := target{name: "net/http", dir: "fixtures/muxapp/", servers: []http.Handler{top, http.DefaultServeMux},
		hosts: []string{"api.example.com"}}
	t.matchKey = func(method, host, path string) string {
		for _, mux := range []*http.ServeMux{top, http.DefaultServeMux} {
			if p := muxPattern(mux, method, host, path); p != "" && p != "/admin/" {
				return p
			}
		}
		if strings.HasPrefix(path, "/admin/") {
			return "/admin" + muxPattern(admin, method, host, strings.TrimPrefix(path, "/admin"))
		}
		return ""
	}
	mounts := map[string]bool{"/admin/": true}
	for _, mux := range []*http.ServeMux{top, http.DefaultServeMux} {
		mux := mux
		for _, p := range muxPatterns(mux) {
			if !mounts[p] {
				e := muxEntry(p, "", mux)
				e.match = func(method, host, path string) string { return muxPattern(mux, method, host, path) }
				t.entries = append(t.entries, e)
			}
		}
	}
	for _, p := range muxPatterns(admin) {
		e := muxEntry(p, "/admin", top)
		e.match = func(method, host, path string) string {
			return "/admin" + muxPattern(admin, method, host, strings.TrimPrefix(path, "/admin"))
		}
		t.entries = append(t.entries, e)
	}
	return t
}

// muxPattern은 ServeMux가 요청에 고르는 패턴이다(mux.Handler는 실행하지 않는다).
func muxPattern(mux *http.ServeMux, method, host, path string) string {
	req := httptest.NewRequest(method, "http://"+host+path, nil)
	_, pattern := mux.Handler(req)
	return pattern
}

// muxPatterns는 ServeMux 등록 색인(routingIndex의 segments·multis)의 패턴 문자열이다. 공개 목록이
// 없어 reflect로 읽기만 한다(Go 1.22+ server.go·routing_index.go의 필드 이름).
func muxPatterns(mux *http.ServeMux) []string {
	index := reflect.ValueOf(mux).Elem().FieldByName("index")
	seen := map[uintptr]string{}
	collect := func(list reflect.Value) {
		for i := 0; i < list.Len(); i++ {
			p := list.Index(i)
			seen[p.Pointer()] = p.Elem().FieldByName("str").String()
		}
	}
	segments := index.FieldByName("segments")
	for _, key := range segments.MapKeys() {
		collect(segments.MapIndex(key))
	}
	collect(index.FieldByName("multis"))
	var out []string
	for _, s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// muxEntry는 ServeMux 패턴 하나의 항목이다. prefix는 StripPrefix 마운트 접두사다.
func muxEntry(pattern, prefix string, server http.Handler) entry {
	method, rest := "ANY", pattern
	if i := strings.IndexAny(pattern, " \t"); i >= 0 {
		method, rest = pattern[:i], strings.TrimLeft(pattern[i+1:], " \t")
	}
	slash := strings.IndexByte(rest, '/')
	host, path := rest[:slash], rest[slash:]
	var b strings.Builder
	for i, seg := range strings.Split(path[1:], "/") {
		b.WriteByte('/')
		switch {
		case seg == "" && i > 0:
			b.WriteString("a/b")
		case seg == "{$}":
		case strings.HasSuffix(seg, "...}"):
			b.WriteString("a/b")
		case strings.HasPrefix(seg, "{"):
			b.WriteString("v1x")
		default:
			b.WriteString(seg)
		}
	}
	sample := prefix + b.String()
	if path == "/" {
		sample = prefix + "/a/b"
	}
	key := pattern
	if prefix != "" {
		key = prefix + pattern
	}
	return entry{Method: method, Pattern: strings.TrimSpace(prefix + " " + pattern), Sample: sample, Host: host,
		key: key, server: server}
}

// chiTarget은 chi fixture다. 정답은 chi.Walk, 매칭은 Mux.Find다.
func chiTarget() target {
	router := chiapp.NewRouter().(*chi.Mux)
	t := target{name: "chi", dir: "fixtures/chiapp/", servers: []http.Handler{router}}
	t.matchKey = func(method, host, path string) string {
		return router.Find(chi.NewRouteContext(), method, path)
	}
	_ = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		t.entries = append(t.entries, entry{Method: method, Pattern: route, Sample: chiSample(route), key: route,
			server: router})
		return nil
	})
	return t
}

// chiParam은 chi 패턴의 파라미터 토큰이다.
var chiParam = regexp.MustCompile(`\{[^{}:]+(?::((?:[^{}]|\{[^{}]*\})+))?\}`)

// chiSample은 chi 패턴의 표본 경로다: 파라미터는 정규식을 만족하는 값, `*`는 "a/b".
func chiSample(route string) string {
	s := chiParam.ReplaceAllStringFunc(route, func(tok string) string {
		m := chiParam.FindStringSubmatch(tok)
		return paramValue(m[1])
	})
	return catchAllSample(s, "*")
}

// catchAllSample은 끝 catch-all 표본이다. `/` 바로 뒤면 "a/b", 세그먼트 안(`/docs*`)이면 세그먼트
// 경계에서 이어지는 "/a/b"다(세그먼트 안 나머지는 생산자가 스코프 있는 한계로 신고한다).
func catchAllSample(s, token string) string {
	at := strings.LastIndex(s, token)
	if at < 0 {
		return s
	}
	if at > 0 && s[at-1] == '/' {
		return s[:at] + "a/b" + s[at+len(token):]
	}
	return s[:at] + "/a/b" + s[at+len(token):]
}

// paramValue는 정규식(없으면 아무 값)을 만족하는 표본 파라미터 값이다.
func paramValue(pattern string) string {
	if pattern == "" {
		return "v1x"
	}
	re := regexp.MustCompile("^(?:" + strings.TrimSuffix(strings.TrimPrefix(pattern, "^"), "$") + ")$")
	for _, candidate := range []string{"123", "v1x", "abc", "a-b"} {
		if re.MatchString(candidate) {
			return candidate
		}
	}
	return "v1x"
}

// ginTarget은 gin fixture다. 정답은 Engine.Routes(), 매칭은 같은 표로 다시 만든 엔진(재생)이다.
func ginTarget() target {
	engine := ginapp.NewRouter()
	replay := gin.New()
	t := target{name: "gin", dir: "fixtures/ginapp/", servers: []http.Handler{engine}}
	for _, route := range engine.Routes() {
		full := route.Path
		replay.Handle(route.Method, route.Path, func(c *gin.Context) { c.Header("X-Route", full) })
		t.entries = append(t.entries, entry{Method: route.Method, Pattern: route.Path,
			Sample: ginSample(route.Path), key: route.Path, server: engine})
	}
	t.matchKey = func(method, host, path string) string {
		rec := httptest.NewRecorder()
		replay.ServeHTTP(rec, httptest.NewRequest(method, "http://"+host+path, nil))
		return rec.Header().Get("X-Route")
	}
	return t
}

// ginSample은 gin 경로의 표본이다.
func ginSample(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		if at := strings.IndexAny(part, ":*"); at >= 0 {
			if part[at] == '*' {
				parts[i] = "a/b"
			} else {
				parts[i] = part[:at] + "v1x"
			}
		}
	}
	return strings.Join(parts, "/")
}

// echoTarget은 echo fixture다. 정답은 Routes()와 host 라우터의 Routes(), 매칭은 Router.Find다.
func echoTarget() target {
	e := echoapp.NewServer()
	t := target{name: "echo", dir: "fixtures/echoapp/", servers: []http.Handler{e}, hosts: echoapp.Hosts}
	routers := map[string]*echo.Router{"": e.Router()}
	for host, r := range e.Routers() {
		routers[host] = r
	}
	t.matchKey = func(method, host, path string) string {
		r, ok := routers[host]
		if !ok {
			r = routers[""]
		}
		c := e.NewContext(httptest.NewRequest(method, "http://"+host+path, nil), httptest.NewRecorder())
		r.Find(method, path, c)
		if c.Handler() == nil || c.Path() == "" {
			return ""
		}
		return c.Path()
	}
	for host, r := range routers {
		for _, route := range r.Routes() {
			if route.Method == echo.RouteNotFound {
				continue
			}
			t.entries = append(t.entries, entry{Method: route.Method, Pattern: route.Path,
				Sample: echoSample(route.Path), Host: host, key: route.Path, server: e})
		}
	}
	return t
}

// echoSample은 echo 경로의 표본이다.
func echoSample(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		if at := strings.IndexByte(part, ':'); at >= 0 {
			parts[i] = part[:at] + "v1x"
		} else if strings.HasSuffix(part, "*") && part != "*" {
			parts[i] = strings.TrimSuffix(part, "*") + "/a/b"
		} else if part == "*" {
			parts[i] = "a/b"
		}
	}
	return strings.Join(parts, "/")
}

// evaluate는 fixture 하나의 정밀도·재현율을 잰다.
func evaluate(t target, all []fact) frameworkReport {
	r := frameworkReport{Framework: t.name}
	var mine []fact
	for _, f := range all {
		if strings.HasPrefix(f.Location.Path, t.dir) {
			mine = append(mine, f)
		}
	}
	t.facts = mine
	for _, f := range mine {
		switch {
		case f.Dynamic:
			r.DynamicFacts++
		case f.PathAnchor != "root":
			r.BaseFacts++
		default:
			r.Facts++
			if problem := verifyFact(t, f); problem == "" {
				r.Verified++
			} else {
				r.Unverified = append(r.Unverified, fmt.Sprintf("%s %s (line %d): %s", f.Method, f.Channel, f.Location.Line, problem))
			}
		}
	}
	for _, e := range t.entries {
		if !contractMethod(e.Method) && e.Method != "ANY" {
			r.ExcludedTable = append(r.ExcludedTable, e.Method+" "+e.Pattern)
			continue
		}
		host := firstNonEmpty(e.Host, defaultHost)
		e.Identity = identify(e.server, e.Method, host, e.Sample)
		r.Entries++
		r.TableSample = append(r.TableSample, e)
		match := t.matchKey
		if e.match != nil {
			match = e.match
		}
		if got := match(e.Method, host, e.Sample); got != e.key {
			r.Uncovered = append(r.Uncovered, fmt.Sprintf("%s %s sample %s hit route %q (oracle sample collision)", e.Method, e.Pattern, e.Sample, got))
			continue
		}
		if covered(e, mine) {
			r.Covered++
		} else {
			r.Uncovered = append(r.Uncovered, fmt.Sprintf("%s %s sample %s identity %s", e.Method, e.Pattern, e.Sample, e.Identity))
		}
	}
	sort.Strings(r.ExcludedTable)
	sort.Slice(r.TableSample, func(i, j int) bool {
		a, b := r.TableSample[i], r.TableSample[j]
		return a.Pattern+a.Method+a.Host < b.Pattern+b.Method+b.Host
	})
	r.Precision = ratio(r.Verified, r.Facts)
	r.Recall = ratio(r.Covered, r.Entries)
	return r
}

// ratio는 비율이다(분모가 0이면 1).
func ratio(n, d int) float64 {
	if d == 0 {
		return 1
	}
	return float64(n) / float64(d)
}

// contractMethod는 계약의 동사인지 본다.
func contractMethod(m string) bool {
	for _, c := range contractMethods {
		if c == m {
			return true
		}
	}
	return false
}

// verifyFact는 사실 하나를 탐침으로 검증한다. 문제가 없으면 빈 문자열이다.
func verifyFact(t target, f fact) string {
	methods := []string{f.Method}
	if f.Method == "ANY" {
		methods = contractMethods
	}
	want := f.usr()
	for _, sample := range samples(f) {
		for _, method := range methods {
			if shadowedByExplicit(t.facts, f, method) {
				continue
			}
			if problem := verifyRequest(t, f, method, sample, want); problem != "" {
				return problem
			}
		}
	}
	return verifyConstraints(t, f, want)
}

// verifyRequest는 표본 요청 하나가 사실의 핸들러에 닿고 끝 슬래시 선언이 맞는지 본다.
func verifyRequest(t target, f fact, method, sample, want string) string {
	host, got := reach(t, f, method, sample)
	if got != want || (want == "" && t.matchKey(method, host, sample) == "") {
		return fmt.Sprintf("%s %s reached %q, want %q", method, sample, got, want)
	}
	toggled := toggleSlash(sample)
	switch f.TrailingSlash {
	case "strict":
		if reachesVia(t, method, host, toggled, sample) == want && want != "" && !declaredElsewhere(t.facts, f, method, toggled) {
			return fmt.Sprintf("strict but %s %s also reached %q", method, toggled, want)
		}
	case "optional":
		if reachesVia(t, method, host, toggled, sample) != want {
			return fmt.Sprintf("optional but %s %s did not reach %q", method, toggled, want)
		}
	}
	return ""
}

// shadowedByExplicit는 같은 템플릿의 더 구체적인 선언이 이 동사를 가져가는지 본다 — 계약은 catch-all
// 접두사 decl보다 같은 키의 명시적 decl을, ANY decl보다 같은 템플릿의 동사 decl을(ServeMux 구체성)
// match로 고르므로, 그 동사로는 이 사실의 핸들러에 닿지 않는 것이 정상이다.
func shadowedByExplicit(facts []fact, self fact, method string) bool {
	for _, g := range facts {
		if g.Channel != self.Channel || g.Dynamic || g.CatchAllPrefix || (g.Method == self.Method && g.usr() == self.usr()) {
			continue
		}
		// HEAD 호출은 GET decl에도 닿는다(계약 head-as-get, ServeMux의 "GET"도 HEAD를 받는다).
		takes := g.Method == method || (method == "HEAD" && g.Method == "GET")
		if self.CatchAllPrefix && (takes || g.Method == "ANY") {
			return true
		}
		if self.Method == "ANY" && takes {
			return true
		}
	}
	return false
}

// declaredElsewhere는 같은 핸들러·동사의 다른 사실이 경로를 선언했는지 본다 — strict는 이 사실이
// 뒤집은 경로와 맞지 않는다는 주장이지, 형제 선언(마운트의 prefix와 prefix/)까지 없다는 주장이 아니다.
func declaredElsewhere(facts []fact, self fact, method, path string) bool {
	for _, f := range facts {
		if f.Channel == self.Channel || f.Dynamic || f.usr() != self.usr() {
			continue
		}
		if (f.Method == method || f.Method == "ANY") && templateMatches(f.Channel, path) {
			return true
		}
	}
	return false
}

// reach는 사실의 host 후보와 fixture의 서버(최상위 mux·DefaultServeMux 등)로 요청을 실행해, 사실의
// 핸들러에 닿는 조합이 있으면 그것을, 없으면 처음 닿은 핸들러를 돌려준다.
func reach(t target, f fact, method, sample string) (string, string) {
	hosts := []string{defaultHost}
	if f.Narrowed {
		hosts = t.hosts
	}
	firstHost, first := hosts[0], ""
	for _, h := range hosts {
		for _, server := range t.servers {
			id := identify(server, method, h, sample)
			if id == f.usr() {
				return h, id
			}
			if first == "" && id != "" {
				firstHost, first = h, id
			}
		}
	}
	return firstHost, first
}

// reachesVia는 경로를 실행하고, 원래 표본으로의 301/307/308 리다이렉트면 따라가 닿은 핸들러를 돌려준다.
func reachesVia(t target, method, host, path, original string) string {
	for _, server := range t.servers {
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, httptest.NewRequest(method, "http://"+host+path, nil))
		if id := rec.Header().Get(mark.Header); id != "" && rec.Code < 300 {
			return normalizeIdentity(id)
		}
		if loc, err := url.Parse(rec.Header().Get("Location")); err == nil && rec.Code >= 301 && rec.Code <= 308 &&
			loc.Path == original {
			return identify(server, method, host, original)
		}
	}
	return ""
}

// verifyConstraints는 int 제약 선언을 정수가 아닌 값으로 확인한다(그 핸들러에 닿으면 틀렸다).
func verifyConstraints(t target, f fact, want string) string {
	for _, c := range f.ParamConstraints {
		if c.Kind != "int" {
			continue
		}
		bad := sampleWith(f, map[int]string{c.Segment: "zzz"})
		if _, got := reach(t, f, firstMethod(f), bad); got == want {
			return fmt.Sprintf("int constraint but %s reached %q", bad, want)
		}
	}
	return ""
}

// firstMethod는 탐침에 쓸 동사다.
func firstMethod(f fact) string {
	if f.Method == "ANY" {
		return "GET"
	}
	return f.Method
}

// identify는 요청을 실행해 fixture 핸들러 신원을 돌려준다(fixture 밖 핸들러·404는 빈 문자열).
func identify(server http.Handler, method, host, path string) string {
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(method, "http://"+host+path, nil))
	if rec.Code >= 300 {
		return ""
	}
	return normalizeIdentity(rec.Header().Get(mark.Header))
}

// closureSuffix는 runtime 함수 이름의 클로저·메서드 값 꼬리다.
var closureSuffix = regexp.MustCompile(`(\.func\d+(\.\d+)*|-fm)$`)

// normalizeIdentity는 runtime 함수 이름을 gartograph 정점 ID 모양으로 바꾼다: 클로저는 감싸는
// 선언, 포인터 리시버 `(*T)`는 `(T)`, 메서드 값 꼬리 `-fm`는 뗀다.
func normalizeIdentity(name string) string {
	for {
		trimmed := closureSuffix.ReplaceAllString(name, "")
		if trimmed == name {
			break
		}
		name = trimmed
	}
	name = strings.Replace(name, "(*", "(", 1)
	// 값 리시버 메서드는 runtime 이름이 "pkg.T.M"이다 — 패키지 함수는 "pkg.F"라 점이 하나 더 있으면 메서드다.
	slash := strings.LastIndex(name, "/")
	if parts := strings.Split(name[slash+1:], "."); len(parts) == 3 && !strings.HasPrefix(parts[1], "(") {
		name = name[:slash+1] + parts[0] + ".(" + parts[1] + ")." + parts[2]
	}
	return name
}

// samples는 사실 템플릿의 표본 경로다: `{}`는 제약을 만족하는 값, `{**}`는 한 세그먼트·두 세그먼트.
func samples(f fact) []string {
	base := sampleWith(f, nil)
	if !strings.Contains(f.Channel, "{**}") {
		return []string{base}
	}
	return []string{base, strings.Replace(base, "/a/b", "/a", 1)}
}

// sampleWith는 템플릿 표본이다. override는 세그먼트 번호 → 값이다.
func sampleWith(f fact, override map[int]string) string {
	segments := strings.Split(f.Channel[1:], "/")
	for i, seg := range segments {
		value := "v1x"
		for _, c := range f.ParamConstraints {
			if c.Segment == i {
				value = constraintValue(c.Kind, c.Pattern)
			}
		}
		if v, ok := override[i]; ok {
			value = v
		}
		switch {
		case seg == "{**}":
			segments[i] = "a/b"
		case strings.Contains(seg, "{}"):
			segments[i] = strings.Replace(seg, "{}", value, 1)
		}
	}
	return "/" + strings.Join(segments, "/")
}

// constraintValue는 제약을 만족하는 값이다.
func constraintValue(kind, pattern string) string {
	switch kind {
	case "int":
		return "123"
	case "regex":
		return paramValue(pattern)
	}
	return "v1x"
}

// toggleSlash는 끝 슬래시를 붙이거나 뗀다.
func toggleSlash(p string) string {
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		return strings.TrimSuffix(p, "/")
	}
	return p + "/"
}

// covered는 정답 항목을 덮는 사실이 있는지 본다.
func covered(e entry, facts []fact) bool {
	for _, f := range facts {
		if f.Dynamic || f.PathAnchor != "root" {
			continue
		}
		if f.Method != e.Method && f.Method != "ANY" {
			continue
		}
		if f.usr() == e.Identity && templateMatches(f.Channel, e.Sample) {
			return true
		}
	}
	return false
}

// templateMatches는 isthmus 세그먼트 매칭이다: 리터럴은 같아야 하고, `{}`는 비어 있지 않은 한
// 세그먼트, `p{}s`는 접두사·접미사 사이가 비어 있지 않은 세그먼트, `{**}`는 세그먼트 하나 이상이다.
func templateMatches(template, path string) bool {
	ts := strings.Split(template[1:], "/")
	ps := strings.Split(path[1:], "/")
	for i, t := range ts {
		if t == "{**}" {
			return len(ps) > i
		}
		if i >= len(ps) || !segmentMatches(t, ps[i]) {
			return false
		}
	}
	return len(ts) == len(ps)
}

// segmentMatches는 세그먼트 하나의 매칭이다.
func segmentMatches(t, p string) bool {
	at := strings.Index(t, "{}")
	if at < 0 {
		return t == p
	}
	prefix, suffix := t[:at], t[at+2:]
	return len(p) > len(prefix)+len(suffix) && strings.HasPrefix(p, prefix) && strings.HasSuffix(p, suffix)
}

// firstNonEmpty는 비어 있지 않은 첫 값이다.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
