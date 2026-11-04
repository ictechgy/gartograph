// isthmus 공유 적합성 벡터(벤더링 사본)로 생산자 규칙을 검증한다.
//
//   - 벡터 파일은 conformance/SHA256SUMS·conformance.lock(isthmus 커밋·파일별 sha256)과 같아야 한다.
//   - 생산자 대상 사례(template.grammar·template.normalize·dispatch.validate, http-limitation-scope의
//     scope.validate·scope.applies, sql-relations의 relations.*, url-compose의 compose.*·wrapper.*)는
//     appliesTo가 "producer"나 "producer:gartograph"면 전부 통과해야 한다.
//   - 이 생산자 대상이 아닌 사례(consumer 전용, 다른 생산자 전용)는 실행하지 않는다. 이 생산자 대상인데
//     실행하지 않는 ruleId는 이유를 적어 건너뛰고, 모르는 ruleId가 생기면 실패해 벤더링 갱신 때 판단하게 한다.
package source

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// conformanceCase는 벡터 사례 하나다.
type conformanceCase struct {
	ID               string          `json:"id"`
	RuleID           string          `json:"ruleId"`
	AppliesTo        []string        `json:"appliesTo"`
	Input            json.RawMessage `json:"input"`
	Expect           json.RawMessage `json:"expect"`
	ExpectDynamic    bool            `json:"expectDynamic"`
	ExpectLimitation string          `json:"expectLimitation"`
}

// producerRules는 이 생산자가 실행하는 ruleId다.
var producerRules = map[string]bool{
	"template.grammar": true, "template.normalize": true, "dispatch.validate": true,
	"compose.interpolation": true, "compose.query-tail": true, "compose.suffix": true, "compose.normalize": true,
	"compose.base-join": true, "compose.strip": true, "compose.mask": true,
	"wrapper.method": true, "wrapper.location": true,
	"scope.validate": true, "scope.applies": true,
	"relations.cte": true, "relations.function-from": true, "relations.distinct-from": true,
}

// appliesToUs는 사례가 이 생산자 대상인지 본다("producer"나 "producer:gartograph").
func appliesToUs(c conformanceCase) bool {
	for _, target := range c.AppliesTo {
		if target == "producer" || target == "producer:gartograph" {
			return true
		}
	}
	return false
}

// skippedRules는 이 생산자 대상인데 실행하지 않는 ruleId 접두사와 이유다. consumer 전용이나 다른 생산자
// 전용 사례는 appliesToUs가 이미 걸러 여기에 적지 않는다.
var skippedRules = map[string]string{
	"scope.dynamic-": "dynamicScope validation (gartograph does not emit dynamicScope)",
}

// conformanceDir는 벤더링한 벡터 디렉터리다(저장소 루트 기준).
func conformanceDir() string { return filepath.Join("..", "conformance") }

// loadConformance는 모든 벡터 사례를 읽는다.
func loadConformance(t *testing.T) []conformanceCase {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(conformanceDir(), "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no vendored vectors: %v", err)
	}
	var out []conformanceCase
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var suite struct {
			Cases []conformanceCase `json:"cases"`
		}
		if err := json.Unmarshal(data, &suite); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, suite.Cases...)
	}
	return out
}

// producerCases는 생산자로서 실행할 사례다.
func producerCases(t *testing.T, rule string) []conformanceCase {
	var out []conformanceCase
	for _, c := range loadConformance(t) {
		if c.RuleID == rule && appliesToUs(c) {
			out = append(out, c)
		}
	}
	return out
}

// TestConformanceLock은 벡터 파일이 SHA256SUMS·잠금 파일과 같고 목록에 빠진 파일이 없는지 본다.
func TestConformanceLock(t *testing.T) {
	sums := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(conformanceDir(), "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		sums[fields[1]] = fields[0]
	}
	var lock struct {
		Commit string            `json:"commit"`
		Files  map[string]string `json:"files"`
	}
	data, err := os.ReadFile(filepath.Join("..", "conformance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	if len(lock.Commit) != 40 {
		t.Fatalf("lock commit %q is not a full sha", lock.Commit)
	}
	paths, _ := filepath.Glob(filepath.Join(conformanceDir(), "*.json"))
	var names []string
	for _, p := range paths {
		name := filepath.Base(p)
		names = append(names, name)
		content, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(content)
		got := hex.EncodeToString(digest[:])
		if got != sums[name] || got != lock.Files[name] {
			t.Errorf("%s: sha256 %s differs from SHA256SUMS/lock — re-vendor it from isthmus", name, got)
		}
	}
	if len(names) != len(sums) || len(names) != len(lock.Files) {
		t.Errorf("vendored files %v, SHA256SUMS %d entries, lock %d entries", names, len(sums), len(lock.Files))
	}
}

// TestConformanceEveryCaseClassified는 모든 사례가 실행되거나 알려진 이유로 건너뛰는지 본다.
func TestConformanceEveryCaseClassified(t *testing.T) {
	var unknown []string
	for _, c := range loadConformance(t) {
		if !appliesToUs(c) || producerRules[c.RuleID] {
			continue
		}
		known := false
		for prefix := range skippedRules {
			known = known || strings.HasPrefix(c.RuleID, prefix)
		}
		if !known {
			unknown = append(unknown, c.ID+" ("+c.RuleID+")")
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		t.Fatalf("unclassified vector cases: %v", unknown)
	}
}

// TestConformanceProducerCaseCount는 실행하는 생산자 사례 수가 줄지 않게 고정한다.
func TestConformanceProducerCaseCount(t *testing.T) {
	total := 0
	for rule := range producerRules {
		total += len(producerCases(t, rule))
	}
	if total != 155 {
		t.Fatalf("producer cases = %d, want 155 (26 grammar + 7 normalize + 18 dispatch.validate + 63 url-compose "+
			"+ 9 scope.validate + 18 scope.applies + 14 sql-relations)", total)
	}
}

// TestConformanceTemplateGrammar는 문법 검사기가 소비자와 같은 판정·사유를 내는지 본다.
func TestConformanceTemplateGrammar(t *testing.T) {
	for _, c := range producerCases(t, "template.grammar") {
		var in struct{ Template string }
		var want struct {
			Valid  bool
			Reason string
		}
		mustDecode(t, c.Input, &in)
		mustDecode(t, c.Expect, &want)
		got := templateProblem(in.Template)
		if (got == "") != want.Valid || (want.Reason != "" && got != want.Reason) {
			t.Errorf("%s: templateProblem(%q) = %q, want valid=%v reason=%q", c.ID, in.Template, got, want.Valid, want.Reason)
		}
	}
}

// TestConformanceTemplateNormalize는 경로 정규화가 벡터와 같은지 본다.
func TestConformanceTemplateNormalize(t *testing.T) {
	for _, c := range producerCases(t, "template.normalize") {
		var in struct{ Path string }
		var want struct{ Template string }
		mustDecode(t, c.Input, &in)
		mustDecode(t, c.Expect, &want)
		if got := normalizeTemplateLiteral(in.Path); got != want.Template {
			t.Errorf("%s: normalize(%q) = %q, want %q", c.ID, in.Path, got, want.Template)
		}
	}
}

// TestConformanceDispatchValidate는 order 검증기가 소비자와 같이 판정하는지 본다. 빠진 위치는
// isthmus 참조 실행기(scripts/verify-conformance.mjs runDispatchValidate)와 같게 채운다.
func TestConformanceDispatchValidate(t *testing.T) {
	for _, c := range producerCases(t, "dispatch.validate") {
		var in struct {
			Document struct {
				Dispatch string           `json:"dispatch"`
				Facts    []map[string]any `json:"facts"`
			} `json:"document"`
		}
		var want struct{ Valid bool }
		mustDecodeNumbers(t, c.Input, &in)
		mustDecode(t, c.Expect, &want)
		var facts []any
		for i, fact := range in.Document.Facts {
			facts = append(facts, completeVectorFact(fact, i))
		}
		doc := map[string]any{"dispatch": in.Document.Dispatch, "facts": facts}
		if got := routeDocumentProblem(doc); (got == "") != want.Valid {
			t.Errorf("%s: problem %q, want valid=%v", c.ID, got, want.Valid)
		}
	}
}

// completeVectorFact는 벡터의 부분 사실에 참조 실행기와 같은 기본값을 채운다.
func completeVectorFact(fact map[string]any, index int) map[string]any {
	out := map[string]any{"kind": "route-decl", "dynamic": false, "pathAnchor": "root"}
	loc, _ := fact["location"].(map[string]any)
	line, column := any(json.Number(itoa(index+1))), any(json.Number("1"))
	if v, ok := loc["line"]; ok {
		line = v
	}
	if v, ok := loc["column"]; ok {
		column = v
	}
	for k, v := range fact {
		out[k] = v
	}
	out["location"] = map[string]any{"path": "shop/urls.py", "line": line, "column": column}
	return out
}

// itoa는 정수를 문자열로 쓴다.
func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// mustDecode는 JSON을 풀어 넣는다.
func mustDecode(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

// mustDecodeNumbers는 수를 json.Number로 보존해 푼다(1.5·"1"·음수 판정용).
func mustDecodeNumbers(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		t.Fatal(err)
	}
}

// TestConformanceScopeValidate는 스코프 검증기가 소비자와 같이 판정하는지 본다 — 생산자는 이 검증을 통과한
// 스코프만 문서에 싣는다(routelimits.go).
func TestConformanceScopeValidate(t *testing.T) {
	for _, c := range producerCases(t, "scope.validate") {
		var in struct {
			Scope map[string]any `json:"scope"`
		}
		var want struct{ Valid bool }
		mustDecodeNumbers(t, c.Input, &in)
		mustDecode(t, c.Expect, &want)
		in.Scope["limitationIndex"] = json.Number("0")
		if got := scopeProblem(in.Scope); (got == "") != want.Valid {
			t.Errorf("%s: problem %q, want valid=%v", c.ID, got, want.Valid)
		}
	}
}

// TestConformanceScopeApplies는 스코프 적용 판정이 벡터와 같은지 본다 — 스코프 설계가 기대는 세그먼트
// 경계·대소문자·끝 슬래시·동사 규칙이 바뀌면 여기서 알린다.
func TestConformanceScopeApplies(t *testing.T) {
	for _, c := range producerCases(t, "scope.applies") {
		var in struct {
			Scope map[string]any `json:"scope"`
			Probe struct {
				Template   string `json:"template"`
				Method     string `json:"method"`
				PathAnchor string `json:"pathAnchor"`
				Side       string `json:"side"`
			} `json:"probe"`
		}
		var want struct{ Applies bool }
		mustDecodeNumbers(t, c.Input, &in)
		mustDecode(t, c.Expect, &want)
		probe := scopeProbe{Template: in.Probe.Template, Method: in.Probe.Method,
			Base: in.Probe.PathAnchor == "base", Side: in.Probe.Side}
		if got := scopeApplies(in.Scope, probe); got != want.Applies {
			t.Errorf("%s: scopeApplies = %v, want %v (%+v)", c.ID, got, want.Applies, probe)
		}
	}
}

// TestConformanceSQLRelations는 SQL 텍스트의 관계 이름 추출이 벡터의 집합과 같은지 본다. 벡터는 순서·중복
// 없이 비교하라고 하지만 이 추출기는 한 텍스트에서 같은 이름을 한 번만 내므로 중복도 실패로 본다.
func TestConformanceSQLRelations(t *testing.T) {
	cases := 0
	for rule := range producerRules {
		if !strings.HasPrefix(rule, "relations.") {
			continue
		}
		for _, c := range producerCases(t, rule) {
			cases++
			var in struct{ SQL string }
			var want struct{ Relations []string }
			mustDecode(t, c.Input, &in)
			mustDecode(t, c.Expect, &want)
			got, _, _ := sqlRelations(in.SQL)
			if sortedNames(got) != sortedNames(want.Relations) {
				t.Errorf("%s: sqlRelations(%q) = %v, want the set %v", c.ID, in.SQL, got, want.Relations)
			}
		}
	}
	if cases == 0 {
		t.Fatal("no sql-relations cases ran; re-vendor conformance/sql-relations.json")
	}
}

// TestValidScopeGuardsEmittedScopes는 생산자가 만든 스코프가 소비자 검증을 거치는지 본다 — 루트가 아닌
// 접두사의 끝 슬래시나 비정규 템플릿이 실리면 isthmus가 문서 전체를 거부한다.
func TestValidScopeGuardsEmittedScopes(t *testing.T) {
	for _, tc := range []struct {
		scope LimitationScope
		valid bool
	}{
		{LimitationScope{Templates: []string{"/orders/{}"}, Methods: []string{"GET"}}, true},
		{LimitationScope{TemplatePrefixes: []string{"/"}}, true},
		{LimitationScope{TemplatePrefixes: []string{"/admin/"}}, false},
		{LimitationScope{TemplateSuffixes: []string{"/{**}"}}, false},
		{LimitationScope{Templates: []string{"orders"}}, false},
		{LimitationScope{Templates: []string{"/a"}, Methods: []string{"ANY"}}, false},
		{LimitationScope{}, false},
	} {
		if got := validScope(tc.scope); got != tc.valid {
			t.Errorf("validScope(%+v) = %v, want %v", tc.scope, got, tc.valid)
		}
	}
}

// TestLimitationsDropInvalidScopes는 limitations가 소비자가 거부할 스코프를 버리되 그 한계 문구는 남기는지
// 본다 — 스코프 하나가 isthmus에서 문서 전체를 입력 오류로 만들고, 스코프 없는 한계는 문서 전체 효과라
// 넓은 쪽이다. 검토에서 호출 지점의 validScope를 지워도 모든 테스트가 통과했다.
func TestLimitationsDropInvalidScopes(t *testing.T) {
	s := &routeScan{gaps: map[string]*routeGap{
		"a-invalid": {prefix: "route-coverage:", message: "registration(s) under a root suffix", count: 1,
			suffixes: map[string]bool{"/": true}},
		"b-valid": {prefix: "route-coverage:", message: "registration(s) under a known template", count: 1,
			templates: map[string]bool{"/orders/{}": true}},
	}}
	lims, scopes := s.limitations()
	if len(lims) != 2 {
		t.Fatalf("both gaps keep their limitation text: %v", lims)
	}
	if len(scopes) != 1 || scopes[0].LimitationIndex != 1 || len(scopes[0].Templates) != 1 ||
		scopes[0].Templates[0] != "/orders/{}" {
		t.Errorf("only the valid scope may be emitted, pointing at its limitation: %+v", scopes)
	}
}
