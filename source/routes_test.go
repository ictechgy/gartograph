// route-decl 생산자 테스트 — 합성 fixture 모듈(프레임워크는 replace 스텁)로 사실 전체를 고정한다.
package source

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// fixedTime은 결정적 generatedAt이다.
var fixedTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// routeDoc은 fixture의 route 문서를 만든다. 문서가 계약의 자기 검증을 통과하는지도 본다.
func routeDoc(t *testing.T, dir string) *RouteFactsDocument {
	t.Helper()
	doc, err := RouteFacts(RouteOptions{Harvest: Options{Dir: dir}, GeneratedAt: fixedTime}, "test")
	if err != nil {
		t.Fatalf("RouteFacts: %v", err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	if problem := routeDocumentProblem(generic); problem != "" {
		t.Fatalf("document fails the contract self-check: %s", problem)
	}
	return doc
}

// compactFact는 사실 하나를 비교용 한 줄로 쓴다. usr는 모듈 경로를 뗀 짧은 이름이다.
func compactFact(f RouteFact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s", f.Method, f.Channel, f.PathAnchor)
	if f.Dynamic {
		b.WriteString(" dynamic")
	}
	if f.TrailingSlash != "" {
		b.WriteString(" ts=" + f.TrailingSlash)
	}
	if f.Narrowed {
		b.WriteString(" narrowed")
	}
	if f.CatchAllPrefix {
		b.WriteString(" cap")
	}
	for _, c := range f.ParamConstraints {
		fmt.Fprintf(&b, " c%d=%s", c.Segment, c.Kind)
	}
	if f.Symbol != nil {
		b.WriteString(" usr=" + f.Symbol.QualifiedName)
	}
	return b.String()
}

// compactFacts는 문서의 사실을 정렬된 한 줄 목록으로 쓴다.
func compactFacts(doc *RouteFactsDocument) []string {
	out := make([]string, len(doc.Facts))
	for i, f := range doc.Facts {
		out[i] = compactFact(f)
	}
	sort.Strings(out)
	return out
}

// assertFacts는 사실 목록이 기대와 정확히 같은지 본다.
func assertFacts(t *testing.T, doc *RouteFactsDocument, want []string) {
	t.Helper()
	sort.Strings(want)
	got := compactFacts(doc)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("facts mismatch\n got: %s\nwant: %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
}

// assertLimitation은 접두사로 시작하고 부분 문자열을 담은 한계가 있는지 보고 그 인덱스를 돌려준다.
func assertLimitation(t *testing.T, doc *RouteFactsDocument, prefix, contains string) int {
	t.Helper()
	for i, l := range doc.Limitations {
		if strings.HasPrefix(l, prefix) && strings.Contains(l, contains) {
			return i
		}
	}
	t.Fatalf("no limitation %q containing %q in %q", prefix, contains, doc.Limitations)
	return -1
}

// scopeOf는 한계 인덱스의 스코프다(없으면 nil).
func scopeOf(doc *RouteFactsDocument, index int) *LimitationScope {
	for i := range doc.LimitationScopes {
		if doc.LimitationScopes[i].LimitationIndex == index {
			return &doc.LimitationScopes[i]
		}
	}
	return nil
}

// TestRouteFactsServeMux는 Go 1.22+ ServeMux 패턴 변환·마운트·핸들러 귀속 전체를 고정한다.
func TestRouteFactsServeMux(t *testing.T) {
	dir := routeModule(t, "1.27", map[string]string{"app/app.go": `package app

import (
	"net/http"
	"os"
)

type Server struct{}

func (s *Server) getItem(w http.ResponseWriter, r *http.Request) {}
func (s *Server) files(w http.ResponseWriter, r *http.Request)   {}
func (s *Server) status(w http.ResponseWriter, r *http.Request)  {}
func (s *Server) wrapped(w http.ResponseWriter, r *http.Request) {}
func home(w http.ResponseWriter, r *http.Request)                {}
func fallback(w http.ResponseWriter, r *http.Request)            {}
func adminUsers(w http.ResponseWriter, r *http.Request)          {}
func ping(w http.ResponseWriter, r *http.Request)                {}

type obj struct{}

func (obj) ServeHTTP(w http.ResponseWriter, r *http.Request) {}

func makeMetrics() http.HandlerFunc { return func(w http.ResponseWriter, r *http.Request) {} }

func logging(next http.Handler) http.Handler { return next }

const prefix = "/v2"

func NewHandler() http.Handler {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", s.getItem)
	mux.HandleFunc("/files/{path...}", s.files)
	mux.HandleFunc("GET /{$}", home)
	mux.HandleFunc("/", fallback)
	mux.HandleFunc("api.example.com"+prefix+"/status", s.status)
	mux.Handle("/admin/", http.StripPrefix("/admin", adminMux()))
	mux.HandleFunc("DELETE /items/{id}", func(w http.ResponseWriter, r *http.Request) {})
	mux.Handle("/metrics", makeMetrics())
	mux.Handle("/wrapped", logging(http.HandlerFunc(s.wrapped)))
	mux.Handle("PUT /obj/{id}", obj{})
	mux.Handle("/fs/", http.FileServer(http.Dir(".")))
	mux.HandleFunc("PROPFIND /dav", ping)
	mux.HandleFunc("GET /a/../b", ping)
	mux.HandleFunc("/bad{x}", ping)
	mux.HandleFunc(os.Getenv("P")+"/dyn", ping)
	mux.HandleFunc("/caf%c3%a9/x%20y", ping)
	mux.HandleFunc("/sp ace", ping)
	return mux
}

func adminMux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /users", adminUsers)
	return m
}

func init() { http.HandleFunc("/legacy/ping", ping) }
`})
	doc := routeDoc(t, dir)
	assertFacts(t, doc, []string{
		"GET /items/{} root ts=strict usr=app.(Server).getItem",
		"ANY /files/{**} root usr=app.(Server).files",
		"ANY /files/ root usr=app.(Server).files",
		"GET / root usr=app.home",
		"ANY /{**} root usr=app.fallback",
		"ANY / root cap usr=app.fallback",
		"ANY /v2/status root ts=strict narrowed usr=app.(Server).status",
		"GET /admin/users root ts=strict usr=app.adminUsers",
		"DELETE /items/{} root ts=strict usr=app.NewHandler",
		"ANY /metrics root ts=strict usr=app.makeMetrics",
		"ANY /wrapped root ts=strict usr=app.(Server).wrapped",
		"PUT /obj/{} root ts=strict usr=app.(obj).ServeHTTP",
		"ANY /fs/{**} root",
		"ANY /fs/ root",
		"ANY os.Getenv(\"P\") + \"/dyn\" root dynamic usr=app.ping",
		"ANY /caf%C3%A9/x%20y root ts=strict usr=app.ping",
		"ANY /legacy/ping root ts=strict usr=app.ping",
	})
	assertLimitation(t, doc, "route-coverage:", "non-constant path patterns")
	assertLimitation(t, doc, "missing-route-usrs:", "2 route-decl fact(s)")
	assertLimitation(t, doc, "anonymous-route-handlers:", "1 route registration(s)")
	if doc.Target != "http" || doc.Dispatch != "specificity" || doc.Platform != "go" ||
		!reflect.DeepEqual(doc.Roles, []string{"server"}) || doc.SourceSets["tests"] != "excluded" {
		t.Errorf("document header = %+v", doc)
	}
}

// TestRouteFactsLegacyServeMux는 go 지시어 1.22 미만 모듈이 GODEBUG httpmuxgo121=1 기본값으로
// 옛 ServeMux 패턴(동사·와일드카드 없음, 끝 `/`는 하위 트리)을 쓰는지 본다.
func TestRouteFactsLegacyServeMux(t *testing.T) {
	dir := routeModule(t, "1.21", map[string]string{"app/app.go": `package app

import "net/http"

func h(w http.ResponseWriter, r *http.Request) {}

func Routes(mux *http.ServeMux) {
	mux.HandleFunc("/static/", h)
	mux.HandleFunc("/items/{id}", h)
	mux.HandleFunc("GET /x", h)
}

func New() *http.ServeMux { m := http.NewServeMux(); Routes(m); return m }
`})
	assertFacts(t, routeDoc(t, dir), []string{
		"ANY /static/{**} root usr=app.h",
		"ANY /static/ root usr=app.h",
		"ANY /items/%7Bid%7D root ts=strict usr=app.h",
	})
}

// TestRouteFactsChi는 chi Route·Mount·Group·With 합성, 정규식 제약, 부분 세그먼트 빈 값 변형,
// catch-all, 필드·파라미터 흐름, 동사 인자를 고정한다.
func TestRouteFactsChi(t *testing.T) {
	dir := routeModule(t, "1.27", map[string]string{"app/app.go": `package app

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
)

type Server struct{ router chi.Router }

func (s *Server) list(w http.ResponseWriter, r *http.Request)  {}
func (s *Server) get(w http.ResponseWriter, r *http.Request)   {}
func (s *Server) slug(w http.ResponseWriter, r *http.Request)  {}
func (s *Server) order(w http.ResponseWriter, r *http.Request) {}
func (s *Server) json(w http.ResponseWriter, r *http.Request)  {}
func (s *Server) files(w http.ResponseWriter, r *http.Request) {}
func (s *Server) docs(w http.ResponseWriter, r *http.Request)  {}
func (s *Server) hook(w http.ResponseWriter, r *http.Request)  {}
func (s *Server) admin(w http.ResponseWriter, r *http.Request) {}
func (s *Server) debug(w http.ResponseWriter, r *http.Request) {}
func (s *Server) dyn(w http.ResponseWriter, r *http.Request)   {}
func (s *Server) conn(w http.ResponseWriter, r *http.Request)  {}

func noop(next http.Handler) http.Handler { return next }

func New() *Server {
	s := &Server{router: chi.NewRouter()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.router.Route("/api", func(r chi.Router) {
		r.Get("/users", s.list)
		r.Get("/users/{id:[0-9]+}", s.get)
		r.Get("/users/{slug:[a-z-]+}", s.slug)
		r.Route("/orders/{id}", func(r chi.Router) {
			r.Get("/", s.order)
		})
		r.With(noop).Get("/files/{name}.json", s.json)
	})
	s.router.Group(func(r chi.Router) { r.Handle("/assets/*", http.HandlerFunc(s.files)) })
	s.router.Get("/docs*", s.docs)
	s.router.HandleFunc("POST /hooks", s.hook)
	s.router.Mount("/admin", adminRouter(s))
	s.router.Mount("/debug", http.HandlerFunc(s.debug))
	s.router.Method(os.Getenv("M"), "/dyn", http.HandlerFunc(s.dyn))
	s.router.Connect("/tunnel", s.conn)
	s.router.Get("/{a}-{b}", s.dyn)
}

func adminRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Get("/", s.admin)
	return r
}
`}, "chi")
	doc := routeDoc(t, dir)
	assertFacts(t, doc, []string{
		"GET /api/users root ts=strict usr=app.(Server).list",
		"GET /api/users/{} root ts=strict c2=int usr=app.(Server).get",
		"GET /api/users/{} root ts=strict c2=regex usr=app.(Server).slug",
		"GET /api/orders/{} root ts=strict usr=app.(Server).order",
		"GET /api/orders/{}/ root ts=strict usr=app.(Server).order",
		"GET /api/files/{}.json root ts=strict usr=app.(Server).json",
		"GET /api/files/.json root ts=strict usr=app.(Server).json",
		"ANY /assets/{**} root usr=app.(Server).files",
		"ANY /assets/ root ts=strict usr=app.(Server).files",
		"GET /docs/{**} root usr=app.(Server).docs",
		"GET /docs root ts=strict cap usr=app.(Server).docs",
		"GET /docs/ root ts=strict usr=app.(Server).docs",
		"POST /hooks root ts=strict usr=app.(Server).hook",
		"GET /admin root ts=strict usr=app.(Server).admin",
		"GET /admin/ root ts=strict usr=app.(Server).admin",
		"ANY /debug/{**} root usr=app.(Server).debug",
		"ANY /debug/ root ts=strict usr=app.(Server).debug",
		"ANY /debug root ts=strict cap usr=app.(Server).debug",
	})
	i := assertLimitation(t, doc, "route-coverage:", "method from a non-constant value")
	if sc := scopeOf(doc, i); sc == nil || !reflect.DeepEqual(sc.Templates, []string{"/dyn"}) {
		t.Errorf("non-constant method scope = %+v, want templates [/dyn]", sc)
	}
	i = assertLimitation(t, doc, "route-coverage:", "end inside a path segment")
	if sc := scopeOf(doc, i); sc == nil || !reflect.DeepEqual(sc.TemplatePrefixes, []string{"/"}) ||
		!reflect.DeepEqual(sc.Methods, []string{"GET"}) {
		t.Errorf("partial catch-all scope = %+v, want prefixes [/] methods [GET]", sc)
	}
	assertLimitation(t, doc, "route-coverage:", "several parameters in one segment")
}

// TestRouteFactsChiSlashMiddleware는 끝 슬래시 미들웨어를 쓰는 모듈의 trailingSlash를 생략하는지 본다.
func TestRouteFactsChiSlashMiddleware(t *testing.T) {
	dir := routeModule(t, "1.27", map[string]string{"app/app.go": `package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func h(w http.ResponseWriter, r *http.Request) {}

func New() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.StripSlashes)
	r.Get("/a", h)
	return r
}
`}, "chi")
	assertFacts(t, routeDoc(t, dir), []string{"GET /a root usr=app.h"})
}

// TestRouteFactsGin은 gin 그룹의 joinPaths 합성, Any·Match·Handle, 정적 파일, catch-all, 부분
// 파라미터, 핸들러 체인의 마지막 인자, RedirectTrailingSlash 기본값(optional)을 고정한다.
func TestRouteFactsGin(t *testing.T) {
	dir := routeModule(t, "1.27", map[string]string{"app/app.go": `package app

import (
	"os"

	"github.com/gin-gonic/gin"
)

type H struct{}

func (H) user(c *gin.Context)  {}
func (H) files(c *gin.Context) {}
func (H) any(c *gin.Context)   {}
func (H) match(c *gin.Context) {}
func (H) patch(c *gin.Context) {}
func (H) av(c *gin.Context)    {}
func (H) set(c *gin.Context)   {}
func (H) rep(c *gin.Context)   {}
func auth(c *gin.Context)      {}

func New() *gin.Engine {
	h := H{}
	r := gin.Default()
	v1 := r.Group("/v1")
	v1.GET("/users/:id.json", auth, h.user)
	v1.GET("/files/*filepath", h.files)
	v1.GET("/avatar_:name", h.av)
	admin := v1.Group("admin/../adm/")
	admin.PUT("settings/", h.set)
	admin.Any("", h.any)
	r.Match([]string{"GET", "post"}, "/match", h.match)
	r.Match([]string{os.Getenv("M")}, "/dyn", h.match)
	r.Handle("PATCH", "/items/:id", h.patch)
	r.StaticFile("/favicon.ico", "./f")
	r.Static("/assets", "./a")
	register(v1.Group("/reports"), h)
	return r
}

func register(g *gin.RouterGroup, h H) { g.GET("/:year", h.rep) }
`}, "gin")
	doc := routeDoc(t, dir)
	assertFacts(t, doc, []string{
		"GET /v1/users/{} root ts=optional usr=app.(H).user",
		"GET /v1/files/{**} root usr=app.(H).files",
		"GET /v1/files/ root ts=optional usr=app.(H).files",
		"GET /v1/avatar_{} root ts=optional usr=app.(H).av",
		"PUT /v1/adm/settings/ root ts=optional usr=app.(H).set",
		"ANY /v1/adm/ root ts=optional usr=app.(H).any",
		"GET /match root ts=optional usr=app.(H).match",
		"POST /match root ts=optional usr=app.(H).match",
		"PATCH /items/{} root ts=optional usr=app.(H).patch",
		"GET /favicon.ico root ts=optional",
		"HEAD /favicon.ico root ts=optional",
		"GET /assets/{**} root",
		"HEAD /assets/{**} root",
		"GET /assets/ root ts=optional",
		"HEAD /assets/ root ts=optional",
		"GET /v1/reports/{} root ts=optional usr=app.(H).rep",
	})
	i := assertLimitation(t, doc, "route-coverage:", "method from a non-constant value")
	if sc := scopeOf(doc, i); sc == nil || !reflect.DeepEqual(sc.Templates, []string{"/dyn"}) {
		t.Errorf("non-constant Match scope = %+v", sc)
	}
}

// TestRouteFactsGinRedirectDisabled는 RedirectTrailingSlash = false인 엔진의 decl이 strict인지 본다.
func TestRouteFactsGinRedirectDisabled(t *testing.T) {
	dir := routeModule(t, "1.27", map[string]string{"app/app.go": `package app

import "github.com/gin-gonic/gin"

func h(c *gin.Context) {}

func New() (*gin.Engine, *gin.Engine) {
	strict := gin.New()
	strict.RedirectTrailingSlash = false
	strict.GET("/strict", h)
	loose := gin.New()
	loose.GET("/loose", h)
	return strict, loose
}
`}, "gin")
	assertFacts(t, routeDoc(t, dir), []string{
		"GET /strict root ts=strict usr=app.h",
		"GET /loose root ts=optional usr=app.h",
	})
}

// TestRouteFactsEcho는 echo 그룹 문자열 연결, Host(narrowed), 끝 파라미터 leaf(optional과 스코프
// 있는 한계), 정적 파일의 부분 catch-all, 이스케이프한 콜론, 중간 `*`(정규 템플릿 아님)를 고정한다.
func TestRouteFactsEcho(t *testing.T) {
	dir := routeModule(t, "1.27", map[string]string{"app/app.go": `package app

import "github.com/labstack/echo/v4"

func user(c echo.Context) error  { return nil }
func posts(c echo.Context) error { return nil }
func order(c echo.Context) error { return nil }
func files(c echo.Context) error { return nil }
func dash(c echo.Context) error  { return nil }
func lit(c echo.Context) error   { return nil }
func all(c echo.Context) error   { return nil }

func New() *echo.Echo {
	e := echo.New()
	e.GET("/users/:id", user)
	e.GET("/users/:id/posts", posts)
	e.GET("/orders/:id", order)
	g := e.Group("/api/")
	g.GET("files/*", files)
	e.Host("admin.example.com").GET("/dash", dash)
	e.GET("/a\\:b", lit)
	e.Any("/any", all)
	e.Static("/static", "public")
	e.GET("/x/*/y", lit)
	return e
}
`}, "echo")
	doc := routeDoc(t, dir)
	assertFacts(t, doc, []string{
		"GET /users/{} root ts=strict usr=app.user",
		"GET /users/{}/posts root ts=strict usr=app.posts",
		"GET /orders/{} root ts=optional usr=app.order",
		"GET /api/files/{**} root usr=app.files",
		"GET /api/files/ root ts=strict usr=app.files",
		"GET /dash root ts=strict narrowed usr=app.dash",
		"GET /a:b root ts=strict usr=app.lit",
		"ANY /any root ts=strict usr=app.all",
		"GET /static/{**} root",
		"GET /static root ts=strict",
		"GET /static/ root ts=strict",
	})
	i := assertLimitation(t, doc, "route-coverage:", "echo route(s) end in a path parameter")
	if sc := scopeOf(doc, i); sc == nil || !reflect.DeepEqual(sc.TemplatePrefixes, []string{"/orders/{}"}) ||
		!reflect.DeepEqual(sc.Methods, []string{"GET"}) {
		t.Errorf("echo leaf scope = %+v, want prefixes [/orders/{}] methods [GET]", sc)
	}
	assertLimitation(t, doc, "route-coverage:", "several parameters in one segment or a mid-path wildcard")
}

// TestRouteFactsUnresolvedPrefix는 모듈 안에서 호출되지 않는 등록 함수의 라우터를 base 앵커와
// 접미사 스코프의 unresolved-route-prefix로 내는지 본다.
func TestRouteFactsUnresolvedPrefix(t *testing.T) {
	dir := routeModule(t, "1.27", map[string]string{"app/app.go": `package app

import "github.com/gin-gonic/gin"

func h(c *gin.Context) {}

// Register는 다른 모듈이 부른다 — 이 모듈 안에는 호출자가 없다.
func Register(g *gin.RouterGroup) {
	g.GET("/users/:id", h)
	g.Group("/v2").GET("/all/*rest", h)
}
`}, "gin")
	doc := routeDoc(t, dir)
	assertFacts(t, doc, []string{
		"GET /users/{} base usr=app.h",
		"GET /v2/all/{**} base usr=app.h",
		"GET /v2/all/ base usr=app.h",
	})
	i := assertLimitation(t, doc, "unresolved-route-prefix:", "3 route declaration(s)")
	if sc := scopeOf(doc, i); sc != nil {
		t.Errorf("a {**} base template cannot be a suffix; scope must be omitted, got %+v", sc)
	}
}

// TestRouteFactsUnsupportedRouter는 수확하지 않는 라우터 import를 서버 측 공백으로 신고하는지 본다.
func TestRouteFactsUnsupportedRouter(t *testing.T) {
	dir := routeModule(t, "1.27", map[string]string{"app/app.go": `package app

import "github.com/gorilla/mux"

var R = mux.NewRouter()
`}, "mux")
	doc := routeDoc(t, dir)
	if len(doc.Facts) != 0 {
		t.Errorf("facts = %v, want none", compactFacts(doc))
	}
	assertLimitation(t, doc, "route-coverage:", "github.com/gorilla/mux")
}

// TestRouteFactsDeterministic는 같은 입력이 같은 바이트인지 본다.
func TestRouteFactsDeterministic(t *testing.T) {
	dir := routeModule(t, "1.27", map[string]string{"app/app.go": `package app

import "net/http"

func a(w http.ResponseWriter, r *http.Request) {}

func New() *http.ServeMux {
	m := http.NewServeMux()
	for _, p := range []string{"/x"} {
		_ = p
	}
	m.HandleFunc("/b/{x...}", a)
	m.HandleFunc("GET /a/{id}", a)
	return m
}
`})
	first, _ := json.Marshal(routeDoc(t, dir))
	second, _ := json.Marshal(routeDoc(t, dir))
	if string(first) != string(second) {
		t.Errorf("non-deterministic output:\n%s\n%s", first, second)
	}
}
