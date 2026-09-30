// Command oracle는 gartograph routes --role client 문서를 실제 요청과 대조한다.
//
// 사용법(이 모듈 루트에서, resty는 proxy.golang.org에서 받는다):
//
//	gartograph routes --role client --dir . --pattern ./fixtures/... --wrappers wrappers.json \
//	  --generated-at 2026-01-01T00:00:00Z --out /tmp/calls.json
//	go run ./cmd/oracle /tmp/calls.json > recorded/report.json
//
// 127.0.0.1 임시 포트의 httptest 서버를 HTTP_PROXY로 걸어 fixture의 모든 요청(net/http·resty 둘 다
// http.ProxyFromEnvironment를 쓴다)을 그 서버가 받게 한다 — 외부로 나가는 요청은 없다. 서버는 받은
// request-target(절대형 "http://host/path?query")에서 동사·host·경로를 기록한다. 시나리오 함수 하나가
// 요청 하나를 보내고, 사실은 symbol.usr(= 시나리오 함수)로 짝짓는다.
//
// 판정: 정적 사실은 동사(methodDynamic이면 동사 비교를 건너뛴다)·authority(있으면 host와 같음)·
// 템플릿이 모두 맞아야 match다. 템플릿은 root면 경로 전체, base면 세그먼트 경계 꼬리와 세그먼트 단위로
// 맞춘다(`{}`는 비어 있지 않은 세그먼트). dynamic 사실은 dynamic으로 센다. 하나라도 mismatch면 종료 코드 1이다.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"

	"example.com/clientoracle/fixtures/clientapp"
)

// usrPrefix는 시나리오 함수의 정점 ID 접두사다.
const usrPrefix = "example.com/clientoracle/fixtures/clientapp."

// runtimeBase는 런타임 base URL이다(prefix는 base 앵커 사실이 꼬리로 맞아야 하는 앞자리).
const runtimeBase = "http://runtime.example.test/prefix"

// scenarios는 시나리오 이름 → 함수다.
var scenarios = map[string]func() error{
	"LiteralQuery": clientapp.LiteralQuery, "ConstConcat": clientapp.ConstConcat,
	"SprintfVersion": clientapp.SprintfVersion, "NewRequestDelete": clientapp.NewRequestDelete,
	"EmptyMethod": clientapp.EmptyMethod, "URLStructHostValue": clientapp.URLStructHostValue,
	"URLStructEscaped": clientapp.URLStructEscaped, "ResolveDots": clientapp.ResolveDots,
	"ResolveAbsolutePath": clientapp.ResolveAbsolutePath, "JoinPathTrailing": clientapp.JoinPathTrailing,
	"JoinPathDots": clientapp.JoinPathDots, "PathJoin": clientapp.PathJoin, "FieldBase": clientapp.FieldBase,
	"DoubleSlashKept": clientapp.DoubleSlashKept, "DotSegmentsKept": clientapp.DotSegmentsKept,
	"QueryTailLocal": clientapp.QueryTailLocal, "PartialSegment": clientapp.PartialSegment,
	"ClientPost": clientapp.ClientPost, "PostForm": clientapp.PostForm, "MaskedToken": clientapp.MaskedToken,
	"TrimSuffixBase": clientapp.TrimSuffixBase, "RestyBaseTrim": clientapp.RestyBaseTrim,
	"RestyRelative": clientapp.RestyRelative, "RestyBaseField": clientapp.RestyBaseField,
	"RestyPathParam": clientapp.RestyPathParam, "RestyUnsetPlaceholder": clientapp.RestyUnsetPlaceholder,
	"RestyExecute": clientapp.RestyExecute, "RestyAbsolute": clientapp.RestyAbsolute,
	"RestyRawParam": clientapp.RestyRawParam, "RestyDynamicBase": clientapp.RestyDynamicBase,
	"RestyPathParamsMap": clientapp.RestyPathParamsMap, "WrapperSend": clientapp.WrapperSend,
}

// request는 기록한 요청 하나다.
type request struct {
	Method string `json:"method"`
	Host   string `json:"host"`
	Path   string `json:"path"`
}

// fact는 route-call 사실(필요한 필드만)이다.
type fact struct {
	Method        string  `json:"method"`
	MethodDynamic bool    `json:"methodDynamic"`
	Channel       *string `json:"channel"`
	Dynamic       bool    `json:"dynamic"`
	PathAnchor    string  `json:"pathAnchor"`
	Authority     string  `json:"authority"`
	ChannelPrefix string  `json:"channelPrefix"`
	Symbol        *struct {
		Usr string `json:"usr"`
	} `json:"symbol"`
}

// row는 시나리오 하나의 결과다.
type row struct {
	Name    string   `json:"name"`
	Request request  `json:"request"`
	Facts   []string `json:"facts"`
	Result  string   `json:"result"`
}

// report는 오라클 기록 전체다.
type report struct {
	Versions  map[string]string `json:"versions"`
	Summary   map[string]int    `json:"summary"`
	Scenarios []row             `json:"scenarios"`
}

// recorder는 기록 서버가 받은 요청이다.
type recorder struct {
	mu       sync.Mutex
	requests []request
}

// ServeHTTP는 절대형 request-target에서 동사·host·경로를 기록한다.
func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	u, err := url.Parse(req.RequestURI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	r.mu.Lock()
	r.requests = append(r.requests, request{Method: req.Method, Host: strings.ToLower(u.Host), Path: path})
	r.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// take는 기록을 꺼내고 비운다.
func (r *recorder) take() []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.requests
	r.requests = nil
	return out
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: oracle <route-call document>")
		os.Exit(2)
	}
	facts, err := readFacts(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	rec := &recorder{}
	server := httptest.NewServer(rec)
	defer server.Close()
	// http.ProxyFromEnvironment는 처음 부를 때 환경을 한 번만 읽는다 — 요청 전에 건다.
	os.Setenv("HTTP_PROXY", server.URL)
	os.Unsetenv("NO_PROXY")
	os.Unsetenv("no_proxy")
	clientapp.Setup(runtimeBase)
	out := run(rec, facts)
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(data))
	if out.Summary["mismatched"] > 0 {
		os.Exit(1)
	}
}

// readFacts는 문서의 사실을 시나리오 이름별로 묶는다.
func readFacts(path string) (map[string][]fact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct{ Facts []fact }
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	out := map[string][]fact{}
	for _, f := range doc.Facts {
		if f.Symbol != nil && strings.HasPrefix(f.Symbol.Usr, usrPrefix) {
			name := strings.TrimPrefix(f.Symbol.Usr, usrPrefix)
			out[name] = append(out[name], f)
		}
	}
	return out, nil
}

// run은 시나리오를 이름 순으로 실행해 판정한다.
func run(rec *recorder, facts map[string][]fact) report {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)
	out := report{Versions: map[string]string{"resty": "v2.17.2"}, Summary: map[string]int{}}
	for _, name := range names {
		err := scenarios[name]()
		got := rec.take()
		r := row{Name: name}
		for _, f := range facts[name] {
			r.Facts = append(r.Facts, compact(f))
		}
		switch {
		case err != nil || len(got) != 1:
			r.Result = fmt.Sprintf("no-request (%d requests, err %v)", len(got), err)
			out.Summary["mismatched"]++
		default:
			r.Request = got[0]
			r.Result = judge(got[0], facts[name])
			out.Summary[r.Result]++
		}
		out.Summary["scenarios"]++
		out.Scenarios = append(out.Scenarios, r)
	}
	return out
}

// compact는 사실을 한 줄로 쓴다.
func compact(f fact) string {
	method := f.Method
	if f.MethodDynamic {
		method = "?"
	}
	channel := "<dynamic>"
	if f.Channel != nil {
		channel = *f.Channel
	}
	s := method + " " + channel + " " + f.PathAnchor
	if f.ChannelPrefix != "" {
		s += " prefix=" + f.ChannelPrefix
	}
	if f.Authority != "" {
		s += " host=" + f.Authority
	}
	return s
}

// judge는 요청 하나와 그 시나리오의 사실을 판정한다: 모든 사실이 요청과 맞으면 matched, dynamic만
// 있으면 dynamic, 사실이 없거나 하나라도 어긋나면 mismatched다.
func judge(req request, facts []fact) string {
	if len(facts) == 0 {
		return "mismatched"
	}
	dynamic := 0
	for _, f := range facts {
		if f.Dynamic {
			dynamic++
			continue
		}
		if !f.MethodDynamic && f.Method != req.Method {
			return "mismatched"
		}
		if f.Authority != "" && f.Authority != req.Host {
			return "mismatched"
		}
		if !templateMatches(*f.Channel, f.PathAnchor, canonical(req.Path)) {
			return "mismatched"
		}
	}
	if dynamic == len(facts) {
		return "dynamic"
	}
	return "matched"
}

// templateMatches는 템플릿이 경로와 맞는지 본다(root는 전체, base는 세그먼트 경계 꼬리).
func templateMatches(template, anchor, path string) bool {
	ts := strings.Split(template[1:], "/")
	ps := strings.Split(path[1:], "/")
	if anchor == "root" {
		return len(ts) == len(ps) && segmentsMatch(ts, ps)
	}
	return len(ps) >= len(ts) && segmentsMatch(ts, ps[len(ps)-len(ts):])
}

// segmentsMatch는 세그먼트를 하나씩 맞춘다(`{}`는 비어 있지 않은 세그먼트).
func segmentsMatch(ts, ps []string) bool {
	for i := range ts {
		if ts[i] == "{}" {
			if ps[i] == "" {
				return false
			}
			continue
		}
		if ts[i] != ps[i] {
			return false
		}
	}
	return true
}

// canonical은 요청 경로를 isthmus 정규 표기로 바꾼다(unreserved 디코드, 대문자 hex, 그 밖 인코딩).
func canonical(path string) string {
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch {
		case c == '%' && i+2 < len(path) && isHex(path[i+1]) && isHex(path[i+2]):
			decoded := unhex(path[i+1])<<4 | unhex(path[i+2])
			if isUnreserved(decoded) {
				b.WriteByte(decoded)
			} else {
				b.WriteString("%" + strings.ToUpper(path[i+1:i+3]))
			}
			i += 2
		case c == '/' || isUnreserved(c) || strings.IndexByte("!$&'()*+,;=:@", c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// isUnreserved는 RFC 3986 unreserved 문자인지 본다.
func isUnreserved(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
		c == '-' || c == '.' || c == '_' || c == '~'
}

// isHex는 16진 숫자인지 본다.
func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// unhex는 16진 숫자 하나의 값이다.
func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}
