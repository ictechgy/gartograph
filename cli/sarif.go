// SARIF 2.1.0 직렬화 — rules 결과를 GitHub code scanning 같은 CI 소비자가
// 읽을 수 있는 형태로보낸다. 패키지 레벨 위반은 파일 위치가 아니라
// 논리 위치(logicalLocations)를 쓴다 — 모르는 줄 번호를 지어내지 않는다.
package cli

import (
	"encoding/json"
	"fmt"
	"strings"

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
	// Properties는 규칙이 요구하는 부가 증거다 — forbidden 위반의
	// 목격 경로처럼 위치 둘로 표현되지 않는 사실을 싣는다.
	Properties map[string]any `json:"properties,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	LogicalLocations []sarifLogical `json:"logicalLocations"`
	PhysicalLocation *sarifPhysical `json:"physicalLocation,omitempty"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           sarifRegion   `json:"region"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
}

type sarifLogical struct {
	FullyQualifiedName string `json:"fullyQualifiedName"`
	Kind               string `json:"kind"`
}

// sarifLog는 결과와 규칙 색인을 SARIF 로그로 감싼다.
// rules·cycles·dead가 같은 봉투를 쓰므로 한 곳에서 만든다.
func sarifLog(rules []sarifRule, results []sarifResult) ([]byte, error) {
	return json.MarshalIndent(sarifDoc{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "gartograph",
				Version:        Version,
				InformationURI: "https://github.com/ictechgy/gartograph",
				Rules:          rules,
			}},
			Results: results,
		}},
	}, "", "  ")
}

// rulesSARIF는 규칙 위반을 SARIF 로그로 직렬화한다.
// 위반 정렬은 CheckRules가 이미 결정적으로 하므로 순서를 그대로 따른다.
func rulesSARIF(violations []analysis.Violation) ([]byte, error) {
	results := make([]sarifResult, 0, len(violations))
	for _, v := range violations {
		msg := fmt.Sprintf("%s may not depend on %s (%s rule: %s -> %s)",
			v.From, v.To, v.Rule, v.FromComponent, v.ToComponent)
		if v.Name != "" {
			msg = fmt.Sprintf("%s may not depend on %s (fileScope %q: %s -> %s)",
				v.From, v.To, v.Name, v.FromComponent, v.ToComponent)
		}
		if v.Reason != "" {
			msg += ": " + v.Reason
		}
		var props map[string]any
		if len(v.Path) > 0 {
			// forbidden은 도달 위반 — 증거는 양 끝이 아니라 사슬 전체다.
			msg += fmt.Sprintf(" (via %s)", strings.Join(v.Path, " -> "))
			props = map[string]any{"witnessPath": v.Path}
		}
		loc := sarifLocation{
			LogicalLocations: []sarifLogical{
				{FullyQualifiedName: v.From, Kind: "module"},
				{FullyQualifiedName: v.To, Kind: "module"},
			},
		}
		if v.Position != nil {
			// v2 문서는 위반 지점을 안다 — 논리 위치와 함께 물리 위치를 준다.
			loc.PhysicalLocation = &sarifPhysical{
				ArtifactLocation: sarifArtifact{URI: v.Position.File},
				Region: sarifRegion{
					StartLine:   v.Position.Line,
					StartColumn: v.Position.Column,
				},
			}
		}
		results = append(results, sarifResult{
			RuleID:     "layer-" + v.Rule,
			Level:      "error",
			Message:    sarifMessage{Text: msg},
			Locations:  []sarifLocation{loc},
			Properties: props,
		})
	}
	return sarifLog([]sarifRule{
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
		{ID: "layer-independence", ShortDescription: sarifMessage{
			Text: "components declared independent reach each other"}},
		{ID: "layer-fileScope", ShortDescription: sarifMessage{
			Text: "import from a file matching a file-scoped rule"}},
	}, results)
}

// cyclesSARIF는 순환을 SARIF 로그로 직렬화한다.
// 순환은 계약 위반이라 error다 — 멤버는 논리 위치로, 순환을 이루는 간선의
// 첫 지점은 물리 위치로 실어 둘 다 볼 수 있게 한다.
func cyclesSARIF(cycles []analysis.Cycle) ([]byte, error) {
	results := make([]sarifResult, 0, len(cycles))
	for _, c := range cycles {
		loc := sarifLocation{}
		for _, m := range c.Members {
			loc.LogicalLocations = append(loc.LogicalLocations,
				sarifLogical{FullyQualifiedName: m, Kind: "module"})
		}
		for _, e := range c.Edges {
			if len(e.Positions) == 0 {
				continue
			}
			p := e.Positions[0]
			loc.PhysicalLocation = &sarifPhysical{
				ArtifactLocation: sarifArtifact{URI: p.File},
				Region:           sarifRegion{StartLine: p.Line, StartColumn: p.Column},
			}
			break
		}
		results = append(results, sarifResult{
			RuleID: "dependency-cycle",
			Level:  "error",
			Message: sarifMessage{Text: fmt.Sprintf(
				"dependency cycle: %s", strings.Join(c.Members, " -> "))},
			Locations: []sarifLocation{loc},
		})
	}
	return sarifLog([]sarifRule{
		{ID: "dependency-cycle", ShortDescription: sarifMessage{
			Text: "vertices form a strongly connected component"}},
	}, results)
}

// deadSARIF는 unreachable 보고를 SARIF 로그로 직렬화한다.
// unreachable은 그래프 사실이지 위반이 아니므로 warning이다 — 삭제 판정을
// 심각도에 싣지 않는다.
func deadSARIF(findings []analysis.Finding) ([]byte, error) {
	results := make([]sarifResult, 0, len(findings))
	for _, f := range findings {
		loc := sarifLocation{
			LogicalLocations: []sarifLogical{
				{FullyQualifiedName: f.ID, Kind: string(f.Kind)},
			},
		}
		if f.Position != nil {
			loc.PhysicalLocation = &sarifPhysical{
				ArtifactLocation: sarifArtifact{URI: f.Position.File},
				Region: sarifRegion{
					StartLine:   f.Position.Line,
					StartColumn: f.Position.Column,
				},
			}
		}
		results = append(results, sarifResult{
			RuleID:    "unreachable-symbol",
			Level:     "warning",
			Message:   sarifMessage{Text: fmt.Sprintf("unreachable %s %s: %s", f.Kind, f.ID, f.Reason)},
			Locations: []sarifLocation{loc},
		})
	}
	return sarifLog([]sarifRule{
		{ID: "unreachable-symbol", ShortDescription: sarifMessage{
			Text: "symbol not reachable from retention roots (a fact, not a deletion verdict)"}},
	}, results)
}
