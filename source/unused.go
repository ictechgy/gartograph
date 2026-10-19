// go.mod 미사용 require 탐지 — `go mod tidy`가 지울 대상을 읽기 전용으로
// 미리 보고한다. tidy가 go.mod를 고치는 명령인 것과 달리, 이 질의는 사실만
// 낸다 — 고치는 것은 소비자의 일이다.
//
// require 목록은 그래프에 없는 입력이라 모듈 파일을 직접 읽는다 —
// 수확은 원문을 옮기기만 하고 "쓰였나"의 판정 자료만 만든다.
package source

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

// UnusedReport는 go.mod require 중 어느 패키지도 import하지 않는 모듈의
// 보고다. Unused는 직접 require, UnusedIndirect는 // indirect 표시된
// 미사용 require다 — 둘은 다른 사실이다: indirect는 다른 의존이 끌어오는
// 핀이라 직접 require처럼 "지워도 되는 표명"이 아닐 수 있다.
// Limitations는 이 실행에서 실제로 센 판정 공백(로드 오류 패키지, 빌드 제약으로 빠진
// 파일)이다 — 없으면 키가 빠진다. 공백을 숨기면 쓰는 require가 미사용으로 보인다.
type UnusedReport struct {
	Module         string   `json:"module"`
	Unused         []string `json:"unused"`
	UnusedIndirect []string `json:"unusedIndirect,omitempty"`
	UsedRequires   int      `json:"usedRequires"`
	Limitations    []string `json:"limitations,omitempty"`
}

// UnusedRequires는 주 모듈의 go.mod를 읽어 미사용 require를 찾는다.
// "사용"은 도달 가능한 패키지들이 속한 비주 모듈 집합이다 — 테스트 변형까지
// 포함해 로드한다. go mod tidy는 테스트 파일의 import도 의존으로 세므로,
// --tests 없이 세면 tidy가 지키는 require를 미사용으로 오보한다.
// 같은 이유로 빌드 제약으로 빠진 파일(다른 OS·태그)의 import와 로드에 실패한 import도
// 사용으로 센다 — tidy는 ignore 외의 모든 태그·플랫폼을 보고, 받지 못한 모듈의 패키지도
// import된 사실은 그대로다.
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
	reachable := walkImports(pkgs)
	modules := requireModules(requires)
	used := usedModules(reachable, modules)
	excluded := scanExcludedFiles(reachable)
	for _, path := range excluded.imports {
		if m, ok := owningRequire(path, modules); ok {
			used[m] = true
		}
	}
	rep := &UnusedReport{Module: modPath, Limitations: unusedLimitations(reachable, excluded)}
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

// requireModules는 require 목록의 모듈 경로 집합이다 — import 경로를 모듈에 귀속하는 색인이다.
func requireModules(requires []requireEntry) map[string]bool {
	out := make(map[string]bool, len(requires))
	for _, r := range requires {
		out[r.path] = true
	}
	return out
}

// usedModules는 도달한 패키지가 속한 비주 모듈 집합이다. 로드에 실패해 모듈 정보가 없는
// 패키지(받을 수 없는 모듈·오프라인·인증 실패)는 import 경로를 require의 가장 긴 모듈 경로에
// 귀속한다 — 모듈 정보가 없다는 이유로 실제로 import하는 require를 미사용이라 하면 안 된다.
// 모듈이 없는 std 패키지는 어느 require에도 맞지 않아 그대로 빠진다.
func usedModules(reachable []*packages.Package, requires map[string]bool) map[string]bool {
	used := map[string]bool{}
	for _, p := range reachable {
		switch {
		case p.Module != nil && !p.Module.Main:
			used[p.Module.Path] = true
		case p.Module == nil:
			if m, ok := owningRequire(p.PkgPath, requires); ok {
				used[m] = true
			}
		}
	}
	return used
}

// owningRequire는 import 경로를 담을 수 있는 가장 긴 require 모듈 경로를 찾는다 — 경로가 모듈
// 경로와 같거나 "모듈 경로/"로 시작해야 한다. 중첩 모듈(a와 a/b)에서는 긴 쪽이 패키지를 담는다.
func owningRequire(importPath string, requires map[string]bool) (string, bool) {
	for p := importPath; p != ""; {
		if requires[p] {
			return p, true
		}
		i := strings.LastIndexByte(p, '/')
		if i < 0 {
			break
		}
		p = p[:i]
	}
	return "", false
}

// unusedLimitations는 판정이 빈 곳을 실제로 센 수로 문장화한다. 로드 오류 패키지는 PkgPath로
// 한 번만 센다 — 테스트 변형이 원 패키지의 오류를 다시 싣는다.
func unusedLimitations(reachable []*packages.Package, excluded excludedScan) []string {
	broken := map[string]bool{}
	for _, p := range reachable {
		if len(p.Errors) > 0 {
			broken[p.PkgPath] = true
		}
	}
	var out []string
	if len(broken) > 0 {
		out = append(out, fmt.Sprintf(
			"%d packages reported load errors; requires needed only through them may be reported as unused",
			len(broken)))
	}
	if excluded.files > 0 {
		out = append(out, fmt.Sprintf(
			"%d source files excluded by build constraints were read for their imports only; requires needed only by the imported packages' dependencies may be reported as unused (load them with --goos or --tags)",
			excluded.files))
	}
	if excluded.unreadable > 0 {
		out = append(out, fmt.Sprintf(
			"%d source files excluded by build constraints could not be read; requires imported only there may be reported as unused",
			excluded.unreadable))
	}
	return out
}

// excludedScan은 주 모듈 패키지에서 빌드 제약으로 빠진 Go 파일을 tidy처럼 읽은 결과다.
type excludedScan struct {
	imports    []string // 읽은 파일들의 import 경로
	files      int      // import를 읽은 파일 수 — tidy도 세지 않는 ignore 태그 파일은 공백이 아니라 빠진다
	unreadable int      // 정규 파일이 아니거나 읽기·파싱에 실패해 import를 모르는 파일 수
}

// scanExcludedFiles는 주 모듈 패키지(테스트 변형 포함)의 IgnoredFiles 중 Go 파일의 import를
// 모은다. 변형은 원 패키지와 같은 디렉터리라 같은 파일을 다시 싣는다 — 경로로 한 번만 읽는다.
func scanExcludedFiles(reachable []*packages.Package) excludedScan {
	var scan excludedScan
	seen := map[string]bool{}
	for _, p := range reachable {
		if p.Module == nil || !p.Module.Main {
			continue
		}
		for _, file := range p.IgnoredFiles {
			if seen[file] || !strings.HasSuffix(file, ".go") {
				continue
			}
			seen[file] = true
			scan.read(file)
		}
	}
	return scan
}

// read는 제외 파일 하나의 import를 모은다. tidy가 세지 않는 ignore 태그 파일은 건너뛴다.
func (s *excludedScan) read(path string) {
	f, err := parseImportHeader(path)
	if err != nil {
		s.unreadable++
		return
	}
	if !tidyBuildsFile(f) {
		return
	}
	s.files++
	for _, spec := range f.Imports {
		if imp, err := strconv.Unquote(spec.Path.Value); err == nil {
			s.imports = append(s.imports, imp)
		}
	}
}

// maxImportHeader는 제외 파일에서 빌드 제약과 import를 찾으며 읽는 앞부분의 상한(바이트)이다.
// 둘 다 파일 머리에 있어 보통 첫 몇 KB로 끝난다 — 상한은 끝없이 읽히는 장치나 거대한 생성
// 파일에서 메모리를 지키는 방어다. import만 파싱하므로 상한 뒤가 잘려도 판정에 쓰이지 않는다.
const maxImportHeader = 1 << 20

// parseImportHeader는 파일 머리를 막히지 않게 읽어 빌드 제약 주석과 import 선언만 파싱한다.
// 제외 파일은 go list가 내용을 열지 않았을 수 있다(이름의 GOOS 접미사로 거른 파일) — 그래서
// FIFO·장치일 수 있어 isGeneratedFile과 같은 규칙(readFlags로 열고 fstat가 정규 파일일 때만
// 읽기)을 쓴다.
func parseImportHeader(path string) (*ast.File, error) {
	f, err := os.OpenFile(path, readFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	src, err := io.ReadAll(io.LimitReader(f, maxImportHeader))
	if err != nil {
		return nil, err
	}
	return parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly|parser.ParseComments)
}

// tidyBuildsFile은 go mod tidy가 이 파일의 import를 세는지 본다. go 명령과 같은 우선순위로
// 패키지 절 앞의 //go:build 줄이 있으면 그 줄이, 없으면 // +build 줄 전부(AND)가 판정한다.
// 해석할 수 없는 //go:build 줄의 파일은 go list가 IgnoredFiles가 아니라 InvalidGoFiles로 보내
// 보통 여기 오지 않는다 — 와도 "사용" 쪽으로 둔다(쓰는 require를 미사용이라 하지 않게).
func tidyBuildsFile(f *ast.File) bool {
	var plus []constraint.Expr
	for _, cg := range f.Comments {
		if cg.Pos() > f.Package {
			break
		}
		for _, c := range cg.List {
			switch {
			case constraint.IsGoBuild(c.Text):
				x, err := constraint.Parse(c.Text)
				return err != nil || tidyBuilds(x)
			case constraint.IsPlusBuild(c.Text):
				if x, err := constraint.Parse(c.Text); err == nil {
					plus = append(plus, x)
				}
			}
		}
	}
	for _, x := range plus {
		if !tidyBuilds(x) {
			return false
		}
	}
	return true
}

// tidyBuilds는 제약식을 go mod tidy의 import 수집 규칙으로 평가한다. tidy는 ignore 외의 태그를
// "참이기도 하고 거짓이기도 한" 것으로 다뤄 그런 항은 부정 여부와 무관하게 참이 되고, ignore 항만
// 부정이 없을 때 거짓이다 — 그래서 //go:build ignore 파일만 빠지고 ignore || !linux는 남는다.
// 부정을 드모르간으로 항까지 내려 보내며 평가한다. 자기 호출 대신 명시적 스택으로 후위 순회한다 —
// 이름 있는 재귀는 자기 분석 cycles --level symbol이 순환으로 잡는다.
func tidyBuilds(x constraint.Expr) bool {
	var vals []bool
	stack := []tidyStep{{expr: x}}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if s.combine {
			a, b := vals[len(vals)-2], vals[len(vals)-1]
			vals = vals[:len(vals)-2]
			vals = append(vals, s.join(a, b))
			continue
		}
		switch e := s.expr.(type) {
		case *constraint.TagExpr:
			vals = append(vals, e.Tag != "ignore" || s.negated)
		case *constraint.NotExpr:
			stack = append(stack, tidyStep{expr: e.X, negated: !s.negated})
		case *constraint.AndExpr:
			stack = append(stack, s.operands(e.X, e.Y)...)
		case *constraint.OrExpr:
			stack = append(stack, s.operands(e.X, e.Y)...)
		}
	}
	return len(vals) == 1 && vals[0]
}

// tidyStep은 tidyBuilds의 스택 원소다. negated는 위에 부정이 홀수 개 있다는 뜻이고, combine이면
// 피연산자 둘의 값이 이미 값 스택 끝에 있어 expr의 연산자로 합칠 차례다.
type tidyStep struct {
	expr    constraint.Expr
	negated bool
	combine bool
}

// operands는 이항식의 합치기 단계와 두 피연산자 단계를 후위 순서로 쌓을 원소로 돌려준다 —
// 스택이라 왼쪽 피연산자가 먼저 평가되게 마지막에 둔다.
func (s tidyStep) operands(x, y constraint.Expr) []tidyStep {
	return []tidyStep{
		{expr: s.expr, negated: s.negated, combine: true},
		{expr: y, negated: s.negated},
		{expr: x, negated: s.negated},
	}
}

// join은 두 피연산자 값을 합친다. 부정 아래의 AND는 OR, OR는 AND다(드모르간).
func (s tidyStep) join(a, b bool) bool {
	_, isAnd := s.expr.(*constraint.AndExpr)
	if isAnd != s.negated {
		return a && b
	}
	return a || b
}
