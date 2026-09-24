// Package config는 .gartograph.yml 규칙 파일을 읽는다.
//
// yaml.v3는 이 패키지 안에서만 import한다 — 설정 형식이 core로 새 나가면
// 순수 도메인이 파일 형식에 묶인다.
package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ictechgy/gartograph/graph"
)

// File은 .gartograph.yml의 형식이다.
//
//	components: 컴포넌트명 → 모듈 상대 경로 패턴들
//	deps: 컴포넌트명 → 의존해도 되는 컴포넌트명들
//	deny: 컴포넌트명 → 절대 의존하면 안 되는 대상들(스칼라 또는 {to, reason})
//	signature: 컴포넌트명 → 공개 API 시그니처가 참조해도 되는 컴포넌트명들
//	common: 모든 컴포넌트가 deps에 적지 않아도 의존할 수 있는 컴포넌트명들
//	visibleTo: 컴포넌트명 → 그 컴포넌트를 의존해도 되는 컴포넌트명들
//	forbidden: 간접 경로까지 금지하는 {from, to} 컴포넌트 쌍 목록
//	independent: 어느 방향으로도 서로 도달하면 안 되는 컴포넌트명들
//	fileRules: import가 일어나는 파일 패턴으로 스코프를 좁히는 규칙들
//	limits: 컴포넌트별 의존 수 상한({component, maxIn?, maxOut?})
//	exclude: 그래프에서 아예 빼는 모듈 상대 패키지 경로 패턴들
//
// components 패턴은 --deps로 수확된 외부 패키지의 전체 import 경로에도
// 매칭된다 — `aws: ["github.com/aws/**"]`를 컴포넌트로 두면 deps/deny가
// vendor 규칙이 된다. 외부 패턴이 어느 정점에도 매칭되지 않으면
// unmatchedComponents로 보고된다 — --deps 없이 수확했다는 신호다.
//
// deps에 없는 컴포넌트는 아무것도 의존할 수 없다 — 허용 목록이 기본이어야
// 누락이 "허용"으로 새지 않는다. deny는 deps보다 먼저 적용된다 —
// "보통 허용하지만 이 조합은 금지"를 표현하기 위함이다.
// signature는 deps보다 좁은 규칙이다 — 본문 의존은 허용하되 공개 API의
// 타입 누출만 막을 때 쓴다. 키가 없으면 시그니처 검사는 하지 않는다.
// visibleTo는 공급자 측 규칙이다 — deps가 "내가 무엇을 쓸 수 있나"라면
// visibleTo는 "누가 나를 쓸 수 있나"다. 둘 다 허용 목록이라 visibleTo는
// deps를 좁힐 뿐 풀지 않는다. 키가 없으면 제한이 없고, 빈 목록이면
// 자기 자신 외 누구도 의존할 수 없다.
// forbidden은 deps/deny와 달리 간접 도달까지 검사한다 — from 컴포넌트의
// 정점이 의존 간선을 몇 홉이든 타고 to 컴포넌트에 닿으면 위반이다.
// 직접 간선만 보는 deps로는 "A가 C에 도달하면 안 됨"을 표현할 수 없다.
// independent는 forbidden을 양방향으로 든 계약이다 — import-linter의
// independence와 같다. {from,to} 두 건을 나열해도 되지만 "둘은 독립"이라는
// 의도가 이름으로 남는다.
// fileRules는 컴포넌트가 아니라 파일로 스코프를 좁힌다 — dependency-cruiser의
// not-to-dev-dep과 같은 계약이다. import 간선의 사용 지점 파일이 from 패턴에
// 맞으면(또는 `!` 접두사면 안 맞으면) to 컴포넌트 의존이 위반이다 —
// "!*_test.go"는 "프로덕션 파일이 테스트 의존을 import하면 위반"이다.
// exclude는 수확 범위에서 패키지를 뺀다 — dependency-cruiser의 exclude와
// 같은 계약이다. 패턴은 컴포넌트 패턴과 같은 의미론(정확 일치, `x/**`,
// 세그먼트 글롭)으로 모듈 상대 경로에 맞춘다. 제외된 패키지는 정점이
// 아니고, 그 패키지로 향하는 import는 "생략"으로 limitation에 센다 —
// 조용히 빼면 "없는 것"과 "뺀 것"을 구분할 수 없다.
// stability는 안정성 방향 계약이다 — dependency-cruiser의 moreUnstable과
// 같은 규칙이다. 켜면 컴포넌트는 자기보다 불안정한(I = Ce/(Ca+Ce)가 더
// 큰) 컴포넌트에 의존할 수 없다 — 의존은 안정된 쪽으로만 흘러야
// 변화가 위로 번지지 않는다.
// limits는 크기 상한이다 — dep-cruiser의 max-dependencies와 같은 계약으로,
// 방향이 아니라 양을 제한한다. maxOut은 "이 컴포넌트가 의존할 수 있는 다른
// 컴포넌트 수", maxIn은 "이 컴포넌트를 의존할 수 있는 다른 컴포넌트 수"다.
// 상한을 넘기면 어느 간선 하나가 아니라 컴포넌트 자체가 위반이다 —
// 새 의존 추가가 기존 어느 것도 직접 어기지 않아도 덩치 계약은 깨진다.
type File struct {
	Version     int                    `yaml:"version"`
	Components  map[string][]string    `yaml:"components"`
	Deps        map[string][]string    `yaml:"deps"`
	Deny        map[string][]DenyEntry `yaml:"deny"`
	Signature   map[string][]string    `yaml:"signature"`
	Common      []string               `yaml:"common"`
	VisibleTo   map[string][]string    `yaml:"visibleTo"`
	Forbidden   []ForbiddenRule        `yaml:"forbidden"`
	Independent []string               `yaml:"independent"`
	FileRules   []FileRule             `yaml:"fileRules"`
	Limits      []LimitRule            `yaml:"limits"`
	Exclude     []string               `yaml:"exclude"`
	Stability   bool                   `yaml:"stability"`
}

// DenyEntry는 deny 목록의 한 항목이다.
// 스칼라("cli")와 맵({to: cli, reason: "use X instead"}) 두 형태를 받는다 —
// reason은 왜 금지인지·대신 무엇을 쓰는지를 위반 보고에 싣는 필드다.
type DenyEntry struct {
	To     string `yaml:"to"`
	Reason string `yaml:"reason,omitempty"`
}

// UnmarshalYAML은 스칼라 항목을 {to: 스칼라}로 승격한다.
// 두 형태를 섞어 쓸 수 있어야 기존 설정이 깨지지 않는다.
func (e *DenyEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		e.To = value.Value
		return nil
	}
	type plain DenyEntry
	return value.Decode((*plain)(e))
}

// ForbiddenRule은 from 컴포넌트가 to 컴포넌트에 간접적으로도
// 도달하면 안 된다는 계약이다 — import-linter의 forbidden 계약과 같다.
type ForbiddenRule struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// LimitRule은 컴포넌트 하나의 의존 수 상한이다.
// MaxIn은 나를 의존할 수 있는 다른 컴포넌트 수(Ca), MaxOut은 내가 의존할
// 수 있는 다른 컴포넌트 수(Ce)의 상한이다 — 둘 다 포인터라 0이
// "아무것도 의존/의존받지 말라"로 표현된다. 하나 이상은 있어야 한다.
type LimitRule struct {
	Component string `yaml:"component"`
	MaxIn     *int   `yaml:"maxIn,omitempty"`
	MaxOut    *int   `yaml:"maxOut,omitempty"`
}

// FileRule은 import가 일어나는 소스 파일로 스코프를 좁히는 규칙이다.
// Name은 보고와 baseline 동일성의 식별자다 — 이름이 같으면 같은 규칙이다.
// From은 파일 글롭이다: `/`가 없으면 파일명에, 있으면 모듈 상대 경로에
// 맞춘다. `!` 접두사는 반전이다 — "!*_test.go"는 테스트가 아닌 파일에서의
// import를 위반으로 본다. To는 대상 컴포넌트다.
// Reason은 왜 금지인지를 위반 보고에 싣는다.
type FileRule struct {
	Name   string `yaml:"name"`
	From   string `yaml:"from"`
	To     string `yaml:"to"`
	Reason string `yaml:"reason,omitempty"`
}

// 파일 후보 이름 — 두 확장자를 다 받는다.
var candidates = []string{".gartograph.yml", ".gartograph.yaml"}

// Find는 dir 아래에서 규칙 파일을 찾는다.
func Find(dir string) (string, bool) {
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

// Load는 규칙 파일을 읽어 검증한다.
// 컴포넌트가 하나도 없으면 규칙 검사 자체가 무의미하므로 에러다.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading rules file: %w", err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(f.Components) == 0 {
		return nil, fmt.Errorf("%s: no components defined — rules need at least one", path)
	}
	if err := f.checkRefs(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

// checkRefs는 규칙이 참조하는 컴포넌트명이 components에 정의됐는지 확인한다.
// 정의되지 않은 이름은 영원히 매칭되지 않는 죽은 규칙이다 — 오타를 조용히
// 삼키면 "규칙이 있다"는 착각을 만든다. 정의됐지만 정점에 매칭 안 되는
// 컴포넌트는 여기서가 아니라 unmatchedComponents로 보고된다.
func (f *File) checkRefs() error {
	// 진단은 결정적이어야 한다 — 맵 순회 순서에 맡기면 같은 설정이
	// 실행마다 다른 첫 오류를 낸다. 섹션별로 키를 정렬해 검사한다.
	defined := func(name string) bool { _, ok := f.Components[name]; return ok }
	check := func(section, side, name string) error {
		if name == "" {
			return fmt.Errorf("%s: empty %s component name", section, side)
		}
		if !defined(name) {
			return fmt.Errorf("%s: %s %q is not a defined component",
				section, side, name)
		}
		return nil
	}
	for _, from := range sortedKeys(f.Deps) {
		if err := check("deps", "key", from); err != nil {
			return err
		}
		for _, to := range f.Deps[from] {
			if err := check("deps", "target", to); err != nil {
				return err
			}
		}
	}
	for _, from := range sortedKeys(f.Deny) {
		if err := check("deny", "key", from); err != nil {
			return err
		}
		for _, e := range f.Deny[from] {
			if err := check("deny", "target", e.To); err != nil {
				return err
			}
		}
	}
	for _, from := range sortedKeys(f.Signature) {
		if err := check("signature", "key", from); err != nil {
			return err
		}
		for _, to := range f.Signature[from] {
			if err := check("signature", "target", to); err != nil {
				return err
			}
		}
	}
	for _, from := range sortedKeys(f.VisibleTo) {
		if err := check("visibleTo", "key", from); err != nil {
			return err
		}
		for _, to := range f.VisibleTo[from] {
			if err := check("visibleTo", "target", to); err != nil {
				return err
			}
		}
	}
	for _, c := range f.Common {
		if err := check("common", "entry", c); err != nil {
			return err
		}
	}
	for _, r := range f.Forbidden {
		if r.From == r.To && r.From != "" {
			return fmt.Errorf("forbidden: %q -> %q is meaningless (same component)", r.From, r.To)
		}
		if err := check("forbidden", "from", r.From); err != nil {
			return err
		}
		if err := check("forbidden", "to", r.To); err != nil {
			return err
		}
	}
	for _, c := range f.Independent {
		if err := check("independent", "entry", c); err != nil {
			return err
		}
	}
	for i, pat := range f.Exclude {
		if strings.TrimSpace(pat) == "" {
			return fmt.Errorf("exclude: entry %d is an empty pattern", i)
		}
		// 문법이 깨진 글롭은 영원히 아무것도 매칭하지 않는 죽은 설정이다 —
		// checkRefs와 같은 이유로 로드 시점에 거부한다.
		if _, err := path.Match(pat, "probe"); err != nil {
			return fmt.Errorf("exclude: entry %d %q is not a valid glob", i, pat)
		}
	}
	names := map[string]bool{}
	for _, r := range f.FileRules {
		if r.Name == "" {
			return fmt.Errorf("fileRules: empty rule name")
		}
		if names[r.Name] {
			return fmt.Errorf("fileRules: duplicate rule name %q", r.Name)
		}
		names[r.Name] = true
		if r.From == "" {
			return fmt.Errorf("fileRules %q: empty from file pattern", r.Name)
		}
		if err := check("fileRules", "to", r.To); err != nil {
			return err
		}
	}
	for i, r := range f.Limits {
		if err := check("limits", "component", r.Component); err != nil {
			return err
		}
		if r.MaxIn == nil && r.MaxOut == nil {
			return fmt.Errorf("limits: entry %d for %q sets neither maxIn nor maxOut",
				i, r.Component)
		}
		for _, bound := range []struct {
			name string
			v    *int
		}{{"maxIn", r.MaxIn}, {"maxOut", r.MaxOut}} {
			if bound.v != nil && *bound.v < 0 {
				return fmt.Errorf("limits: %s for %q is negative (%d)",
					bound.name, r.Component, *bound.v)
			}
		}
	}
	return nil
}

// sortedKeys는 맵의 키를 정렬해 돌려준다 — 진단 순서를 입력이 아닌
// 맵 순회에 맡기지 않기 위한 장치다.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Render는 규칙 파일을 .gartograph.yml 텍스트로 만든다 — init 명령이 쓴다.
// yaml.v3가 이 패키지 안에 있어야 해서 직렬화도 여기서 한다.
// deps의 빈 항목도 `[]`로 적는다 — "관찰된 의존 없음"과 "규칙 누락"이
// 파일에서 구분되어야 허용 목록 의미론이 성립한다.
func Render(f *File) ([]byte, error) {
	// 출력 키 순서를 고정하기 위해 맵이 아닌 구조체로 직렬화한다.
	doc := struct {
		Components map[string][]string `yaml:"components"`
		Deps       map[string][]string `yaml:"deps"`
	}{Components: f.Components, Deps: f.Deps}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encoding rules: %w", err)
	}
	header := `# .gartograph.yml — generated by gartograph init
# deps mirrors observed imports — tighten it as boundaries firm up.
`
	return append([]byte(header), body...), nil
}

// ComponentOf는 모듈 상대 경로를 컴포넌트로 해석한다.
// 패턴은 정확 일치, `x/**` 재귀 접두사, path.Match 글롭 순으로 본다.
// 여러 컴포넌트가 맞으면 더 긴 패턴이 이긴다 — 구체적 규칙이 우선한다.
// 길이까지 같으면 이름이 작은 쪽이 이긴다 — 맵 순회 순서에 맡기면
// 같은 설정이 실행마다 다른 컴포넌트를 골라 baseline이 흔들린다.
func (f *File) ComponentOf(relPath string) (string, bool) {
	best, bestLen := "", -1
	for name, pats := range f.Components {
		for _, pat := range pats {
			if !matchPath(pat, relPath) {
				continue
			}
			if len(pat) > bestLen || (len(pat) == bestLen && name < best) {
				best, bestLen = name, len(pat)
			}
		}
	}
	return best, best != ""
}

// Allowed는 from 컴포넌트가 to 컴포넌트에 의존할 수 있는지 본다.
// 자기 자신으로의 의존과 common 컴포넌트로의 의존은 항상 허용된다 —
// 컴포넌트 내부 import와 모두가 쓰는 공통 부품은 규칙 밖이다.
func (f *File) Allowed(from, to string) bool {
	if from == to {
		return true
	}
	for _, d := range f.Deps[from] {
		if d == to {
			return true
		}
	}
	for _, c := range f.Common {
		if c == to {
			return true
		}
	}
	return false
}

// Visible은 from 컴포넌트가 to 컴포넌트를 의존해도 되는지 공급자 측에서 본다.
// visibleTo에 항목이 없는 컴포넌트는 제한이 없고, 있는 컴포넌트는
// 목록(과 자기 자신)만이 의존할 수 있다 — deps와 같은 허용 목록 의미론이다.
func (f *File) Visible(from, to string) bool {
	allowed, declared := f.VisibleTo[to]
	if !declared || from == to {
		return true
	}
	for _, c := range allowed {
		if c == from {
			return true
		}
	}
	return false
}

// Denied는 from→to 의존이 명시적으로 금지됐는지 본다.
// 금지됐으면 설정된 사유(reason)를 함께 돌려준다 — "왜/대신 무엇"은
// 위반을 읽는 에이전트가 다음 행동을 정하는 데 필요한 정보다.
// deny는 허용 목록보다 먼저 적용된다 — deps에 있어도 deny가 이긴다.
func (f *File) Denied(from, to string) (reason string, denied bool) {
	if from == to {
		return "", false
	}
	for _, e := range f.Deny[from] {
		if e.To == to {
			return e.Reason, true
		}
	}
	return "", false
}

// SignatureAllowed는 from 컴포넌트의 공개 API 시그니처가 to 컴포넌트의
// 타입을 참조할 수 있는지 본다. Signature 키가 없는 컴포넌트는 시그니처
// 검사 대상이 아니다 — 규칙을 모르는 것과 금지된 것은 다르다.
func (f *File) SignatureAllowed(from, to string) bool {
	allowed, declared := f.Signature[from]
	if !declared || from == to {
		return true
	}
	for _, c := range allowed {
		if c == to {
			return true
		}
	}
	return false
}

// MatchFile은 파일 규칙의 from 패턴을 실제 파일 경로와 맞춘다.
// `!` 접두사는 반전이다. 패턴에 `/`가 없으면 파일명에, 있으면 relPath
// (보통 모듈 상대 경로)에 맞춘다 — "_test.go" 접미사 규칙과
// "internal/**" 경로 규칙을 한 장치로 표현하기 위함이다.
func MatchFile(pattern, relPath string) bool {
	neg := strings.HasPrefix(pattern, "!")
	pat := strings.TrimPrefix(pattern, "!")
	target := relPath
	if !strings.Contains(pat, "/") {
		if i := strings.LastIndex(relPath, "/"); i >= 0 {
			target = relPath[i+1:]
		}
	}
	return matchPath(pat, target) != neg
}

// Excluded는 모듈 상대 경로(또는 --deps 외부 패키지의 전체 경로)가
// exclude 패턴 중 하나에 맞는지 본다 — 수확 측이 이 패키지를 그래프에서
// 뺄지 결정하는 단일 판정점이다.
func (f *File) Excluded(relPath string) bool {
	for _, pat := range f.Exclude {
		if matchPath(pat, relPath) {
			return true
		}
	}
	return false
}

// matchPath는 패턴 하나와 경로를 맞춘다 — 의미론의 정본은
// graph.MatchPath다. 컴포넌트·exclude·fileRules가 같은 "맞다"를
// 공유해야 설정이 거짓말하지 않는다.
func matchPath(pattern, path string) bool {
	return graph.MatchPath(pattern, path)
}
