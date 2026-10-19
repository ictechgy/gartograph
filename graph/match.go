// 경로 패턴 매처 — 컴포넌트 패턴, exclude, fileRules가 같은 의미론을
// 공유하기 위한 유일한 글롭 장치다. 패키지가 나뉘어 있어도 "맞다"의 뜻이
// 갈라지면 설정 파일이 거짓말을 하게 된다.
package graph

import (
	"errors"
	"fmt"
	"strings"
)

// MatchPath는 패턴 하나와 `/`로 구분된 경로를 맞춘다.
// 지원하는 형태: 정확 일치, `x/**` 재귀 접두사(자기 자신 포함, x는 리터럴),
// 세그먼트 글롭(세그먼트마다 `*` 하나, `/`를 넘지 않는다 — `a/*`는 `a/b/c`를 맞지 않는다).
// `?`·`[...]`는 리터럴 문자로 비교되고, 끝이 아닌 `**`는 재귀 와일드카드가 아니다 —
// 설정은 ValidatePattern으로 이런 패턴을 로드 시점에 거부한다.
func MatchPath(pattern, path string) bool {
	switch {
	case pattern == path:
		return true
	case strings.HasSuffix(pattern, "/**"):
		// prefix+"/"를 만들지 않고 바로 뒤 바이트를 본다 — 컴포넌트 해석은 패키지마다
		// 패턴을 맞추므로 판정마다 문자열을 만들면 패키지 수 × 패턴 수만큼 할당한다.
		prefix := strings.TrimSuffix(pattern, "/**")
		return path == prefix || (len(path) > len(prefix) && path[len(prefix)] == '/' &&
			strings.HasPrefix(path, prefix))
	case strings.ContainsAny(pattern, "*?"):
		return matchGlob(pattern, path)
	default:
		return false
	}
}

// patternSyntax는 오류에 덧붙이는 지원 문법 안내다 — 무엇이 거부됐는지와 함께 무엇을
// 쓰면 되는지가 보여야 설정 작성자가 바로 고칠 수 있다.
const patternSyntax = `supported patterns are an exact path, "dir/**" (dir and everything below it), ` +
	`or "/"-separated segments with at most one "*" each (e.g. "internal/*/api", "*_test.go")`

// ValidatePattern은 패턴이 MatchPath가 적힌 그대로 맞출 수 있는 문법인지 본다.
// 설정 작성자는 dependency-cruiser·gitignore 습관대로 `?`·`[...]`·중간 `**`를 쓰기 쉽다.
// MatchPath는 이들을 글롭으로 해석하지 않으므로 그 패턴은 영원히 아무것도 맞지 않고,
// fileRules라면 rules --strict가 위반 없음으로 조용히 통과한다. 그래서 MatchPath와 같은
// 문법을 이 패키지 한 곳에 두고 설정이 로드 시점에 쓴다.
// 거부: 빈 패턴과 빈 세그먼트, `?`·`[`·`]`·`{`·`}`·`\`, 한 세그먼트의 `*` 두 개 이상,
// 마지막 세그먼트가 아닌 `**`(단독 `**` 포함), `x/**`의 x에 든 `*`(리터럴로 비교된다).
func ValidatePattern(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("empty pattern; %s", patternSyntax)
	}
	if i := strings.IndexAny(pattern, `?[]{}\`); i >= 0 {
		return fmt.Errorf("%q has no wildcard meaning here; %s", pattern[i:i+1], patternSyntax)
	}
	segments := strings.Split(pattern, "/")
	last := len(segments) - 1
	recursive := last > 0 && segments[last] == "**"
	for i, segment := range segments {
		if err := validateSegment(segment, i == last, recursive); err != nil {
			return fmt.Errorf("%w; %s", err, patternSyntax)
		}
	}
	return nil
}

// validateSegment는 패턴 세그먼트 하나를 MatchPath 문법으로 본다.
// recursive는 패턴이 `x/**`로 끝난다는 뜻이다 — 그때 앞 세그먼트의 `*`는 글롭이 아니다.
func validateSegment(segment string, isLast, recursive bool) error {
	switch {
	case segment == "":
		return errors.New(`empty segment (leading, trailing, or doubled "/")`)
	case segment == "**":
		if isLast && recursive {
			return nil
		}
		return errors.New(`"**" may only end a pattern, as in "dir/**"`)
	case recursive && strings.Contains(segment, "*"):
		return errors.New(`"*" before "/**" is compared literally; the part before "/**" must be a plain path`)
	case strings.Count(segment, "*") > 1:
		return errors.New(`a segment may hold at most one "*"`)
	}
	return nil
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
