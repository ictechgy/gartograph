// 프레임워크 경로 패턴 → isthmus 정규 경로 템플릿 변환.
//
// 변환표의 근거는 각 프레임워크의 공식 소스다(routeapi.go 머리말의 버전). 요점:
//   - ServeMux(Go 1.22+ pattern.go): `[METHOD ][HOST]/PATH`. 리터럴 세그먼트는 url.PathUnescape
//     후 비교하고, `{x}`는 비어 있지 않은 세그먼트 하나(끝 슬래시 세그먼트와 맞지 않음), `{x...}`와
//     끝 `/`는 나머지 전부(빈 나머지 포함), `{$}`는 끝 슬래시만이다. 와일드카드는 세그먼트 전체여야 한다.
//   - chi(tree.go patNextSegment·findRoute): `{name}`·`{name:regexp}`는 다음 tail 바이트까지(기본 `/`),
//     세그먼트 안에 리터럴과 섞일 수 있다. 정규식은 `^…$`로 고정된다. `*`는 패턴 끝에만 오고 빈
//     나머지도 받는다. 끝 파라미터는 빈 값을 받지 않지만(xsearch == ""), 뒤에 같은 세그먼트의
//     리터럴이 오는 파라미터는 빈 값을 받는다(p == 0 허용).
//   - gin(tree.go findWildcard·getValue): `:name`은 `/`까지(이름에 `.` 포함 가능), 앞에 리터럴
//     접두사만 올 수 있다. `*name`은 `/` 바로 뒤, 경로 끝에만 오고 빈 나머지(`/x/`)도 받는다.
//   - echo(router.go insert·Find): `:name`은 `/`까지(`\:`는 리터럴 콜론), `*`는 나머지 전부(빈 값
//     포함)다. 자식이 없는 끝 파라미터 노드는 `/`를 넘어 나머지 전부를 받는다(isLeaf) — 이것은
//     템플릿으로 표현할 수 없어 수집 단계가 스코프 있는 route-coverage 한계로 신고한다.
package source

import (
	"path"
	"strings"
)

// paramConstraint는 계약의 paramConstraints 항목이다.
type paramConstraint struct {
	Segment int    `json:"segment"`
	Kind    string `json:"kind"`
	Pattern string `json:"pattern,omitempty"`
}

// routeShape는 정규 템플릿 하나와 그 증거다.
type routeShape struct {
	segments    []templateSegment
	constraints []paramConstraint
	// catchAllPrefix는 0세그먼트 catch-all을 펼친 접두사 decl이다(계약 catchAllPrefix).
	catchAllPrefix bool
	// variant는 빈 값을 받는 자리를 빈 값으로 채운 변형이다 — 같은 라우터에 같은 템플릿의
	// 명시적 decl이 있으면 그쪽이 요청을 받으므로 뺀다.
	variant bool
}

// template은 정규 템플릿 문자열이다.
func (s routeShape) template() string { return renderTemplate(s.segments) }

// endsWithCatchAll은 끝 세그먼트가 `{**}`인지 본다.
func (s routeShape) endsWithCatchAll() bool {
	return len(s.segments) > 0 && s.segments[len(s.segments)-1].kind == segCatchAll
}

// endsWithSlash는 템플릿이 `/`로 끝나는지(끝 세그먼트가 빈 리터럴인지) 본다.
func (s routeShape) endsWithSlash() bool {
	last := s.segments[len(s.segments)-1]
	return last.kind == segLiteral && last.value == ""
}

// rawSeg는 프레임워크 패턴 세그먼트 하나의 해석이다(템플릿으로 펼치기 전).
type rawSeg struct {
	kind   string // segLiteral·segParam·segPartial·segCatchAll·segPartialCatchAll
	value  string // literal 값·partial 접두사·부분 catch-all 앞 리터럴(정규 표기)
	suffix string // partial 접미사(정규 표기)
	regex  string // 파라미터 정규식(chi)
	// emptyOK는 파라미터가 빈 값을 받는 자리라는 표시다(빈 값 변형 대상).
	emptyOK bool
}

// segPartialCatchAll은 세그먼트 안 리터럴 뒤의 catch-all(`/files*`)이다 — 정규 문법으로 쓸 수
// 없어(catch-all-partial) 접두사 decl·`{**}`·빈 변형과 스코프 있는 한계로 펼친다.
const segPartialCatchAll = "partial-catch-all"

// maxRouteVariants는 한 등록이 펼칠 수 있는 템플릿 수의 상한이다(계약의 16개).
const maxRouteVariants = 16

// parsedRoute는 한 패턴의 변환 결과다.
type parsedRoute struct {
	shapes []routeShape
	// dynamic은 정규 템플릿으로 쓸 수 없는 패턴이다(한 세그먼트의 파라미터 둘 이상 등).
	dynamic bool
	// capped는 펼칠 변형이 상한을 넘었다는 표시다(dynamic과 함께).
	capped bool
	// anySuffixUnder는 부분 catch-all이 세그먼트 경계가 아닌 경로(`/filesX`)도 받는 부모 템플릿이다.
	anySuffixUnder string
	// leafTokens는 echo 끝 파라미터 판정을 위한 정규화 토큰열(`/users/:`)이다.
	leafTokens string
}

// buildShapes는 해석한 세그먼트를 템플릿과 변형으로 펼친다.
func buildShapes(raw []rawSeg) parsedRoute {
	var out parsedRoute
	if n := len(raw); n > 0 && raw[n-1].kind == segPartialCatchAll {
		return partialCatchAllShapes(raw)
	}
	base, constraints, emptyAt := baseShape(raw)
	shapes := []routeShape{{segments: base, constraints: constraints}}
	if len(base) > 0 && base[len(base)-1].kind == segCatchAll {
		shapes = append(shapes, catchAllEmpty(base[:len(base)-1], constraints))
	}
	shapes, capped := expandEmptyValues(shapes, emptyAt)
	if capped {
		return parsedRoute{dynamic: true, capped: true}
	}
	out.shapes = shapes
	return out
}

// baseShape는 세그먼트를 정규 템플릿으로 옮기고, 제약과 빈 값 자리를 모은다.
func baseShape(raw []rawSeg) ([]templateSegment, []paramConstraint, []int) {
	segments := make([]templateSegment, len(raw))
	var constraints []paramConstraint
	var emptyAt []int
	for i, r := range raw {
		switch r.kind {
		case segParam:
			segments[i] = templateSegment{kind: segParam}
		case segPartial:
			segments[i] = templateSegment{kind: segPartial, prefix: r.value, suffix: r.suffix}
		case segCatchAll:
			segments[i] = templateSegment{kind: segCatchAll}
		default:
			segments[i] = templateSegment{kind: segLiteral, value: r.value}
		}
		if r.regex != "" {
			constraints = append(constraints, regexConstraint(i, r.regex))
		}
		if r.emptyOK {
			emptyAt = append(emptyAt, i)
		}
	}
	return segments, constraints, emptyAt
}

// regexConstraint는 chi 정규식을 제약으로 옮긴다. 정수만 받는 정규식은 int로 분류한다 —
// 계약의 int(`[+-]?[0-9]+`)는 더 넓은 정의라 호출 리터럴을 거짓으로 빼지 않는다.
func regexConstraint(segment int, regex string) paramConstraint {
	body := strings.TrimSuffix(strings.TrimPrefix(regex, "^"), "$")
	if body == "[0-9]+" || body == `\d+` {
		return paramConstraint{Segment: segment, Kind: "int"}
	}
	return paramConstraint{Segment: segment, Kind: "regex", Pattern: regex}
}

// catchAllEmpty는 끝 catch-all이 빈 나머지를 받는 변형이다. 루트(`/{**}`)의 빈 나머지는 `/`
// 자체라 계약의 catch-all 접두사 decl이고, 그 밖은 끝 슬래시 경로(`/files/`)의 빈 값 변형이다.
func catchAllEmpty(base []templateSegment, constraints []paramConstraint) routeShape {
	segments := append(append([]templateSegment(nil), base...), templateSegment{kind: segLiteral})
	if len(base) == 0 {
		return routeShape{segments: segments, constraints: constraints, catchAllPrefix: true}
	}
	return routeShape{segments: segments, constraints: constraints, variant: true}
}

// partialCatchAllShapes는 `…/files*`를 펼친다: `…/files/{**}`, 접두사 decl `…/files`,
// 빈 변형 `…/files/`. `…/filesX` 같은 세그먼트 안 나머지는 템플릿으로 쓸 수 없어 부모 템플릿을
// 스코프로 돌려준다.
func partialCatchAllShapes(raw []rawSeg) parsedRoute {
	last := raw[len(raw)-1]
	head := append(append([]rawSeg(nil), raw[:len(raw)-1]...), rawSeg{kind: segLiteral, value: last.value})
	base, constraints, emptyAt := baseShape(head)
	parent := routeShape{segments: base[:len(base)-1]}
	all := append(append([]templateSegment(nil), base...), templateSegment{kind: segCatchAll})
	shapes := []routeShape{
		{segments: all, constraints: constraints},
		{segments: base, constraints: constraints, catchAllPrefix: true},
		{segments: append(append([]templateSegment(nil), base...), templateSegment{kind: segLiteral}),
			constraints: constraints, variant: true},
	}
	shapes, capped := expandEmptyValues(shapes, emptyAt)
	if capped {
		return parsedRoute{dynamic: true, capped: true}
	}
	scope := "/"
	if len(parent.segments) > 0 {
		scope = parent.template()
	}
	return parsedRoute{shapes: shapes, anySuffixUnder: scope}
}

// expandEmptyValues는 빈 값을 받는 파라미터 자리마다 빈 값 변형을 더한다(계약 "빈 값 변형").
// 변형 수가 상한을 넘으면 capped다.
func expandEmptyValues(shapes []routeShape, emptyAt []int) ([]routeShape, bool) {
	if len(emptyAt) == 0 {
		return shapes, false
	}
	total := len(shapes) << len(emptyAt)
	if len(emptyAt) > 4 || total > maxRouteVariants {
		return nil, true
	}
	var out []routeShape
	for _, shape := range shapes {
		for mask := 0; mask < 1<<len(emptyAt); mask++ {
			out = append(out, emptied(shape, emptyAt, mask))
		}
	}
	return out, false
}

// emptied는 mask가 켠 자리의 부분 세그먼트를 빈 값(접두사+접미사 리터럴)으로 바꾼 변형이다.
func emptied(shape routeShape, emptyAt []int, mask int) routeShape {
	if mask == 0 {
		return shape
	}
	segments := append([]templateSegment(nil), shape.segments...)
	emptiedAt := map[int]bool{}
	for bit, at := range emptyAt {
		if mask&(1<<bit) != 0 {
			s := segments[at]
			segments[at] = templateSegment{kind: segLiteral, value: s.prefix + s.suffix}
			emptiedAt[at] = true
		}
	}
	var constraints []paramConstraint
	for _, c := range shape.constraints {
		if !emptiedAt[c.Segment] {
			constraints = append(constraints, c)
		}
	}
	return routeShape{segments: segments, constraints: constraints,
		catchAllPrefix: false, variant: true}
}

// serveMuxPattern은 ServeMux 패턴의 해석 결과다.
type serveMuxPattern struct {
	method string // 빈 문자열이면 method 없는 패턴(모든 동사)
	host   string
	raw    []rawSeg
	// valid가 거짓이면 등록이 패닉하거나(문법 오류) 어떤 요청과도 맞지 않는 패턴이다.
	valid bool
}

// parseServeMuxPattern은 Go 1.22+ ServeMux 패턴을 해석한다(net/http/pattern.go parsePattern).
func parseServeMuxPattern(s string) serveMuxPattern {
	method, rest := "", s
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		method, rest = s[:i], strings.TrimLeft(s[i+1:], " \t")
	}
	slash := strings.IndexByte(rest, '/')
	if s == "" || slash < 0 || strings.IndexByte(rest[:slash], '{') >= 0 || !validMethodToken(method) {
		return serveMuxPattern{}
	}
	p := serveMuxPattern{method: method, host: rest[:slash]}
	pathPart := rest[slash:]
	// CONNECT가 아닌 method의 정리되지 않은 경로는 어떤 요청과도 맞지 않는다(요청 경로는 정리된다).
	if method != "" && method != "CONNECT" && pathPart != cleanURLPath(pathPart) {
		return serveMuxPattern{}
	}
	raw, ok := serveMuxSegments(pathPart)
	if !ok {
		return serveMuxPattern{}
	}
	p.raw, p.valid = raw, true
	return p
}

// serveMuxSegments는 ServeMux 경로를 세그먼트로 해석한다. 끝 `/`와 `{x...}`는 catch-all,
// `{$}`는 끝 슬래시(빈 리터럴 세그먼트)다.
func serveMuxSegments(pathPart string) ([]rawSeg, bool) {
	var raw []rawSeg
	rest := pathPart
	for len(rest) > 0 {
		rest = rest[1:]
		if rest == "" {
			raw = append(raw, rawSeg{kind: segCatchAll})
			break
		}
		i := strings.IndexByte(rest, '/')
		if i < 0 {
			i = len(rest)
		}
		seg := rest[:i]
		rest = rest[i:]
		if !strings.Contains(seg, "{") {
			// ServeMux는 리터럴을 url.PathUnescape한 값으로 비교한다 — 계약 정규화(unreserved 디코드,
			// 나머지 대문자 %XX)가 같은 정규 표기를 만든다.
			raw = append(raw, rawSeg{kind: segLiteral, value: normalizeTemplateLiteral(seg)})
			continue
		}
		if seg[0] != '{' || seg[len(seg)-1] != '}' {
			return nil, false
		}
		name := seg[1 : len(seg)-1]
		switch {
		case name == "$":
			if rest != "" {
				return nil, false
			}
			raw = append(raw, rawSeg{kind: segLiteral})
		case strings.HasSuffix(name, "..."):
			if rest != "" || name == "..." {
				return nil, false
			}
			raw = append(raw, rawSeg{kind: segCatchAll})
		case name == "":
			return nil, false
		default:
			raw = append(raw, rawSeg{kind: segParam})
		}
	}
	return raw, true
}

// parseLegacyServeMuxPattern은 GODEBUG httpmuxgo121=1(또는 go 지시어 1.22 미만) ServeMux의
// 패턴이다. 동사·와일드카드가 없고 `/`로 끝나면 하위 트리, 아니면 정확한 경로다. host로
// 시작하는 패턴은 host 부분을 뗀다.
func parseLegacyServeMuxPattern(s string) serveMuxPattern {
	slash := strings.IndexByte(s, '/')
	if slash < 0 || strings.ContainsAny(s[:slash], " \t") {
		return serveMuxPattern{}
	}
	p := serveMuxPattern{host: s[:slash], valid: true}
	parts := strings.Split(s[slash+1:], "/")
	for i, part := range parts {
		if i == len(parts)-1 && part == "" {
			p.raw = append(p.raw, rawSeg{kind: segCatchAll})
			break
		}
		p.raw = append(p.raw, rawSeg{kind: segLiteral, value: encodeSegmentValue(part)})
	}
	return p
}

// validMethodToken은 ServeMux의 method 토큰 규칙(RFC 9110 token 문자)이다. 빈 값은 method 없음이다.
func validMethodToken(method string) bool {
	for i := 0; i < len(method); i++ {
		c := method[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !alnum && strings.IndexByte("!#$%&'*+-.^_`|~", c) < 0 {
			return false
		}
	}
	return true
}

// cleanURLPath는 net/http cleanPath와 같은 정리다(끝 슬래시 보존).
func cleanURLPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	np := path.Clean(p)
	if p[len(p)-1] == '/' && np != "/" {
		np += "/"
	}
	return np
}

// chiPiece는 chi 패턴 토큰 하나다.
type chiPiece struct {
	kind  string // segLiteral·segParam·segCatchAll
	text  string
	regex string
}

// parseChiPattern은 chi 패턴을 세그먼트로 해석한다(tree.go patNextSegment와 같은 토큰 규칙).
func parseChiPattern(pattern string) ([]rawSeg, bool, bool) {
	if !strings.HasPrefix(pattern, "/") {
		return nil, false, false
	}
	pieces, ok := chiPieces(pattern)
	if !ok {
		return nil, false, false
	}
	groups := splitPieces(pieces)
	raw := make([]rawSeg, 0, len(groups))
	for i, group := range groups {
		seg, dynamic := chiSegment(group, i == len(groups)-1)
		if dynamic {
			return nil, true, true
		}
		raw = append(raw, seg)
	}
	return raw, false, true
}

// chiPieces는 패턴을 리터럴·파라미터·catch-all 토큰으로 나눈다. 중괄호는 짝을 세어 정규식의
// `{2}` 같은 수량자를 담는다. `*`가 파라미터보다 앞서거나 끝이 아니면 chi가 패닉하는 패턴이다.
func chiPieces(pattern string) ([]chiPiece, bool) {
	var pieces []chiPiece
	for pattern != "" {
		ps, ws := strings.IndexByte(pattern, '{'), strings.IndexByte(pattern, '*')
		switch {
		case ps < 0 && ws < 0:
			return append(pieces, chiPiece{kind: segLiteral, text: pattern}), true
		case ws >= 0 && (ps < 0 || ws < ps):
			if ws != len(pattern)-1 || ps >= 0 {
				return nil, false
			}
			pieces = append(pieces, chiPiece{kind: segLiteral, text: pattern[:ws]}, chiPiece{kind: segCatchAll})
			return pieces, true
		}
		end := closingBrace(pattern, ps)
		if end < 0 {
			return nil, false
		}
		_, regex, isRegex := strings.Cut(pattern[ps+1:end], ":")
		if isRegex && regex != "" {
			if !strings.HasPrefix(regex, "^") {
				regex = "^" + regex
			}
			if !strings.HasSuffix(regex, "$") {
				regex += "$"
			}
		}
		pieces = append(pieces, chiPiece{kind: segLiteral, text: pattern[:ps]}, chiPiece{kind: segParam, regex: regex})
		pattern = pattern[end+1:]
	}
	return pieces, true
}

// closingBrace는 start의 `{`와 짝이 맞는 `}` 위치다(없으면 -1).
func closingBrace(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitPieces는 토큰을 `/`로 세그먼트 묶음으로 나눈다. 패턴이 `/`로 시작하므로 첫 묶음(루트 앞)은 버린다.
func splitPieces(pieces []chiPiece) [][]chiPiece {
	var groups [][]chiPiece
	var cur []chiPiece
	for _, piece := range pieces {
		if piece.kind != segLiteral {
			cur = append(cur, piece)
			continue
		}
		for i, part := range strings.Split(piece.text, "/") {
			if i > 0 {
				groups = append(groups, cur)
				cur = nil
			}
			if part != "" {
				cur = append(cur, chiPiece{kind: segLiteral, text: part})
			}
		}
	}
	groups = append(groups, cur)
	return groups[1:]
}

// chiSegment는 세그먼트 토큰 묶음 하나를 해석한다. 파라미터가 둘 이상이거나 catch-all 앞에
// 파라미터가 있으면 정규 템플릿으로 쓸 수 없다(dynamic).
func chiSegment(group []chiPiece, last bool) (rawSeg, bool) {
	var prefix, suffix strings.Builder
	params, param := 0, chiPiece{}
	for i, piece := range group {
		switch piece.kind {
		case segParam:
			params++
			param = piece
		case segCatchAll:
			if params > 0 || i != len(group)-1 || !last {
				return rawSeg{}, true
			}
			if prefix.Len() == 0 {
				return rawSeg{kind: segCatchAll}, false
			}
			return rawSeg{kind: segPartialCatchAll, value: encodeSegmentValue(prefix.String())}, false
		default:
			if params == 0 {
				prefix.WriteString(piece.text)
			} else {
				suffix.WriteString(piece.text)
			}
		}
	}
	switch {
	case params > 1:
		return rawSeg{}, true
	case params == 0:
		return rawSeg{kind: segLiteral, value: encodeSegmentValue(prefix.String())}, false
	case prefix.Len() == 0 && suffix.Len() == 0:
		return rawSeg{kind: segParam, regex: param.regex}, false
	}
	// 같은 세그먼트 안에서 리터럴이 뒤따르는 비정규식 파라미터는 빈 값을 받는다(findRoute p == 0).
	return rawSeg{kind: segPartial, value: encodeSegmentValue(prefix.String()),
		suffix: encodeSegmentValue(suffix.String()), regex: param.regex,
		emptyOK: suffix.Len() > 0 && param.regex == ""}, false
}

// chiMountExpand는 chi Mount(prefix, sub)의 경로 합성이다(mux.go Mount·nextRoutePath).
// 접두사가 `/`로 끝나지 않으면 prefix·prefix+"/"·prefix+"/*"가 모두 하위 라우터로 가고, 하위
// 라우터는 나머지를 "/"+나머지로 본다 — 그래서 하위 "/"는 prefix와 prefix+"/" 둘 다, 하위 "/*"는
// prefix 자체(catch-all 접두사)도 받는다. `/`로 끝나는 접두사는 prefix+"*" 하나만 등록한다.
// 반환은 (합성 패턴, catch-all 접두사 여부) 목록이다.
func chiMountExpand(prefix, sub string) []mountedPattern {
	switch {
	case prefix == "":
		return []mountedPattern{{pattern: sub}}
	case strings.HasSuffix(prefix, "/"):
		return []mountedPattern{{pattern: prefix + strings.TrimPrefix(sub, "/")}}
	}
	out := []mountedPattern{{pattern: prefix + sub}}
	switch sub {
	case "/":
		out = append(out, mountedPattern{pattern: prefix})
	case "/*":
		out = append(out, mountedPattern{pattern: prefix, catchAllPrefix: true})
	}
	return out
}

// mountedPattern은 마운트 합성 결과 패턴 하나다.
type mountedPattern struct {
	pattern        string
	catchAllPrefix bool
}

// ginJoinPaths는 gin joinPaths(utils.go)와 같다: path.Join으로 정리하고 상대 경로의 끝 슬래시를 보존한다.
func ginJoinPaths(absolute, relative string) string {
	if relative == "" {
		return absolute
	}
	final := path.Join(absolute, relative)
	if strings.HasSuffix(relative, "/") && !strings.HasSuffix(final, "/") {
		return final + "/"
	}
	return final
}

// parseGinPath는 gin 절대 경로를 해석한다. 두 번째 반환은 gin이 패닉하는(등록되지 않는) 경로다.
func parseGinPath(p string) ([]rawSeg, bool) {
	if !strings.HasPrefix(p, "/") {
		return nil, false
	}
	parts := strings.Split(p[1:], "/")
	raw := make([]rawSeg, 0, len(parts))
	for i, part := range parts {
		at := strings.IndexAny(part, ":*")
		switch {
		case at < 0:
			raw = append(raw, rawSeg{kind: segLiteral, value: encodeSegmentValue(part)})
		case strings.ContainsAny(part[at+1:], ":*") || at+1 == len(part):
			return nil, false
		case part[at] == '*':
			if at != 0 || i != len(parts)-1 {
				return nil, false
			}
			raw = append(raw, rawSeg{kind: segCatchAll})
		case at == 0:
			raw = append(raw, rawSeg{kind: segParam})
		default:
			raw = append(raw, rawSeg{kind: segPartial, value: encodeSegmentValue(part[:at])})
		}
	}
	return raw, true
}

// normalizeEchoPath는 echo normalizePathSlash다: 빈 경로는 "/", `/`로 시작하지 않으면 앞에 붙인다.
func normalizeEchoPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		return "/" + p
	}
	return p
}

// parseEchoPath는 echo 경로를 해석한다(router.go insert). 두 번째 반환은 정규 템플릿으로
// 쓸 수 없는 패턴(끝이 아닌 `*`)이다. leaf 판정 토큰열도 함께 돌려준다.
func parseEchoPath(p string) ([]rawSeg, bool, string) {
	parts := strings.Split(p[1:], "/")
	raw := make([]rawSeg, 0, len(parts))
	var tokens strings.Builder
	for i, part := range parts {
		tokens.WriteByte('/')
		seg, dynamic := echoSegment(part, i == len(parts)-1)
		if dynamic {
			return nil, true, ""
		}
		raw = append(raw, seg)
		tokens.WriteString(echoToken(part, seg))
	}
	return raw, false, tokens.String()
}

// echoSegment는 echo 세그먼트 하나를 해석한다. `\:`는 리터럴 콜론이다.
func echoSegment(part string, last bool) (rawSeg, bool) {
	var literal strings.Builder
	for i := 0; i < len(part); i++ {
		c := part[i]
		switch {
		case c == '\\' && i+1 < len(part) && part[i+1] == ':':
			literal.WriteByte(':')
			i++
		case c == ':':
			if literal.Len() == 0 {
				return rawSeg{kind: segParam}, false
			}
			return rawSeg{kind: segPartial, value: encodeSegmentValue(literal.String())}, false
		case c == '*':
			if i != len(part)-1 || !last {
				return rawSeg{}, true
			}
			if literal.Len() == 0 {
				return rawSeg{kind: segCatchAll}, false
			}
			return rawSeg{kind: segPartialCatchAll, value: encodeSegmentValue(literal.String())}, false
		default:
			literal.WriteByte(c)
		}
	}
	return rawSeg{kind: segLiteral, value: encodeSegmentValue(literal.String())}, false
}

// echoToken은 leaf 판정용 세그먼트 토큰이다 — 파라미터 이름은 트리 노드를 가르지 않으므로
// `:`로 접는다(echo는 같은 자리의 파라미터를 한 노드로 합친다).
func echoToken(part string, seg rawSeg) string {
	switch seg.kind {
	case segParam:
		return ":"
	case segPartial:
		return seg.value + ":"
	case segCatchAll:
		return "*"
	case segPartialCatchAll:
		return seg.value + "*"
	}
	return part
}
