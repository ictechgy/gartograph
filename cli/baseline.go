// rules baseline 파일 입출력.
// baseline은 "이미 알고 있는 위반"의 스냅샷이다 — 기존 레포가 규칙 검사를
// 도입할 때 현재 위반을 합법화하고 새 위반만 막기 위한 파일이다.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ictechgy/gartograph/analysis"
	"github.com/ictechgy/gartograph/export"
	"github.com/ictechgy/gartograph/graph"
)

// baselineVersion은 baseline 파일 형식의 버전이다.
// 위치를 모듈 루트 상대 경로로 쓰게 된 뒤에도 1이다 — 필드와 뜻이 같고 짝짓기 키가 경로를
// 보지 않아(fileScope는 파일 이름) 절대 경로로 쓴 예전 baseline과 새 baseline이 서로 맞는다.
// 올리면 그 사이 판의 바이너리가 짝짓기에 문제없는 새 baseline까지 거부한다.
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
// 위치 파일은 root(moduleRoot) 기준 상대 경로로 바꿔 쓴다 — portablePosition.
func saveBaseline(path, root string, violations []analysis.Violation) error {
	portable := slices.Clone(violations)
	for i := range portable {
		portable[i].Position = portablePosition(root, portable[i].Position)
	}
	return saveBaselineJSON(path, baselineFile{
		Tool: graph.Tool, Kind: baselineKind,
		Version: baselineVersion, Violations: portable,
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
// 위치 파일은 root(moduleRoot) 기준 상대 경로로 바꿔 쓴다 — portablePosition.
func saveDeadBaseline(path, root string, findings []analysis.Finding) error {
	portable := slices.Clone(findings)
	for i := range portable {
		portable[i].Position = portablePosition(root, portable[i].Position)
	}
	return saveBaselineJSON(path, deadBaselineFile{
		Tool: graph.Tool, Kind: "dead-baseline",
		Version: baselineVersion, Findings: portable,
	})
}

// moduleRoot는 baseline·SARIF가 위치를 상대화하는 기준 디렉터리다 — 주 모듈의 go.mod
// 디렉터리(ModuleDir)이고, 그것을 싣지 않은 옛 문서에서는 Root다. analysis.relFile과 지금은 같은
// 기준이지만 따로 둔다: 규칙 패턴을 맞추는 기준은 패키지마다 달라질 수 있어도, 커밋하는 baseline과
// 업로드하는 SARIF는 문서 하나에 기준 하나라야 같은 파일이 늘 같은 경로로 적힌다.
// 저장 문서(--graph)의 ModuleDir과 위치는 같은 수확 머신의 값이라 서로 상대화해도 맞다.
func moduleRoot(doc *graph.Document) string {
	if doc.ModuleDir != "" {
		return doc.ModuleDir
	}
	return doc.Root
}

// relativeToRoot는 root 안의 file을 root 기준 '/' 구분 상대 경로로 바꾼다. 절대 경로가 아닌
// 위치(다른 도구가 만든 문서나 테스트의 "a/a.go")는 이미 root 기준으로 보고 정리만 한다.
// file이 root 안에 있지 않으면 ok는 거짓이다 — ".."로 벗어나는 파일(표준 라이브러리·의존 모듈·
// 다른 go.work 모듈), root를 모르는 문서, 다른 볼륨, 다른 OS에서 수확해 이 OS가 절대 경로로 알아보지
// 못한 경로가 그렇다. 벗어나는 파일의 "../" 경로는 쓰지 않는다: 머신마다 다른 GOROOT·모듈 캐시
// 위치를 담고, 파일시스템 루트까지 올라가면 절대 경로를 그대로 적은 셈이 된다.
// 어휘적으로만 계산한다: 저장 문서의 경로는 이 머신에 없을 수 있어 심링크를 풀지 않는다.
func relativeToRoot(root, file string) (rel string, ok bool) {
	rel = filepath.Clean(file)
	if filepath.IsAbs(file) {
		var err error
		if rel, err = filepath.Rel(root, file); err != nil {
			// 상대 형태가 없다(root를 모름·다른 볼륨) — root 밖과 같이 다룬다.
			return "", false
		}
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || rootedElsewhere(rel) {
		return "", false
	}
	return rel, true
}

// rootedElsewhere는 상대 경로로 보인 위치가 어느 OS 기준으로든 루트에서 시작하는지 본다 —
// 윈도에서 수확한 문서를 리눅스에서 읽으면 "C:\m\x.go"가 절대 경로로 보이지 않아, 거르지 않으면
// 그 머신의 경로가 상대 경로인 양 그대로 옮겨 적힌다.
func rootedElsewhere(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	if len(p) < 2 || p[1] != ':' {
		return false
	}
	drive := p[0]
	return 'a' <= drive && drive <= 'z' || 'A' <= drive && drive <= 'Z'
}

// portablePosition은 baseline에 쓸 위치를 만든다 — 파일을 root 기준 '/' 구분 상대 경로로 바꾼
// 사본이다. 커밋하는 baseline에 수확한 머신의 절대 경로(사용자 이름·디렉터리 구조)가 남지 않게 한다.
// root 밖 파일(relativeToRoot)은 파일 이름만 남긴다. 이름은 analysis.ViolationBaselineKey가
// fileScope 짝짓기에 읽는 부분이라('/'와 '\' 둘 다 구분자) 짝짓기는 그대로다 — dead 키는 위치를
// 읽지 않는다. 넘겨받은 위치는 고치지 않는다: 같은 위치를 보고서가 절대 경로로 쓴다.
func portablePosition(root string, p *graph.Position) *graph.Position {
	if p == nil {
		return nil
	}
	portable := *p
	if rel, ok := relativeToRoot(root, p.File); ok {
		portable.File = rel
	} else {
		portable.File = p.File[strings.LastIndexAny(p.File, `/\`)+1:]
	}
	return &portable
}

// saveBaselineJSON은 baseline 파일을 원자적으로 쓴다.
// 부분 쓰기가 남지 않게 임시 파일에 쓰고 rename한다 — export.SaveFile과
// 같은 이유다.
func saveBaselineJSON(path string, body any) error {
	data, err := marshalReport(body)
	if err != nil {
		return fmt.Errorf("encoding baseline: %w", err)
	}
	return export.WriteFile(path, append(data, '\n'))
}
