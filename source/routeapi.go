// isthmus http route-decl 생산자가 알아보는 라우터 API 표.
//
// 호출은 이름이 아니라 타입으로 확인한다 — types.Info가 가리키는 피호출 함수의
// (패키지 경로, 리시버 타입, 이름)이 표에 있을 때만 라우터 호출이다. 인터페이스(chi.Router,
// gin.IRouter)로 부르는 호출은 인터페이스 메서드가, 임베드로 승격된 메서드(gin.Engine의
// GET)는 선언한 타입(RouterGroup)의 메서드가 피호출자다.
//
// 검증한 소스(각 프레임워크의 공식 모듈, proxy.golang.org에서 받은 사본):
//   - net/http(Go 1.27.1 표준 라이브러리) pattern.go·routing_tree.go·server.go
//   - github.com/go-chi/chi/v5 v5.2.5 mux.go·tree.go
//   - github.com/gin-gonic/gin v1.10.1 routergroup.go·tree.go·gin.go·utils.go
//   - github.com/labstack/echo/v4 v4.16.0 echo.go·group.go·router.go·echo_fs.go·group_fs.go
package source

import (
	"go/types"
	"strings"
)

// routeFramework는 라우터 구현이다. 템플릿 변환·디스패치 규칙이 구현마다 다르다.
type routeFramework string

// 지원하는 라우터 구현이다.
const (
	fwServeMux routeFramework = "net/http"
	fwChi      routeFramework = "chi"
	fwGin      routeFramework = "gin"
	fwEcho     routeFramework = "echo"
)

// 프레임워크 모듈의 import 경로다.
const (
	chiPath  = "github.com/go-chi/chi/v5"
	ginPath  = "github.com/gin-gonic/gin"
	echoPath = "github.com/labstack/echo/v4"
)

// regKind는 라우터 호출의 역할이다.
type regKind int

const (
	regRoute  regKind = iota // 경로 하나를 핸들러에 묶는다
	regDerive                // 접두사가 붙은(또는 같은) 하위 라우터를 만든다
	regMount                 // 다른 핸들러·라우터를 접두사 아래에 붙인다
	regStatic                // 프레임워크 정적 파일 핸들러를 접두사 아래에 붙인다
	regFile                  // 프레임워크 파일 핸들러를 경로 하나에 붙인다
)

// deriveKind는 하위 라우터가 부모 경로를 물려받는 방식이다.
type deriveKind int

const (
	deriveChiRoute  deriveKind = iota + 1 // chi Route: 새 Mux를 접두사에 Mount한다
	deriveChiInline                       // chi Group·With: 같은 트리의 inline Mux
	deriveGinGroup                        // gin Group: joinPaths(base, rel)
	deriveEchoGroup                       // echo Group: 접두사 문자열을 이어 붙인다
	deriveEchoHost                        // echo Host: host 전용 라우터
)

// regSpec은 라우터 호출 하나의 인자 규칙이다. 인자 번호가 -1이면 그 인자가 없다.
type regSpec struct {
	fw   routeFramework
	kind regKind
	// method는 고정 동사다. 빈 문자열이면 methodArg·methodsArg에서 읽는다.
	// "ANY"는 모든 동사, "PATTERN"은 ServeMux·chi Handle처럼 패턴 앞의 동사를 쓴다.
	method     string
	methodArg  int
	methodsArg int
	pathArg    int
	// handlerArg는 핸들러 인자다. -2는 가변 인자의 마지막(gin 핸들러 체인의 끝)이다.
	handlerArg int
	derive     deriveKind
	// fnArg는 chi Route·Group의 하위 라우터 콜백 인자다.
	fnArg int
	// defaultMux는 net/http 패키지 함수(http.Handle)처럼 DefaultServeMux에 등록하는 호출이다.
	defaultMux bool
	// staticMethods는 정적 핸들러가 받는 동사다(gin은 GET·HEAD, echo는 GET).
	staticMethods []string
}

// handlerLast는 가변 인자 핸들러 체인의 마지막 인자다.
const handlerLast = -2

// route는 고정 동사 경로 등록 규칙을 만든다.
func route(fw routeFramework, method string, pathArg, handlerArg int) regSpec {
	return regSpec{fw: fw, kind: regRoute, method: method, methodArg: -1, methodsArg: -1,
		pathArg: pathArg, handlerArg: handlerArg, fnArg: -1}
}

// routeWithMethodArg는 동사를 인자로 받는 경로 등록 규칙을 만든다.
func routeWithMethodArg(fw routeFramework, methodArg, pathArg, handlerArg int) regSpec {
	spec := route(fw, "", pathArg, handlerArg)
	spec.methodArg = methodArg
	return spec
}

// routeWithMethodsArg는 동사 목록을 인자로 받는 경로 등록 규칙(Match)을 만든다.
func routeWithMethodsArg(fw routeFramework, methodsArg, pathArg, handlerArg int) regSpec {
	spec := route(fw, "", pathArg, handlerArg)
	spec.methodsArg = methodsArg
	return spec
}

// derive는 하위 라우터 규칙을 만든다.
func derive(fw routeFramework, kind deriveKind, pathArg, fnArg int) regSpec {
	return regSpec{fw: fw, kind: regDerive, derive: kind, methodArg: -1, methodsArg: -1,
		pathArg: pathArg, handlerArg: -1, fnArg: fnArg}
}

// static은 정적 파일 규칙을 만든다.
func static(fw routeFramework, kind regKind, pathArg int, methods ...string) regSpec {
	return regSpec{fw: fw, kind: kind, methodArg: -1, methodsArg: -1, pathArg: pathArg,
		handlerArg: -1, fnArg: -1, staticMethods: methods}
}

// routeAPI는 "패키지 경로.리시버 타입.이름" → 규칙이다. 패키지 함수는 리시버 자리가 비어 있다.
var routeAPI = buildRouteAPI()

// buildRouteAPI는 네 구현의 호출 규칙 표를 만든다.
func buildRouteAPI() map[string]regSpec {
	api := map[string]regSpec{}
	addServeMuxAPI(api)
	addChiAPI(api)
	addGinAPI(api)
	addEchoAPI(api)
	return api
}

// addServeMuxAPI는 net/http ServeMux 등록을 더한다. http.Handle·HandleFunc는 DefaultServeMux다.
func addServeMuxAPI(api map[string]regSpec) {
	for _, name := range []string{"Handle", "HandleFunc"} {
		spec := route(fwServeMux, "PATTERN", 0, 1)
		api["net/http.ServeMux."+name] = spec
		spec.defaultMux = true
		api["net/http.."+name] = spec
	}
}

// addChiAPI는 chi Mux와 Router 인터페이스의 호출을 더한다(mux.go·chi.go).
func addChiAPI(api map[string]regSpec) {
	for _, recv := range []string{"Mux", "Router"} {
		prefix := chiPath + "." + recv + "."
		for _, name := range []string{"Get", "Post", "Put", "Patch", "Delete", "Head", "Options", "Trace", "Connect"} {
			api[prefix+name] = route(fwChi, strings.ToUpper(name), 0, 1)
		}
		// Handle·HandleFunc는 "METHOD pattern"이면 그 동사, 아니면 모든 동사(mALL)다.
		api[prefix+"Handle"] = route(fwChi, "PATTERN", 0, 1)
		api[prefix+"HandleFunc"] = route(fwChi, "PATTERN", 0, 1)
		api[prefix+"Method"] = routeWithMethodArg(fwChi, 0, 1, 2)
		api[prefix+"MethodFunc"] = routeWithMethodArg(fwChi, 0, 1, 2)
		api[prefix+"Route"] = derive(fwChi, deriveChiRoute, 0, 1)
		api[prefix+"Group"] = derive(fwChi, deriveChiInline, -1, 0)
		api[prefix+"With"] = derive(fwChi, deriveChiInline, -1, -1)
		api[prefix+"Mount"] = regSpec{fw: fwChi, kind: regMount, methodArg: -1, methodsArg: -1,
			pathArg: 0, handlerArg: 1, fnArg: -1}
	}
}

// addGinAPI는 gin RouterGroup과 IRoutes·IRouter 인터페이스의 호출을 더한다(routergroup.go).
// Engine은 RouterGroup을 임베드하므로 engine.GET의 피호출자는 RouterGroup.GET이다.
func addGinAPI(api map[string]regSpec) {
	for _, recv := range []string{"RouterGroup", "IRoutes", "IRouter"} {
		prefix := ginPath + "." + recv + "."
		for _, name := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
			api[prefix+name] = route(fwGin, name, 0, handlerLast)
		}
		api[prefix+"Handle"] = routeWithMethodArg(fwGin, 0, 1, handlerLast)
		// Any는 anyMethods(GET·POST·PUT·PATCH·HEAD·OPTIONS·DELETE·CONNECT·TRACE)를 등록한다 —
		// 계약의 동사 집합 전체를 덮으므로 ANY다.
		api[prefix+"Any"] = route(fwGin, "ANY", 0, handlerLast)
		api[prefix+"Match"] = routeWithMethodsArg(fwGin, 0, 1, handlerLast)
		api[prefix+"Group"] = derive(fwGin, deriveGinGroup, 0, -1)
		// Static·StaticFS는 path.Join(rel, "/*filepath")에 GET·HEAD, StaticFile은 경로에 GET·HEAD다.
		api[prefix+"Static"] = static(fwGin, regStatic, 0, "GET", "HEAD")
		api[prefix+"StaticFS"] = static(fwGin, regStatic, 0, "GET", "HEAD")
		api[prefix+"StaticFile"] = static(fwGin, regFile, 0, "GET", "HEAD")
		api[prefix+"StaticFileFS"] = static(fwGin, regFile, 0, "GET", "HEAD")
	}
}

// addEchoAPI는 echo Echo·Group의 호출을 더한다(echo.go·group.go·echo_fs.go·group_fs.go).
func addEchoAPI(api map[string]regSpec) {
	for _, recv := range []string{"Echo", "Group"} {
		prefix := echoPath + "." + recv + "."
		for _, name := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT"} {
			api[prefix+name] = route(fwEcho, name, 0, 1)
		}
		api[prefix+"Add"] = routeWithMethodArg(fwEcho, 0, 1, 2)
		// Any는 methods(CONNECT·DELETE·GET·HEAD·OPTIONS·PATCH·POST·PROPFIND·PUT·TRACE·REPORT)를
		// 등록한다 — 계약의 동사 집합 전체를 덮으므로 ANY다.
		api[prefix+"Any"] = route(fwEcho, "ANY", 0, 1)
		api[prefix+"Match"] = routeWithMethodsArg(fwEcho, 0, 1, 2)
		api[prefix+"Group"] = derive(fwEcho, deriveEchoGroup, 0, -1)
		// Static·StaticFS는 접두사+"*"에 GET만, File·FileFS는 경로에 GET만 등록한다.
		api[prefix+"Static"] = static(fwEcho, regStatic, 0, "GET")
		api[prefix+"StaticFS"] = static(fwEcho, regStatic, 0, "GET")
		api[prefix+"File"] = static(fwEcho, regFile, 0, "GET")
		api[prefix+"FileFS"] = static(fwEcho, regFile, 0, "GET")
	}
	api[echoPath+".Echo.Host"] = derive(fwEcho, deriveEchoHost, 0, -1)
}

// routerConstructors는 새 라우터(루트)를 만드는 함수다.
var routerConstructors = map[string]routeFramework{
	"net/http..NewServeMux": fwServeMux,
	chiPath + "..NewRouter": fwChi,
	chiPath + "..NewMux":    fwChi,
	ginPath + "..New":       fwGin,
	ginPath + "..Default":   fwGin,
	echoPath + "..New":      fwEcho,
}

// routerTypes는 흐름 분석이 값을 추적하는 라우터·핸들러 타입이다("패키지 경로.이름").
// http.Handler를 넣는 이유: 라우터를 핸들러 변수·필드에 담아 Mount·Handle로 넘기는 흔한 구성을
// 따라가기 위해서다.
var routerTypes = map[string]bool{
	"net/http.ServeMux":      true,
	"net/http.Handler":       true,
	chiPath + ".Mux":         true,
	chiPath + ".Router":      true,
	ginPath + ".Engine":      true,
	ginPath + ".RouterGroup": true,
	ginPath + ".IRouter":     true,
	ginPath + ".IRoutes":     true,
	echoPath + ".Echo":       true,
	echoPath + ".Group":      true,
}

// unsupportedRouters는 route를 수확하지 않는 라우터 패키지다. 이것을 import하는 저장소의
// 문서는 "스캔했으나 없음"이 아니다 — 서버 측 공백(route-coverage)으로 신고한다.
var unsupportedRouters = []string{
	"github.com/gorilla/mux",
	"github.com/julienschmidt/httprouter",
	"github.com/go-chi/chi",
	"github.com/labstack/echo",
	"github.com/labstack/echo/v5",
	"github.com/gofiber/fiber",
	"github.com/gofiber/fiber/v2",
	"github.com/gofiber/fiber/v3",
	"github.com/beego/beego/v2/server/web",
	"github.com/astaxie/beego",
	"github.com/kataras/iris/v12",
	"github.com/revel/revel",
	"github.com/cloudwego/hertz/pkg/app/server",
	"github.com/zeromicro/go-zero/rest",
	"github.com/danielgtaylor/huma/v2",
	"github.com/grpc-ecosystem/grpc-gateway/runtime",
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime",
	"github.com/bmizerany/pat",
	"github.com/go-martini/martini",
	"github.com/emicklei/go-restful",
	"github.com/emicklei/go-restful/v3",
	"goa.design/goa/v3/http",
	"github.com/uptrace/bunrouter",
	"github.com/fasthttp/router",
	"github.com/dimfeld/httptreemux",
	"github.com/dimfeld/httptreemux/v5",
}

// trailingSlashMiddleware는 요청 경로의 끝 슬래시를 라우팅 전에 바꾸는 미들웨어다. 모듈이 이것을
// 쓰면 그 프레임워크 decl의 trailingSlash를 증명할 수 없어 생략한다.
var trailingSlashMiddleware = map[string]routeFramework{
	chiPath + "/middleware.StripSlashes":                   fwChi,
	chiPath + "/middleware.RedirectSlashes":                fwChi,
	echoPath + "/middleware.AddTrailingSlash":              fwEcho,
	echoPath + "/middleware.AddTrailingSlashWithConfig":    fwEcho,
	echoPath + "/middleware.RemoveTrailingSlash":           fwEcho,
	echoPath + "/middleware.RemoveTrailingSlashWithConfig": fwEcho,
}

// funcKey는 함수 객체의 표 조회 키("패키지 경로.리시버 타입.이름")다.
func funcKey(f *types.Func) string {
	if f == nil || f.Pkg() == nil {
		return ""
	}
	recv := ""
	if sig := f.Signature(); sig != nil && sig.Recv() != nil {
		recv = namedTypeName(sig.Recv().Type())
	}
	return f.Pkg().Path() + "." + recv + "." + f.Name()
}

// namedTypeName은 포인터·별칭을 벗긴 명명 타입의 이름이다(없으면 빈 문자열).
func namedTypeName(t types.Type) string {
	t = types.Unalias(t)
	if ptr, ok := t.(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}

// typeKey는 명명 타입의 "패키지 경로.이름"이다(포인터·별칭을 벗긴다).
func typeKey(t types.Type) string {
	t = types.Unalias(t)
	if ptr, ok := t.(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return ""
	}
	return named.Obj().Pkg().Path() + "." + named.Obj().Name()
}

// isRouterType은 흐름 분석이 추적할 타입인지 본다.
func isRouterType(t types.Type) bool {
	return t != nil && routerTypes[typeKey(t)]
}
