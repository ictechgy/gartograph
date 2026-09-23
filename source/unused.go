// go.mod 미사용 require 탐지 — `go mod tidy`가 지울 대상을 읽기 전용으로
// 미리 보고한다. tidy가 go.mod를 고치는 명령인 것과 달리, 이 질의는 사실만
// 낸다 — 고치는 것은 소비자의 일이다.
//
// require 목록은 그래프에 없는 입력이라 모듈 파일을 직접 읽는다 —
// 수확은 원문을 옮기기만 하고 "쓰였나"의 판정 자료만 만든다.
package source

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/mod/modfile"
)

// UnusedReport는 go.mod require 중 어느 패키지도 import하지 않는 모듈의
// 보고다. Unused는 직접 require, UnusedIndirect는 // indirect 표시된
// 미사용 require다 — 둘은 다른 사실이다: indirect는 다른 의존이 끌어오는
// 핀이라 직접 require처럼 "지워도 되는 표명"이 아닐 수 있다.
type UnusedReport struct {
	Module         string   `json:"module"`
	Unused         []string `json:"unused"`
	UnusedIndirect []string `json:"unusedIndirect,omitempty"`
	UsedRequires   int      `json:"usedRequires"`
}

// UnusedRequires는 주 모듈의 go.mod를 읽어 미사용 require를 찾는다.
// "사용"은 도달 가능한 패키지들이 속한 비주 모듈 집합이다 — 테스트 변형까지
// 포함해 로드한다. go mod tidy는 테스트 파일의 import도 의존으로 세므로,
// --tests 없이 세면 tidy가 지키는 require를 미사용으로 오보한다.
func UnusedRequires(opts Options) (*UnusedReport, error) {
	opts.Tests = true
	pkgs, err := load(opts)
	if err != nil {
		return nil, err
	}
	modPath, modDir := "", ""
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Main {
			modPath, modDir = p.Module.Path, p.Module.Dir
			break
		}
	}
	if modDir == "" {
		return nil, fmt.Errorf("no main module found under %s — is there a go.mod?", opts.Dir)
	}
	requires, err := readRequires(filepath.Join(modDir, "go.mod"))
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, p := range walkImports(pkgs) {
		if p.Module != nil && !p.Module.Main {
			used[p.Module.Path] = true
		}
	}
	rep := &UnusedReport{Module: modPath}
	for _, r := range requires {
		if used[r.path] {
			rep.UsedRequires++
			continue
		}
		if r.indirect {
			rep.UnusedIndirect = append(rep.UnusedIndirect, r.path)
		} else {
			rep.Unused = append(rep.Unused, r.path)
		}
	}
	sort.Strings(rep.Unused)
	sort.Strings(rep.UnusedIndirect)
	return rep, nil
}

// requireEntry는 go.mod의 require 한 줄에서 필요한 두 필드다.
type requireEntry struct {
	path     string
	indirect bool
}

// readRequires는 go.mod의 require 목록을 읽는다.
// tool·toolchain·exclude 지시문은 require가 아니라 건너뛴다.
func readRequires(path string) ([]requireEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	out := make([]requireEntry, 0, len(f.Require))
	for _, r := range f.Require {
		out = append(out, requireEntry{path: r.Mod.Path, indirect: r.Indirect})
	}
	return out, nil
}
