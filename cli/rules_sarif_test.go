package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestRulesComponentViolationOutput는 정점 없는 컴포넌트 위반(limits)이 텍스트와 SARIF에서 컴포넌트로
// 보고되고, SARIF의 모든 결과 ruleId가 tool.driver.rules에 있는지 확인한다. 예전에는 텍스트가
// `violation[limit:maxOut]:  (web) ->  ()`, SARIF 메시지가 ` may not depend on  (fileScope "maxOut": web -> )`
// (fileScope 위반이라는 거짓 문구)였고, 빈 fullyQualifiedName 논리 위치 둘을 실었으며, layer-limit·
// layer-stability 결과의 규칙이 색인에 없었다.
func TestRulesComponentViolationOutput(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"api/api.go":     "package api\n\nimport _ \"example.com/fixture/web\"\n",
		"web/web.go":     "package web\n\nimport (\n\t_ \"example.com/fixture/cache\"\n\t_ \"example.com/fixture/db\"\n)\n",
		"cache/cache.go": "package cache\n\nimport _ \"example.com/fixture/db\"\n",
		"db/db.go":       "package db\n\nimport _ \"example.com/fixture/util\"\n",
		"util/util.go":   "package util\n\nimport _ \"example.com/fixture/leaf\"\n",
		"leaf/leaf.go":   "package leaf\n",
		".gartograph.yml": `components: {api: [api], web: [web], cache: [cache], db: [db], util: [util], leaf: [leaf]}
deps: {api: [web], web: [cache, db], cache: [db], db: [util], util: [leaf], leaf: []}
stability: true
limits:
  - {component: web, maxOut: 1}
  - {component: db, maxIn: 1}
`,
	})
	code, out, errb := run(t, "rules", "--dir", dir)
	if code != 0 || !strings.Contains(out, "violation[limit:maxOut]: component web\n") ||
		!strings.Contains(out, "violation[limit:maxIn]: component db\n") ||
		!strings.Contains(out, "violation[stability]: example.com/fixture/db (db) -> example.com/fixture/util (util)\n") ||
		strings.Contains(out, " ->  (") {
		t.Fatalf("text: want component lines for limits and the stability edge, got %d\n%s%s", code, out, errb)
	}

	code, out, errb = run(t, "rules", "--dir", dir, "--format", "sarif")
	if code != 0 {
		t.Fatalf("rules --format sarif: exit %d\n%s", code, errb)
	}
	var log struct {
		Runs []struct {
			Tool struct {
				Driver struct {
					Rules []struct {
						ID string `json:"id"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID  string `json:"ruleId"`
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
				Locations []struct {
					LogicalLocations []map[string]string `json:"logicalLocations"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &log); err != nil || len(log.Runs) != 1 {
		t.Fatalf("sarif is not one run: %v\n%s", err, out)
	}
	declared := map[string]bool{}
	for _, r := range log.Runs[0].Tool.Driver.Rules {
		declared[r.ID] = true
	}
	seen := map[string]int{}
	for _, r := range log.Runs[0].Results {
		seen[r.RuleID]++
		if !declared[r.RuleID] {
			t.Errorf("result rule %q is not declared in tool.driver.rules", r.RuleID)
		}
		for _, l := range r.Locations[0].LogicalLocations {
			if l["fullyQualifiedName"] == "" {
				t.Errorf("%s: empty logical location in %q", r.RuleID, r.Message.Text)
			}
		}
		if r.RuleID != "layer-limit" {
			continue
		}
		if strings.Contains(r.Message.Text, "fileScope") || strings.Contains(r.Message.Text, "may not depend on") ||
			len(r.Locations[0].LogicalLocations) != 1 {
			t.Errorf("limit result must name only its component: %q %v", r.Message.Text, r.Locations[0].LogicalLocations)
		}
	}
	want := []string{
		"component web (limit rule: maxOut): depends on 2 components; limit maxOut is 1",
		"component db (limit rule: maxIn): depended on by 2 components; limit maxIn is 1",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("sarif must carry %q:\n%s", w, out)
		}
	}
	if seen["layer-limit"] != 2 || seen["layer-stability"] != 1 {
		t.Errorf("want two limit results and one stability result, got %v", seen)
	}
}
