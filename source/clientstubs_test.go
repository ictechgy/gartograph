// route-call 수확 테스트용 resty 스텁 — 실제 모듈과 같은 import 경로·타입·시그니처만 둔 합성 모듈이다.
//
// 수확은 타입(패키지 경로, 리시버 타입, 이름)만 보므로 본문은 비어 있어도 된다. 실제 결합 의미론은
// experiments/client-oracle이 proxy.golang.org에서 받은 resty v2.17.2로 확인한다.
package source

import (
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// restyStub은 github.com/go-resty/resty/v2의 요청 API 스텁이다.
const restyStub = `package resty

import "net/http"

const (
	MethodGet    = "GET"
	MethodPost   = "POST"
	MethodPut    = "PUT"
	MethodDelete = "DELETE"
	MethodPatch  = "PATCH"
)

type Client struct {
	BaseURL string
	HostURL string
}

type Request struct {
	URL    string
	Method string
}

type Response struct{}

func New() *Client                          { return &Client{} }
func NewWithClient(hc *http.Client) *Client { return &Client{} }

func (c *Client) SetBaseURL(url string) *Client                    { return c }
func (c *Client) SetHostURL(url string) *Client                    { return c }
func (c *Client) SetHeader(header, value string) *Client           { return c }
func (c *Client) SetPathParam(param, value string) *Client         { return c }
func (c *Client) SetPathParams(params map[string]string) *Client   { return c }
func (c *Client) SetRawPathParam(param, value string) *Client      { return c }
func (c *Client) SetRawPathParams(params map[string]string) *Client { return c }
func (c *Client) R() *Request                                      { return &Request{} }
func (c *Client) NewRequest() *Request                             { return &Request{} }

func (r *Request) SetHeader(header, value string) *Request           { return r }
func (r *Request) SetBody(body interface{}) *Request                 { return r }
func (r *Request) SetQueryParam(param, value string) *Request        { return r }
func (r *Request) SetPathParam(param, value string) *Request         { return r }
func (r *Request) SetPathParams(params map[string]string) *Request   { return r }
func (r *Request) SetRawPathParam(param, value string) *Request      { return r }
func (r *Request) SetRawPathParams(params map[string]string) *Request { return r }
func (r *Request) Get(url string) (*Response, error)                 { return nil, nil }
func (r *Request) Head(url string) (*Response, error)                { return nil, nil }
func (r *Request) Post(url string) (*Response, error)                { return nil, nil }
func (r *Request) Put(url string) (*Response, error)                 { return nil, nil }
func (r *Request) Delete(url string) (*Response, error)              { return nil, nil }
func (r *Request) Options(url string) (*Response, error)             { return nil, nil }
func (r *Request) Patch(url string) (*Response, error)               { return nil, nil }
func (r *Request) Send() (*Response, error)                          { return nil, nil }
func (r *Request) Execute(method, url string) (*Response, error)     { return nil, nil }
`

// clientStubs는 스텁 이름 → (모듈 경로, 파일)이다.
var clientStubs = map[string]struct {
	path  string
	files map[string]string
}{
	"resty":    {restyPath, map[string]string{"resty.go": restyStub}},
	"fasthttp": {"github.com/valyala/fasthttp", map[string]string{"fasthttp.go": "package fasthttp\n\nfunc Get(dst []byte, url string) {}\n"}},
}

// clientModule은 files와 스텁 모듈(replace)을 담은 모듈을 만든다. modulePath가 비면 example.com/fixture다.
func clientModule(t *testing.T, modulePath string, app map[string]string, stubs ...string) string {
	t.Helper()
	if modulePath == "" {
		modulePath = "example.com/fixture"
	}
	files := map[string]string{}
	for name, content := range app {
		files[name] = content
	}
	var requires, replaces strings.Builder
	for _, name := range stubs {
		stub, ok := clientStubs[name]
		if !ok {
			t.Fatalf("unknown stub %q", name)
		}
		version := "v0.0.0"
		if strings.HasSuffix(stub.path, "/v2") {
			version = "v2.0.0"
		}
		requires.WriteString("\t" + stub.path + " " + version + "\n")
		replaces.WriteString("replace " + stub.path + " => ./" + name + "stub\n")
		files[name+"stub/go.mod"] = "module " + stub.path + "\n\ngo 1.22\n"
		for file, content := range stub.files {
			files[name+"stub/"+file] = content
		}
	}
	gomod := "module " + modulePath + "\n\ngo 1.27\n"
	if requires.Len() > 0 {
		gomod += "\nrequire (\n" + requires.String() + ")\n\n" + replaces.String()
	}
	files["go.mod"] = gomod
	return testutil.WriteModule(t, files)
}
