// isthmus url-compose 벡터(벤더링 사본)로 route-call 조립 규칙을 검증한다.
//
// 벡터 입력의 조각({literal}·{value}·{queryTail})은 urlPart로 옮겨 제품 함수(composePath·composeURL·
// maskTemplate·base 결합·wrapperMethod)를 그대로 부른다. wrapper.location은 실제 스캐너로 확인한다.
package source

import (
	"encoding/json"
	"strings"
	"testing"
)

// vectorPart는 벡터의 경로 조각이다.
type vectorPart struct {
	Literal   *string `json:"literal"`
	Value     *string `json:"value"`
	QueryTail *string `json:"queryTail"`
}

// toParts는 벡터 조각을 제품 조각으로 바꾼다.
func toParts(in []vectorPart) []urlPart {
	var out []urlPart
	for _, p := range in {
		switch {
		case p.Literal != nil:
			out = append(out, literalPart(*p.Literal))
		case p.QueryTail != nil:
			out = append(out, urlPart{kind: partQueryTail})
		default:
			out = append(out, valuePart())
		}
	}
	return out
}

// composeExpect는 조립 벡터의 기대값이다.
type composeExpect struct {
	Template          string `json:"template"`
	ChannelPrefix     string `json:"channelPrefix"`
	QueryTailStripped bool   `json:"queryTailStripped"`
	PathAnchor        string `json:"pathAnchor"`
	Authority         string `json:"authority"`
	MaskedSegments    *int   `json:"maskedSegments"`
	Method            string `json:"method"`
	Line              int    `json:"line"`
}

// expectOf는 사례의 기대값이다(expect가 null이면 빈 값).
func expectOf(t *testing.T, c conformanceCase) composeExpect {
	var want composeExpect
	if len(c.Expect) > 0 && string(c.Expect) != "null" {
		mustDecode(t, c.Expect, &want)
	}
	return want
}

// TestConformanceComposePath는 경로 조립 규칙(보간·query 꼬리·suffix·정규화)을 벡터로 본다.
func TestConformanceComposePath(t *testing.T) {
	for _, rule := range []string{"compose.interpolation", "compose.query-tail", "compose.suffix", "compose.normalize"} {
		for _, c := range producerCases(t, rule) {
			var in struct{ Parts []vectorPart }
			mustDecode(t, c.Input, &in)
			want := expectOf(t, c)
			got := composePath(toParts(in.Parts))
			if got.dynamic != c.ExpectDynamic || got.template != want.Template ||
				got.channelPrefix != want.ChannelPrefix || got.queryTailStripped != want.QueryTailStripped {
				t.Errorf("%s: got %+v, want %+v dynamic=%v", c.ID, got, want, c.ExpectDynamic)
			}
		}
	}
}

// TestConformanceBaseJoin은 base 결합 벡터를 Go 결합으로 본다. rfc3986은 (*URL).ResolveReference,
// slash-join은 resty v2 결합, dio-concat은 단순 문자열 연결이다 — Go 문자열 `+`와 같다. dio가 더 하는
// `//` 축약·점 세그먼트 제거는 이 벡터 사례가 쓰지 않는다(Go 연결은 둘 다 하지 않는다).
func TestConformanceBaseJoin(t *testing.T) {
	for _, c := range producerCases(t, "compose.base-join") {
		var in struct {
			Join string
			Base *string
			Path string
		}
		mustDecode(t, c.Input, &in)
		base := []urlPart{valuePart()}
		if in.Base != nil {
			base = []urlPart{literalPart(*in.Base)}
		}
		path := []urlPart{literalPart(in.Path)}
		var joined []urlPart
		switch in.Join {
		case "rfc3986":
			joined = resolveReferenceParts(base, path)
		case "slash-join":
			joined = slashJoinParts(base, true, path)
		case "dio-concat":
			joined = appendParts(append([]urlPart(nil), base...), path...)
		default:
			t.Fatalf("%s: join %q is not modelled for this producer", c.ID, in.Join)
		}
		got := composeURL(joined, anchorRule{pathOnly: "base"})
		want := expectOf(t, c)
		ok := got.dynamic == c.ExpectDynamic && got.template == want.Template &&
			(want.PathAnchor == "" || got.anchor == want.PathAnchor) &&
			(want.Authority == "" || got.authority == want.Authority) &&
			(c.ExpectLimitation != "ambiguous-base-join:" || got.ambiguousJoin)
		if !ok {
			t.Errorf("%s: got %+v, want %+v dynamic=%v limitation=%q", c.ID, got, want, c.ExpectDynamic, c.ExpectLimitation)
		}
	}
}

// TestConformanceStripAndMask는 scheme·userinfo·query 제거와 마스킹 벡터를 본다.
func TestConformanceStripAndMask(t *testing.T) {
	for _, c := range producerCases(t, "compose.strip") {
		var in struct{ URL string }
		mustDecode(t, c.Input, &in)
		want := expectOf(t, c)
		got := composeURL([]urlPart{literalPart(in.URL)}, anchorRule{pathOnly: "root"})
		if got.template != want.Template || got.authority != want.Authority || got.queryTailStripped != want.QueryTailStripped {
			t.Errorf("%s: got %+v, want %+v", c.ID, got, want)
		}
	}
	for _, c := range producerCases(t, "compose.mask") {
		var in struct{ Authority, Template string }
		mustDecode(t, c.Input, &in)
		want := expectOf(t, c)
		template, masked := maskTemplate(in.Authority, in.Template)
		if template != want.Template || want.MaskedSegments == nil || masked != *want.MaskedSegments {
			t.Errorf("%s: got %s (%d), want %+v", c.ID, template, masked, want)
		}
	}
}

// TestConformanceWrapperMethod는 래퍼 동사 바인딩 벡터를 본다.
func TestConformanceWrapperMethod(t *testing.T) {
	for _, c := range producerCases(t, "wrapper.method") {
		var in struct {
			Declaration WrapperDecl
			Call        struct {
				Args []struct {
					Label string
					Value struct {
						EnumCase *string `json:"enumCase"`
						Literal  *string `json:"literal"`
					}
				}
			}
		}
		mustDecode(t, c.Input, &in)
		var args []wrapperArg
		for _, a := range in.Call.Args {
			arg := wrapperArg{label: a.Label, literal: a.Value.Literal}
			if a.Value.EnumCase != nil {
				arg.enumCase = *a.Value.EnumCase
			}
			args = append(args, arg)
		}
		want := expectOf(t, c)
		got := wrapperMethod(in.Declaration, args)
		if (got == "") != c.ExpectDynamic || (!c.ExpectDynamic && got != want.Method) {
			t.Errorf("%s: method %q, want %q dynamic=%v", c.ID, got, want.Method, c.ExpectDynamic)
		}
	}
}

// TestConformanceWrapperLocation은 여러 줄 래퍼 호출의 위치가 호출식이 시작하는 줄인지 실제 스캐너로 본다.
func TestConformanceWrapperLocation(t *testing.T) {
	for _, c := range producerCases(t, "wrapper.location") {
		var in struct{ CallStartLine, MethodArgLine, PathArgLine int }
		mustDecode(t, c.Input, &in)
		want := expectOf(t, c)
		src := locationFixture(in.CallStartLine, in.MethodArgLine, in.PathArgLine)
		dir := clientModule(t, "", map[string]string{"app/app.go": src})
		idx := 1
		file := &WrapperFile{Format: "http-wrappers", Version: 1, Wrappers: []WrapperDecl{{Language: "go",
			Kind: "function", Owner: "example.com/fixture/app", Name: "Send", MethodArg: &WrapperArgRef{Label: "method"},
			PathArg: &WrapperArgRef{Index: &idx}, PathAnchor: "root"}}}
		doc := clientDoc(t, dir, file)
		if len(doc.Facts) != 1 || doc.Facts[0].Location.Line != want.Line {
			data, _ := json.Marshal(doc.Facts)
			t.Errorf("%s: facts %s, want one fact on line %d", c.ID, data, want.Line)
		}
	}
}

// locationFixture는 호출식이 callLine에서 시작하고 동사·경로 인자가 그 뒤 줄에 오는 소스를 만든다.
func locationFixture(callLine, methodLine, pathLine int) string {
	var b strings.Builder
	b.WriteString("package app\n\nfunc Send(method, path string) {}\n\nfunc Use() {\n")
	line := 6
	for ; line < callLine; line++ {
		b.WriteString("\t// padding\n")
	}
	b.WriteString("\tSend(\n")
	line++
	for ; line < methodLine; line++ {
		b.WriteString("\t\t// padding\n")
	}
	b.WriteString("\t\t\"GET\",\n")
	line++
	for ; line < pathLine; line++ {
		b.WriteString("\t\t// padding\n")
	}
	b.WriteString("\t\t\"/v1/items\",\n\t)\n}\n")
	return b.String()
}
