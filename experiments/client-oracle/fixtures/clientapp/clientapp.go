// Package clientapp는 route-call 오라클의 합성 Go 클라이언트다.
//
// 시나리오 함수 하나가 요청 하나를 보낸다. 함수 이름이 곧 사실의 symbol.usr이라 오라클이 기록한 요청과
// gartograph 사실을 이름으로 짝짓는다. host는 모두 .test(예약 TLD)이고, 오라클이 HTTP_PROXY로 모든
// 요청을 127.0.0.1의 기록 서버로 보낸다 — 외부로 나가는 요청은 없다. 런타임 값(id·host·base)은
// 함수 결과나 여러 번 대입된 변수라 gartograph가 값으로 본다.
package clientapp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/go-resty/resty/v2"
)

// apiHost는 상수 base다(패키지 밖에서 바꿀 수 없다).
const apiHost = "http://api.example.test"

// apiVersion은 fmt %d로 들어가는 상수다.
const apiVersion = 2

// counter는 런타임 id의 재료다.
var counter = 40

// runtimeBase는 Setup이 넣는 base URL이다 — 대입이 둘(영값·Setup)이라 값을 모른다.
var runtimeBase string

// 시나리오가 쓰는 resty 클라이언트들이다.
var (
	orders  *resty.Client
	legacy  *resty.Client
	plain   *resty.Client
	dynamic *resty.Client
	api     *API
)

// API는 base URL을 필드로 받는 클라이언트이자 선언된 래퍼(Send)다.
type API struct {
	base string
	hc   *http.Client
}

// Setup은 런타임 base URL과 클라이언트를 준비한다.
func Setup(base string) {
	runtimeBase = base
	orders = resty.New().SetBaseURL("http://orders.example.test/v2/")
	legacy = resty.New()
	legacy.BaseURL = "http://legacy.example.test/"
	plain = resty.New().SetBaseURL("http://plain.example.test")
	dynamic = resty.New().SetBaseURL(runtimeBase)
	dynamic.SetPathParam("id", "99")
	api = &API{base: base, hc: &http.Client{}}
}

// id는 런타임 값이다.
func id() string {
	counter++
	return strconv.Itoa(counter)
}

// host는 런타임 host다.
func host() string { return "accounts.example.test" }

// send는 요청을 보내고 오류를 돌려준다.
func send(req *http.Request, err error) error {
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// closeResp는 응답 본문을 닫는다.
func closeResp(resp *http.Response, err error) error {
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// restyErr는 resty 응답의 오류만 돌려준다.
func restyErr(_ *resty.Response, err error) error { return err }

// Send는 선언된 래퍼다(http-wrappers: methodArg label method, pathArg index 1, pathAnchor base).
func (a *API) Send(method, p string) error {
	req, err := http.NewRequest(method, a.base+p, nil)
	if err != nil {
		return err
	}
	resp, err := a.hc.Do(req)
	return closeResp(resp, err)
}

// LiteralQuery: 원문 URL, query 꼬리.
func LiteralQuery() error {
	return closeResp(http.Get("http://api.example.test/v1/items?page=2"))
}

// ConstConcat: 상수 + 원문 + 값.
func ConstConcat() error {
	return closeResp(http.Get(apiHost + "/v1/items/" + id()))
}

// SprintfVersion: fmt.Sprintf의 %s·%d(상수)·%s(값).
func SprintfVersion() error {
	return closeResp(http.Post(fmt.Sprintf("%s/v%d/orders/%s", apiHost, apiVersion, id()), "text/plain", nil))
}

// NewRequestDelete: 지역 변수 URL과 http.MethodDelete.
func NewRequestDelete() error {
	u := apiHost + "/v1/carts/" + id()
	return send(http.NewRequest(http.MethodDelete, u, nil))
}

// EmptyMethod: 빈 동사는 GET이다.
func EmptyMethod() error {
	return send(http.NewRequestWithContext(context.Background(), "", apiHost+"/v1/empty", nil))
}

// URLStructHostValue: url.URL의 Host가 값이어도 경로는 root다.
func URLStructHostValue() error {
	u := url.URL{Scheme: "http", Host: host(), Path: "/v1/accounts/" + id()}
	return closeResp(http.Get(u.String()))
}

// URLStructEscaped: url.URL.Path는 디코드된 경로라 EscapedPath로 인코딩된다.
func URLStructEscaped() error {
	u := &url.URL{Scheme: "http", Host: "api.example.test", Path: "/café menu/x"}
	return closeResp(http.Head(u.String()))
}

// ResolveDots: RFC 3986 병합과 점 세그먼트 제거.
func ResolveDots() error {
	base, _ := url.Parse("http://api.example.test/v1/users/")
	ref, _ := url.Parse("../orders/7?x=1")
	return closeResp(http.Get(base.ResolveReference(ref).String()))
}

// ResolveAbsolutePath: `/`로 시작하는 ref는 base 경로를 버린다.
func ResolveAbsolutePath() error {
	base, _ := url.Parse("http://api.example.test/v1/")
	u, _ := base.Parse("/abs/path")
	return closeResp(http.Get(u.String()))
}

// JoinPathTrailing: url.JoinPath는 마지막 원소의 끝 `/`를 보존한다.
func JoinPathTrailing() error {
	s, _ := url.JoinPath("http://api.example.test/v1/", "users", id(), "/")
	return closeResp(http.Get(s))
}

// JoinPathDots: url.JoinPath는 path.Join으로 `..`를 정리한다.
func JoinPathDots() error {
	s, _ := url.JoinPath("http://api.example.test/a/b", "..", "c")
	return closeResp(http.Get(s))
}

// PathJoin: path.Join으로 만든 경로.
func PathJoin() error {
	return closeResp(http.Get(apiHost + path.Join("/v1", "..", "v2", "items", id())))
}

// FieldBase: 필드 base(런타임) 뒤의 경로는 base 앵커다.
func FieldBase() error {
	return closeResp(api.hc.Get(api.base + "/v1/field/" + id()))
}

// DoubleSlashKept: Go는 `//`를 줄이지 않는다.
func DoubleSlashKept() error {
	return closeResp(http.Get("http://api.example.test/v1//double"))
}

// DotSegmentsKept: 문자열 URL의 점 세그먼트는 그대로 나간다.
func DotSegmentsKept() error {
	return closeResp(http.Get("http://api.example.test/v1/./x/../y"))
}

// QueryTailLocal: 모든 대입이 `?`로 시작하거나 빈 지역 변수는 query 꼬리다.
func QueryTailLocal() error {
	suffix := ""
	if counter > 0 {
		suffix = "?q=" + url.QueryEscape(id())
	}
	return closeResp(http.Get(apiHost + "/v1/search" + suffix))
}

// PartialSegment: 세그먼트 일부만 채우는 값은 dynamic이다.
func PartialSegment() error {
	return closeResp(http.Get(apiHost + "/files/" + id() + ".json"))
}

// ClientPost: (*http.Client).Post.
func ClientPost() error {
	c := &http.Client{}
	return closeResp(c.Post(apiHost+"/v1/client-post", "text/plain", nil))
}

// PostForm: http.PostForm은 POST다.
func PostForm() error {
	return closeResp(http.PostForm(apiHost+"/v1/form", url.Values{"a": {"b"}}))
}

// MaskedToken: 16자 이상 글자·숫자 세그먼트는 마스킹한다.
func MaskedToken() error {
	return closeResp(http.Get(apiHost + "/v1/tokens/a1b2c3d4e5f6a7b8c9d0"))
}

// TrimSuffixBase: strings.TrimSuffix(base, "/").
func TrimSuffixBase() error {
	base := strings.TrimSuffix("http://api.example.test/v1/", "/")
	return closeResp(http.Get(base + "/trim"))
}

// RestyBaseTrim: SetBaseURL은 끝 `/`를 떼고 경로를 잇는다.
func RestyBaseTrim() error {
	return restyErr(orders.R().SetHeader("Accept", "application/json").Get("/orders/" + id()))
}

// RestyRelative: `/` 없는 경로 앞에 `/`를 붙인다.
func RestyRelative() error {
	return restyErr(orders.R().Post("orders"))
}

// RestyBaseField: BaseURL 필드에 직접 넣은 값은 끝 `/`를 떼지 않는다.
func RestyBaseField() error {
	return restyErr(legacy.R().Delete("/items"))
}

// RestyPathParam: 요청의 SetPathParam이 `{id}`를 치환한다.
func RestyPathParam() error {
	return restyErr(orders.R().SetPathParam("id", id()).Put("/users/{id}/profile"))
}

// RestyUnsetPlaceholder: path param이 하나도 없으면 `{id}`가 그대로 나간다.
func RestyUnsetPlaceholder() error {
	return restyErr(plain.R().Get("/raw/{id}"))
}

// RestyExecute: Execute의 동사.
func RestyExecute() error {
	return restyErr(orders.R().Execute(resty.MethodPatch, "/exec"))
}

// RestyAbsolute: 절대 URL은 base를 쓰지 않는다.
func RestyAbsolute() error {
	return restyErr(orders.R().Get("http://elsewhere.example.test/x"))
}

// RestyRawParam: raw path param은 `/`를 담을 수 있어 dynamic이다.
func RestyRawParam() error {
	return restyErr(plain.NewRequest().SetRawPathParam("path", "a/b").Get("/files/{path}"))
}

// RestyDynamicBase: 런타임 base 뒤 경로는 base 앵커다(클라이언트의 SetPathParam으로 {id} 치환).
func RestyDynamicBase() error {
	return restyErr(dynamic.R().Get("/dyn/{id}"))
}

// RestyPathParamsMap: SetPathParams map 리터럴.
func RestyPathParamsMap() error {
	return restyErr(orders.R().SetPathParams(map[string]string{"org": "o", "repo": id()}).Get("/repos/{org}/{repo}"))
}

// WrapperSend: 선언된 래퍼 호출(base 앵커).
func WrapperSend() error {
	return api.Send("POST", "/v1/wrapped/"+id())
}
