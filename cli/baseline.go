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

// loadBaseline은 baseline 파일을 읽는다.
// 더 새로운 형식은 거부한다 — 모르는 필드를 버리면 "알려진 위반"이
// 조용히 새 위반으로 둔갑한다.
func loadBaseline(path string) (*baselineFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading baseline %s: %w", path, err)
	}
	var f baselineFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing baseline %s: %w", path, err)
	}
	// 봉투를 검증한다 — 그래프 JSON이나 빈 문서를 baseline으로 읽으면
	// 모든 위반이 fresh로 보고돼 "알려진 위반" 계약이 깨진다.
	if f.Tool != graph.Tool || f.Kind != baselineKind {
		return nil, fmt.Errorf(
			"baseline %s: not a gartograph violations baseline — "+
				"generate one with rules --write-baseline", path)
	}
	if f.Version < 1 || f.Version > baselineVersion {
		return nil, fmt.Errorf(
			"baseline %s: version %d outside supported 1..%d — regenerate the baseline",
			path, f.Version, baselineVersion)
	}
	return &f, nil
}

// saveBaseline은 현재 위반 전부를 baseline 파일로 쓴다.
// 부분 쓰기가 남지 않게 임시 파일에 쓰고 rename한다 — export.SaveFile과
// 같은 이유다.
func saveBaseline(path string, violations []analysis.Violation) error {
	data, err := json.MarshalIndent(baselineFile{
		Tool: graph.Tool, Kind: baselineKind,
		Version: baselineVersion, Violations: violations,
	}, "", "  ")
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
