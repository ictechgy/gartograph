// isthmus http 도메인의 정규 경로 템플릿 — 문법 검증과 생산자 정규화.
//
// 계약은 ../isthmus의 docs/GRAPH-EXCHANGE.md "정규 경로 템플릿" 절이 정본이고, 판정은
// 공유 적합성 벡터(conformance/http-template.json)의 template.grammar·template.normalize
// 사례로 고정한다. isthmus는 문법을 어긴 문서를 다시 정규화하지 않고 거부하므로, 생산자가
// 내기 전에 같은 검사기로 스스로 확인한다.
package source

import (
	"strings"
	"unicode/utf16"
)

// maxRouteTemplateLength는 템플릿·dynamic 원문의 최대 UTF-16 길이다(계약 상한).
const maxRouteTemplateLength = 2048

// 템플릿 세그먼트 종류다. isthmus route-template.ts의 RouteSegment와 같은 어휘다.
const (
	segLiteral  = "literal"
	segParam    = "param"
	segPartial  = "partial"
	segCatchAll = "catch-all"
)

// templateSegment는 정규 템플릿 세그먼트 하나다. 이름·정규식은 템플릿에 남지 않는다.
// value는 literal의 정규 표기, prefix·suffix는 partial의 골격이다.
type templateSegment struct {
	kind   string
	value  string
	prefix string
	suffix string
}

// render는 세그먼트 하나를 정규 표기로 쓴다.
func (s templateSegment) render() string {
	switch s.kind {
	case segParam:
		return "{}"
	case segPartial:
		return s.prefix + "{}" + s.suffix
	case segCatchAll:
		return "{**}"
	}
	return s.value
}

// renderTemplate은 세그먼트 목록을 정규 템플릿 문자열로 쓴다(해석의 역연산).
func renderTemplate(segments []templateSegment) string {
	parts := make([]string, len(segments))
	for i, s := range segments {
		parts[i] = s.render()
	}
	return "/" + strings.Join(parts, "/")
}

// templateProblem은 정규 문법을 어긴 이유 코드를 돌려준다(통과하면 빈 문자열).
// 사유 코드와 판정 순서는 isthmus parseRouteTemplate과 같다 — 벡터의 expect.reason이
// 같은 어휘를 쓰므로 생산자와 소비자가 같은 판정을 재현해야 한다.
func templateProblem(template string) string {
	if len(utf16.Encode([]rune(template))) > maxRouteTemplateLength {
		return "too-long"
	}
	if !strings.HasPrefix(template, "/") {
		return "not-rooted"
	}
	raw := strings.Split(template[1:], "/")
	for i, segment := range raw {
		kind, problem := segmentProblem(segment)
		if problem != "" {
			return problem
		}
		if kind == segCatchAll && i != len(raw)-1 {
			return "catch-all-not-last"
		}
	}
	return ""
}

// segmentProblem은 세그먼트 하나를 검사해 종류나 거부 사유를 돌려준다.
// `{**}`는 세그먼트 전체일 때만 토큰이고, `{}`는 세그먼트당 하나다.
func segmentProblem(raw string) (string, string) {
	if raw == "{**}" {
		return segCatchAll, ""
	}
	params := 0
	for i := 0; i < len(raw); {
		c := raw[i]
		switch {
		case c == '{':
			if strings.HasPrefix(raw[i:], "{**}") {
				return "", "catch-all-partial"
			}
			if i+1 >= len(raw) || raw[i+1] != '}' {
				return "", "stray-brace"
			}
			if params > 0 {
				return "", "multiple-parameters"
			}
			params++
			i += 2
		case c == '}':
			return "", "stray-brace"
		case c == '%':
			if problem := percentProblem(raw, i); problem != "" {
				return "", problem
			}
			i += 3
		case isPcharLiteral(c):
			i++
		default:
			return "", "invalid-character"
		}
	}
	if params == 0 {
		return segLiteral, ""
	}
	return segParam, ""
}

// percentProblem은 `%XX` 하나가 정규형(대문자 hex, unreserved 아님)인지 본다.
func percentProblem(raw string, i int) string {
	if i+2 >= len(raw) || !isHex(raw[i+1]) || !isHex(raw[i+2]) {
		return "malformed-percent"
	}
	hex := raw[i+1 : i+3]
	if hex != strings.ToUpper(hex) {
		return "lowercase-percent-hex"
	}
	if isUnreserved(hexByte(raw[i+1], raw[i+2])) {
		return "encoded-unreserved"
	}
	return ""
}

// normalizeTemplateLiteral은 경로 원문(한 세그먼트나 여러 세그먼트)을 정규 표기로 바꾼다.
// unreserved를 인코딩한 `%XX`는 디코드하고, 나머지 `%XX`는 대문자 hex로, pchar·`/`가 아닌
// 바이트(비ASCII UTF-8 바이트·공백·중괄호 등)는 대문자 `%XX`로 인코딩한다. 잘못된 `%`는
// 그 자체를 `%25`로 인코딩한다 — 원문을 버리지 않고 같은 규칙으로 옮긴다.
func normalizeTemplateLiteral(path string) string {
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch {
		case c == '%' && i+2 < len(path) && isHex(path[i+1]) && isHex(path[i+2]):
			decoded := hexByte(path[i+1], path[i+2])
			if isUnreserved(decoded) {
				b.WriteByte(decoded)
			} else {
				b.WriteByte('%')
				b.WriteString(strings.ToUpper(path[i+1 : i+3]))
			}
			i += 2
		case c == '/' || isPcharLiteral(c):
			b.WriteByte(c)
		default:
			writePercent(&b, c)
		}
	}
	return b.String()
}

// encodeSegmentValue는 이미 디코드된 세그먼트 값(ServeMux 리터럴처럼 프레임워크가
// unescape한 값)을 정규 표기로 쓴다. `/`와 `%`도 값의 일부라 인코딩한다.
func encodeSegmentValue(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if isPcharLiteral(c) {
			b.WriteByte(c)
			continue
		}
		writePercent(&b, c)
	}
	return b.String()
}

// writePercent는 바이트 하나를 대문자 `%XX`로 쓴다.
func writePercent(b *strings.Builder, c byte) {
	const digits = "0123456789ABCDEF"
	b.WriteByte('%')
	b.WriteByte(digits[c>>4])
	b.WriteByte(digits[c&0x0f])
}

// isPcharLiteral은 인코딩 없이 쓸 수 있는 pchar 리터럴(unreserved·sub-delims·`:`·`@`)인지 본다.
func isPcharLiteral(c byte) bool {
	return isUnreserved(c) || strings.IndexByte("!$&'()*+,;=:@", c) >= 0
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

// hexByte는 16진 숫자 두 개를 바이트로 바꾼다.
func hexByte(hi, lo byte) byte {
	return hexValue(hi)<<4 | hexValue(lo)
}

// hexValue는 16진 숫자 하나의 값이다.
func hexValue(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}
