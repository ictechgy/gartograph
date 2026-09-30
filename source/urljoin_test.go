// base 결합 테스트 — 원문만 있는 입력은 실제 net/url·path 함수의 결과가 정답이다(자기 오라클).
package source

import (
	"net/url"
	"path"
	"testing"
)

// literals는 원문들을 조각 목록으로 바꾼다.
func literals(texts ...string) [][]urlPart {
	out := make([][]urlPart, len(texts))
	for i, t := range texts {
		out[i] = []urlPart{literalPart(t)}
	}
	return out
}

// templateOf는 조각을 조립한 템플릿이다(dynamic이면 "<dynamic>").
func templateOf(parts []urlPart) string {
	res := composeURL(parts, anchorRule{pathOnly: "root"})
	if res.dynamic {
		return "<dynamic>"
	}
	return res.template
}

// goEscapedPath는 URL 문자열을 Go가 보내는 경로 표기로 바꾼 뒤 정규 템플릿으로 조립한다(빈 경로는 "/").
func goEscapedPath(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return composePath([]urlPart{literalPart(u.EscapedPath())}).template
}

var joinBases = []string{"http://h", "http://h/", "http://h/v1", "http://h/v1/", "http://h/a/b/", "http://h/a//b"}

// TestJoinPathMatchesNetURL은 joinPathParts가 url.JoinPath와 같은 경로를 내는지 본다.
func TestJoinPathMatchesNetURL(t *testing.T) {
	elemSets := [][]string{{"users"}, {"/users"}, {"users/"}, {"a", "..", "b"}, {"./x"}, {"x//y"},
		{"..", "..", "z"}, {"a?b"}, {"", "c"}, {"d", "/"}, {}}
	for _, base := range joinBases {
		for _, elems := range elemSets {
			joined, err := url.JoinPath(base, elems...)
			if err != nil {
				t.Fatalf("JoinPath(%q, %q): %v", base, elems, err)
			}
			want := goEscapedPath(t, joined)
			got := templateOf(joinPathParts([]urlPart{literalPart(base)}, literals(elems...)))
			if got != want {
				t.Errorf("JoinPath(%q, %q) = %s, net/url sends %s (%s)", base, elems, got, want, joined)
			}
		}
	}
}

// TestResolveReferenceMatchesNetURL은 resolveReferenceParts가 (*URL).ResolveReference와 같은 경로를 내는지 본다.
func TestResolveReferenceMatchesNetURL(t *testing.T) {
	refs := []string{"x", "/x", "../x", "./", "..", "a/../b", "?q=1", "", "http://o/p/../q", "x/./y/../z/", "../../../x"}
	for _, base := range joinBases {
		b, _ := url.Parse(base)
		for _, ref := range refs {
			r, _ := url.Parse(ref)
			want := composePath([]urlPart{literalPart(b.ResolveReference(r).EscapedPath())}).template
			got := templateOf(resolveReferenceParts([]urlPart{literalPart(base)}, []urlPart{literalPart(ref)}))
			if got != want {
				t.Errorf("ResolveReference(%q, %q) = %s, net/url gives %s", base, ref, got, want)
			}
		}
	}
}

// TestPathJoinMatchesStdlib는 pathJoinParts가 path.Join과 같은 문자열을 내는지 본다.
func TestPathJoinMatchesStdlib(t *testing.T) {
	sets := [][]string{{"/a", "b"}, {"a", "b/"}, {"/a/", "/b/"}, {"/a", "..", "..", "b"}, {"a", "..", "x"},
		{"", "/x"}, {"/", ""}, {"a", "."}, {"/a//b", "./c"}}
	for _, elems := range sets {
		want := path.Join(elems...)
		parts := pathJoinParts(literals(elems...))
		got := ""
		for _, p := range parts {
			got += p.text
		}
		if got != want {
			t.Errorf("path.Join(%q) = %q, want %q", elems, got, want)
		}
	}
}

// TestJoinsWithUnknownBase는 미상 base 결합의 앵커와 `..` 처리를 고정한다(벡터 compose.base-join의 Go 결합).
func TestJoinsWithUnknownBase(t *testing.T) {
	base := []urlPart{{kind: partValue, ref: "example.com/app.base"}}
	cases := []struct {
		name  string
		parts []urlPart
		want  string
	}{
		{"JoinPath rooted", joinPathParts(base, literals("/users", "7")), "base /users/7"},
		{"JoinPath relative", joinPathParts(base, literals("users")), "base /users"},
		{"JoinPath climbs", joinPathParts(base, literals("..", "x")), "base <dynamic>"},
		{"ResolveReference absolute path", resolveReferenceParts(base, []urlPart{literalPart("/x")}), "root /x"},
		{"ResolveReference relative", resolveReferenceParts(base, []urlPart{literalPart("x/y")}), "base /x/y"},
		{"ResolveReference climbs", resolveReferenceParts(base, []urlPart{literalPart("../x")}), "base <dynamic>"},
		{"resty rooted", slashJoinParts(base, true, []urlPart{literalPart("/x")}), "base /x"},
		{"resty relative", slashJoinParts(base, true, []urlPart{literalPart("x")}), "base /x"},
		{"resty literal base trimmed", slashJoinParts([]urlPart{literalPart("http://h/api//")}, true,
			[]urlPart{literalPart("x")}), "root /api/x"},
		{"resty literal base untrimmed", slashJoinParts([]urlPart{literalPart("http://h/api/")}, false,
			[]urlPart{literalPart("/x")}), "root /api//x"},
		{"concat rooted", appendParts(base, literalPart("/x")), "base /x"},
		{"concat relative", appendParts(base, literalPart("x")), "base <dynamic>"},
	}
	for _, c := range cases {
		res := composeURL(c.parts, anchorRule{pathOnly: "base"})
		got := res.anchor + " " + res.template
		if res.dynamic {
			got = res.anchor + " <dynamic>"
		}
		if got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	if res := composeURL(appendParts(base, literalPart("x")), anchorRule{}); !res.ambiguousJoin || res.baseRef != "example.com/app.base" {
		t.Errorf("concat relative: %+v", res)
	}
}

// TestNormalizeAuthority는 authority가 isthmus 문법(소문자 label·포트 5자리)을 넘지 않는지 본다.
func TestNormalizeAuthority(t *testing.T) {
	cases := map[string]string{
		"User:pw@API.Example.com:8080": "api.example.com:8080",
		"[::1]:443":                    "[::1]:443",
		"host:":                        "host",
		"my_host.internal":             "",
		"host:123456":                  "",
		"-bad.example.com":             "",
		"a..b":                         "",
	}
	for raw, want := range cases {
		if got := normalizeAuthority(raw); got != want {
			t.Errorf("normalizeAuthority(%q) = %q, want %q", raw, got, want)
		}
	}
}
