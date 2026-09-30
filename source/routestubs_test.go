// route 수확 테스트용 프레임워크 스텁 — 실제 모듈과 같은 import 경로·타입·시그니처만 둔 합성 모듈이다.
//
// 테스트는 네트워크 없이 돌아야 하므로 go.mod replace로 이 스텁을 심는다. 수확은 타입(패키지 경로,
// 리시버 타입, 이름)만 보므로 본문은 비어 있어도 된다. 실제 라우팅 의미론은 experiments/routes-oracle이
// proxy.golang.org에서 받은 실제 모듈로 확인한다.
package source

import (
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// chiStub은 github.com/go-chi/chi/v5의 라우팅 API 스텁이다.
const chiStub = `package chi

import "net/http"

type Middlewares []func(http.Handler) http.Handler

type Router interface {
	http.Handler
	Use(middlewares ...func(http.Handler) http.Handler)
	With(middlewares ...func(http.Handler) http.Handler) Router
	Group(fn func(r Router)) Router
	Route(pattern string, fn func(r Router)) Router
	Mount(pattern string, h http.Handler)
	Handle(pattern string, h http.Handler)
	HandleFunc(pattern string, h http.HandlerFunc)
	Method(method, pattern string, h http.Handler)
	MethodFunc(method, pattern string, h http.HandlerFunc)
	Connect(pattern string, h http.HandlerFunc)
	Delete(pattern string, h http.HandlerFunc)
	Get(pattern string, h http.HandlerFunc)
	Head(pattern string, h http.HandlerFunc)
	Options(pattern string, h http.HandlerFunc)
	Patch(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
	Put(pattern string, h http.HandlerFunc)
	Trace(pattern string, h http.HandlerFunc)
}

type Mux struct{}

func NewRouter() *Mux { return &Mux{} }
func NewMux() *Mux    { return &Mux{} }

func (mx *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request)                 {}
func (mx *Mux) Use(middlewares ...func(http.Handler) http.Handler)               {}
func (mx *Mux) With(middlewares ...func(http.Handler) http.Handler) Router       { return mx }
func (mx *Mux) Group(fn func(r Router)) Router                                   { return mx }
func (mx *Mux) Route(pattern string, fn func(r Router)) Router                   { return mx }
func (mx *Mux) Mount(pattern string, h http.Handler)                             {}
func (mx *Mux) Handle(pattern string, h http.Handler)                            {}
func (mx *Mux) HandleFunc(pattern string, h http.HandlerFunc)                    {}
func (mx *Mux) Method(method, pattern string, h http.Handler)                    {}
func (mx *Mux) MethodFunc(method, pattern string, h http.HandlerFunc)            {}
func (mx *Mux) Connect(pattern string, h http.HandlerFunc)                       {}
func (mx *Mux) Delete(pattern string, h http.HandlerFunc)                        {}
func (mx *Mux) Get(pattern string, h http.HandlerFunc)                           {}
func (mx *Mux) Head(pattern string, h http.HandlerFunc)                          {}
func (mx *Mux) Options(pattern string, h http.HandlerFunc)                       {}
func (mx *Mux) Patch(pattern string, h http.HandlerFunc)                         {}
func (mx *Mux) Post(pattern string, h http.HandlerFunc)                          {}
func (mx *Mux) Put(pattern string, h http.HandlerFunc)                           {}
func (mx *Mux) Trace(pattern string, h http.HandlerFunc)                         {}
`

// chiMiddlewareStub은 끝 슬래시 미들웨어 스텁이다.
const chiMiddlewareStub = `package middleware

import "net/http"

func StripSlashes(next http.Handler) http.Handler { return next }
`

// ginStub은 github.com/gin-gonic/gin의 라우팅 API 스텁이다.
const ginStub = `package gin

import "net/http"

type Context struct{}
type HandlerFunc func(*Context)

type IRoutes interface {
	Use(...HandlerFunc) IRoutes
	Handle(string, string, ...HandlerFunc) IRoutes
	Any(string, ...HandlerFunc) IRoutes
	GET(string, ...HandlerFunc) IRoutes
	POST(string, ...HandlerFunc) IRoutes
	DELETE(string, ...HandlerFunc) IRoutes
	PATCH(string, ...HandlerFunc) IRoutes
	PUT(string, ...HandlerFunc) IRoutes
	OPTIONS(string, ...HandlerFunc) IRoutes
	HEAD(string, ...HandlerFunc) IRoutes
	Match([]string, string, ...HandlerFunc) IRoutes
	StaticFile(string, string) IRoutes
	Static(string, string) IRoutes
}

type IRouter interface {
	IRoutes
	Group(string, ...HandlerFunc) *RouterGroup
}

type RouterGroup struct{}

func (g *RouterGroup) Use(h ...HandlerFunc) IRoutes                            { return g }
func (g *RouterGroup) Group(p string, h ...HandlerFunc) *RouterGroup           { return g }
func (g *RouterGroup) Handle(m, p string, h ...HandlerFunc) IRoutes            { return g }
func (g *RouterGroup) Any(p string, h ...HandlerFunc) IRoutes                  { return g }
func (g *RouterGroup) GET(p string, h ...HandlerFunc) IRoutes                  { return g }
func (g *RouterGroup) POST(p string, h ...HandlerFunc) IRoutes                 { return g }
func (g *RouterGroup) DELETE(p string, h ...HandlerFunc) IRoutes               { return g }
func (g *RouterGroup) PATCH(p string, h ...HandlerFunc) IRoutes                { return g }
func (g *RouterGroup) PUT(p string, h ...HandlerFunc) IRoutes                  { return g }
func (g *RouterGroup) OPTIONS(p string, h ...HandlerFunc) IRoutes              { return g }
func (g *RouterGroup) HEAD(p string, h ...HandlerFunc) IRoutes                 { return g }
func (g *RouterGroup) Match(ms []string, p string, h ...HandlerFunc) IRoutes   { return g }
func (g *RouterGroup) StaticFile(p, f string) IRoutes                          { return g }
func (g *RouterGroup) Static(p, root string) IRoutes                           { return g }

type Engine struct {
	RouterGroup
	RedirectTrailingSlash bool
}

func New() *Engine     { return &Engine{} }
func Default() *Engine { return &Engine{} }

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {}
`

// echoStub은 github.com/labstack/echo/v4의 라우팅 API 스텁이다.
const echoStub = `package echo

import "net/http"

type Context interface{ Path() string }
type HandlerFunc func(Context) error
type MiddlewareFunc func(HandlerFunc) HandlerFunc
type Route struct{ Method, Path string }

type Echo struct{}
type Group struct{}

func New() *Echo { return &Echo{} }

func (e *Echo) ServeHTTP(w http.ResponseWriter, r *http.Request)                          {}
func (e *Echo) Pre(m ...MiddlewareFunc)                                                     {}
func (e *Echo) GET(p string, h HandlerFunc, m ...MiddlewareFunc) *Route                     { return nil }
func (e *Echo) POST(p string, h HandlerFunc, m ...MiddlewareFunc) *Route                    { return nil }
func (e *Echo) PUT(p string, h HandlerFunc, m ...MiddlewareFunc) *Route                     { return nil }
func (e *Echo) DELETE(p string, h HandlerFunc, m ...MiddlewareFunc) *Route                  { return nil }
func (e *Echo) Add(method, p string, h HandlerFunc, m ...MiddlewareFunc) *Route             { return nil }
func (e *Echo) Any(p string, h HandlerFunc, m ...MiddlewareFunc) []*Route                   { return nil }
func (e *Echo) Match(ms []string, p string, h HandlerFunc, m ...MiddlewareFunc) []*Route    { return nil }
func (e *Echo) Group(prefix string, m ...MiddlewareFunc) *Group                             { return nil }
func (e *Echo) Host(name string, m ...MiddlewareFunc) *Group                                { return nil }
func (e *Echo) Static(prefix, root string) *Route                                           { return nil }
func (e *Echo) File(p, file string, m ...MiddlewareFunc) *Route                             { return nil }
func (g *Group) Use(m ...MiddlewareFunc)                                                    {}
func (g *Group) GET(p string, h HandlerFunc, m ...MiddlewareFunc) *Route                    { return nil }
func (g *Group) POST(p string, h HandlerFunc, m ...MiddlewareFunc) *Route                   { return nil }
func (g *Group) Group(prefix string, m ...MiddlewareFunc) *Group                            { return nil }
`

// echoMiddlewareStub은 echo 끝 슬래시 미들웨어 스텁이다.
const echoMiddlewareStub = `package middleware

import "github.com/labstack/echo/v4"

func RemoveTrailingSlash() echo.MiddlewareFunc { return nil }
`

// routeModule은 앱 파일과 필요한 프레임워크 스텁(replace)을 담은 fixture 모듈을 만든다.
// frameworks는 "chi"·"gin"·"echo"·"mux"(gorilla, 지원하지 않는 라우터) 중 필요한 것이다.
func routeModule(t *testing.T, goVersion string, app map[string]string, frameworks ...string) string {
	t.Helper()
	files := map[string]string{}
	for name, content := range app {
		files[name] = content
	}
	var requires, replaces strings.Builder
	add := func(path, dir string, stubs map[string]string) {
		version := "v0.0.0"
		if strings.HasSuffix(path, "/v5") {
			version = "v5.0.0"
		} else if strings.HasSuffix(path, "/v4") {
			version = "v4.0.0"
		}
		requires.WriteString("\t" + path + " " + version + "\n")
		replaces.WriteString("replace " + path + " => ./" + dir + "\n")
		files[dir+"/go.mod"] = "module " + path + "\n\ngo 1.22\n"
		for name, content := range stubs {
			files[dir+"/"+name] = content
		}
	}
	for _, fw := range frameworks {
		switch fw {
		case "chi":
			add("github.com/go-chi/chi/v5", "chistub", map[string]string{
				"chi.go": chiStub, "middleware/middleware.go": chiMiddlewareStub})
		case "gin":
			add("github.com/gin-gonic/gin", "ginstub", map[string]string{"gin.go": ginStub})
		case "echo":
			add("github.com/labstack/echo/v4", "echostub", map[string]string{
				"echo.go": echoStub, "middleware/middleware.go": echoMiddlewareStub})
		case "mux":
			add("github.com/gorilla/mux", "muxstub", map[string]string{
				"mux.go": "package mux\n\ntype Router struct{}\n\nfunc NewRouter() *Router { return &Router{} }\n"})
		}
	}
	gomod := "module example.com/fixture\n\ngo " + goVersion + "\n"
	if requires.Len() > 0 {
		gomod += "\nrequire (\n" + requires.String() + ")\n\n" + replaces.String()
	}
	files["go.mod"] = gomod
	return testutil.WriteModule(t, files)
}
