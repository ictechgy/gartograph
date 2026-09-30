// route-call 생산자 테스트 — 합성 fixture 모듈(resty는 replace 스텁)로 사실 전체를 고정한다.
package source

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// clientDoc은 fixture의 route-call 문서를 만든다.
func clientDoc(t *testing.T, dir string, wrappers *WrapperFile) *ClientRouteDocument {
	t.Helper()
	doc, err := ClientRouteFacts(ClientRouteOptions{Harvest: Options{Dir: dir}, GeneratedAt: fixedTime,
		Wrappers: wrappers}, "test")
	if err != nil {
		t.Fatalf("ClientRouteFacts: %v", err)
	}
	return doc
}

// compactCall은 사실 하나를 비교용 한 줄로 쓴다. usr는 짧은 이름이다.
func compactCall(f RouteCallFact) string {
	var b strings.Builder
	method := f.Method
	if f.MethodDynamic {
		method = "?"
	}
	channel := "<dynamic>"
	if f.Channel != nil {
		channel = *f.Channel
	}
	fmt.Fprintf(&b, "%s %s %s", method, channel, f.PathAnchor)
	if f.ChannelPrefix != "" {
		b.WriteString(" prefix=" + f.ChannelPrefix)
	}
	if f.Authority != "" {
		b.WriteString(" host=" + f.Authority)
	}
	if f.BaseRef != "" {
		b.WriteString(" baseRef=" + f.BaseRef)
	}
	if f.QueryTailStripped {
		b.WriteString(" qts")
	}
	if f.MaskedSegments > 0 {
		fmt.Fprintf(&b, " masked=%d", f.MaskedSegments)
	}
	if f.Service != "" {
		b.WriteString(" service=" + f.Service)
	}
	if f.Symbol != nil {
		b.WriteString(" usr=" + f.Symbol.QualifiedName)
	}
	return b.String()
}

// compactCalls는 문서의 사실을 정렬된 한 줄 목록으로 쓴다.
func compactCalls(doc *ClientRouteDocument) []string {
	out := make([]string, len(doc.Facts))
	for i, f := range doc.Facts {
		out[i] = compactCall(f)
	}
	sort.Strings(out)
	return out
}

// assertCalls는 사실 목록이 기대와 정확히 같은지 본다.
func assertCalls(t *testing.T, doc *ClientRouteDocument, want []string) {
	t.Helper()
	sort.Strings(want)
	got := compactCalls(doc)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("facts mismatch\n got: %s\nwant: %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
}

// assertClientLimitation은 접두사로 시작하고 부분 문자열을 담은 한계가 있는지 본다.
func assertClientLimitation(t *testing.T, doc *ClientRouteDocument, prefix, contains string) {
	t.Helper()
	for _, l := range doc.Limitations {
		if strings.HasPrefix(l, prefix) && strings.Contains(l, contains) {
			return
		}
	}
	t.Errorf("no limitation %q containing %q in %q", prefix, contains, doc.Limitations)
}

// TestClientRoutesNetHTTP는 net/http 호출의 URL 조립 전체를 고정한다.
func TestClientRoutesNetHTTP(t *testing.T) {
	dir := clientModule(t, "", map[string]string{"app/app.go": `package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
)

const apiHost = "https://API.Example.com"

const version = 2

var base = "http://users.internal:8081/api"

type Client struct {
	base string
	hc   *http.Client
}

func Literal() { http.Get("https://user:pw@api.example.com/v1/items?page=2") }

func Concat(id int) {
	http.Get(apiHost + "/v1/items/" + strconv.Itoa(id))
}

func Sprintf(id string) {
	http.Post(fmt.Sprintf("%s/v%d/orders/%s/cancel", apiHost, version, id), "application/json", nil)
}

func Local(id string) {
	u := base + "/users/" + id
	req, _ := http.NewRequest(http.MethodDelete, u, nil)
	_ = req
}

func (c *Client) Field(id string) {
	req, _ := http.NewRequestWithContext(context.Background(), "PATCH", c.base+"/items/"+id, nil)
	c.hc.Do(req)
}

func (c *Client) Relative() {
	c.hc.Get(c.base + "items")
}

func Partial(name string) {
	http.Get(apiHost + "/files/" + name + ".json")
}

func Query(q string) {
	suffix := ""
	if q != "" {
		suffix = "?q=" + url.QueryEscape(q)
	}
	http.Get(apiHost + "/search" + suffix)
}

func URLLiteral(host, id string) {
	u := url.URL{Scheme: "https", Host: host, Path: "/v1/accounts/" + id}
	http.Get(u.String())
}

func URLLiteralConst() {
	u := &url.URL{Scheme: "http", Host: "api.example.com", Path: "/café menu"}
	http.Head(u.String())
}

func Resolve() {
	b, _ := url.Parse("http://api.example.com/v1/users/")
	ref, _ := url.Parse("../orders/7?x=1")
	http.Get(b.ResolveReference(ref).String())
}

func (c *Client) ResolveUnknown() {
	b, _ := url.Parse(c.base)
	u, _ := b.Parse("/abs/path")
	http.Get(u.String())
}

func JoinPath(id string) {
	s, _ := url.JoinPath("http://api.example.com/v1/", "users", id, "/")
	http.Get(s)
}

func (c *Client) JoinPathUnknown(id string) {
	s, _ := url.JoinPath(c.base, "users", id)
	http.Get(s)
}

func PathJoin(id string) {
	http.Get(apiHost + path.Join("/v1", "..", "v2", "items", id))
}

func Method(m string) {
	http.NewRequest(m, apiHost+"/dyn", nil)
}

func EmptyMethod() {
	http.NewRequest("", apiHost+"/empty", nil)
}

func Lower() {
	http.NewRequest("get", apiHost+"/lower", nil)
}

func Masked() {
	http.Get("https://hooks.slack.com/services/T000/B000/XXXXXXXX")
	http.Get(apiHost + "/v1/items/550e8400-e29b-41d4-a716-446655440000")
}

func Passthrough(p string) {
	http.Get(p)
}

func PathOnly() {
	http.NewRequest("GET", "/relative/only", nil)
}

func Rewrite(req *http.Request) {
	req.URL.Path = "/other"
}

func Literal2() {
	r := &http.Request{Method: "GET"}
	_ = r
}
`})
	doc := clientDoc(t, dir, nil)
	assertCalls(t, doc, []string{
		"? /dyn root host=api.example.com usr=app.Method",
		"? /lower root host=api.example.com usr=app.Lower",
		"DELETE /api/users/{} root host=users.internal:8081 usr=app.Local",
		"GET /abs/path root usr=app.(Client).ResolveUnknown",
		"GET /empty root host=api.example.com usr=app.EmptyMethod",
		"GET /relative/only base usr=app.PathOnly",
		"GET /search root host=api.example.com qts usr=app.Query",
		"GET /users/{} base baseRef=example.com/fixture/app.(Client).base usr=app.(Client).JoinPathUnknown",
		"GET /v1/accounts/{} root usr=app.URLLiteral",
		"GET /v1/items root host=api.example.com qts usr=app.Literal",
		"GET /v1/items/{} root host=api.example.com masked=1 usr=app.Masked",
		"GET /v1/items/{} root host=api.example.com usr=app.Concat",
		"GET /v1/orders/7 root host=api.example.com qts usr=app.Resolve",
		"GET /v1/users/{}/ root host=api.example.com usr=app.JoinPath",
		"GET /v2/items/{} root host=api.example.com usr=app.PathJoin",
		"GET /{}/{}/{}/{} root host=hooks.slack.com masked=4 usr=app.Masked",
		"GET <dynamic> base baseRef=example.com/fixture/app.(Client).base usr=app.(Client).Relative",
		"GET <dynamic> base usr=app.Passthrough",
		"GET <dynamic> root prefix=/files/ host=api.example.com usr=app.Partial",
		"HEAD /caf%C3%A9%20menu root host=api.example.com usr=app.URLLiteralConst",
		"PATCH /items/{} base baseRef=example.com/fixture/app.(Client).base usr=app.(Client).Field",
		"POST /v2/orders/{}/cancel root host=api.example.com usr=app.Sprintf",
	})
	assertClientLimitation(t, doc, "route-call-coverage: 1 ", "net/http Request literals")
	// Field·JoinPathUnknown(필드 base)이다. PathOnly는 base 식을 잇지 않아 세지 않는다.
	assertClientLimitation(t, doc, "unresolved-base-url: 2 ", "")
	assertClientLimitation(t, doc, "ambiguous-base-join: 1 ", "")
	assertClientLimitation(t, doc, "url-rewrite-interceptors: 1 ", "")
	// Passthrough(URL 전체가 파라미터)와 Method(동사가 파라미터)다. Partial은 파라미터가 경로 중간이라 아니다.
	assertClientLimitation(t, doc, "http-wrapper-undeclared: 2 ", "")
	if doc.Roles[0] != "client" || doc.Target != "http" || doc.Platform != "go" || doc.SourceSets["tests"] != "excluded" {
		t.Errorf("bad envelope: %+v", doc)
	}
	for _, f := range doc.Facts {
		if f.Dynamic && f.Channel != nil {
			t.Errorf("dynamic facts carry a null channel: %+v", f)
		}
	}
}

// TestClientRoutesResty는 resty base 결합·path param 치환·흐름 추적을 고정한다.
func TestClientRoutesResty(t *testing.T) {
	dir := clientModule(t, "", map[string]string{"api/api.go": `package api

import (
	"strconv"

	"github.com/go-resty/resty/v2"
)

type API struct {
	client *resty.Client
	other  *resty.Client
	dyn    *resty.Client
}

var dynamicBase string

func SetBase(b string) { dynamicBase = b }

func New(base string) *API {
	c := resty.New().SetBaseURL("http://orders.internal:9000/v2/")
	o := resty.New()
	o.BaseURL = "http://legacy.internal/"
	d := resty.New().SetHostURL(dynamicBase)
	d.SetPathParam("id", "1")
	return &API{client: c, other: o, dyn: d}
}

func (a *API) Order(id int) {
	a.client.R().SetHeader("A", "b").Get("/orders/" + strconv.Itoa(id))
}

func (a *API) Relative() {
	a.client.R().Post("orders")
}

func (a *API) Legacy() {
	a.other.R().Delete("/items")
}

func (a *API) Placeholder() {
	a.dyn.R().Put("/users/{id}/profile")
}

func (a *API) RawPlaceholder() {
	req := a.client.R()
	req.SetRawPathParam("path", "a/b")
	req.Get("/files/{path}")
}

func (a *API) Unset() {
	a.other.R().Get("/users/{id}")
}

func (a *API) Exec(m string) {
	a.client.R().Execute(m, "/exec")
	a.client.R().Execute(resty.MethodPatch, "/exec")
}

func (a *API) Absolute() {
	a.client.R().Get("https://elsewhere.example.com/x")
}

func Untraced(c *resty.Client) {
	c.R().Get("/untraced/{id}")
}

func (a *API) Send() {
	r := a.client.R()
	r.Method = "GET"
	r.URL = "/send"
	r.Send()
}
`}, "resty")
	doc := clientDoc(t, dir, nil)
	assertCalls(t, doc, []string{
		"? /v2/exec root host=orders.internal:9000 usr=api.(API).Exec",
		// BaseURL 필드에 직접 넣은 값은 끝 "/"를 떼지 않는다(SetBaseURL만 뗀다).
		"DELETE //items root host=legacy.internal usr=api.(API).Legacy",
		// path param을 하나도 설정하지 않은 클라이언트는 {id}를 치환하지 않는다.
		"GET //users/%7Bid%7D root host=legacy.internal usr=api.(API).Unset",
		"GET /untraced/{} base usr=api.Untraced",
		"GET /v2/orders/{} root host=orders.internal:9000 usr=api.(API).Order",
		"GET /x root host=elsewhere.example.com usr=api.(API).Absolute",
		"GET <dynamic> root prefix=/v2/files/ host=orders.internal:9000 usr=api.(API).RawPlaceholder",
		"PATCH /v2/exec root host=orders.internal:9000 usr=api.(API).Exec",
		"POST /v2/orders root host=orders.internal:9000 usr=api.(API).Relative",
		"PUT /users/{}/profile base baseRef=example.com/fixture/api.dynamicBase usr=api.(API).Placeholder",
	})
	assertClientLimitation(t, doc, "route-call-coverage: 1 ", "resty Request.Send")
	assertClientLimitation(t, doc, "unresolved-base-url: 2 ", "")
	assertClientLimitation(t, doc, "url-rewrite-interceptors: 2 ", "")
	assertClientLimitation(t, doc, "http-wrapper-undeclared: 1 ", "")
}

// TestClientRoutesWrappers는 http-wrappers v1 Go 선언(함수·메서드·인터페이스·생성자)을 고정한다.
func TestClientRoutesWrappers(t *testing.T) {
	dir := clientModule(t, "", map[string]string{
		"httpx/httpx.go": `package httpx

import "net/http"

type Verb int

const (
	Get Verb = iota
	Post
)

type Client struct{ base string }

func (c *Client) Send(method, path string, body any) error {
	req, _ := http.NewRequest(method, c.base+path, nil)
	_ = req
	return nil
}

func (c *Client) Do(v Verb, path string) {}

func Fetch(path string) { http.Get("http://gateway.internal" + path) }

type Endpoint struct {
	Method string
	Path   string
}

type Doer interface {
	Call(method, path string)
}
`,
		"app/app.go": `package app

import (
	"net/http"

	"example.com/fixture/httpx"
)

func Use(c *httpx.Client, d httpx.Doer, id string) {
	c.Send(http.MethodPost, "/v1/orders", nil)
	c.Send("GET",
		"/v1/orders/"+id,
		nil)
	c.Do(httpx.Post, "/v1/do")
	httpx.Fetch("/v1/fetch")
	_ = httpx.Endpoint{Method: "DELETE", Path: "/v1/endpoints/" + id}
	_ = &httpx.Endpoint{"PUT", "items"}
	d.Call("OPTIONS", "/v1/doer")
}
`})
	idx := func(i int) *int { return &i }
	file := &WrapperFile{Format: "http-wrappers", Version: 1, Wrappers: []WrapperDecl{
		{Language: "go", Kind: "function", Owner: "example.com/fixture/httpx.Client", Name: "Send",
			MethodArg: &WrapperArgRef{Label: "method"}, PathArg: &WrapperArgRef{Index: idx(1)}, PathAnchor: "base", Service: "orders"},
		{Language: "go", Kind: "function", Owner: "example.com/fixture/httpx.Client", Name: "Do",
			MethodArg: &WrapperArgRef{Index: idx(0)}, MethodEnum: map[string]string{"Get": "GET", "Post": "POST"},
			PathArg: &WrapperArgRef{Label: "path"}, PathAnchor: "base"},
		{Language: "go", Kind: "function", Owner: "example.com/fixture/httpx", Name: "Fetch",
			DefaultMethod: "GET", PathArg: &WrapperArgRef{Index: idx(0)}, PathAnchor: "root"},
		{Language: "go", Kind: "constructor", Owner: "example.com/fixture/httpx.Endpoint", Name: "Endpoint",
			MethodArg: &WrapperArgRef{Label: "Method"}, PathArg: &WrapperArgRef{Label: "Path"}, PathAnchor: "base"},
		{Language: "go", Kind: "function", Owner: "example.com/fixture/httpx.Doer", Name: "Call",
			MethodArg: &WrapperArgRef{Index: idx(0)}, PathArg: &WrapperArgRef{Index: idx(1)}, PathAnchor: "root"},
		{Language: "go", Kind: "function", Owner: "example.com/fixture/httpx.Client", Name: "Missing",
			DefaultMethod: "GET", PathArg: &WrapperArgRef{Index: idx(0)}, PathAnchor: "root"},
		{Language: "kotlin", Kind: "function", Owner: "x", Name: "y", DefaultMethod: "GET",
			PathArg: &WrapperArgRef{Index: idx(0)}, PathAnchor: "root"},
	}}
	doc := clientDoc(t, dir, file)
	assertCalls(t, doc, []string{
		"DELETE /v1/endpoints/{} base usr=app.Use",
		"GET /v1/fetch root usr=app.Use",
		"GET /v1/orders/{} base service=orders usr=app.Use",
		"OPTIONS /v1/doer root usr=app.Use",
		"POST /v1/do base usr=app.Use",
		"POST /v1/orders base service=orders usr=app.Use",
		"PUT /items base usr=app.Use",
	})
	// 여러 줄 호출은 호출식이 시작하는 줄이다(wrapper.location).
	for _, f := range doc.Facts {
		if f.Channel != nil && *f.Channel == "/v1/orders/{}" && (f.Location.Line != 11 || f.Location.Column != 2) {
			t.Errorf("multi-line wrapper call location = %+v, want line 11 column 2", f.Location)
		}
	}
	assertClientLimitation(t, doc, "http-wrapper-unresolved: wrappers[5] ", "matches no Go declaration")
	for _, l := range doc.Limitations {
		// 선언된 래퍼 본문(Send·Fetch)의 dynamic 요청은 억제하고, 선언한 base 앵커는 풀지 못한 base가 아니다.
		if strings.HasPrefix(l, "http-wrapper-undeclared:") || strings.HasPrefix(l, "unresolved-base-url:") ||
			strings.Contains(l, "wrappers[6]") {
			t.Errorf("unexpected limitation %q", l)
		}
	}
}

// TestLoadWrapperFile은 선언 파일의 검증(모르는 필드·빠진 필드·잘못된 동사)을 고정한다.
func TestLoadWrapperFile(t *testing.T) {
	write := func(body string) string {
		path := filepath.Join(t.TempDir(), "w.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valid := `{"format":"http-wrappers","version":1,"wrappers":[{"language":"go","kind":"function",
		"owner":"example.com/app/api","name":"Send","methodArg":{"index":0},"pathArg":{"label":"path"},
		"methodEnum":{"Get":"GET"},"pathAnchor":"base","service":"orders"}]}`
	file, err := LoadWrapperFile(write(valid))
	if err != nil || len(file.Wrappers) != 1 || *file.Wrappers[0].MethodArg.Index != 0 {
		t.Fatalf("valid file: %+v %v", file, err)
	}
	for name, body := range map[string]string{
		"unknown field":   `{"format":"http-wrappers","version":1,"wrappers":[],"extra":1}`,
		"unknown wrapper": `{"format":"http-wrappers","version":1,"wrappers":[{"language":"go","kind":"function","owner":"a","name":"b","defaultMethod":"GET","pathArg":{"index":0},"pathAnchor":"root","bogus":true}]}`,
		"no anchor":       `{"format":"http-wrappers","version":1,"wrappers":[{"language":"go","kind":"function","owner":"a","name":"b","defaultMethod":"GET","pathArg":{"index":0}}]}`,
		"no method":       `{"format":"http-wrappers","version":1,"wrappers":[{"language":"go","kind":"function","owner":"a","name":"b","pathArg":{"index":0},"pathAnchor":"root"}]}`,
		"bad enum":        `{"format":"http-wrappers","version":1,"wrappers":[{"language":"go","kind":"function","owner":"a","name":"b","methodArg":{"index":0},"methodEnum":{"x":"get"},"pathArg":{"index":1},"pathAnchor":"root"}]}`,
		"bad language":    `{"format":"http-wrappers","version":1,"wrappers":[{"language":"rust","kind":"function","owner":"a","name":"b","defaultMethod":"GET","pathArg":{"index":0},"pathAnchor":"root"}]}`,
		"bad version":     `{"format":"http-wrappers","version":2,"wrappers":[]}`,
		"no path":         `{"format":"http-wrappers","version":1,"wrappers":[{"language":"go","kind":"function","owner":"a","name":"b","defaultMethod":"GET","pathAnchor":"root"}]}`,
		"trailing":        `{"format":"http-wrappers","version":1,"wrappers":[]} {}`,
	} {
		if _, err := LoadWrapperFile(write(body)); err == nil {
			t.Errorf("%s: want a declaration error", name)
		}
	}
	if _, err := LoadWrapperFile(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file: want an error")
	}
}

// TestClientRoutesServiceConflict는 선언의 service와 --service가 다르면 입력 오류인지 본다.
func TestClientRoutesServiceConflict(t *testing.T) {
	dir := clientModule(t, "", map[string]string{"app/app.go": "package app\n"})
	idx := 0
	file := &WrapperFile{Format: "http-wrappers", Version: 1, Wrappers: []WrapperDecl{{Language: "go",
		Kind: "function", Owner: "example.com/fixture/app", Name: "Send", DefaultMethod: "GET",
		PathArg: &WrapperArgRef{Index: &idx}, PathAnchor: "root", Service: "orders"}}}
	_, err := ClientRouteFacts(ClientRouteOptions{Harvest: Options{Dir: dir}, Service: "users", Wrappers: file}, "test")
	if err == nil || !strings.Contains(err.Error(), "--service") {
		t.Fatalf("want a service conflict error, got %v", err)
	}
}

// TestClientRoutesCoverageAndDeterminism은 지원하지 않는 클라이언트 import가 호출 측 공백이 되고, 호출 0건
// 문서도 target http를 유지하며, 같은 입력이 같은 JSON인지 본다.
func TestClientRoutesCoverageAndDeterminism(t *testing.T) {
	dir := testModuleWithStub(t)
	first, _ := json.Marshal(clientDoc(t, dir, nil))
	second, _ := json.Marshal(clientDoc(t, dir, nil))
	if string(first) != string(second) {
		t.Fatalf("nondeterministic output:\n%s\n%s", first, second)
	}
	doc := clientDoc(t, dir, nil)
	if len(doc.Facts) != 0 || doc.Target != "http" || doc.Facts == nil {
		t.Fatalf("zero-fact document = %+v", doc)
	}
	assertClientLimitation(t, doc, "route-call-coverage: 1 ", "github.com/valyala/fasthttp")
	unused, _ := LoadWrapperFile(writeTemp(t, `{"format":"http-wrappers","version":1,"wrappers":[{"language":"go",
		"kind":"function","owner":"example.com/fixture/app","name":"Ping","defaultMethod":"GET","pathArg":{"index":0},
		"pathAnchor":"root"}]}`))
	doc = clientDoc(t, dir, unused)
	assertClientLimitation(t, doc, "http-wrapper-unresolved: wrappers[0] ", "has no call site")
}

// testModuleWithStub은 fasthttp 스텁을 import하는 호출 없는 모듈이다.
func testModuleWithStub(t *testing.T) string {
	return clientModule(t, "", map[string]string{"app/app.go": `package app

import "github.com/valyala/fasthttp"

func Ping(path string) { fasthttp.Get(nil, path) }
`}, "fasthttp")
}

// writeTemp는 임시 파일을 쓰고 경로를 돌려준다.
func writeTemp(t *testing.T, body string) string {
	path := filepath.Join(t.TempDir(), "w.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestClientRoutesEvaluator는 식 평가의 나머지 모양(변환·TrimSuffix·(*URL).JoinPath·host 값·공개 패키지
// 변수·SetPathParams 리터럴·%d 파라미터·다중 결과)을 고정한다.
func TestClientRoutesEvaluator(t *testing.T) {
	dir := clientModule(t, "", map[string]string{"lib/lib.go": `package lib

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-resty/resty/v2"
)

type Path string

// DefaultBase는 공개 패키지 변수다 — 다른 모듈이 바꿀 수 있어 값을 믿지 않는다.
var DefaultBase = "https://public.example.com"

var trimmed = strings.TrimSuffix("https://api.example.com/v1/", "/")

func Conversion() { http.Get(string(Path("https://api.example.com/typed"))) }

func Trim() { http.Get(trimmed + "/items") }

func Exported() { http.Get(DefaultBase + "/x") }

func MethodJoin(id string) {
	u, _ := url.ParseRequestURI("https://api.example.com/v3")
	http.Get(u.JoinPath("users", id).String())
}

func HostValue(region string) { http.Get("https://" + region + ".example.com/v1/status") }

func HostOnly(host string) { http.Get("https://" + host) }

func PrintfParam(p string) { http.Get(fmt.Sprintf("%s/v1/%d", p, 7)) }

func Indexed(id int) { http.Get(fmt.Sprintf("https://api.example.com/%[1]d", id)) }

func Mutated() {
	u, _ := url.Parse("https://api.example.com/a")
	u.Path = "/b"
	http.Get(u.String())
}

func Params() {
	c := resty.New().SetBaseURL("https://api.example.com")
	c.SetPathParams(map[string]string{"org": "o", "repo": "r"})
	c.R().Get("/repos/{org}/{repo}/{missing}")
}

func RawMap(m map[string]string) {
	c := resty.NewWithClient(nil)
	c.SetBaseURL("https://api.example.com")
	c.SetRawPathParams(m)
	c.R().Get("/files/{path}")
}

func NoArgs() {
	req, _ := http.NewRequest("POST", "https://api.example.com/a?x=1#frag", nil)
	_ = req
}
`}, "resty")
	doc := clientDoc(t, dir, nil)
	assertCalls(t, doc, []string{
		"GET /repos/{}/{}/%7Bmissing%7D root host=api.example.com usr=lib.Params",
		"GET /typed root host=api.example.com usr=lib.Conversion",
		// %s 파라미터가 URL 머리라 미상 base다.
		"GET /v1/7 base usr=lib.PrintfParam",
		"GET /v1/items root host=api.example.com usr=lib.Trim",
		"GET /v1/status base usr=lib.HostValue",
		"GET /v3/users/{} root host=api.example.com usr=lib.MethodJoin",
		"GET /x base baseRef=example.com/fixture/lib.DefaultBase usr=lib.Exported",
		"GET <dynamic> base usr=lib.HostOnly",
		"GET <dynamic> base usr=lib.Mutated",
		"GET <dynamic> root prefix=/files/ host=api.example.com usr=lib.RawMap",
		"GET <dynamic> base usr=lib.Indexed",
		"POST /a root host=api.example.com qts usr=lib.NoArgs",
	})
}

// TestRouteCallProblem은 자기 검증이 isthmus가 거부할 모양을 잡는지 본다.
func TestRouteCallProblem(t *testing.T) {
	channel := "/v1/{}"
	bad := "/v1/{**}"
	loc := &BridgeLocation{Path: "a.go", Line: 1, Column: 1}
	good := RouteCallFact{Kind: "route-call", Method: "GET", Channel: &channel, PathAnchor: "root", Location: loc}
	if p := routeCallProblem(good); p != "" {
		t.Fatalf("good fact rejected: %s", p)
	}
	cases := map[string]func(f *RouteCallFact){
		"both methods":    func(f *RouteCallFact) { f.MethodDynamic = true },
		"no method":       func(f *RouteCallFact) { f.Method = "" },
		"bad method":      func(f *RouteCallFact) { f.Method = "get" },
		"bad anchor":      func(f *RouteCallFact) { f.PathAnchor = "" },
		"no location":     func(f *RouteCallFact) { f.Location = nil },
		"bad authority":   func(f *RouteCallFact) { f.Authority = "API.example.com" },
		"catch-all":       func(f *RouteCallFact) { f.Channel = &bad },
		"static prefix":   func(f *RouteCallFact) { f.ChannelPrefix = "/v1" },
		"too many masked": func(f *RouteCallFact) { f.MaskedSegments = 2 },
		"dynamic channel": func(f *RouteCallFact) { f.Dynamic = true },
		"dynamic prefix": func(f *RouteCallFact) {
			f.Dynamic, f.Channel, f.ChannelPrefix = true, nil, "v1"
		},
	}
	for name, mutate := range cases {
		f := good
		mutate(&f)
		if routeCallProblem(f) == "" {
			t.Errorf("%s: want a self-check problem", name)
		}
	}
}

// TestClientRoutesReviewFindings는 리뷰에서 재현한 결함을 고정한다: 메서드 식 호출의 인자 위치, 알려진
// escape 키를 덮던 모르는 raw 키, 클라이언트 노드 사이에 섞이던 path param, 경로만 있는 net/http URL의
// unresolved-base-url 계수, 지역 변수 동사.
func TestClientRoutesReviewFindings(t *testing.T) {
	dir := clientModule(t, "", map[string]string{"app/app.go": `package app

import (
	"net/http"

	"github.com/go-resty/resty/v2"
)

type holder struct{ c *resty.Client }

func MethodExpr() {
	c := &http.Client{}
	(*http.Client).Get(c, "http://api.example.test/v1/ping")
}

func RestyMethodExpr() {
	c := resty.New().SetBaseURL("http://orders.example.test")
	(*resty.Request).Get(c.R(), "/v1/orders")
}

func KnownKeyWins(k string) {
	c := resty.New().SetBaseURL("http://a.example.test")
	c.SetPathParam("id", "7")
	c.R().SetRawPathParam(k, "x").Get("/a/{id}")
}

func PerNode(flag bool) {
	raw := resty.New().SetBaseURL("http://raw.example.test")
	raw.SetRawPathParam("id", "a/b")
	esc := resty.New().SetBaseURL("http://esc.example.test")
	esc.SetPathParam("id", "7")
	h := holder{c: raw}
	if flag {
		h = holder{c: esc}
	}
	h.c.R().Get("/files/{id}")
}

func PathOnly() {
	http.NewRequest("GET", "/relative/only", nil)
}

func LocalMethod() {
	m := "PUT"
	http.NewRequest(m, "http://api.example.test/v1/local", nil)
}
`}, "resty")
	doc := clientDoc(t, dir, nil)
	assertCalls(t, doc, []string{
		"GET /v1/ping root host=api.example.test usr=app.MethodExpr",
		"GET /v1/orders root host=orders.example.test usr=app.RestyMethodExpr",
		"GET /a/{} root host=a.example.test usr=app.KnownKeyWins",
		"GET <dynamic> root prefix=/files/ host=raw.example.test usr=app.PerNode",
		"GET /files/{} root host=esc.example.test usr=app.PerNode",
		"GET /relative/only base usr=app.PathOnly",
		"PUT /v1/local root host=api.example.test usr=app.LocalMethod",
	})
	for _, l := range doc.Limitations {
		if strings.HasPrefix(l, "unresolved-base-url:") {
			t.Errorf("a net/http path-only URL joins no base URL: %q", l)
		}
	}
}

// TestClientRoutesWrapperReviewFindings는 바인딩하지 못한 래퍼 호출이 한계로 드러나고, 패키지 함수 선언이
// 같은 이름의 타입 메서드를 끌어오지 않는지 본다.
func TestClientRoutesWrapperReviewFindings(t *testing.T) {
	// owner "example.com/fixture/api.v2"는 패키지 경로다. 마지막 점을 타입 경계로 읽으면 패키지
	// example.com/fixture/api의 타입 v2 메서드 Get이 되는데, 그 메서드가 실제로 있다.
	dir := clientModule(t, "", map[string]string{
		"api.v2/api.go": `package apiv2

func Get(path string) {}
`,
		"api/api.go": `package api

type v2 struct{}

func (v2) Get(path string) {}

func Use() { v2{}.Get("/v1/method") }
`,
		"app.go": `package fixture

import apiv2 "example.com/fixture/api.v2"

func Send(method, p string) {}

func Use() {
	apiv2.Get("/v1/pkg")
	Send("GET", "/v1/unbound")
}
`})
	zero := 0
	file := &WrapperFile{Format: "http-wrappers", Version: 1, Wrappers: []WrapperDecl{
		{Language: "go", Kind: "function", Owner: "example.com/fixture/api.v2", Name: "Get", DefaultMethod: "GET",
			PathArg: &WrapperArgRef{Index: &zero}, PathAnchor: "root"},
		{Language: "go", Kind: "function", Owner: "example.com/fixture", Name: "Send", DefaultMethod: "GET",
			PathArg: &WrapperArgRef{Label: "path"}, PathAnchor: "root"},
	}}
	doc := clientDoc(t, dir, file)
	assertCalls(t, doc, []string{"GET /v1/pkg root usr=fixture.Use"})
	assertClientLimitation(t, doc, "http-wrapper-unresolved: wrappers[1] ", "could not be bound")
}
