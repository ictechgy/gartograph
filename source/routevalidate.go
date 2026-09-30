// route 문서 자기 검증 — isthmus가 입력 오류로 거부할 모양을 생산자가 먼저 잡는다.
//
// isthmus parse.ts의 route-decl 규칙 중 이 생산자가 낼 수 있는 부분(정규 템플릿, 동사, catch-all
// 접두사 decl의 원본, registration-order의 `order`)을 같은 판정으로 옮긴다. `order` 규칙은 공유
// 적합성 벡터 http-dispatch의 dispatch.validate 생산자 사례로 고정한다(conformance_test.go).
package source

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf16"
)

// maxOrderGroupLength는 order.group의 최대 UTF-16 길이다(계약 256자).
const maxOrderGroupLength = 256

// routeMethods는 계약의 route-decl 동사다(ANY 제외).
var routeMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "OPTIONS": true, "TRACE": true,
}

// firstNonEmpty는 비어 있지 않은 첫 값이다.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// routeDocumentProblem은 JSON으로 풀어 둔 http 문서의 거부 사유를 돌려준다(없으면 빈 문자열).
func routeDocumentProblem(doc map[string]any) string {
	facts, _ := doc["facts"].([]any)
	dispatch, _ := doc["dispatch"].(string)
	service, _ := doc["service"].(string)
	orders := map[string]string{}
	groups := map[string]string{}
	for i, raw := range facts {
		fact, ok := raw.(map[string]any)
		if !ok {
			return fmt.Sprintf("fact %d is not an object", i)
		}
		if problem := routeFactProblem(fact); problem != "" {
			return fmt.Sprintf("fact %d: %s", i, problem)
		}
		if problem := orderProblem(fact, dispatch, service, orders, groups); problem != "" {
			return fmt.Sprintf("fact %d: %s", i, problem)
		}
	}
	return catchAllOriginalsProblem(facts, service)
}

// routeFactProblem은 사실 하나의 템플릿·동사·catch-all 접두사 규칙을 본다.
func routeFactProblem(fact map[string]any) string {
	method, _ := fact["method"].(string)
	if method != "ANY" && !routeMethods[method] {
		return "invalid route method"
	}
	dynamic, _ := fact["dynamic"].(bool)
	channel, _ := fact["channel"].(string)
	if !dynamic {
		if problem := templateProblem(channel); problem != "" {
			return "route channel is not a canonical path template (" + problem + ")"
		}
	}
	if cap, _ := fact["catchAllPrefix"].(bool); cap {
		if dynamic || strings.Contains(channel, "{**}") || factUsr(fact) == "" {
			return "a catch-all prefix declaration must be a static template without {**} and carry symbol.usr"
		}
	}
	return ""
}

// factUsr는 사실의 symbol.usr다(없으면 빈 문자열).
func factUsr(fact map[string]any) string {
	symbol, _ := fact["symbol"].(map[string]any)
	usr, _ := symbol["usr"].(string)
	return usr
}

// orderProblem은 `order` 필드를 검증한다: registration-order 문서에서만, {group, index} 두 키만,
// 같은 (group, index)는 같은 위치(한 등록), 같은 group은 같은 유효 service.
func orderProblem(fact map[string]any, dispatch, docService string, orders, groups map[string]string) string {
	raw, present := fact["order"]
	if !present {
		return ""
	}
	if dispatch != "registration-order" {
		return "order is allowed only in registration-order documents"
	}
	order, ok := raw.(map[string]any)
	if !ok || len(order) != 2 {
		return "order must be {group, index}"
	}
	group, problem := orderGroup(order["group"])
	if problem != "" {
		return problem
	}
	index, ok := safeIndex(order["index"])
	if !ok {
		return "order.index must be a non-negative safe integer"
	}
	key := fmt.Sprintf("%s\x00%d", group, index)
	location := locationKey(fact["location"])
	if prev, seen := orders[key]; seen && prev != location {
		return "two registrations share one (group, index)"
	}
	orders[key] = location
	service, _ := fact["service"].(string)
	service = firstNonEmpty(service, docService)
	if prev, seen := groups[group]; seen && prev != service {
		return "one order group spans several services"
	}
	groups[group] = service
	return ""
}

// orderGroup은 order.group을 검증한다.
func orderGroup(raw any) (string, string) {
	group, ok := raw.(string)
	if !ok || group == "" || strings.TrimSpace(group) != group || forbiddenControl(group) ||
		len(utf16.Encode([]rune(group))) > maxOrderGroupLength {
		return "", "order.group must be a non-empty string without control characters or surrounding whitespace, at most 256 characters"
	}
	return group, ""
}

// forbiddenControl은 제어 문자(C0·DEL·C1)가 있는지 본다.
func forbiddenControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return true
		}
	}
	return false
}

// safeIndex는 JSON 수가 0 이상의 안전 정수인지 본다(소수·문자열·음수 거부).
func safeIndex(raw any) (int64, bool) {
	var f float64
	switch v := raw.(type) {
	case float64:
		f = v
	case json.Number:
		parsed, err := v.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if f < 0 || f != math.Trunc(f) || f > 1<<53-1 {
		return 0, false
	}
	return int64(f), true
}

// locationKey는 위치의 비교 키다.
func locationKey(raw any) string {
	loc, _ := raw.(map[string]any)
	return fmt.Sprintf("%v:%v:%v", loc["path"], loc["line"], loc["column"])
}

// catchAllOriginalsProblem은 catch-all 접두사 decl마다 같은 문서에 원본(같은 method·usr·유효 service·
// order, 템플릿은 접두사 + "/{**}", 접두사가 "/"면 "/{**}")이 있는지 본다.
func catchAllOriginalsProblem(facts []any, docService string) string {
	originals := map[string]bool{}
	for _, raw := range facts {
		fact, _ := raw.(map[string]any)
		cap, _ := fact["catchAllPrefix"].(bool)
		dynamic, _ := fact["dynamic"].(bool)
		if !cap && !dynamic {
			channel, _ := fact["channel"].(string)
			originals[originalKey(fact, channel, docService)] = true
		}
	}
	for i, raw := range facts {
		fact, _ := raw.(map[string]any)
		if cap, _ := fact["catchAllPrefix"].(bool); !cap {
			continue
		}
		channel, _ := fact["channel"].(string)
		original := channel + "/{**}"
		if channel == "/" {
			original = "/{**}"
		}
		if !originals[originalKey(fact, original, docService)] {
			return fmt.Sprintf("fact %d: catch-all prefix declaration has no original %s declaration", i, original)
		}
	}
	return ""
}

// originalKey는 catch-all 원본 대조 키다.
func originalKey(fact map[string]any, channel, docService string) string {
	service, _ := fact["service"].(string)
	order, _ := json.Marshal(fact["order"])
	method, _ := fact["method"].(string)
	return strings.Join([]string{method, factUsr(fact), channel, firstNonEmpty(service, docService), string(order)}, "\x00")
}
