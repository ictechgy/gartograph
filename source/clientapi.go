// isthmus http route-call 생산자가 알아보는 HTTP 클라이언트 API 표.
//
// route-decl 표(routeapi.go)와 같이 호출은 타입으로 확인한다 — 피호출 함수의 (패키지 경로, 리시버 타입,
// 이름)이 표에 있을 때만 요청이다. 검증한 소스:
//   - net/http(Go 1.27.1) client.go(Get·Head·Post·PostForm, Client의 같은 메서드)·request.go
//     (NewRequest·NewRequestWithContext — 빈 동사는 GET, 동사는 대소문자 그대로 보낸다)
//   - github.com/go-resty/resty/v2 v2.17.2 request.go(Get…Patch·Execute — Execute의 동사는
//     http.NewRequest로 그대로 간다)·client.go(R·NewRequest·SetBaseURL·SetHostURL·SetPathParam(s)·
//     SetRawPathParam(s))·middleware.go(parseRequestURL)
package source

// clientLib는 요청을 만드는 라이브러리다. base 결합 규칙이 라이브러리마다 다르다.
type clientLib int

const (
	libNetHTTP clientLib = iota + 1 // base 없음 — URL 문자열 그대로
	libResty                        // Client base URL 뒤에 슬래시 결합(slashJoinParts)
)

// restyPath는 resty v2 모듈의 import 경로다.
const restyPath = "github.com/go-resty/resty/v2"

// fwResty는 흐름 분석이 resty 클라이언트 값을 추적할 때 쓰는 구현 이름이다(라우터가 아니다).
const fwResty routeFramework = "resty"

// callSpec은 요청 호출 하나의 인자 규칙이다. 인자 번호가 -1이면 그 인자가 없다.
type callSpec struct {
	lib clientLib
	// method는 고정 동사다. 빈 문자열이면 methodArg에서 읽는다.
	method    string
	methodArg int
	urlArg    int
}

// fixedCall은 고정 동사 요청 규칙을 만든다.
func fixedCall(lib clientLib, method string, urlArg int) callSpec {
	return callSpec{lib: lib, method: method, methodArg: -1, urlArg: urlArg}
}

// clientAPI는 "패키지 경로.리시버 타입.이름" → 요청 규칙이다.
var clientAPI = buildClientAPI()

// buildClientAPI는 net/http와 resty v2의 요청 호출 표를 만든다.
func buildClientAPI() map[string]callSpec {
	api := map[string]callSpec{}
	for _, recv := range []string{"", "Client"} {
		prefix := "net/http." + recv + "."
		api[prefix+"Get"] = fixedCall(libNetHTTP, "GET", 0)
		api[prefix+"Head"] = fixedCall(libNetHTTP, "HEAD", 0)
		api[prefix+"Post"] = fixedCall(libNetHTTP, "POST", 0)
		api[prefix+"PostForm"] = fixedCall(libNetHTTP, "POST", 0)
	}
	api["net/http..NewRequest"] = callSpec{lib: libNetHTTP, methodArg: 0, urlArg: 1}
	api["net/http..NewRequestWithContext"] = callSpec{lib: libNetHTTP, methodArg: 1, urlArg: 2}
	for _, name := range []string{"Get", "Head", "Post", "Put", "Delete", "Options", "Patch"} {
		api[restyPath+".Request."+name] = fixedCall(libResty, upperASCII(name), 0)
	}
	api[restyPath+".Request.Execute"] = callSpec{lib: libResty, methodArg: 0, urlArg: 1}
	return api
}

// upperASCII는 ASCII 대문자로 바꾼다.
func upperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

// restyConstructors는 새 resty 클라이언트를 만드는 함수다.
var restyConstructors = map[string]bool{
	restyPath + "..New":              true,
	restyPath + "..NewWithClient":    true,
	restyPath + "..NewWithLocalAddr": true,
}

// restyValueTypes는 흐름 분석이 추적하는 resty 값 타입이다. Request는 만든 Client와 같은 노드로
// 본다(R()·NewRequest()와 체인 메서드는 수신 클라이언트를 그대로 넘긴다) — base URL과 path param이
// 클라이언트에서 오기 때문이다.
var restyValueTypes = map[string]bool{
	restyPath + ".Client":  true,
	restyPath + ".Request": true,
}

// restySetterKind는 resty 클라이언트 설정 호출의 종류다.
type restySetterKind int

const (
	setBaseURL       restySetterKind = iota + 1 // SetBaseURL·SetHostURL — 끝 `/`를 뗀다
	setPathParam                                // SetPathParam(키, 값) — 값은 url.PathEscape
	setPathParams                               // SetPathParams(map) — 값은 url.PathEscape
	setRawPathParam                             // SetRawPathParam(키, 값) — 값을 그대로 넣는다
	setRawPathParams                            // SetRawPathParams(map)
)

// restySetters는 "패키지 경로.리시버 타입.이름" → 설정 종류다(Client·Request 모두).
var restySetters = map[string]restySetterKind{
	restyPath + ".Client.SetBaseURL":        setBaseURL,
	restyPath + ".Client.SetHostURL":        setBaseURL,
	restyPath + ".Client.SetPathParam":      setPathParam,
	restyPath + ".Client.SetPathParams":     setPathParams,
	restyPath + ".Client.SetRawPathParam":   setRawPathParam,
	restyPath + ".Client.SetRawPathParams":  setRawPathParams,
	restyPath + ".Request.SetPathParam":     setPathParam,
	restyPath + ".Request.SetPathParams":    setPathParams,
	restyPath + ".Request.SetRawPathParam":  setRawPathParam,
	restyPath + ".Request.SetRawPathParams": setRawPathParams,
}

// unmodelledRequests는 모델링한 라이브러리 안에서 URL을 읽지 않는 요청 경로다 — route-call-coverage로 센다.
var unmodelledRequests = map[string]string{
	restyPath + ".Request.Send": "resty Request.Send (method and URL from Request fields)",
}

// unsupportedClients는 route-call을 수확하지 않는 HTTP 클라이언트 패키지다. 이것을 import하는
// 저장소의 client 문서는 "스캔했으나 없음"이 아니다 — 호출 측 공백(route-call-coverage)으로 신고한다.
var unsupportedClients = []string{
	"github.com/go-resty/resty",
	"resty.dev/v3",
	"github.com/valyala/fasthttp",
	"github.com/imroc/req",
	"github.com/imroc/req/v3",
	"github.com/parnurzeal/gorequest",
	"github.com/hashicorp/go-retryablehttp",
	"github.com/carlmjohnson/requests",
	"github.com/dghubble/sling",
	"github.com/levigross/grequests",
	"github.com/h2non/gentleman",
	"github.com/gojek/heimdall/v7/httpclient",
	"github.com/cloudwego/hertz/pkg/app/client",
	"github.com/go-openapi/runtime/client",
	"github.com/monaco-io/request",
}
