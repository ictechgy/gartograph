// SARIF 2.1.0 직렬화 — rules 결과를 GitHub code scanning 같은 CI 소비자가
// 읽을 수 있는 형태로보낸다. 패키지 레벨 위반은 파일 위치가 아니라
// 논리 위치(logicalLocations)를 쓴다 — 모르는 줄 번호를 지어내지 않는다.
// 물리 위치는 모듈 루트 기준 상대 URI(uriBaseId %SRCROOT%)로 적는다 — 업로드하는
// 로그에 수확한 머신의 절대 경로를 남기지 않는다.
package cli

import (
	"fmt"
	"strings"

	"github.com/ictechgy/gartograph/analysis"
	"github.com/ictechgy/gartograph/graph"
)

// srcRootBaseID는 물리 위치 URI의 기준(uriBaseId)이다. GitHub code scanning 문서의 상대 URI
// 예가 쓰는 이름이고, 여기서는 moduleRoot(go.mod 디렉터리)를 가리킨다.
const srcRootBaseID = "%SRCROOT%"

// srcRootDescription은 run.originalUriBaseIds가 %SRCROOT%에 붙이는 설명이다. 절대 경로(uri)는
// 싣지 않는다 — 기준을 밝히되 수확한 머신의 경로는 드러내지 않으려는 것이다. SARIF 2.1.0 §3.14.14는
// 절대 URI를 뺀 항목을 허용하고, 사용자 이름을 감추려고 uri를 지운 예를 그 절에 든다.
const srcRootDescription = "Module root: the directory of go.mod (the graph document's moduleDir, " +
	"else its root). The absolute path is omitted."

// locationOmittedNote는 모듈 루트 밖 파일을 가리키는 결과의 메시지 꼬리다 — type T time.Time의
// 필드처럼 표준 라이브러리에 선언된 것, 의존 모듈, 다른 go.work 모듈의 파일이 그렇다. 그런 파일은
// %SRCROOT% 상대 URI로 적을 수 없어 physicalLocation을 생략하는데, 생략한 이유를 소비자가 볼 수
// 있어야 한다. 논리 위치는 그대로 남는다.
const locationOmittedNote = " (location omitted: the file is not under the module root)"

// sarifDoc은 SARIF 로그의 뿌리다.
type sarifDoc struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool sarifTool `json:"tool"`
	// OriginalURIBaseIDs는 %SRCROOT%가 무엇인지 설명한다 — 그 기준을 쓰는 결과가 있을 때만 싣는다.
	OriginalURIBaseIDs map[string]sarifBaseLocation `json:"originalUriBaseIds,omitempty"`
	Results            []sarifResult                `json:"results"`
}

// sarifBaseLocation은 originalUriBaseIds의 항목이다 — 절대 uri 없이 설명만 싣는다(srcRootDescription).
type sarifBaseLocation struct {
	Description sarifMessage `json:"description"`
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
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId,omitempty"`
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
	run := sarifRun{
		Tool: sarifTool{Driver: sarifDriver{
			Name:           "gartograph",
			Version:        Version,
			InformationURI: "https://github.com/ictechgy/gartograph",
			Rules:          rules,
		}},
		Results: results,
	}
	if usesSourceRoot(results) {
		run.OriginalURIBaseIDs = map[string]sarifBaseLocation{
			srcRootBaseID: {Description: sarifMessage{Text: srcRootDescription}},
		}
	}
	return marshalReport(sarifDoc{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs:    []sarifRun{run},
	})
}

// usesSourceRoot는 %SRCROOT% 상대 물리 위치를 가진 결과가 하나라도 있는지 본다 — 없으면 기준
// 설명을 싣지 않는다. 물리 위치가 하나도 없는 로그(결과가 없거나 위치 없는 옛 문서)는 그래서 예전과
// 같은 바이트다.
func usesSourceRoot(results []sarifResult) bool {
	for _, r := range results {
		for _, l := range r.Locations {
			if l.PhysicalLocation != nil && l.PhysicalLocation.ArtifactLocation.URIBaseID == srcRootBaseID {
				return true
			}
		}
	}
	return false
}

// placeFile은 위치 p를 loc의 물리 위치로 싣는다. URI는 root(moduleRoot) 기준 상대 경로를 퍼센트
// 인코딩한 것이고 uriBaseId %SRCROOT%를 단다 — 업로드한 SARIF에 수확한 머신의 절대 경로가 남지
// 않게 한다. root 밖 파일은 그 기준으로 적을 수 없어 물리 위치를 싣지 않고, 메시지에 붙일 꼬리
// (locationOmittedNote)를 돌려준다. 위치가 없으면 아무것도 하지 않는다.
func placeFile(loc *sarifLocation, root string, p *graph.Position) (note string) {
	if p == nil {
		return ""
	}
	rel, ok := relativeToRoot(root, p.File)
	if !ok {
		return locationOmittedNote
	}
	loc.PhysicalLocation = &sarifPhysical{
		ArtifactLocation: sarifArtifact{URI: encodeURIPath(rel), URIBaseID: srcRootBaseID},
		Region:           sarifRegion{StartLine: p.Line, StartColumn: p.Column},
	}
	return ""
}

// encodeURIPath는 '/' 구분 상대 경로를 RFC 3986 경로로 퍼센트 인코딩한다. 세그먼트 구분자 '/'는
// 그대로 두고, 세그먼트 안에서는 unreserved·sub-delims·'@'만 남기고 나머지 바이트(공백·'#'·'%'·
// '?'·비ASCII 문자의 UTF-8 바이트 등)를 %XX로 바꾼다. ':'도 바꾼다 — 상대 참조의 첫 세그먼트에
// 든 ':'는 scheme 구분자로 읽힌다(RFC 3986 §4.2).
func encodeURIPath(rel string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(rel); i++ {
		c := rel[i]
		if c == '/' || isPlainSegmentByte(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0x0f])
	}
	return b.String()
}

// isPlainSegmentByte는 인코딩 없이 경로 세그먼트에 남길 바이트인지 본다 — RFC 3986 pchar에서
// ':'를 뺀 집합(unreserved, sub-delims, '@')이다.
func isPlainSegmentByte(c byte) bool {
	if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' {
		return true
	}
	return strings.IndexByte("-._~!$&'()*+,;=@", c) >= 0
}

// rulesSARIF는 규칙 위반을 SARIF 로그로 직렬화한다.
// 위반 정렬은 CheckRules가 이미 결정적으로 하므로 순서를 그대로 따른다.
// root는 물리 위치 URI의 기준(moduleRoot)이다.
func rulesSARIF(root string, violations []analysis.Violation) ([]byte, error) {
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
		// v2 문서는 위반 지점을 안다 — 논리 위치와 함께 물리 위치를 준다.
		msg += placeFile(&loc, root, v.Position)
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
// 첫 지점(Cycle.Position)은 물리 위치로 실어 둘 다 볼 수 있게 한다.
// 증거 간선은 위치 없이 관계만 담으므로 위치는 Cycle.Position에서만 읽는다.
// root는 물리 위치 URI의 기준(moduleRoot)이다.
func cyclesSARIF(root string, cycles []analysis.Cycle) ([]byte, error) {
	results := make([]sarifResult, 0, len(cycles))
	for _, c := range cycles {
		loc := sarifLocation{}
		for _, m := range c.Members {
			loc.LogicalLocations = append(loc.LogicalLocations,
				sarifLogical{FullyQualifiedName: m, Kind: "module"})
		}
		msg := fmt.Sprintf("dependency cycle: %s", strings.Join(c.Members, " -> "))
		msg += placeFile(&loc, root, c.Position)
		results = append(results, sarifResult{
			RuleID:    "dependency-cycle",
			Level:     "error",
			Message:   sarifMessage{Text: msg},
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
// 심각도에 싣지 않는다. root는 물리 위치 URI의 기준(moduleRoot)이다.
func deadSARIF(root string, findings []analysis.Finding) ([]byte, error) {
	results := make([]sarifResult, 0, len(findings))
	for _, f := range findings {
		loc := sarifLocation{
			LogicalLocations: []sarifLogical{
				{FullyQualifiedName: f.ID, Kind: string(f.Kind)},
			},
		}
		msg := fmt.Sprintf("unreachable %s %s: %s", f.Kind, f.ID, f.Reason)
		msg += placeFile(&loc, root, f.Position)
		results = append(results, sarifResult{
			RuleID:    "unreachable-symbol",
			Level:     "warning",
			Message:   sarifMessage{Text: msg},
			Locations: []sarifLocation{loc},
		})
	}
	return sarifLog([]sarifRule{
		{ID: "unreachable-symbol", ShortDescription: sarifMessage{
			Text: "symbol not reachable from retention roots (a fact, not a deletion verdict)"}},
	}, results)
}
