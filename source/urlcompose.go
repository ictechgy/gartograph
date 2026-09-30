// route-call URL 조립 — 평가한 URL 조각을 isthmus 정규 경로 템플릿으로 만든다.
//
// 규칙의 정본은 ../isthmus의 docs/HTTP-WRAPPERS.md "공통 해석 규칙"이고, 판정은 공유 적합성
// 벡터(conformance/url-compose.json)로 고정한다. 여기에는 언어와 무관한 조립 규칙(query 꼬리,
// 세그먼트 보간, 정규화, scheme·userinfo 제거, 마스킹)과 base 결합 방식만 둔다. 어떤 Go 식이 어떤
// 조각이 되는지는 urlexpr.go, 어떤 API가 어떤 결합을 쓰는지는 clientroutes.go가 정한다.
package source

import (
	"net/url"
	"regexp"
	"strings"
)

// partKind는 URL 조각의 종류다.
type partKind int

const (
	// partLiteral은 URL 문자열에 그대로 들어가는 원문 조각이다(인코딩된 URL 표기).
	partLiteral partKind = iota
	// partValue는 값을 모르는 조각이다. 경로에서는 세그먼트 전체를 채울 때만 `{}`가 된다.
	partValue
	// partOrigin은 scheme·authority 자리만 채우는 값이다 — 경로를 담을 수 없음이 증명됐다
	// (url.URL{Host: h}의 Host는 String()이 `/`를 이스케이프한다). 뒤의 경로는 root다.
	partOrigin
	// partQueryTail은 비어 있지 않은 값이 모두 `?`로 시작하고 나머지는 빈 문자열인 끝 조각이다
	// (compose.suffix). 경로 끝에 올 때만 떼어 낼 수 있다.
	partQueryTail
)

// urlPart는 평가한 URL 식의 조각 하나다.
type urlPart struct {
	kind partKind
	// text는 partLiteral의 원문이다.
	text string
	// param은 값이 감싸는 함수의 파라미터에서 왔다는 표시다(http-wrapper-undeclared 판정).
	param bool
	// ref는 값을 담은 선언(패키지 변수·필드)의 정점 ID다 — base 식이면 route-call baseRef가 된다.
	ref string
	// multi는 값이 세그먼트 여러 개일 수 있다는 표시다(resty raw path param 등). 경로에 오면 dynamic이다.
	multi bool
	// ambiguous는 미상 base와의 결합을 증명하지 못해 값이 된 결과라는 표시다(`..`가 base로 오르는 경로,
	// 빈 참조) — 조립이 ambiguous-base-join으로 센다.
	ambiguous bool
}

// literalPart는 원문 조각을 만든다.
func literalPart(text string) urlPart { return urlPart{kind: partLiteral, text: text} }

// valuePart는 값을 모르는 조각을 만든다.
func valuePart() urlPart { return urlPart{kind: partValue} }

// appendParts는 조각을 이어 붙이며 이웃한 원문 조각을 합친다 — 규칙이 "원문 안의 `/`"를 볼 때
// 조각 경계에 흔들리지 않게 하기 위해서다. 빈 원문은 버린다.
func appendParts(dst []urlPart, src ...urlPart) []urlPart {
	for _, p := range src {
		if p.kind == partLiteral {
			if p.text == "" {
				continue
			}
			if n := len(dst); n > 0 && dst[n-1].kind == partLiteral {
				dst[n-1].text += p.text
				continue
			}
		}
		dst = append(dst, p)
	}
	return dst
}

// composedPath는 경로 조각을 조립한 결과다.
type composedPath struct {
	template          string
	dynamic           bool
	channelPrefix     string
	queryTailStripped bool
}

// composePath는 `/`로 시작하는 경로 조각을 정규 템플릿으로 조립한다(compose.query-tail·suffix·
// interpolation·normalize). 값은 앞이 `/`로 끝나고 뒤가 `/`·query·끝일 때만 `{}`다. 그렇지 않으면
// dynamic이고, 문제 되는 첫 값 앞까지의 조립 결과가 `/`로 시작하면 그것이 channelPrefix다.
func composePath(parts []urlPart) composedPath {
	parts = appendParts(nil, parts...)
	var out composedPath
	var b strings.Builder
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		switch p.kind {
		case partLiteral:
			text, cut := cutQueryTail(p.text)
			b.WriteString(normalizeTemplateLiteral(text))
			if cut {
				out.queryTailStripped = true
				return finishPath(out, b.String())
			}
		case partQueryTail:
			if i == len(parts)-1 {
				out.queryTailStripped = true
				return finishPath(out, b.String())
			}
			return dynamicPath(b.String())
		default:
			if p.multi || p.kind == partOrigin || !wholeSegment(b.String(), parts, i) {
				return dynamicPath(b.String())
			}
			b.WriteString("{}")
		}
	}
	return finishPath(out, b.String())
}

// cutQueryTail은 원문의 첫 `?`·`#`부터 끝을 뗀다.
func cutQueryTail(text string) (string, bool) {
	if i := strings.IndexAny(text, "?#"); i >= 0 {
		return text[:i], true
	}
	return text, false
}

// wholeSegment는 i번째 값이 세그먼트 전체를 채우는지 본다 — 앞까지의 조립이 `/`로 끝나고 뒤가
// `/`·`?`·`#`로 시작하는 원문이거나 경로 끝(떼어 낼 query 꼬리 포함)이어야 한다.
func wholeSegment(before string, parts []urlPart, i int) bool {
	if !strings.HasSuffix(before, "/") {
		return false
	}
	if i == len(parts)-1 {
		return true
	}
	next := parts[i+1]
	switch next.kind {
	case partLiteral:
		return strings.IndexAny(next.text[:1], "/?#") == 0
	case partQueryTail:
		return i+1 == len(parts)-1
	}
	return false
}

// finishPath는 정적 템플릿을 확정한다. 빈 경로는 `/`다(Go는 빈 경로를 `/`로 보낸다).
func finishPath(out composedPath, template string) composedPath {
	if template == "" {
		template = "/"
	}
	if templateProblem(template) != "" {
		return composedPath{dynamic: true}
	}
	out.template = template
	return out
}

// dynamicPath는 문제 되는 값 앞까지의 조립으로 channelPrefix를 만든다.
func dynamicPath(before string) composedPath {
	out := composedPath{dynamic: true}
	if strings.HasPrefix(before, "/") && templateProblem(before) == "" {
		out.channelPrefix = before
	}
	return out
}

// anchorRule은 base가 없는 경로(`/x`·`x`)를 API가 어떻게 보내는지다.
type anchorRule struct {
	// pathOnly는 `/`로 시작하는 경로의 앵커다("root"·"base").
	pathOnly string
	// relativeBase는 `/` 없이 시작하는 경로를 base 뒤에 슬래시로 붙이는지다. 거짓이면 dynamic이다.
	relativeBase bool
}

// composedURL은 URL 하나를 route-call 필드로 조립한 결과다.
type composedURL struct {
	composedPath
	anchor         string
	authority      string
	baseRef        string
	maskedSegments int
	// unresolvedBase는 base 식을 풀지 못해 base 앵커가 된 호출이다(unresolved-base-url 계수).
	unresolvedBase bool
	// ambiguousJoin은 미상 base 뒤에 `/` 없이 붙은 경로다(ambiguous-base-join 계수).
	ambiguousJoin bool
	// param은 URL 머리(base 자리나 authority 바로 뒤)의 값이 감싸는 함수의 파라미터라는 표시다 —
	// 경로를 통째로 받아 요청에 넘기는 선언되지 않은 래퍼의 모양이다(http-wrapper-undeclared).
	param bool
}

// schemePrefix는 절대 URL의 scheme과 `//`다(RFC 3986 scheme 문법).
var schemePrefix = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://`)

// composeURL은 URL 조각을 route-call 필드로 조립한다(compose.strip·base-join·mask 포함).
//   - `scheme://authority/…` 원문: authority를 소문자로, 경로는 root다. authority에 값이 섞이면 그 값이
//     경로를 담을 수 있어 base다(계약 "host가 동적이면 base").
//   - partOrigin으로 시작: 경로는 root, authority는 모른다.
//   - 값으로 시작: 문자열 연결의 미상 base다. 뒤가 `/`로 시작하면 base, `/` 없이 붙으면 dynamic과
//     ambiguous-base-join이다.
//   - `/`나 그 밖의 원문으로 시작: rule이 정한다.
func composeURL(parts []urlPart, rule anchorRule) composedURL {
	parts = appendParts(nil, parts...)
	out := composedURL{anchor: "root"}
	if len(parts) == 0 {
		return dynamicURL(out)
	}
	first := parts[0]
	switch first.kind {
	case partOrigin:
		return finishURL(out, parts[1:])
	case partValue, partQueryTail:
		return composeAfterBase(out, first, parts[1:])
	}
	text := first.text
	if loc := schemePrefix.FindStringIndex(text); loc != nil {
		return composeAbsolute(out, text[loc[1]:], parts[1:])
	}
	switch {
	case strings.HasPrefix(text, "//"):
		return dynamicURL(out)
	case strings.HasPrefix(text, "/"):
		// 경로만 있는 URL은 base 식을 잇지 않았으므로 unresolved-base-url로 세지 않는다.
		out.anchor = rule.pathOnly
		return finishURL(out, parts)
	case rule.relativeBase && !strings.Contains(text[:strings.IndexAny(text+"/", "/?#")], ":"):
		out.anchor = "base"
		return finishURL(out, appendParts([]urlPart{literalPart("/")}, parts...))
	}
	return dynamicURL(out)
}

// composeAfterBase는 미상 base 값 뒤의 경로를 조립한다(단순 문자열 연결).
func composeAfterBase(out composedURL, base urlPart, rest []urlPart) composedURL {
	out.anchor = "base"
	out.baseRef = base.ref
	out.param = base.param
	out.ambiguousJoin = base.ambiguous
	if len(rest) == 0 || rest[0].kind != partLiteral {
		return dynamicURL(out)
	}
	if !strings.HasPrefix(rest[0].text, "/") {
		out.ambiguousJoin = !strings.HasPrefix(rest[0].text, "?") && !strings.HasPrefix(rest[0].text, "#")
		return dynamicURL(out)
	}
	out.unresolvedBase = true
	return finishURL(out, rest)
}

// composeAbsolute는 `scheme://` 뒤의 authority와 경로를 조립한다.
func composeAbsolute(out composedURL, afterScheme string, rest []urlPart) composedURL {
	if i := strings.IndexAny(afterScheme, "/?#"); i >= 0 {
		out.authority = normalizeAuthority(afterScheme[:i])
		return finishURL(out, appendParts([]urlPart{literalPart(afterScheme[i:])}, rest...))
	}
	if len(rest) == 0 {
		out.authority = normalizeAuthority(afterScheme)
		return finishURL(out, nil)
	}
	// authority 안에 값이 있다 — 첫 `/`부터가 경로이고 앵커는 base다.
	out.anchor = "base"
	for i, p := range rest {
		if p.kind != partLiteral {
			out.baseRef = firstNonEmpty(out.baseRef, p.ref)
			out.param = out.param || p.param
			continue
		}
		j := strings.IndexAny(p.text, "/?#")
		if j < 0 {
			continue
		}
		if p.text[j] != '/' {
			return dynamicURL(out)
		}
		out.unresolvedBase = true
		return finishURL(out, appendParts([]urlPart{literalPart(p.text[j:])}, rest[i+1:]...))
	}
	return dynamicURL(out)
}

// finishURL은 경로를 조립하고 마스킹한다.
func finishURL(out composedURL, path []urlPart) composedURL {
	if len(path) > 0 && path[0].kind == partLiteral && !strings.HasPrefix(path[0].text, "/") &&
		strings.IndexAny(path[0].text[:1], "?#") != 0 {
		return dynamicURL(out)
	}
	out.composedPath = composePath(path)
	if out.dynamic {
		out.channelPrefix, out.maskedSegments = maskTemplate(out.authority, out.channelPrefix)
		if out.channelPrefix == "" {
			out.maskedSegments = 0
		}
		return out
	}
	out.template, out.maskedSegments = maskTemplate(out.authority, out.template)
	return out
}

// dynamicURL은 경로를 증명하지 못한 결과다.
func dynamicURL(out composedURL) composedURL {
	out.composedPath = composedPath{dynamic: true}
	return out
}

// authorityPattern은 계약이 받는 authority 문법이다 — isthmus parse.ts authorityPattern과 같은 식이다
// (점으로 나눈 소문자 label, IPv6는 [...], 포트 1~5자리). 이보다 넓으면 isthmus가 문서를 거부한다.
var authorityPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*|\[[0-9a-f:.]+\])(?::[0-9]{1,5})?$`)

// normalizeAuthority는 userinfo를 떼고 host를 소문자로 바꾼다(compose.strip). net/http처럼 끝 `:`는
// 뗀다(NewRequestWithContext의 u.Host = TrimSuffix(u.Host, ":")). 문법에 맞지 않으면 싣지 않는다.
func normalizeAuthority(raw string) string {
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		raw = raw[i+1:]
	}
	host := strings.TrimSuffix(strings.ToLower(raw), ":")
	if !authorityPattern.MatchString(host) {
		return ""
	}
	return host
}

// maskMinLength는 고엔트로피 세그먼트의 최소 길이다(compose.mask).
const maskMinLength = 16

// maskTemplate은 고엔트로피 리터럴 세그먼트와 알려진 웹훅 host의 경로를 `{}`로 바꾸고 바꾼 수를
// 돌려준다. 퍼센트 디코드한 세그먼트가 16자 이상이고 ASCII 글자와 숫자를 모두 담으면 고엔트로피다.
func maskTemplate(authority, template string) (string, int) {
	if template == "" || template == "/" {
		return template, 0
	}
	segments := strings.Split(template[1:], "/")
	from := webhookMaskFrom(authority, segments)
	masked := 0
	for i, s := range segments {
		if s == "" || s == "{}" || strings.Contains(s, "{}") {
			continue
		}
		if (from >= 0 && i >= from) || highEntropy(s) {
			segments[i] = "{}"
			masked++
		}
	}
	return "/" + strings.Join(segments, "/"), masked
}

// webhookMaskFrom은 웹훅 host에서 모두 가릴 첫 세그먼트 번호다(해당 없으면 -1).
func webhookMaskFrom(authority string, segments []string) int {
	host := authority
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.HasSuffix(host, "]") {
		host = host[:i]
	}
	switch host {
	case "hooks.slack.com":
		return 0
	case "discord.com", "discordapp.com":
		if len(segments) >= 2 && segments[0] == "api" && segments[1] == "webhooks" {
			return 2
		}
	}
	return -1
}

// highEntropy는 세그먼트가 고엔트로피 기준을 넘는지 본다.
func highEntropy(segment string) bool {
	decoded, err := url.PathUnescape(segment)
	if err != nil {
		decoded = segment
	}
	if len([]rune(decoded)) < maskMinLength {
		return false
	}
	return strings.ContainsAny(decoded, "0123456789") &&
		strings.IndexFunc(decoded, func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }) >= 0
}
