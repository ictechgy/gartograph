// http limitation 스코프의 검증과 적용 판정.
//
// 정본은 isthmus docs/GRAPH-EXCHANGE.md의 "http limitation 스코프" 절이고, 공유 벡터는
// conformance/http-limitation-scope.json(scope.validate·scope.applies)이다.
//
//   - scopeProblem: 소비자가 거부할 스코프 항목을 생산자가 미리 걸러 낸다. 잘못된 스코프 하나가
//     isthmus에서 문서 전체를 입력 오류로 만들므로, 통과하지 못한 스코프는 버리고 한계는 스코프 없이
//     (문서 전체 효과로) 남긴다 — 넓은 쪽이라 거짓 error가 생기지 않는다.
//   - scopeApplies(routescope_applies_test.go): 한계가 호출·선언 하나에 적용되는지의 소비자 판정이다.
//     생산자는 문서를 만들 때 이 판정을 쓰지 않으므로 테스트 파일에 두고, 스코프 설계가 기대는 규칙
//     (세그먼트 경계, 대소문자·끝 슬래시 접기, 동사 규칙)이 바뀌면 벡터 테스트가 알리게 한다.
package source

import "strings"

// scopeKeys는 스코프 항목에 올 수 있는 키다.
var scopeKeys = map[string]bool{
	"limitationIndex": true, "templates": true, "templatePrefixes": true, "templateSuffixes": true, "methods": true,
}

// scopePathFields는 경로 필드 이름이다 — 하나 이상 있어야 한다.
var scopePathFields = []string{"templates", "templatePrefixes", "templateSuffixes"}

// scopeProblem은 JSON으로 풀어 둔 스코프 항목 하나가 계약을 어기면 이유를 돌려준다(없으면 빈 문자열).
// 이유에 입력 원문은 싣지 않는다.
func scopeProblem(scope map[string]any) string {
	for key := range scope {
		if !scopeKeys[key] {
			return "unknown scope key"
		}
	}
	present := false
	for _, field := range scopePathFields {
		value, ok := scope[field]
		if !ok {
			continue
		}
		present = true
		if problem := scopePathFieldProblem(field, value); problem != "" {
			return problem
		}
	}
	if !present {
		return "a scope needs templates, templatePrefixes, or templateSuffixes"
	}
	if methods, ok := scope["methods"]; ok {
		return scopeMethodsProblem(methods)
	}
	return ""
}

// scopePathFieldProblem은 경로 필드 하나(비지 않은 정규 템플릿 배열)를 검사한다.
func scopePathFieldProblem(field string, value any) string {
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		return field + " must be a non-empty array"
	}
	for _, raw := range list {
		element, ok := raw.(string)
		if !ok || templateProblem(element) != "" {
			return field + " holds a non-canonical template"
		}
		if problem := scopeElementProblem(field, element); problem != "" {
			return problem
		}
	}
	return ""
}

// scopeElementProblem은 필드별 원소 규칙이다: 접두사·접미사에는 {**}가 올 수 없고, 루트가 아닌
// 접두사는 /로 끝날 수 없으며, 접미사는 /일 수 없다.
func scopeElementProblem(field, element string) string {
	switch {
	case field != "templates" && strings.Contains(element, "{**}"):
		return field + " cannot hold {**}"
	case field == "templatePrefixes" && element != "/" && strings.HasSuffix(element, "/"):
		return "a non-root template prefix cannot end with /"
	case field == "templateSuffixes" && element == "/":
		return "a template suffix cannot be /"
	}
	return ""
}

// scopeMethodsProblem은 methods 필드(중복 없는 계약 동사 배열, ANY 불가)를 검사한다.
func scopeMethodsProblem(value any) string {
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		return "methods must be a non-empty array"
	}
	seen := map[string]bool{}
	for _, raw := range list {
		method, ok := raw.(string)
		if !ok || !routeMethods[method] {
			return "methods holds a value that is not an HTTP method"
		}
		if seen[method] {
			return "methods holds duplicates"
		}
		seen[method] = true
	}
	return ""
}

// validScope는 생산자가 만든 스코프가 소비자 검증을 통과하는지 본다.
func validScope(sc LimitationScope) bool {
	return scopeProblem(sc.asJSON()) == ""
}

// asJSON은 스코프를 JSON으로 풀어 둔 모양(검증기 입력)으로 바꾼다 — 빈 필드는 JSON에서 빠지는 것처럼 뺀다.
func (sc LimitationScope) asJSON() map[string]any {
	out := map[string]any{"limitationIndex": sc.LimitationIndex}
	add := func(key string, values []string) {
		if len(values) == 0 {
			return
		}
		list := make([]any, len(values))
		for i, v := range values {
			list[i] = v
		}
		out[key] = list
	}
	add("templates", sc.Templates)
	add("templatePrefixes", sc.TemplatePrefixes)
	add("templateSuffixes", sc.TemplateSuffixes)
	add("methods", sc.Methods)
	return out
}
