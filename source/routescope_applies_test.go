package source

// http limitation 스코프의 소비자 적용 판정(scope.applies 벡터의 오라클). 정본은 isthmus
// docs/GRAPH-EXCHANGE.md의 "http limitation 스코프" 절이다. 생산 경로는 이 판정을 쓰지 않는다 — 제품
// 코드에 두면 자기 분석 dead가 테스트에서만 쓰는 코드를 보고한다.

import "strings"

// anySegments는 0개 이상 세그먼트를 뜻하는 비교용 내부 표식이다 — 템플릿 문법에 없는 바이트라 실제
// 세그먼트와 겹치지 않는다.
const anySegments = "\x00*"

// scopeProbe는 적용 판정의 사실 요약이다. Method가 비면 동적 동사 호출이다. Side는 "call" 또는
// "declaration"이다.
type scopeProbe struct {
	Template string
	Method   string
	Base     bool
	Side     string
}

// scopeApplies는 스코프가 있는 한계가 호출·선언 하나에 적용되는지(겹칠 수 있는지) 판정한다. 항상
// 넓게 근사한다: 리터럴은 ASCII 대소문자를 접고, 끝 슬래시 하나는 무시하며, {}와 부분 세그먼트
// 파라미터는 빈 값을 포함한 어떤 값도 될 수 있다고 본다.
func scopeApplies(scope map[string]any, probe scopeProbe) bool {
	if !scopeMethodApplies(scope["methods"], probe) {
		return false
	}
	tokens := templateTokens(probe.Template)
	if probe.Base {
		tokens = append([]string{anySegments}, tokens...)
	}
	for _, element := range scopeElements(scope) {
		if tokensOverlap(tokens, element) {
			return true
		}
	}
	return false
}

// scopeMethodApplies는 스코프 methods가 사실의 동사를 덮는지 본다. methods가 없으면 모든 동사다.
// 선언 쪽은 ANY·OPTIONS 선언이나 GET 선언의 HEAD를 덮고, 호출 쪽은 동적 동사와 OPTIONS를 늘 덮는다.
func scopeMethodApplies(raw any, probe scopeProbe) bool {
	list, ok := raw.([]any)
	if !ok {
		return true
	}
	allowed := map[string]bool{}
	for _, m := range list {
		if s, ok := m.(string); ok {
			allowed[s] = true
		}
	}
	method := probe.Method
	if probe.Side == "declaration" {
		return method == "ANY" || allowed[method] || (method == "GET" && allowed["HEAD"]) || allowed["OPTIONS"]
	}
	switch method {
	case "", "OPTIONS":
		return true
	case "HEAD":
		return allowed["HEAD"] || allowed["GET"]
	}
	return allowed[method]
}

// scopeElements는 스코프의 경로 원소를 비교용 토큰열로 바꾼다 — 접두사는 뒤에, 접미사는 앞에 0개
// 이상 세그먼트를 둔다.
func scopeElements(scope map[string]any) [][]string {
	var out [][]string
	for _, t := range stringList(scope["templates"]) {
		out = append(out, templateTokens(t))
	}
	for _, p := range stringList(scope["templatePrefixes"]) {
		out = append(out, append(templateTokens(p), anySegments))
	}
	for _, s := range stringList(scope["templateSuffixes"]) {
		out = append(out, append([]string{anySegments}, templateTokens(s)...))
	}
	return out
}

// stringList는 값이 배열이면 그 안의 문자열을, 아니면 빈 목록을 돌려준다.
func stringList(value any) []string {
	list, _ := value.([]any)
	var out []string
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// templateTokens는 템플릿을 세그먼트 토큰열로 바꾼다. 끝 슬래시 하나는 접고 {**}는 0개 이상
// 세그먼트다. 루트 "/"는 빈 토큰열이다.
func templateTokens(template string) []string {
	segments := strings.Split(strings.TrimPrefix(template, "/"), "/")
	if n := len(segments); n > 0 && segments[n-1] == "" {
		segments = segments[:n-1]
	}
	out := make([]string, len(segments))
	for i, s := range segments {
		if s == "{**}" {
			s = anySegments
		}
		out[i] = s
	}
	return out
}

// tokensOverlap은 와일드카드를 포함한 두 토큰열이 같은 요청 경로를 받을 수 있는지 판정한다.
// 메모는 (i, j) 위치로 — 같은 위치를 다시 풀지 않아 와일드카드가 여럿이어도 O(len·len)이다.
func tokensOverlap(left, right []string) bool {
	memo := map[[2]int]bool{}
	var from func(i, j int) bool
	from = func(i, j int) bool {
		key := [2]int{i, j}
		if v, ok := memo[key]; ok {
			return v
		}
		v := overlapStep(left, right, i, j, from)
		memo[key] = v
		return v
	}
	return from(0, 0)
}

// overlapStep은 tokensOverlap의 한 단계다 — 와일드카드는 비우거나 상대 세그먼트 하나를 삼킨다.
func overlapStep(left, right []string, i, j int, next func(i, j int) bool) bool {
	switch {
	case i == len(left) && j == len(right):
		return true
	case i < len(left) && left[i] == anySegments:
		return next(i+1, j) || (j < len(right) && next(i, j+1))
	case j < len(right) && right[j] == anySegments:
		return next(i, j+1) || (i < len(left) && next(i+1, j))
	case i == len(left) || j == len(right):
		return false
	}
	return segmentsCompatible(left[i], right[j]) && next(i+1, j+1)
}

// segmentsCompatible은 두 세그먼트(리터럴, {}, 부분 세그먼트 p{}s)가 같은 값일 수 있는지 본다.
func segmentsCompatible(left, right string) bool {
	leftParam, rightParam := strings.Contains(left, "{}"), strings.Contains(right, "{}")
	switch {
	case leftParam && rightParam:
		return true
	case leftParam:
		return literalFitsPartial(right, left)
	case rightParam:
		return literalFitsPartial(left, right)
	}
	return strings.EqualFold(left, right)
}

// literalFitsPartial은 리터럴 세그먼트가 p{}s 모양에 맞을 수 있는지(가운데는 빈 값 허용) 본다.
func literalFitsPartial(literal, partial string) bool {
	prefix, suffix, _ := strings.Cut(strings.ToLower(partial), "{}")
	folded := strings.ToLower(literal)
	return len(folded) >= len(prefix)+len(suffix) && strings.HasPrefix(folded, prefix) && strings.HasSuffix(folded, suffix)
}
