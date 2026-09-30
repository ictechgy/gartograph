// 프레임워크 패턴 변환의 순수 함수 테스트 — 각 규칙의 근거는 routepattern.go 머리말의 소스다.
package source

import (
	"reflect"
	"testing"
)

// shapeTemplates는 변환 결과의 템플릿과 표시(cap·variant)를 모은다.
func shapeTemplates(p parsedRoute) []string {
	var out []string
	for _, s := range p.shapes {
		t := s.template()
		if s.catchAllPrefix {
			t += " cap"
		}
		if s.variant {
			t += " variant"
		}
		out = append(out, t)
	}
	return out
}

// TestParseServeMuxPattern은 pattern.go parsePattern의 문법·거부 규칙을 고정한다.
func TestParseServeMuxPattern(t *testing.T) {
	cases := []struct {
		in     string
		method string
		host   string
		want   []string
	}{
		{"/", "", "", []string{"/{**}", "/ cap"}},
		{"GET /{$}", "GET", "", []string{"/"}},
		{"GET  /items/{id}", "GET", "", []string{"/items/{}"}},
		{"POST\t/a/{rest...}", "POST", "", []string{"/a/{**}", "/a/ variant"}},
		{"example.com/x/", "", "example.com", []string{"/x/{**}", "/x/ variant"}},
		{"/a%2Fb/c%20d", "", "", []string{"/a%2Fb/c%20d"}},
		{"CONNECT /a/../b", "CONNECT", "", []string{"/a/../b"}},
	}
	for _, c := range cases {
		p := parseServeMuxPattern(c.in)
		if !p.valid || p.method != c.method || p.host != c.host {
			t.Errorf("%q: valid=%v method=%q host=%q", c.in, p.valid, p.method, p.host)
			continue
		}
		if got := shapeTemplates(buildShapes(p.raw)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: templates %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "items", "GET /a/../b", "/a{x}", "/{x}/{x...}/y", "/{$}/x", "/{}", "ho{st}/x", "G@T /x", "/{...}"} {
		if p := parseServeMuxPattern(bad); p.valid {
			t.Errorf("%q: must be rejected (the registration panics or never matches)", bad)
		}
	}
}

// TestParseChiPattern은 chi 토큰 규칙(정규식 중괄호 수, 부분 세그먼트, catch-all 위치)을 고정한다.
func TestParseChiPattern(t *testing.T) {
	cases := map[string][]string{
		"/":                  {"/"},
		"/*":                 {"/{**}", "/ cap"},
		"/a/{id:[0-9]{2,3}}": {"/a/{}"},
		"/f/{name}.json":     {"/f/{}.json", "/f/.json variant"},
		"/f/v{n:[0-9]+}":     {"/f/v{}"},
		"/files*":            {"/files/{**}", "/files cap", "/files/ variant"},
		"/{a}.{b}.tar/{c}.x": nil,
		"/a/{x}/b*":          {"/a/{}/b/{**}", "/a/{}/b cap", "/a/{}/b/ variant"},
	}
	for in, want := range cases {
		raw, dynamic, ok := parseChiPattern(in)
		if !ok {
			t.Errorf("%q: rejected", in)
			continue
		}
		if want == nil {
			if !dynamic {
				t.Errorf("%q: want dynamic", in)
			}
			continue
		}
		if got := shapeTemplates(buildShapes(raw)); dynamic || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: templates %v (dynamic %v), want %v", in, got, dynamic, want)
		}
	}
	for _, bad := range []string{"no-slash", "/a/*/b", "/{unclosed", "/*/{x}"} {
		if _, _, ok := parseChiPattern(bad); ok {
			t.Errorf("%q: chi panics on this pattern; it must not become a template", bad)
		}
	}
}

// TestChiMountExpand는 Mount의 prefix·prefix/·prefix/* 합성(mux.go Mount·nextRoutePath)을 고정한다.
func TestChiMountExpand(t *testing.T) {
	cases := []struct {
		prefix, sub string
		want        []mountedPattern
	}{
		{"", "/x", []mountedPattern{{pattern: "/x"}}},
		{"/api", "/", []mountedPattern{{pattern: "/api/"}, {pattern: "/api"}}},
		{"/api", "/*", []mountedPattern{{pattern: "/api/*"}, {pattern: "/api", catchAllPrefix: true}}},
		{"/api", "/users", []mountedPattern{{pattern: "/api/users"}}},
		{"/api/", "/", []mountedPattern{{pattern: "/api/"}}},
		{"/api/", "/users", []mountedPattern{{pattern: "/api/users"}}},
	}
	for _, c := range cases {
		if got := chiMountExpand(c.prefix, c.sub); !reflect.DeepEqual(got, c.want) {
			t.Errorf("chiMountExpand(%q, %q) = %v, want %v", c.prefix, c.sub, got, c.want)
		}
	}
}

// TestGinJoinPaths는 gin joinPaths(utils.go)의 정리·끝 슬래시 보존을 고정한다.
func TestGinJoinPaths(t *testing.T) {
	cases := [][3]string{
		{"/", "", "/"}, {"/", "users", "/users"}, {"/v1", "/users/", "/v1/users/"},
		{"/v1", "a/../b", "/v1/b"}, {"/v1/", "", "/v1/"}, {"/v1", "//x", "/v1/x"},
	}
	for _, c := range cases {
		if got := ginJoinPaths(c[0], c[1]); got != c[2] {
			t.Errorf("ginJoinPaths(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

// TestParseGinAndEchoPaths는 gin·echo 파라미터 규칙(이름은 `/`까지, 접두사 부분 세그먼트)을 고정한다.
func TestParseGinAndEchoPaths(t *testing.T) {
	gin := map[string][]string{
		"/u/:id.json": {"/u/{}"},
		"/a_:n/x":     {"/a_{}/x"},
		"/f/*p":       {"/f/{**}", "/f/ variant"},
		"/*p":         {"/{**}", "/ cap"},
	}
	for in, want := range gin {
		raw, ok := parseGinPath(in)
		if got := shapeTemplates(buildShapes(raw)); !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("gin %q: %v (ok %v), want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"/a/*p/b", "/a/x*p", "/:a:b", "/:"} {
		if _, ok := parseGinPath(bad); ok {
			t.Errorf("gin %q: gin panics on this path", bad)
		}
	}
	echo := map[string][]string{
		"/u/:id":    {"/u/{}"},
		"/f-:n":     {"/f-{}"},
		"/s*":       {"/s/{**}", "/s cap", "/s/ variant"},
		"/a\\:b/:c": {"/a:b/{}"},
		"/":         {"/"},
	}
	for in, want := range echo {
		raw, dynamic, _ := parseEchoPath(in)
		if got := shapeTemplates(buildShapes(raw)); dynamic || !reflect.DeepEqual(got, want) {
			t.Errorf("echo %q: %v (dynamic %v), want %v", in, got, dynamic, want)
		}
	}
	if _, dynamic, _ := parseEchoPath("/a/*/b"); !dynamic {
		t.Error("echo mid-path * must be dynamic")
	}
}

// TestLegacyServeMuxDetection은 go 지시어·godebug·//go:debug의 httpmuxgo121 판정을 고정한다.
func TestLegacyServeMuxDetection(t *testing.T) {
	versions := map[string]bool{"1.21": true, "1.21.9": true, "1.22": false, "1.22rc1": false, "1.27": false, "2.0": false}
	for v, want := range versions {
		if got := goVersionLess(v, 1, 22); got != want {
			t.Errorf("goVersionLess(%q) = %v, want %v", v, got, want)
		}
	}
	gomod := "module x\n\ngo 1.27\n\n// httpmuxgo121=1 in a comment does not count\ngodebug (\n\tdefault=go1.21\n\thttpmuxgo121=1\n)\n"
	if !muxGoDebug.Match(godebugLines(gomod)) {
		t.Error("godebug block httpmuxgo121=1 not detected")
	}
	if muxGoDebug.Match(godebugLines("module x\n// httpmuxgo121=1\n")) {
		t.Error("a comment must not switch ServeMux semantics")
	}
}
