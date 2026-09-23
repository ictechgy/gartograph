// rules baseline 파일 입출력.
// baseline은 "이미 알고 있는 위반"의 스냅샷이다 — 기존 레포가 규칙 검사를
// 도입할 때 현재 위반을 합법화하고 새 위반만 막기 위한 파일이다.
package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ictechgy/gartograph/analysis"
	"github.com/ictechgy/gartograph/graph"
)

// baselineVersion은 baseline 파일 형식의 버전이다.
const baselineVersion = 1

// baselineKind는 baseline 파일의 판별자다 — 그래프 문서도 tool/version을
// 쓰므로 둘을 구분하는 전용 필드가 필요하다. 그래프 JSON을 baseline으로
// 읽으면 모든 위반이 fresh로 보고돼 계약이 깨진다.
const baselineKind = "violations-baseline"

// baselineFile은 --baseline이 읽고 --write-baseline이 쓰는 파일 형식이다.
// 결정적 JSON이어야 diff가 의미가 있다 — Violations는 CheckRules의
// 정렬된 출력을 그대로 싣는다.
type baselineFile struct {
	Tool       string               `json:"tool"`
	Kind       string               `json:"kind"`
	Version    int                  `json:"version"`
	Violations []analysis.Violation `json:"violations"`
}

// cyclesBaselineFile은 cycles --baseline이 읽는 파일 형식이다.
// 순환의 동일성은 CycleBaselineKey의 멤버 튜플이다.
type cyclesBaselineFile struct {
	Tool    string           `json:"tool"`
	Kind    string           `json:"kind"`
	Version int              `json:"version"`
	Cycles  []analysis.Cycle `json:"cycles"`
}

// deadBaselineFile은 dead --baseline이 읽는 파일 형식이다.
// 항목의 동일성은 FindingBaselineKey의 (kind, id) 튜플이다.
type deadBaselineFile struct {
	Tool     string             `json:"tool"`
	Kind     string             `json:"kind"`
	Version  int                `json:"version"`
	Findings []analysis.Finding `json:"findings"`
}

// loadBaseline은 baseline 파일을 읽는다.
// 더 새로운 형식은 거부한다 — 모르는 필드를 버리면 "알려진 위반"이
// 조용히 새 위반으로 둔갑한다.
func loadBaseline(path string) (*baselineFile, error) {
	var f baselineFile
	if err := loadBaselineJSON(path, baselineKind, "rules --write-baseline", &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// loadCyclesBaseline은 cycles baseline을 읽는다.
func loadCyclesBaseline(path string) (*cyclesBaselineFile, error) {
	var f cyclesBaselineFile
	if err := loadBaselineJSON(path, "cycles-baseline",
		"cycles --write-baseline", &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// loadDeadBaseline은 dead baseline을 읽는다.
func loadDeadBaseline(path string) (*deadBaselineFile, error) {
	var f deadBaselineFile
	if err := loadBaselineJSON(path, "dead-baseline",
		"dead --write-baseline", &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// baselineEnvelope는 모든 baseline 파일이 공유하는 판별 헤더다.
type baselineEnvelope struct {
	Tool    string `json:"tool"`
	Kind    string `json:"kind"`
	Version int    `json:"version"`
}

// loadBaselineJSON은 baseline 파일을 읽어 봉투를 검증한 뒤 out에 디코드한다.
// 봉투 검증이 본체다 — 그래프 JSON이나 다른 kind의 baseline을 읽으면
// 모든 항목이 fresh로 보고돼 "알려진 항목" 계약이 깨진다.
func loadBaselineJSON(path, wantKind, genHint string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading baseline %s: %w", path, err)
	}
	var env baselineEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("parsing baseline %s: %w", path, err)
	}
	if env.Tool != graph.Tool || env.Kind != wantKind {
		return fmt.Errorf(
			"baseline %s: not a gartograph %s — generate one with %s",
			path, wantKind, genHint)
	}
	if env.Version < 1 || env.Version > baselineVersion {
		return fmt.Errorf(
			"baseline %s: version %d outside supported 1..%d — regenerate the baseline",
			path, env.Version, baselineVersion)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parsing baseline %s: %w", path, err)
	}
	return nil
}

// saveBaseline은 현재 위반 전부를 baseline 파일로 쓴다.
func saveBaseline(path string, violations []analysis.Violation) error {
	return saveBaselineJSON(path, baselineFile{
		Tool: graph.Tool, Kind: baselineKind,
		Version: baselineVersion, Violations: violations,
	})
}

// saveCyclesBaseline은 현재 순환 전부를 baseline 파일로 쓴다.
func saveCyclesBaseline(path string, cycles []analysis.Cycle) error {
	return saveBaselineJSON(path, cyclesBaselineFile{
		Tool: graph.Tool, Kind: "cycles-baseline",
		Version: baselineVersion, Cycles: cycles,
	})
}

// saveDeadBaseline은 현재 unreachable 보고 전부를 baseline 파일로 쓴다.
func saveDeadBaseline(path string, findings []analysis.Finding) error {
	return saveBaselineJSON(path, deadBaselineFile{
		Tool: graph.Tool, Kind: "dead-baseline",
		Version: baselineVersion, Findings: findings,
	})
}

// saveBaselineJSON은 baseline 파일을 원자적으로 쓴다.
// 부분 쓰기가 남지 않게 임시 파일에 쓰고 rename한다 — export.SaveFile과
// 같은 이유다.
func saveBaselineJSON(path string, body any) error {
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding baseline: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s: %w", path, err)
	}
	return nil
}
