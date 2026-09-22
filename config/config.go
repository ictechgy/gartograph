// Package config는 .gartograph.yml 규칙 파일을 읽는다.
//
// yaml.v3는 이 패키지 안에서만 import한다 — 설정 형식이 core로 새 나가면
// 순수 도메인이 파일 형식에 묶인다.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// File은 .gartograph.yml의 형식이다.
//
//	components: 컴포넌트명 → 모듈 상대 경로 패턴들
//	deps: 컴포넌트명 → 의존해도 되는 컴포넌트명들
//
// deps에 없는 컴포넌트는 아무것도 의존할 수 없다 — 허용 목록이 기본이어야
// 누락이 "허용"으로 새지 않는다.
type File struct {
	Version    int                 `yaml:"version"`
	Components map[string][]string `yaml:"components"`
	Deps       map[string][]string `yaml:"deps"`
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
	return &f, nil
}

// ComponentOf는 모듈 상대 경로를 컴포넌트로 해석한다.
// 패턴은 정확 일치, `x/**` 재귀 접두사, path.Match 글롭 순으로 본다.
// 여러 컴포넌트가 맞으면 더 긴 패턴이 이긴다 — 구체적 규칙이 우선한다.
func (f *File) ComponentOf(relPath string) (string, bool) {
	best, bestLen := "", -1
	for name, pats := range f.Components {
		for _, pat := range pats {
			if !matchPath(pat, relPath) {
				continue
			}
			if len(pat) > bestLen {
				best, bestLen = name, len(pat)
			}
		}
	}
	return best, best != ""
}

// Allowed는 from 컴포넌트가 to 컴포넌트에 의존할 수 있는지 본다.
// 자기 자신으로의 의존은 항상 허용된다 — 컴포넌트 내부 import는 규칙 밖이다.
func (f *File) Allowed(from, to string) bool {
	if from == to {
		return true
	}
	for _, d := range f.Deps[from] {
		if d == to {
			return true
		}
	}
	return false
}

// matchPath는 패턴 하나와 경로를 맞춘다.
func matchPath(pattern, path string) bool {
	switch {
	case pattern == path:
		return true
	case strings.HasSuffix(pattern, "/**"):
		prefix := strings.TrimSuffix(pattern, "/**")
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	case strings.ContainsAny(pattern, "*?"):
		return matchGlob(pattern, path)
	default:
		return false
	}
}

// matchGlob은 path.Match와 달리 `*`가 `/`를 넘지 않는 단순 글롭을 쓴다.
// 세그먼트 단위로 비교해 `a/*`가 `a/b/c`를 맞지 않게 한다.
func matchGlob(pattern, path string) bool {
	pp, sp := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(pp) != len(sp) {
		return false
	}
	for i := range pp {
		if !matchSegment(pp[i], sp[i]) {
			return false
		}
	}
	return true
}

// matchSegment는 `/` 없는 한 세그먼트의 `*` 글롭을 비교한다.
func matchSegment(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	// `*`를 최대 하나만 지원한다 — 설정 파일의 패턴은 이 정도면 충분하다.
	i := strings.Index(pattern, "*")
	return strings.HasPrefix(s, pattern[:i]) &&
		strings.HasSuffix(s, pattern[i+1:]) &&
		len(s) >= len(pattern)-1
}
