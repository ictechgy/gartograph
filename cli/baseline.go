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

// baselineFile은 --baseline이 읽고 --write-baseline이 쓰는 파일 형식이다.
// 결정적 JSON이어야 diff가 의미가 있다 — Violations는 CheckRules의
// 정렬된 출력을 그대로 싣는다.
type baselineFile struct {
	Tool       string               `json:"tool"`
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
	if f.Version > baselineVersion {
		return nil, fmt.Errorf(
			"baseline %s: version %d > supported %d — regenerate with a newer gartograph",
			path, f.Version, baselineVersion)
	}
	return &f, nil
}

// saveBaseline은 현재 위반 전부를 baseline 파일로 쓴다.
// 부분 쓰기가 남지 않게 임시 파일에 쓰고 rename한다 — export.SaveFile과
// 같은 이유다.
func saveBaseline(path string, violations []analysis.Violation) error {
	data, err := json.MarshalIndent(baselineFile{
		Tool: graph.Tool, Version: baselineVersion, Violations: violations,
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
