// 경로 패턴 매처 — 컴포넌트 패턴, exclude, fileRules가 같은 의미론을
// 공유하기 위한 유일한 글롭 장치다. 패키지가 나뉘어 있어도 "맞다"의 뜻이
// 갈라지면 설정 파일이 거짓말을 하게 된다.
package graph

import "strings"

// MatchPath는 패턴 하나와 `/`로 구분된 경로를 맞춘다.
// 지원하는 형태: 정확 일치, `x/**` 재귀 접두사(자기 자신 포함),
// 세그먼트 글롭(`*`·`?`은 `/`를 넘지 않는다 — `a/*`는 `a/b/c`를 맞지 않는다).
func MatchPath(pattern, path string) bool {
	switch {
	case pattern == path:
		return true
	case strings.HasSuffix(pattern, "/**"):
		prefix := strings.TrimSuffix(pattern, "/**")
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	case strings.ContainsAny(pattern, "*?"):
		return matchGlob(pattern, path)
	default:
		return false
	}
}

// matchGlob은 세그먼트 단위 글롭을 비교한다.
// 세그먼트 수가 다르면 맞지 않는다 — `*`가 `/`를 넘는다는 암묵 해석은
// 설정 작성자가 의도를 벗어나는 범위까지 맞게 만든다.
func matchGlob(pattern, path string) bool {
	pp, sp := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(pp) != len(sp) {
		return false
	}
	for i := range pp {
		if !matchSegment(pp[i], sp[i]) {
			return false
		}
	}
	return true
}

// matchSegment는 `/` 없는 한 세그먼트의 `*` 글롭을 비교한다.
func matchSegment(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	// `*`를 최대 하나만 지원한다 — 설정 파일의 패턴은 이 정도면 충분하다.
	i := strings.Index(pattern, "*")
	return strings.HasPrefix(s, pattern[:i]) &&
		strings.HasSuffix(s, pattern[i+1:]) &&
		len(s) >= len(pattern)-1
}
