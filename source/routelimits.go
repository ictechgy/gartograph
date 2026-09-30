// route 문서의 limitations와 limitationScopes.
//
// 한계는 수확에서 실제로 센 공백만 낸다. 서버 측 공백 접두사(route-coverage:·unresolved-route-
// prefix:·route-template-expansion-capped:)는 isthmus가 그 문서의 호출 판정을 unverified로 내리는
// 근거라, 가릴 수 있는 요청의 상한을 증명할 수 있으면 스코프로 좁힌다(계약 "http limitation 스코프").
// 증명하지 못하면 스코프를 생략한다 — 넓게 잡으면 error 하나가 unverified가 될 뿐이지만 좁게
// 잡으면 거짓 error가 된다.
package source

import (
	"fmt"
	"sort"
	"strings"
)

// isthmus가 받는 스코프 상한이다(문서당 스코프 1,000개, 경로 원소 합계 10,000개).
const (
	maxLimitationScopes   = 1000
	maxLimitationElements = 10000
)

// limitations는 한계 문구와 스코프를 결정적 순서로 만든다.
func (s *routeScan) limitations() ([]string, []LimitationScope) {
	out := []string{}
	if s.loadErrors > 0 {
		out = append(out, fmt.Sprintf(
			"route-coverage: %d package load or parse error(s); route registrations there are not declared", s.loadErrors))
	}
	if len(s.unsupported) > 0 {
		out = append(out, s.unsupportedLimitation())
	}
	var scopes []LimitationScope
	elements := 0
	for _, key := range sortedKeys(s.gaps) {
		g := s.gaps[key]
		if g.count == 0 {
			continue
		}
		index := len(out)
		out = append(out, fmt.Sprintf("%s %d %s", g.prefix, g.count, g.message))
		if scope, ok := g.scope(index); ok && len(scopes) < maxLimitationScopes &&
			elements+scope.size() <= maxLimitationElements {
			scopes = append(scopes, scope)
			elements += scope.size()
		}
	}
	if s.missingUsrs > 0 {
		// isthmus 체인 전용 접두사다 — check 심각도에는 영향이 없고 trace가 그 route에서 순회로
		// 이어 가지 못한다는 것을 드러낸다.
		out = append(out, fmt.Sprintf(
			"missing-route-usrs: %d route-decl fact(s) have no handler symbol in the impact graph; they carry no symbol",
			s.missingUsrs))
	}
	if s.anonymous > 0 {
		out = append(out, fmt.Sprintf(
			"anonymous-route-handlers: %d route registration(s) use a function literal handler; symbol.usr is the enclosing declaration, so reach from it may include sibling handlers",
			s.anonymous))
	}
	return out, scopes
}

// unsupportedLimitation은 수확하지 않는 라우터 import를 서버 측 공백으로 낸다.
func (s *routeScan) unsupportedLimitation() string {
	paths := sortedKeys(s.unsupported)
	packages := 0
	for _, p := range paths {
		packages += s.unsupported[p]
	}
	return fmt.Sprintf("route-coverage: %d package import(s) of router frameworks gartograph does not harvest (%s); their routes are not declared",
		packages, strings.Join(paths, ", "))
}

// scope는 공백 하나의 스코프 항목이다. 스코프 없는 공백이거나 원소가 없으면 false다.
func (g *routeGap) scope(index int) (LimitationScope, bool) {
	if g.unscoped {
		return LimitationScope{}, false
	}
	scope := LimitationScope{LimitationIndex: index, Templates: sortedSet(g.templates),
		TemplateSuffixes: sortedSet(g.suffixes)}
	prefixes := map[string]bool{}
	for p := range g.prefixes {
		prefixes[scopePrefix(p)] = true
	}
	scope.TemplatePrefixes = sortedSet(prefixes)
	if scope.size() == 0 {
		return LimitationScope{}, false
	}
	if len(g.methods) > 0 && !g.methods["ANY"] {
		scope.Methods = sortedSet(g.methods)
	}
	return scope, true
}

// size는 스코프의 경로 원소 수다.
func (sc LimitationScope) size() int {
	return len(sc.Templates) + len(sc.TemplatePrefixes) + len(sc.TemplateSuffixes)
}

// scopePrefix는 템플릿을 계약의 templatePrefixes 원소로 넓힌다: `{**}`부터 뒤를 자르고, 루트가
// 아닌 접두사의 끝 슬래시를 뗀다(접두사는 세그먼트 경계로 그 아래 전부를 덮으므로 넓어질 뿐이다).
func scopePrefix(template string) string {
	if i := strings.Index(template, "{**}"); i >= 0 {
		template = template[:i]
	}
	for len(template) > 1 && strings.HasSuffix(template, "/") {
		template = strings.TrimSuffix(template, "/")
	}
	if template == "" {
		return "/"
	}
	return template
}

// sortedSet은 집합을 정렬한 목록이다(비면 nil이라 JSON에서 빠진다).
func sortedSet(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
