// go.mod 미사용 require 탐지 — `go mod tidy`가 지울 대상을 읽기 전용으로
// 미리 보고한다. tidy가 go.mod를 고치는 명령인 것과 달리, 이 질의는 사실만
// 낸다 — 고치는 것은 소비자의 일이다.
//
// require 목록은 그래프에 없는 입력이라 모듈 파일을 직접 읽는다 —
// 수확은 원문을 옮기기만 하고 "쓰였나"의 판정 자료만 만든다.
package source

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
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
// Limitations는 이 실행에서 실제로 센 판정 공백(로드 오류 패키지, 로드하지 않은 패키지를
// import하는 빌드 제약 제외 파일, 읽지 못한 제외 파일, 이 플랫폼 파일이 없어 패키지가 되지 않은
// 디렉터리, 읽지 못한 디렉터리)이다 — 없으면 키가 빠진다. 공백을 숨기면 쓰는 require가 미사용으로 보인다.
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
// import된 사실은 그대로다. 이 플랫폼 파일이 하나도 없는 디렉터리는 go list가 패키지로 내지 않아
// import를 읽을 길이 없다 — 그 수를 limitation으로 센다(scanUnloadedDirs).
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
	mod, err := readGoMod(filepath.Join(modDir, "go.mod"))
	if err != nil {
		return nil, err
	}
	requires := mod.requires
	reachable := walkImports(pkgs)
	modules := requireModules(requires)
	used := usedModules(reachable, modules)
	loaded := loadedPackagePaths(reachable)
	excluded := scanExcludedFiles(reachable, func(path string) bool {
		return unseenImport(path, loaded, modPath, modules)
	})
	for _, path := range excluded.imports {
		if m, ok := owningRequire(path, modules); ok {
			used[m] = true
		}
	}
	unloaded, err := scanUnloadedDirs(opts, modPath, modDir, mod.ignores, newLoadedDirs(reachable))
	if err != nil {
		return nil, err
	}
	rep := &UnusedReport{Module: modPath, Limitations: unusedLimitations(reachable, excluded, unloaded)}
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

// goModFacts는 go.mod에서 이 질의가 쓰는 두 가지다. ignores는 ignore 지시문(Go 1.25)의 경로다 — go
// 명령은 그 디렉터리를 ./... 순회와 tidy에서 빼므로, 패키지가 되지 않은 디렉터리를 셀 때도 빼야 한다.
type goModFacts struct {
	requires []requireEntry
	ignores  []string
}

// readGoMod는 go.mod의 require 목록과 ignore 지시문을 한 번의 파싱으로 읽는다.
// tool·toolchain·exclude 지시문은 require가 아니라 건너뛴다.
func readGoMod(path string) (goModFacts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return goModFacts{}, fmt.Errorf("reading %s: %w", path, err)
	}
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		return goModFacts{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	facts := goModFacts{requires: make([]requireEntry, 0, len(f.Require))}
	for _, r := range f.Require {
		facts.requires = append(facts.requires, requireEntry{path: r.Mod.Path, indirect: r.Indirect})
	}
	for _, ig := range f.Ignore {
		facts.ignores = append(facts.ignores, ig.Path)
	}
	return facts, nil
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
// 한 번만 센다 — 테스트 변형이 원 패키지의 오류를 다시 싣는다. 디렉터리 순회의 두 수는 끝에 붙인다
// — 그런 디렉터리가 없는 모듈의 문구와 순서는 예전과 같다.
func unusedLimitations(reachable []*packages.Package, excluded excludedScan, unloaded unloadedScan) []string {
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
	if excluded.unseenFiles > 0 {
		out = append(out, fmt.Sprintf(
			"%d source files excluded by build constraints import packages that were not loaded; requires needed only by those packages' dependencies may be reported as unused (load them with --goos or --tags)",
			excluded.unseenFiles))
	}
	if excluded.unreadable > 0 {
		out = append(out, fmt.Sprintf(
			"%d source files excluded by build constraints could not be read; requires imported only there may be reported as unused",
			excluded.unreadable))
	}
	return append(out, unloaded.limitations()...)
}

// excludedScan은 주 모듈 패키지에서 빌드 제약으로 빠진 Go 파일을 tidy처럼 읽은 결과다.
type excludedScan struct {
	imports []string // 읽은 파일들의 import 경로
	// unseenFiles는 이번 로드가 보지 못한 패키지(unseenImport)를 import하는 읽은 파일 수다 — 그
	// 패키지 자신의 import가 공백이다. import가 모두 std거나 로드된 패키지인 파일은 공백이 없어 세지
	// 않는다(대부분 저장소의 OS별 파일은 std만 쓴다). ignore 태그 파일은 tidy도 세지 않아 빠진다.
	unseenFiles int
	unreadable  int // 정규 파일이 아니거나 읽기·파싱에 실패해 import를 모르는 파일 수
}

// scanExcludedFiles는 주 모듈 패키지(테스트 변형 포함)의 IgnoredFiles 중 Go 파일의 import를
// 모은다. 변형은 원 패키지와 같은 디렉터리라 같은 파일을 다시 싣는다 — 경로로 한 번만 읽는다.
// unseen은 import 하나가 이번 로드가 보지 못한 패키지인지다(unseenImport).
func scanExcludedFiles(reachable []*packages.Package, unseen func(string) bool) excludedScan {
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
			scan.read(file, unseen)
		}
	}
	return scan
}

// read는 제외 파일 하나의 import를 모은다. tidy가 세지 않는 ignore 태그 파일은 건너뛴다.
func (s *excludedScan) read(path string, unseen func(string) bool) {
	f, err := parseImportHeader(path)
	if err != nil {
		s.unreadable++
		return
	}
	if !tidyBuildsFile(f) {
		return
	}
	blind := false
	for _, spec := range f.Imports {
		if imp, err := strconv.Unquote(spec.Path.Value); err == nil {
			s.imports = append(s.imports, imp)
			blind = blind || unseen(imp)
		}
	}
	if blind {
		s.unseenFiles++
	}
}

// loadedPackagePaths는 이번 로드에 든 패키지 경로 집합이다(테스트 변형은 원 패키지와 같은 경로).
// 로드에 실패한 패키지도 든다 — 그 공백은 로드 오류 limitation이 따로 센다.
func loadedPackagePaths(reachable []*packages.Package) map[string]bool {
	out := make(map[string]bool, len(reachable))
	for _, p := range reachable {
		out[p.PkgPath] = true
	}
	return out
}

// unseenImport는 제외 파일의 import가 판정 공백을 남기는지 본다 — 이번 로드에 없는 비 std 패키지는
// 자기 import(다른 require가 필요할 수 있다)를 보지 못했다. 로드된 패키지의 import는 이번 로드가 이미
// 따라갔고, std는 require에 기대지 않는다. std는 go 명령의 규칙대로 첫 경로 요소에 점이 없는 경로다.
// 다만 점 없는 주 모듈 경로(module myapp)와 require 경로(로컬 replace)는 std가 아니다.
func unseenImport(path string, loaded map[string]bool, modPath string, requires map[string]bool) bool {
	if loaded[path] {
		return false
	}
	if path == modPath || strings.HasPrefix(path, modPath+"/") {
		return true
	}
	if _, ok := owningRequire(path, requires); ok {
		return true
	}
	first, _, _ := strings.Cut(path, "/")
	return strings.Contains(first, ".")
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

// unloadedScan은 이번 실행의 패턴이 덮는 주 모듈 디렉터리 중 tidy가 읽을 Go 파일이 있는데 로드된 패키지가
// 없는 곳을 센 결과다. go list는 이 플랫폼·태그로 빌드할 파일이 하나도 없는 디렉터리(windows 전용 패키지
// 등)를 패키지로 내지 않아 그 import가 판정에 들어오지 않는다 — tidy는 그 import도 보므로 거기서만 쓰는
// require가 미사용으로 보인다. import를 읽어 require에 귀속하지는 않는다: go의 패키지 해석을 다시 하는
// 일이라 판정은 그대로 두고 공백만 센다.
type unloadedScan struct {
	dirs       []string // 그런 디렉터리의 모듈 상대 경로(/ 구분), 정렬
	unreadable int      // 목록을 읽지 못해 그 패턴의 순회를 멈춘 디렉터리 수
}

// limitations는 두 수를 limitation 문장으로 만든다. 알릴 것이 없으면 nil이다.
func (s unloadedScan) limitations() []string {
	var out []string
	if len(s.dirs) > 0 {
		out = append(out, fmt.Sprintf(
			"%d directories have Go files but none for this platform or build tags, so go list loaded no package from them; requires imported only there may be reported as unused (load them with --goos, --goarch, or --tags)",
			len(s.dirs)))
	}
	if s.unreadable > 0 {
		out = append(out, fmt.Sprintf(
			"%d directories could not be read, so the directories under and after them were not checked for Go files of other platforms; requires imported only there may be reported as unused",
			s.unreadable))
	}
	return out
}

// scanUnloadedDirs는 패턴이 덮는 주 모듈 트리를 go list의 순회 규칙으로 걸어 unloadedScan을 만든다.
// modPath·modDir는 go list가 보고한 주 모듈이고, ignores는 그 go.mod의 ignore 지시문이다.
func scanUnloadedDirs(opts Options, modPath, modDir string, ignores []string, loaded *loadedDirs) (unloadedScan, error) {
	base, err := filepath.Abs(opts.Dir)
	if err != nil {
		return unloadedScan{}, fmt.Errorf("resolving --dir %s: %w", opts.Dir, err)
	}
	w := &dirWalk{modPath: modPath, modDir: filepath.Clean(modDir), ignore: newIgnoreRules(ignores),
		loaded: loaded, visited: map[string]bool{}, files: map[string][]dirFile{}}
	for _, root := range w.scanRoots(opts.Patterns, base) {
		if belongsToModule(w.modDir, root) {
			w.walk(root)
		}
	}
	w.countGaps()
	return w.scan, nil
}

// dirWalk는 scanUnloadedDirs 한 번의 상태다. 경로는 모두 go list가 보고한 modDir의 경로 공간이다 — go list는
// 같은 디렉터리를 심링크를 푼 경로로 보고하기도 한다(상대 --dir, macOS의 /var). realModDir는 그 공간으로
// 옮길 때 쓰는 modDir의 실제 경로이고 처음 필요할 때 구한다.
type dirWalk struct {
	modPath, modDir, realModDir string
	ignore                      ignoreRules
	loaded                      *loadedDirs
	visited                     map[string]bool      // 걸은 디렉터리 — 겹치는 패턴이 같은 트리를 다시 걷지 않게
	files                       map[string][]dirFile // 로드된 패키지가 없는 디렉터리의 Go 파일 후보
	scan                        unloadedScan
}

// dirFile은 순회가 만난 Go 파일 후보 하나다. 종류 확인(정규 파일인가)은 셀 때 한다.
type dirFile struct {
	path  string
	entry fs.DirEntry
}

// scanRoots는 패턴마다 걸을 뿌리를 모은다(빈 패턴 목록은 load와 같이 ./...). 뿌리까지 가는 경로를 읽지
// 못한 패턴은 unreadable로 센다 — 그 트리는 보지 못했다.
func (w *dirWalk) scanRoots(patterns []string, base string) []string {
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	var roots []string
	for _, p := range patterns {
		root, err := w.scanRoot(p, base)
		if err != nil {
			w.scan.unreadable++
		} else if root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

// scanRoot는 패턴 하나가 덮는 주 모듈 트리의 뿌리다. `/...`로 끝나는 디렉터리 패턴(./..., ./x/..., ../x/...,
// 절대 경로)은 --dir 기준 그 디렉터리, 주 모듈을 덮는 import 경로 패턴(모듈 경로/..., 모듈 경로/x/..., 그 위
// 경로/..., ...)과 all·work는 모듈의 그 디렉터리다. 나머지는 ""(걷지 않음)다 — 와일드카드 없는 패턴은 go list가
// 파일이 없어도 오류 패키지로 실어 로드 오류 limitation이 세고, std·cmd·tool은 주 모듈 밖이며, 마지막이 아닌
// 자리의 ...는 go 명령의 패턴 해석을 다시 해야 한다.
func (w *dirWalk) scanRoot(pattern, base string) (string, error) {
	if pattern == "all" || pattern == "work" {
		return w.modDir, nil
	}
	slash := filepath.ToSlash(pattern)
	prefix, ok := strings.CutSuffix(slash, "/...")
	if slash == "..." {
		prefix, ok = "", true
	}
	if !ok || strings.Contains(prefix, "...") {
		return "", nil
	}
	if strings.HasPrefix(slash, "./") || strings.HasPrefix(slash, "../") || filepath.IsAbs(pattern) {
		return w.localRoot(prefix, base)
	}
	return w.importRoot(prefix)
}

// localRoot는 디렉터리 패턴의 뿌리다. go 명령(cmd/go MatchDirs)처럼 걷기 시작하는 디렉터리 자신의 이름이
// .·_로 시작하거나 testdata면 아무것도 맞지 않는다("."·".."는 예외). 경로는 modDir 공간으로 옮긴다.
func (w *dirWalk) localRoot(prefix, base string) (string, error) {
	clean := path.Clean(prefix)
	if elem := path.Base(clean); elem != "." && elem != ".." && skippedDirName(elem) {
		return "", nil
	}
	dir := filepath.FromSlash(clean)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(base, dir)
	}
	return w.moduleSubdir(dir)
}

// moduleSubdir는 dir을 modDir 공간의 같은 디렉터리로 옮긴다. 둘 다 심링크를 풀어 상대 경로를 구한다. 디렉터리가
// 아니면 ""다(isDir). modDir 밖(상위 디렉터리·다른 모듈·다른 볼륨이라 상대 경로가 없는 곳)도 ""다 — 이 go.mod의
// 판정 대상이 아니다.
func (w *dirWalk) moduleSubdir(dir string) (string, error) {
	if ok, err := isDir(dir); !ok || err != nil {
		return "", err
	}
	if w.realModDir == "" {
		resolved, err := filepath.EvalSymlinks(w.modDir)
		if err != nil {
			return "", err
		}
		w.realModDir = resolved
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(w.realModDir, realDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil
	}
	return filepath.Join(w.modDir, rel), nil
}

// importRoot는 import 경로 패턴의 뿌리다. 패턴이 모듈 전체를 덮으면(빈 접두, 모듈 경로, 그 위 경로) 모듈 루트,
// 모듈 안 경로면 그 하위 디렉터리다. go list·tidy의 모듈 순회(cmd/go matchPackages)는 모듈 루트 밑 모든
// 세그먼트에 .·_·testdata 규칙을 걸어, 하위 경로의 세그먼트 하나라도 걸리면 아무것도 맞지 않는다.
func (w *dirWalk) importRoot(prefix string) (string, error) {
	if prefix == "" || prefix == w.modPath || strings.HasPrefix(w.modPath, prefix+"/") {
		return w.modDir, nil
	}
	rest, ok := strings.CutPrefix(prefix, w.modPath+"/")
	if !ok {
		return "", nil
	}
	for _, seg := range strings.Split(rest, "/") {
		if seg == "" || skippedDirName(seg) {
			return "", nil
		}
	}
	root := filepath.Join(w.modDir, filepath.FromSlash(rest))
	if ok, err := isDir(root); !ok || err != nil {
		return "", err
	}
	return root, nil
}

// isDir는 패턴의 뿌리 자리가 (심링크를 따라) 디렉터리인지 본다. 없거나 디렉터리가 아니면 false다 — 걸을 트리가
// 없고 go list도 거기서 패키지를 찾지 못했다(파일을 가리킨 패턴은 go list의 패턴 오류로 로드 오류 limitation이
// 센다). 다른 stat 오류는 돌려준다 — 그 트리를 읽지 못했다.
func isDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// walk는 root 트리를 걸어 로드된 패키지가 없는 디렉터리의 Go 파일을 모은다. 뿌리가 디렉터리 심링크여도
// 따라가도록 끝에 구분자를 붙인다 — go 명령과 같다(go.dev/issue/50807). 목록을 읽지 못한 디렉터리에서는 이
// 패턴의 순회를 멈추고 unreadable로 센다 — go list도 같은 자리에서 순회를 멈추고 패턴 오류를 낸다(로드 오류
// limitation이 센다). 그 뒤 디렉터리는 go list가 보지 않았으니 이어 걸어 세면 거짓 이유가 붙는다.
func (w *dirWalk) walk(root string) {
	start := root
	if !strings.HasSuffix(start, string(filepath.Separator)) {
		start += string(filepath.Separator)
	}
	if err := filepath.WalkDir(start, w.visit(root, start)); err != nil {
		w.scan.unreadable++
	}
}

// visit은 walk의 WalkDir 콜백이다. 읽기 오류는 그대로 돌려 순회를 멈춘다.
func (w *dirWalk) visit(root, start string) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == start {
			path = root
		}
		if !d.IsDir() {
			w.noteFile(path, d)
			return nil
		}
		if w.visited[path] || w.skip(root, path) {
			return filepath.SkipDir
		}
		w.visited[path] = true
		return nil
	}
}

// skip은 go list의 ./... 순회가 들어가지 않거나 패턴에 맞추지 않는 디렉터리인지 본다: root 밑의 .·_로 시작하는
// 디렉터리와 testdata, vendor 디렉터리의 하위(vendor 자신은 패키지일 수 있다), go.mod ignore 지시문이 가리키는
// 디렉터리, go.mod가 있는 하위 모듈. 디렉터리 심링크는 filepath.WalkDir가 따라가지 않는다 — go도 같다.
func (w *dirWalk) skip(root, dir string) bool {
	rel := moduleRel(w.modDir, dir)
	if dir != root && skippedDirName(filepath.Base(dir)) {
		return true
	}
	if underVendor(rel) || w.ignore.matches(rel) {
		return true
	}
	return dir != root && hasGoModFile(dir)
}

// noteFile은 Go 파일 후보를 디렉터리별로 모은다. 이름이 _·.로 시작하는 파일은 go 명령이 Go 파일로 보지
// 않는다. 이번 로드가 import를 읽은 디렉터리(경로가 같은 것)는 모으지 않는다.
func (w *dirWalk) noteFile(path string, d fs.DirEntry) {
	name := d.Name()
	if !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
		return
	}
	if dir := filepath.Dir(path); !w.loaded.paths[dir] {
		w.files[dir] = append(w.files[dir], dirFile{path: path, entry: d})
	}
}

// countGaps는 모은 디렉터리 중 tidy가 import를 읽는데 이번 로드가 패키지를 싣지 않은 곳을 센다.
func (w *dirWalk) countGaps() {
	for dir, files := range w.files {
		if tidyReadsDir(files) && !w.loaded.contains(dir) {
			w.scan.dirs = append(w.scan.dirs, moduleRel(w.modDir, dir))
		}
	}
	sort.Strings(w.scan.dirs)
}

// tidyReadsDir는 go mod tidy가 이 디렉터리의 import를 읽는지 본다 — tidy(cmd/go imports.ScanDir)는 심링크를
// 푼 정규 파일만 읽고 ignore 외의 태그를 참으로 둔다(tidyBuildsFile). 읽지 못한 파일이 있으면 tidy도 그
// 디렉터리를 오류 난 패키지로 다루고 우리는 import를 모르므로 센다 — 공백을 숨기지 않는다. 머리는 FIFO·장치를
// 열지 않는 parseImportHeader로 읽는다.
func tidyReadsDir(files []dirFile) bool {
	for _, f := range files {
		if !isRegularFile(f) {
			continue
		}
		header, err := parseImportHeader(f.path)
		if err != nil || tidyBuildsFile(header) {
			return true
		}
	}
	return false
}

// isRegularFile은 디렉터리 항목이(심링크면 그 대상이) 정규 파일인지 본다. 대상을 stat할 수 없는 심링크(깨진
// 링크)는 tidy처럼 Go 파일이 아닌 것으로 건너뛴다.
func isRegularFile(f dirFile) bool {
	if f.entry.Type().IsRegular() {
		return true
	}
	if f.entry.Type()&fs.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(f.path)
	return err == nil && info.Mode().IsRegular()
}

// loadedDirs는 이번 로드가 import를 읽은 패키지 디렉터리다(로드 오류 패키지 포함 — 그 공백은 로드 오류
// limitation이 센다). 같은 디렉터리를 다른 경로(심링크 경로)로 걸었을 수 있어, 경로가 다르면 파일 동일성으로
// 한 번 더 본다. 그 stat은 처음 필요할 때 한 번만 한다 — 그런 디렉터리가 없는 저장소는 stat하지 않는다.
type loadedDirs struct {
	paths   map[string]bool
	infos   []os.FileInfo
	statted bool
}

// newLoadedDirs는 도달한 패키지(테스트 변형 포함)의 디렉터리로 loadedDirs를 만든다.
func newLoadedDirs(reachable []*packages.Package) *loadedDirs {
	l := &loadedDirs{paths: map[string]bool{}}
	for _, p := range reachable {
		if p.Dir != "" {
			l.paths[p.Dir] = true
		}
	}
	return l
}

// contains는 dir이 로드된 패키지 디렉터리인지 본다. stat할 수 없는 경로는 같은 디렉터리임을 보일 수 없으니
// 아니라고 본다 — 지금 dir을 stat하지 못하면 센다(공백을 숨기지 않는다).
func (l *loadedDirs) contains(dir string) bool {
	if l.paths[dir] {
		return true
	}
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	if !l.statted {
		l.statted = true
		for d := range l.paths {
			if di, err := os.Stat(d); err == nil {
				l.infos = append(l.infos, di)
			}
		}
	}
	for _, di := range l.infos {
		if os.SameFile(info, di) {
			return true
		}
	}
	return false
}

// ignoreRules는 go.mod ignore 지시문을 go 명령(cmd/go search.IgnorePatterns)과 같게 해석한다 — "./x"는 모듈
// 루트의 x 아래만, "x"는 어디에 있든 x라는 이름의 디렉터리 아래를 뺀다. 와일드카드는 없다.
type ignoreRules struct {
	rooted   []string // ignoreKey 꼴("/x/")
	anywhere []string
}

// newIgnoreRules는 지시문 경로들을 두 갈래로 나눠 정규화한다.
func newIgnoreRules(paths []string) ignoreRules {
	var r ignoreRules
	for _, p := range paths {
		if rest, ok := strings.CutPrefix(p, "./"); ok {
			r.rooted = append(r.rooted, ignoreKey(rest))
		} else {
			r.anywhere = append(r.anywhere, ignoreKey(p))
		}
	}
	return r
}

// matches는 모듈 상대 디렉터리 rel(모듈 루트는 ".")이 지시문에 걸리는지 본다.
func (r ignoreRules) matches(rel string) bool {
	key := ignoreKey(rel)
	for _, p := range r.rooted {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	for _, p := range r.anywhere {
		if strings.Contains(key, p) {
			return true
		}
	}
	return false
}

// ignoreKey는 경로 앞뒤에 /를 붙인다(cmd/go normalizePath) — 세그먼트 경계에서만 맞추려는 것이다.
func ignoreKey(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// skippedDirName은 go 명령의 패키지 순회가 들어가지 않는 디렉터리 이름(.·_로 시작, testdata)인지 본다.
func skippedDirName(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata"
}

// underVendor는 모듈 상대 경로의 마지막이 아닌 세그먼트에 vendor가 있는지 본다 — ./...의 와일드카드는 vendor
// 세그먼트를 넘지 않아(cmd/go pkgpattern) vendor 디렉터리 자신은 맞고 그 밑은 맞지 않는다. tidy의 주 모듈
// 순회도 vendor 밑으로 내려가지 않는다.
func underVendor(rel string) bool {
	segs := strings.Split(rel, "/")
	for _, s := range segs[:len(segs)-1] {
		if s == "vendor" {
			return true
		}
	}
	return false
}

// moduleRel은 modDir 밑 디렉터리의 모듈 상대 경로(/ 구분, 모듈 루트는 ".")다. 순회 경로는 modDir에 이어 붙여
// 만든 것이라 앞부분을 어휘적으로 잘라 낸다.
func moduleRel(modDir, dir string) string {
	rel := strings.TrimPrefix(strings.TrimPrefix(dir, modDir), string(filepath.Separator))
	if rel == "" {
		return "."
	}
	return filepath.ToSlash(rel)
}

// hasGoModFile은 dir에 go.mod 파일이 있는지 본다. go 명령처럼 stat이 성공하고 디렉터리가 아니어야 모듈 경계다
// — stat 오류(없음·권한)는 경계가 아닌 것으로 본다(go 명령도 같다).
func hasGoModFile(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil && !info.IsDir()
}

// belongsToModule은 modDir 밑 디렉터리 root가 modDir 모듈의 것인지 본다 — root부터 modDir 바로 아래까지 go.mod가
// 있으면 하위 모듈의 디렉터리라 이 go.mod의 판정 대상이 아니다.
func belongsToModule(modDir, root string) bool {
	for d := root; d != modDir; d = filepath.Dir(d) {
		if hasGoModFile(d) || filepath.Dir(d) == d {
			return false
		}
	}
	return true
}
