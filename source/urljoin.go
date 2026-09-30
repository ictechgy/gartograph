// route-call base 결합 — Go 표준 라이브러리와 resty가 base URL과 경로를 합치는 방식.
//
// 확인한 소스:
//   - Go 1.27.1 net/url url.go: (*URL).ResolveReference·resolvePath(RFC 3986 병합과 점 세그먼트
//     제거), JoinPath·(*URL).joinPath(path.Join으로 합치고 마지막 원소의 끝 `/` 하나를 보존), Parse
//   - Go 1.27.1 path.Join·Clean(빈 원소 무시, `//`·`.`·`..` 정리, 끝 `/` 제거)
//   - github.com/go-resty/resty/v2 v2.17.2 client.go SetBaseURL(끝 `/`를 모두 뗀다)·middleware.go
//     parseRequestURL(상대 URL이면 앞에 `/`를 붙여 base 문자열 뒤에 잇고, 절대 URL은 그대로 쓴다)
//
// 값 조각은 세그먼트 하나를 채우는 불투명한 값으로 본다(계약의 `{}`가 비어 있지 않은 단일 세그먼트와
// 맞는 것과 같은 가정). 미상 base 앞자리를 `..`가 거슬러 오르면 결과를 증명할 수 없어 값 하나(dynamic)가 된다.
package source

import (
	"net/url"
	"strings"
)

// urlSegment는 경로 세그먼트 하나다(조각 여러 개가 한 세그먼트를 이룰 수 있다).
type urlSegment []urlPart

// isDot은 세그먼트가 원문 "."인지 본다.
func (s urlSegment) isDot() bool { return len(s) == 1 && s[0].kind == partLiteral && s[0].text == "." }

// isDotDot은 세그먼트가 원문 ".."인지 본다.
func (s urlSegment) isDotDot() bool {
	return len(s) == 1 && s[0].kind == partLiteral && s[0].text == ".."
}

// isEmpty는 빈 세그먼트인지 본다.
func (s urlSegment) isEmpty() bool { return len(s) == 0 }

// splitSegments는 경로 조각을 `/`로 나눈다. 앞의 `/`는 absolute로 돌려주고, 끝 `/`는 빈 마지막
// 세그먼트로 남는다.
func splitSegments(parts []urlPart) (bool, []urlSegment) {
	var segs []urlSegment
	current := urlSegment{}
	for _, p := range appendParts(nil, parts...) {
		if p.kind != partLiteral {
			current = append(current, p)
			continue
		}
		pieces := strings.Split(p.text, "/")
		for i, piece := range pieces {
			if i > 0 {
				segs = append(segs, current)
				current = urlSegment{}
			}
			if piece != "" {
				current = append(current, literalPart(piece))
			}
		}
	}
	segs = append(segs, current)
	absolute := len(segs) > 1 && segs[0].isEmpty()
	if absolute {
		segs = segs[1:]
	}
	return absolute, segs
}

// renderSegments는 세그먼트를 조각으로 되돌린다.
func renderSegments(absolute bool, segs []urlSegment) []urlPart {
	var out []urlPart
	for i, s := range segs {
		if i > 0 || absolute {
			out = appendParts(out, literalPart("/"))
		}
		out = appendParts(out, s...)
	}
	if absolute && len(segs) == 0 {
		out = appendParts(out, literalPart("/"))
	}
	return out
}

// cleanSegments는 path.Clean의 세그먼트 규칙이다: 빈 세그먼트와 "."를 지우고 ".."는 앞 세그먼트를
// 지운다(absolute면 루트에서 멈춘다). floor는 지울 수 없는 앞 세그먼트 수다 — 미상 base 경로처럼
// 거슬러 오를 수 없는 자리를 넘으면 false다.
func cleanSegments(segs []urlSegment, absolute bool) ([]urlSegment, bool) {
	var out []urlSegment
	for _, s := range segs {
		switch {
		case s.isEmpty() || s.isDot():
			continue
		case s.isDotDot():
			if len(out) > 0 && !out[len(out)-1].isDotDot() {
				out = out[:len(out)-1]
				continue
			}
			if !absolute {
				return nil, false
			}
			continue
		}
		out = append(out, s)
	}
	return out, true
}

// removeDotSegments는 RFC 3986 5.2.4(Go resolvePath)의 점 세그먼트 제거다. 빈 세그먼트는 남긴다.
// 마지막 세그먼트가 "."·".."이면 끝 `/`를 남긴다. relative는 미상 base 뒤라 루트 위로 오르면 false다.
func removeDotSegments(segs []urlSegment, relative bool) ([]urlSegment, bool) {
	var out []urlSegment
	for i, s := range segs {
		last := i == len(segs)-1
		switch {
		case s.isDot():
			if last {
				out = append(out, urlSegment{})
			}
			continue
		case s.isDotDot():
			if len(out) == 0 && relative {
				return nil, false
			}
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, urlSegment{})
			}
			continue
		}
		out = append(out, s)
	}
	return out, true
}

// baseParts는 base URL을 (origin 조각, 경로 조각)으로 나눈 결과다. known이 거짓이면 base 경로를
// 모른다(값으로 시작하는 base).
type baseParts struct {
	origin []urlPart
	path   []urlPart
	known  bool
	// unknown은 미상 base 전체를 대신하는 값 조각이다(ref·param을 물려준다).
	unknown urlPart
}

// splitBase는 base URL 조각을 나눈다. 원문 하나면 net/url.Parse로 나누고, `scheme://authority/`
// 원문 뒤에 값이 오면 경로 안의 값으로, partOrigin으로 시작하면 origin을 모르는 root 경로로 본다.
func splitBase(parts []urlPart) baseParts {
	parts = appendParts(nil, parts...)
	if len(parts) == 0 {
		return baseParts{known: true}
	}
	first := parts[0]
	switch first.kind {
	case partOrigin:
		return baseParts{origin: parts[:1], path: parts[1:], known: pathRooted(parts[1:])}
	case partLiteral:
		if len(parts) == 1 {
			return splitLiteralBase(first.text)
		}
		if loc := schemePrefix.FindStringIndex(first.text); loc != nil {
			if i := strings.IndexByte(first.text[loc[1]:], '/'); i >= 0 {
				cut := loc[1] + i
				return baseParts{origin: []urlPart{literalPart(first.text[:cut])},
					path: appendParts([]urlPart{literalPart(first.text[cut:])}, parts[1:]...), known: true}
			}
		}
	}
	unknown := valuePart()
	for _, p := range parts {
		if p.kind != partLiteral {
			unknown.param = unknown.param || p.param
			unknown.ref = firstNonEmpty(unknown.ref, p.ref)
		}
	}
	return baseParts{unknown: unknown}
}

// pathRooted는 경로 조각이 비었거나 `/`로 시작하는지 본다.
func pathRooted(parts []urlPart) bool {
	return len(parts) == 0 || (parts[0].kind == partLiteral && strings.HasPrefix(parts[0].text, "/"))
}

// splitLiteralBase는 원문 base를 net/url.Parse로 나눈다. 경로는 EscapedPath다(Go가 보내는 표기).
func splitLiteralBase(text string) baseParts {
	u, err := url.Parse(text)
	if err != nil || u.Opaque != "" {
		return baseParts{unknown: valuePart()}
	}
	out := baseParts{known: true}
	if u.Scheme != "" || u.Host != "" {
		out.origin = []urlPart{literalPart(u.Scheme + "://" + u.Host)}
	}
	if p := u.EscapedPath(); p != "" {
		out.path = []urlPart{literalPart(p)}
	}
	return out
}

// escapePathElement는 JoinPath 원소의 `?`·`#`를 경로 문자로 이스케이프한다 — joinPath는 원소를
// setPath로 넣으므로 query 구분자가 되지 않고 EscapedPath에서 %3F·%23으로 나간다.
func escapePathElement(parts []urlPart) []urlPart {
	out := make([]urlPart, len(parts))
	for i, p := range parts {
		if p.kind == partLiteral {
			p.text = strings.NewReplacer("?", "%3F", "#", "%23").Replace(p.text)
		}
		out[i] = p
	}
	return out
}

// joinPathParts는 url.JoinPath(base, elem...)·(*URL).JoinPath(elem...)다: base 경로와 원소를
// path.Join으로 합치고, 마지막 원소가 `/`로 끝나면 끝 `/` 하나를 보존한다. host가 있으면 String()이
// 경로 앞에 `/`를 넣는다. 미상 base면 base 뒤에 정리한 원소를 붙인다(`..`가 base로 오르면 dynamic).
func joinPathParts(base []urlPart, elems [][]urlPart) []urlPart {
	b := splitBase(base)
	var joined []urlSegment
	trailing := false
	for _, e := range elems {
		e = escapePathElement(e)
		_, segs := splitSegments(e)
		joined = append(joined, segs...)
		if n := len(e); n > 0 {
			trailing = e[n-1].kind == partLiteral && strings.HasSuffix(e[n-1].text, "/")
		}
	}
	if !b.known {
		if len(elems) == 0 {
			return []urlPart{b.unknown}
		}
		cleaned, ok := cleanSegments(joined, false)
		if !ok {
			return []urlPart{b.unknown}
		}
		return withTrailing(appendParts([]urlPart{b.unknown}, renderSegments(true, cleaned)...), trailing)
	}
	// base가 상대 URL(origin 없음, `/` 없는 경로)이면 결과도 상대 경로다. origin이 있으면 String()이
	// 경로 앞에 `/`를 넣으므로 절대 경로와 같다.
	absolute := len(b.origin) > 0 || pathRooted(b.path)
	_, baseSegs := splitSegments(b.path)
	cleaned, ok := cleanSegments(append(baseSegs, joined...), absolute)
	if !ok {
		return []urlPart{valuePart()}
	}
	out := appendParts(append([]urlPart(nil), b.origin...), renderSegments(absolute, cleaned)...)
	if len(elems) == 0 {
		trailing = pathEndsWithSlash(b.path)
	}
	return withTrailing(out, trailing && len(cleaned) > 0)
}

// pathEndsWithSlash는 경로 조각이 `/`로 끝나는지 본다.
func pathEndsWithSlash(parts []urlPart) bool {
	n := len(parts)
	return n > 0 && parts[n-1].kind == partLiteral && strings.HasSuffix(parts[n-1].text, "/")
}

// withTrailing은 끝 `/` 하나를 보존한다(이미 있으면 그대로).
func withTrailing(parts []urlPart, trailing bool) []urlPart {
	if !trailing || pathEndsWithSlash(parts) {
		return parts
	}
	return appendParts(parts, literalPart("/"))
}

// pathJoinParts는 path.Join(elem...)이다: 비어 있지 않은 원소를 `/`로 잇고 Clean한다. 첫 원소가
// 값이면 그 값 뒤에 정리한 나머지를 붙인다(`..`가 그 값으로 오르면 dynamic).
func pathJoinParts(elems [][]urlPart) []urlPart {
	var nonEmpty [][]urlPart
	for _, e := range elems {
		if e = appendParts(nil, e...); len(e) > 0 {
			nonEmpty = append(nonEmpty, e)
		}
	}
	if len(nonEmpty) == 0 {
		return nil
	}
	head := nonEmpty[0]
	if head[0].kind != partLiteral {
		var rest []urlSegment
		_, first := splitSegments(head[1:])
		rest = append(rest, first...)
		for _, e := range nonEmpty[1:] {
			_, segs := splitSegments(e)
			rest = append(rest, segs...)
		}
		cleaned, ok := cleanSegments(rest, false)
		if !ok {
			return []urlPart{head[0]}
		}
		return appendParts([]urlPart{head[0]}, renderSegments(true, cleaned)...)
	}
	absolute := strings.HasPrefix(head[0].text, "/")
	var all []urlSegment
	for _, e := range nonEmpty {
		_, segs := splitSegments(e)
		all = append(all, segs...)
	}
	cleaned, ok := cleanSegments(all, absolute)
	if !ok {
		return []urlPart{valuePart()}
	}
	if !absolute && len(cleaned) == 0 {
		return []urlPart{literalPart(".")}
	}
	return renderSegments(absolute, cleaned)
}

// resolveReferenceParts는 (*URL).ResolveReference(ref)·(*URL).Parse(ref)다(RFC 3986).
//   - ref가 절대 URL이면 ref(점 세그먼트 제거), `//`로 시작하면 증명하지 못한다.
//   - `/`로 시작하면 base origin + ref, 경로가 비면(query·fragment만) base 경로다.
//   - 상대 경로면 base 경로의 마지막 `/`까지 + ref를 합치고 점 세그먼트를 지운다. base 경로를 모르면
//     base 뒤에 `/` + ref다(`..`가 base로 오르면 dynamic).
func resolveReferenceParts(base, ref []urlPart) []urlPart {
	ref = appendParts(nil, ref...)
	b := splitBase(base)
	tail, cut := cutQueryParts(ref)
	if len(ref) > 0 && ref[0].kind != partLiteral {
		return []urlPart{mergeUnknown(ref[0], b.unknown)}
	}
	if len(ref) > 0 && schemePrefix.MatchString(ref[0].text) {
		rb := splitBase(tail)
		if !rb.known {
			return withQuery(tail, cut)
		}
		return withQuery(dotRemoved(rb.origin, rb.path, false), cut)
	}
	if len(tail) > 0 && strings.HasPrefix(tail[0].text, "//") {
		return []urlPart{valuePart()}
	}
	origin := b.origin
	if !b.known {
		origin = []urlPart{{kind: partOrigin, param: b.unknown.param, ref: b.unknown.ref}}
	}
	switch {
	case len(tail) == 0:
		if !b.known {
			return withQuery([]urlPart{b.unknown}, cut)
		}
		return withQuery(appendParts(append([]urlPart(nil), b.origin...), b.path...), cut)
	case strings.HasPrefix(tail[0].text, "/"):
		return withQuery(dotRemoved(origin, tail, false), cut)
	case !b.known:
		out := dotRemoved(nil, appendParts([]urlPart{literalPart("/")}, tail...), true)
		if len(out) == 1 && out[0].kind == partValue {
			return []urlPart{b.unknown}
		}
		return withQuery(appendParts([]urlPart{b.unknown}, out...), cut)
	}
	merged := appendParts(mergeBasePath(b.path), tail...)
	return withQuery(dotRemoved(b.origin, merged, false), cut)
}

// mergeUnknown은 값으로 시작하는 ref의 결과다 — ref가 절대 URL일 수도 있어 증명하지 못한다.
func mergeUnknown(ref, base urlPart) urlPart {
	out := valuePart()
	out.param = ref.param || base.param
	out.ref = firstNonEmpty(ref.ref, base.ref)
	return out
}

// mergeBasePath는 base 경로의 마지막 `/`까지다(Go resolvePath). `/`가 없으면 빈 경로라 `/`다.
func mergeBasePath(path []urlPart) []urlPart {
	for i := len(path) - 1; i >= 0; i-- {
		p := path[i]
		if p.kind != partLiteral {
			continue
		}
		if j := strings.LastIndexByte(p.text, '/'); j >= 0 {
			out := append(append([]urlPart(nil), path[:i]...), literalPart(p.text[:j+1]))
			return out
		}
	}
	return []urlPart{literalPart("/")}
}

// dotRemoved는 origin 뒤에 점 세그먼트를 지운 경로를 붙인다. relative는 미상 base 뒤라는 뜻이다.
func dotRemoved(origin, path []urlPart, relative bool) []urlPart {
	_, segs := splitSegments(path)
	cleaned, ok := removeDotSegments(segs, relative)
	if !ok {
		return []urlPart{valuePart()}
	}
	return appendParts(append([]urlPart(nil), origin...), renderSegments(true, cleaned)...)
}

// cutQueryParts는 첫 원문 `?`·`#`부터 뒤를 뗀다.
func cutQueryParts(parts []urlPart) ([]urlPart, bool) {
	for i, p := range parts {
		if p.kind != partLiteral {
			continue
		}
		if j := strings.IndexAny(p.text, "?#"); j >= 0 {
			return appendParts(append([]urlPart(nil), parts[:i]...), literalPart(p.text[:j])), true
		}
	}
	return parts, false
}

// withQuery는 떼어 낸 query 자리를 `?` 표식으로 되돌린다 — 조립이 queryTailStripped를 단다.
func withQuery(parts []urlPart, cut bool) []urlPart {
	if !cut {
		return parts
	}
	return appendParts(parts, literalPart("?"))
}

// slashJoinParts는 resty v2의 결합이다: 경로가 절대 URL이면 그대로, 아니면 `/`로 시작하게 한 뒤
// base 문자열 뒤에 잇는다. trimmed면 SetBaseURL처럼 base 끝의 `/`를 모두 뗀다(BaseURL 필드에 직접
// 넣은 값은 떼지 않는다). base가 비면(설정 없음) 경로만 남는다.
func slashJoinParts(base []urlPart, trimmed bool, path []urlPart) []urlPart {
	path = appendParts(nil, path...)
	if len(path) == 0 {
		return appendParts(nil, base...)
	}
	if path[0].kind != partLiteral {
		return path
	}
	if schemePrefix.MatchString(path[0].text) {
		return path
	}
	if !strings.HasPrefix(path[0].text, "/") {
		path = appendParts([]urlPart{literalPart("/")}, path...)
	}
	base = appendParts(nil, base...)
	if trimmed && len(base) > 0 && base[len(base)-1].kind == partLiteral {
		last := &base[len(base)-1]
		last.text = strings.TrimRight(last.text, "/")
		if last.text == "" {
			base = base[:len(base)-1]
		}
	}
	return appendParts(base, path...)
}
