// SARIF 2.1.0 직렬화 — rules 결과를 GitHub code scanning 같은 CI 소비자가
// 읽을 수 있는 형태로보낸다. 패키지 레벨 위반은 파일 위치가 아니라
// 논리 위치(logicalLocations)를 쓴다 — 모르는 줄 번호를 지어내지 않는다.
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/ictechgy/gartograph/analysis"
)

// sarifDoc은 SARIF 로그의 뿌리다.
type sarifDoc struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string       `json:"id"`
	ShortDescription sarifMessage `json:"shortDescription"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	LogicalLocations []sarifLogical `json:"logicalLocations"`
}

type sarifLogical struct {
	FullyQualifiedName string `json:"fullyQualifiedName"`
	Kind               string `json:"kind"`
}

// rulesSARIF는 규칙 위반을 SARIF 로그로 직렬화한다.
// 위반 정렬은 CheckRules가 이미 결정적으로 하므로 순서를 그대로 따른다.
func rulesSARIF(violations []analysis.Violation) ([]byte, error) {
	results := make([]sarifResult, 0, len(violations))
	for _, v := range violations {
		msg := fmt.Sprintf("%s may not depend on %s (%s rule: %s -> %s)",
			v.From, v.To, v.Rule, v.FromComponent, v.ToComponent)
		if v.Reason != "" {
			msg += ": " + v.Reason
		}
		results = append(results, sarifResult{
			RuleID:  "layer-" + v.Rule,
			Level:   "error",
			Message: sarifMessage{Text: msg},
			Locations: []sarifLocation{{
				LogicalLocations: []sarifLogical{
					{FullyQualifiedName: v.From, Kind: "module"},
					{FullyQualifiedName: v.To, Kind: "module"},
				},
			}},
		})
	}
	return json.MarshalIndent(sarifDoc{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "gartograph",
				Version:        Version,
				InformationURI: "https://github.com/ictechgy/gartograph",
				Rules: []sarifRule{
					{ID: "layer-allow", ShortDescription: sarifMessage{
						Text: "dependency not in the component allowlist"}},
					{ID: "layer-deny", ShortDescription: sarifMessage{
						Text: "explicitly denied dependency"}},
					{ID: "layer-signature", ShortDescription: sarifMessage{
						Text: "public API signature leaks a disallowed component type"}},
					{ID: "layer-visibleTo", ShortDescription: sarifMessage{
						Text: "dependency on a component that restricts its consumers"}},
					{ID: "layer-forbidden", ShortDescription: sarifMessage{
						Text: "component reaches a forbidden component, possibly indirectly"}},
				},
			}},
			Results: results,
		}},
	}, "", "  ")
}
