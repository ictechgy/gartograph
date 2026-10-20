package source

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// fixture는 a→b import와 b의 stdlib import를 가진 모듈이다.
// stdlib은 모듈이 없어 생략되고 limitation으로 남아야 한다.
func fixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

import _ "example.com/fixture/b"
`,
		"b/b.go": `package b

import "fmt"

var _ = fmt.Sprint
`,
	})
}

// TestLoadPackageGraph는 패키지 그래프 수확의 기본 계약을 확인한다:
// 모듈 내부 정점, import 간선, 외부 생략 limitation.
func TestLoadPackageGraph(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Level != graph.LevelPackage {
		t.Fatalf("expected package level, got %s", doc.Level)
	}
	if !doc.HasVertex("example.com/fixture/a") || !doc.HasVertex("example.com/fixture/b") {
		t.Fatalf("missing package vertices: %+v", doc.Vertices)
	}
	kinds := graph.EdgeKinds(doc, "example.com/fixture/a", "example.com/fixture/b")
	if len(kinds) != 1 || kinds[0] != graph.EdgeImport {
		t.Fatalf("missing import edge a→b: %+v", doc.Edges)
	}
	// fmt는 모듈 밖이라 정점이 되지 않고 limitation으로 남는다.
	if doc.HasVertex("fmt") {
		t.Fatal("stdlib package leaked as vertex")
	}
	if len(doc.Limitations) == 0 ||
		!strings.Contains(doc.Limitations[0], "outside the module") {
		t.Fatalf("expected external-import limitation: %v", doc.Limitations)
	}
}

// TestImportEdgePositions는 import 간선이 선언 위치를 싣는지 확인한다.
// v2 스키마에서 "의존이 어디서 일어나는가"는 간선이 직접 담는 사실이다.
func TestImportEdgePositions(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	var e *graph.Edge
	for i := range doc.Edges {
		if doc.Edges[i].Kind == graph.EdgeImport &&
			doc.Edges[i].From == "example.com/fixture/a" {
			e = &doc.Edges[i]
		}
	}
	if e == nil || len(e.Positions) != 1 {
		t.Fatalf("import edge must carry its decl site: %+v", doc.Edges)
	}
	pos := e.Positions[0]
	if !strings.HasSuffix(pos.File, "a/a.go") || pos.Line != 3 {
		t.Fatalf("position must point at the import spec in a/a.go: %+v", pos)
	}
}

// TestTestVariantMerge는 --tests 수확에서 _test.go의 import가 원 패키지의
// 간선으로 합쳐지는지 확인한다 — 변형을 통째로 버리면 테스트만의 내부
// 의존이 그래프에서 사라진다.
func TestTestVariantMerge(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"lib/lib.go": `package lib

func Add() {}
`,
		"lib/lib_test.go": `package lib

import (
	"example.com/fixture/util"
	"testing"
)

func TestAdd(t *testing.T) { util.Shout(); Add() }
`,
		"util/util.go": `package util

func Shout() {}
`,
	})
	doc, err := LoadPackageGraph(Options{Dir: dir, Tests: true})
	if err != nil {
		t.Fatal(err)
	}
	// 변형 정점은 생기지 않는다 — 정점은 PkgPath 하나다.
	if doc.HasVertex("example.com/fixture/lib [example.com/fixture/lib.test]") {
		t.Fatal("test variant leaked as a vertex")
	}
	kinds := graph.EdgeKinds(doc, "example.com/fixture/lib", "example.com/fixture/util")
	if len(kinds) != 1 || kinds[0] != graph.EdgeImport {
		t.Fatalf("_test.go import must merge into the base package: %+v", doc.Edges)
	}
	// 사용 지점은 _test.go 파일을 가리켜야 한다 — 어느 파일이 이
	// 의존을 만드는지가 파일 스코프 판단의 재료다.
	var merged bool
	for _, e := range doc.Edges {
		if e.From == "example.com/fixture/lib" && e.To == "example.com/fixture/util" {
			for _, p := range e.Positions {
				if strings.HasSuffix(p.File, "lib_test.go") {
					merged = true
				}
			}
		}
	}
	if !merged {
		t.Fatal("merged edge must carry the _test.go site")
	}
	var noted bool
	for _, l := range doc.Limitations {
		if strings.Contains(l, "test-variant") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("variant merge must be noted: %v", doc.Limitations)
	}
}

// testsCountFixture는 --tests 수치 비교용 모듈이다. lib은 reflect를 import하고 //go:linkname과
// 모듈 밖 참조를 가지며, 빌드 제약으로 빠진 파일 하나와 _test.go 하나를 둔다. extra가 있으면
// lib에 그 내용의 extra.go를 더한다.
func testsCountFixture(t *testing.T, extra string) string {
	t.Helper()
	files := map[string]string{
		"lib/lib.go": `package lib

import (
	"fmt"
	"reflect"
	_ "unsafe"
)

//go:linkname runtimeNano runtime.nanotime
func runtimeNano() int64

// Describe는 값을 찍고 타입 이름을 돌려준다.
func Describe(v any) string {
	fmt.Println(v)
	return reflect.TypeOf(v).String()
}
`,
		"lib/lib_other.go": "//go:build never\n\npackage lib\n\nfunc Other() {}\n",
		"lib/lib_test.go":  "package lib\n\nimport \"testing\"\n\nfunc TestDescribe(t *testing.T) { t.Log(Describe(1)) }\n",
	}
	if extra != "" {
		files["lib/extra.go"] = extra
	}
	return testutil.WriteModule(t, files)
}

// extraLibFile은 testsCountFixture의 lib에 더하는 파일이다 — 모듈 밖 import 하나(strings), 모듈
// 밖 참조 둘, //go:linkname 하나를 더한다.
const extraLibFile = `package lib

import (
	"strings"
	_ "unsafe"
)

//go:linkname runtimeFastrand runtime.fastrand
func runtimeFastrand() uint32

// Shout은 앞뒤 공백을 지우고 대문자로 바꾼다.
func Shout(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
`

// limitationCount는 phrase를 담은 limitation의 앞 숫자다. 없으면 0이다 — 알릴 것이 없으면
// 문장도 없다.
func limitationCount(t *testing.T, doc *graph.Document, phrase string) int {
	t.Helper()
	for _, l := range doc.Limitations {
		if !strings.Contains(l, phrase) {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(l, "%d ", &n); err != nil {
			t.Fatalf("limitation %q has no leading count: %v", l, err)
		}
		return n
	}
	return 0
}

// TestTestsCountsEachFileOnce는 --tests 수확의 limitation 수치가 테스트 변형("p [p.test]")이 다시
// 담은 원 패키지 파일을 두 번 세지 않는지 확인한다 — 정점은 PkgPath로 합쳐지는데 수치만 변형마다
// 더하면 실제의 두세 배가 된다. 합성 test main의 기여는 go 버전마다 달라 값으로 박지 않고, 원
// 패키지에 파일 하나를 더했을 때의 증가분이 --tests 유무와 같은지로 본다.
func TestTestsCountsEachFileOnce(t *testing.T) {
	harvest := func(dir string, tests bool) *graph.Document {
		doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Tests: tests})
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	base, more := testsCountFixture(t, ""), testsCountFixture(t, extraLibFile)
	plainBase, plainMore := harvest(base, false), harvest(more, false)
	testsBase, testsMore := harvest(base, true), harvest(more, true)
	for _, phrase := range []string{
		"imports of packages outside the module were omitted",
		"references to symbols outside the module were omitted",
		"//go:linkname directives found",
	} {
		plain := limitationCount(t, plainMore, phrase) - limitationCount(t, plainBase, phrase)
		tests := limitationCount(t, testsMore, phrase) - limitationCount(t, testsBase, phrase)
		if plain == 0 || tests != plain {
			t.Errorf("%q: one base file adds %d without --tests but %d with it", phrase, plain, tests)
		}
	}
	// 제외 파일과 reflect를 쓰는 패키지는 테스트가 늘리지 않는다 — 합성 test main이 import하는
	// reflect는 go 도구의 코드라 세지 않는다.
	for _, phrase := range []string{
		"source files were excluded by build constraints",
		"packages import reflect",
	} {
		if plain, tests := limitationCount(t, plainBase, phrase), limitationCount(t, testsBase, phrase); plain != 1 || tests != 1 {
			t.Errorf("%q: %d without --tests and %d with it, want 1 and 1", phrase, plain, tests)
		}
	}
}

// TestTestsCountsLoadErrorsOnce는 원 패키지의 타입 오류를 테스트 변형이 다시 싣더라도 한 번만
// 세는지 확인한다 — 같은 패키지의 같은 오류다.
func TestTestsCountsLoadErrorsOnce(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"lib/lib.go":      "package lib\n\nfunc broken() int { return undefinedName }\n",
		"lib/lib_test.go": "package lib\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { _ = broken() }\n",
	})
	for _, tests := range []bool{false, true} {
		doc, err := Load(Options{Dir: dir, Level: graph.LevelType, Tests: tests})
		if err != nil {
			t.Fatal(err)
		}
		if n := limitationCount(t, doc, "packages reported load errors"); n != 1 {
			t.Errorf("tests=%v: %d load errors counted, want 1: %v", tests, n, doc.Limitations)
		}
	}
}

// TestLoadErrorsCountPackages는 로드 오류 limitation이 문구("N packages reported load errors")대로
// 패키지 수를 세는지 확인한다 — 타입 오류가 둘인 패키지는 하나로 센다. 오류 문장을 세면 같은 문구의
// unused-deps(PkgPath 수)와 다른 것을 센다.
func TestLoadErrorsCountPackages(t *testing.T) {
	twoErrors := "package a\n\nvar x int = \"x\"\nvar y string = 1\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  int
	}{
		{"one package with two errors",
			map[string]string{"a/a.go": twoErrors, "c/c.go": "package c\n\nfunc C() {}\n"}, 1},
		{"two broken packages",
			map[string]string{"a/a.go": twoErrors, "b/b.go": "package b\n\nvar z int = \"z\"\n"}, 2},
	} {
		dir := testutil.WriteModule(t, tc.files)
		for _, level := range []graph.Level{graph.LevelType, graph.LevelSymbol} {
			for _, tests := range []bool{false, true} {
				doc, err := Load(Options{Dir: dir, Level: level, Tests: tests})
				if err != nil {
					t.Fatal(err)
				}
				if n := limitationCount(t, doc, "packages reported load errors"); n != tc.want {
					t.Errorf("%s level=%s tests=%v: %d packages counted, want %d: %v",
						tc.name, level, tests, n, tc.want, doc.Limitations)
				}
			}
		}
	}
}

// TestLoadPackageGraphDeps는 --deps가 외부 패키지를 정점으로 담는지 확인한다.
func TestLoadPackageGraphDeps(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t), IncludeDeps: true})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.HasVertex("fmt") {
		t.Fatal("expected stdlib vertex with --deps")
	}
	kinds := graph.EdgeKinds(doc, "example.com/fixture/b", "fmt")
	if len(kinds) != 1 {
		t.Fatalf("missing edge b→fmt: %+v", doc.Edges)
	}
}

// TestPlatformSelection은 GOOS=linux 수확이 linux 전용 파일을 싣고
// 다른 타깃 파일을 싣지 않는지 확인한다 — 조건부 파일이 다른 그래프를
// 만드는 게 이 옵션의 존재 이유다.
func TestPlatformSelection(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"lib/lib_linux.go": `package lib

func LinuxOnly() {}
`,
		"lib/lib_windows.go": `package lib

func WindowsOnly() {}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol", GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.HasVertex("example.com/fixture/lib.LinuxOnly") {
		t.Fatalf("linux file must be harvested: %+v", doc.Vertices)
	}
	if doc.HasVertex("example.com/fixture/lib.WindowsOnly") {
		t.Fatal("windows file must not be harvested under GOOS=linux")
	}
	var noted bool
	for _, l := range doc.Limitations {
		if strings.Contains(l, "GOOS=linux") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("platform selection must be a limitation: %v", doc.Limitations)
	}
}

// TestPlatformDefault는 플랫폼 옵션이 없으면 limitation을 싣지 않는지
// 확인한다 — 기본 호스트 수확에 노이즈를 얹으면 안 된다.
func TestPlatformDefault(t *testing.T) {
	doc, err := LoadPackageGraph(Options{Dir: fixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range doc.Limitations {
		if strings.Contains(l, "GOOS") {
			t.Fatalf("default harvest must not carry a platform note: %v", l)
		}
	}
}

// tooNewModule은 설치된 Go보다 새 go·toolchain 줄을 가진 모듈이다. GOTOOLCHAIN=auto면 go 명령이 go1.99.0
// 툴체인을 내려받아 실행하려 한다(GOPROXY=off라 받지 못하고 실패한다).
func tooNewModule(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"go.mod":  "module example.com/fixture\n\ngo 1.99.0\n\ntoolchain go1.99.0\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})
}

// setToolchainEnv는 GOTOOLCHAIN 환경 변수를 value로 두고, unset이면 아예 지운다. 툴체인·모듈 다운로드가
// 네트워크에 닿지 않게 GOPROXY=off도 둔다. CI(setup-go)가 GOTOOLCHAIN을 정해 둘 수 있어 테스트마다 명시적으로
// 정한다 — t.Setenv가 테스트 끝에 원래 값을 되돌린다.
func setToolchainEnv(t *testing.T, value string, unset bool) {
	t.Helper()
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", value)
	if unset {
		if err := os.Unsetenv("GOTOOLCHAIN"); err != nil {
			t.Fatal(err)
		}
	}
}

// TestHarvestKeepsTheInstalledToolchain은 GOTOOLCHAIN 환경 변수가 없거나 비었을 때, 설치된 Go보다 새 Go를
// 요구하는 모듈을 읽는 go list 세 호출(Load·LoadSession·patternRoots)이 툴체인을 내려받지 않고 원인과 해결 방향을
// 담은 오류를 내는지 본다. 분석 대상 go.mod가 호스트에 다른 Go 툴체인을 받아 실행하게 하면 안 된다
// (DECISIONS gartograph GOTOOLCHAIN).
func TestHarvestKeepsTheInstalledToolchain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		unset bool
	}{{"unset", true}, {"empty", false}} {
		t.Run(tc.name, func(t *testing.T) {
			setToolchainEnv(t, "", tc.unset)
			dir := tooNewModule(t)
			_, loadErr := Load(Options{Dir: dir})
			_, _, sessionErr := LoadSession(Options{Dir: dir})
			_, patternErr := patternRoots(nil, dir, Options{Patterns: []string{"./..."}})
			for _, got := range []struct {
				call string
				err  error
			}{{"load", loadErr}, {"LoadSession", sessionErr}, {"patternRoots", patternErr}} {
				if got.err == nil {
					t.Fatalf("%s: a module that requires go 1.99.0 must not load", got.call)
				}
				msg := got.err.Error()
				if strings.Contains(msg, "downloading go1.99.0") {
					t.Errorf("%s: the go command tried to download the toolchain go.mod asks for:\n%s", got.call, msg)
				}
				for _, want := range []string{"requires go >= 1.99.0", "GOTOOLCHAIN=local", "install that Go release",
					"rerun with GOTOOLCHAIN=auto"} {
					if !strings.Contains(msg, want) {
						t.Errorf("%s: the error must name the cause and the way out (%q):\n%s", got.call, want, msg)
					}
				}
			}
		})
	}
}

// TestHarvestHonorsGOTOOLCHAIN은 사용자가 환경 변수로 준 GOTOOLCHAIN을 덮지 않는지 본다. auto면 go 명령이
// 예전처럼 go.mod가 요구하는 툴체인으로 바꾸려 하고(GOPROXY=off라 받지 못한다), 그 실패에는 기본값 안내 대신
// 예전 안내가 그대로 붙는다 — 막은 것이 gartograph의 기본값이 아니기 때문이다.
func TestHarvestHonorsGOTOOLCHAIN(t *testing.T) {
	setToolchainEnv(t, "auto", false)
	_, err := Load(Options{Dir: tooNewModule(t)})
	if err == nil || !strings.Contains(err.Error(), "downloading go1.99.0") {
		t.Fatalf("GOTOOLCHAIN=auto from the environment must reach the go command, got %v", err)
	}
	if strings.Contains(err.Error(), "rerun with GOTOOLCHAIN=auto") ||
		!strings.HasSuffix(err.Error(), " — check go.mod and build tags") {
		t.Fatalf("a failed switch keeps the usual advice, not the default-toolchain one:\n%v", err)
	}
}

// TestHarvestIgnoresNewerToolchainLine은 go 줄은 설치된 Go 이하이고 toolchain 줄만 더 새 모듈을 설치된 Go로
// 수확하는지 본다(README Security 절). toolchain 줄은 권고라 GOTOOLCHAIN=local이면 go 명령이 따르지 않는다 —
// auto였다면 go1.99.0을 내려받으려다 실패했다.
func TestHarvestIgnoresNewerToolchainLine(t *testing.T) {
	setToolchainEnv(t, "", true)
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod":  "module example.com/fixture\n\ngo 1.27\n\ntoolchain go1.99.0\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})
	doc, err := Load(Options{Dir: dir})
	if err != nil {
		t.Fatalf("a newer toolchain line alone must not stop the harvest: %v", err)
	}
	if !doc.HasVertex("example.com/fixture") {
		t.Fatalf("the installed Go must load the module: %+v", doc.Vertices)
	}
}

// fakeGoLog는 PATH 맨 앞에 진짜 go로 넘겨주는 가짜 go를 두고, 호출마다 그 프로세스가 받은 GOTOOLCHAIN·GOOS·
// GOARCH 값을 한 줄(공백 구분, 없으면 unset)로 남길 로그 경로를 돌려준다. go/packages가 띄우는 go 명령의
// 환경을 수확 결과가 아니라 실제 프로세스 환경으로 보기 위한 장치다.
func fakeGoLog(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake go is a POSIX shell script")
	}
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command is not on PATH: %v", err)
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls.log")
	script := "#!/bin/sh\n" +
		"printf '%s %s %s\\n' \"${GOTOOLCHAIN-unset}\" \"${GOOS-unset}\" \"${GOARCH-unset}\" >> \"$GARTOGRAPH_TEST_GO_LOG\"\n" +
		"exec \"$GARTOGRAPH_TEST_REAL_GO\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GARTOGRAPH_TEST_GO_LOG", log)
	t.Setenv("GARTOGRAPH_TEST_REAL_GO", realGo)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// loggedGoCalls는 로그를 비우고 run을 실행한 뒤, 그동안 띄운 go 명령마다 fakeGoLog가 남긴 값
// (GOTOOLCHAIN, GOOS, GOARCH)을 돌려준다. go를 한 번도 띄우지 않았으면 실패다 — 빈 로그는 아무것도 검증하지 않는다.
func loggedGoCalls(t *testing.T, log, name string, run func() error) [][]string {
	t.Helper()
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if fields := strings.Split(line, " "); len(fields) == 3 {
			calls = append(calls, fields)
		}
	}
	if len(calls) == 0 {
		t.Fatalf("%s started no go command through PATH (log %q)", name, data)
	}
	return calls
}

// goListCalls는 go list를 띄우는 세 호출이다. LoadSession은 dead --algo rta의 적재(symbol 수확과 같은 load)이고,
// patternRoots는 routes --pattern의 패턴 해석이다.
func goListCalls(dir string) []struct {
	name string
	run  func(Options) error
} {
	return []struct {
		name string
		run  func(Options) error
	}{
		{"load", func(o Options) error { _, err := load(o); return err }},
		{"LoadSession", func(o Options) error { _, _, err := LoadSession(o); return err }},
		{"patternRoots", func(o Options) error {
			o.Patterns = []string{"./..."}
			_, err := patternRoots(nil, dir, o)
			return err
		}},
	}
}

// TestGoCallsCarryTheToolchainPolicy는 세 go list 호출이 띄운 go 명령마다 GOTOOLCHAIN=local(환경 변수가 없거나
// 빈 값일 때) 또는 사용자가 환경 변수로 준 값을 받는지 본다. routes --pattern의 패턴 해석도 수확과 같은 규칙이어야
// 분석 대상 go.mod가 툴체인 전환을 일으킬 구멍이 남지 않는다.
func TestGoCallsCarryTheToolchainPolicy(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	for _, tc := range []struct {
		name, value string
		unset       bool
		want        string
	}{
		{"unset", "", true, "local"},
		{"empty", "", false, "local"},
		{"environment", "auto", false, "auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := fakeGoLog(t)
			setToolchainEnv(t, tc.value, tc.unset)
			for _, call := range goListCalls(dir) {
				run := func() error { return call.run(Options{Dir: dir}) }
				for _, got := range loggedGoCalls(t, log, call.name, run) {
					if got[0] != tc.want {
						t.Errorf("%s: a go command ran with GOTOOLCHAIN=%s, want %s", call.name, got[0], tc.want)
					}
				}
			}
		})
	}
}

// TestGoCallsCarryThePlatform은 --goos·--goarch 덮어쓰기가 load와 LoadSession이 띄우는 go 명령마다 그대로 가는지
// 본다 — 환경을 GOTOOLCHAIN 규칙과 한 함수로 합친 뒤에도 graph와 dead --algo rta가 같은 플랫폼을 수확해야 한다.
func TestGoCallsCarryThePlatform(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	log := fakeGoLog(t)
	setToolchainEnv(t, "", true)
	opts := Options{Dir: dir, GOOS: "linux", GOARCH: "arm64"}
	// load·LoadSession만 본다 — routes에는 --goos·--goarch가 없어 patternRoots의 플랫폼은 계약이 아니다.
	for _, call := range goListCalls(dir)[:2] {
		run := func() error { return call.run(opts) }
		for _, got := range loggedGoCalls(t, log, call.name, run) {
			if got[1] != "linux" || got[2] != "arm64" {
				t.Errorf("%s: a go command ran with GOOS=%s GOARCH=%s, want linux arm64", call.name, got[1], got[2])
			}
		}
	}
}

// TestExclude는 exclude 패턴에 맞는 패키지가 정점도 도달 집합도 아니며,
// 그쪽으로의 import가 limitation으로 남는지 확인한다 —
// "제외됐다"와 "모듈 밖이다"는 다른 사실이다.
func TestExclude(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"a/a.go": `package a

import _ "example.com/fixture/gen"
`,
		"gen/gen.go": `package gen
`,
	})
	doc, err := LoadPackageGraph(Options{Dir: dir, Exclude: []string{"gen/**", "gen"}})
	if err != nil {
		t.Fatal(err)
	}
	if doc.HasVertex("example.com/fixture/gen") {
		t.Fatal("excluded package leaked as a vertex")
	}
	for _, e := range doc.Edges {
		if e.To == "example.com/fixture/gen" {
			t.Fatalf("import to excluded package must not be an edge: %+v", e)
		}
	}
	var matched, imports bool
	for _, l := range doc.Limitations {
		if strings.Contains(l, "matched exclude patterns") {
			matched = true
		}
		if strings.Contains(l, "imports to excluded packages") {
			imports = true
		}
	}
	if !matched || !imports {
		t.Fatalf("exclusion must be reported as limitations: %v", doc.Limitations)
	}
}

// TestConstValueHarvest는 const 정점이 값을 싣는지 확인한다 —
// diff의 breaking 판정 재료다.
func TestConstValueHarvest(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"lib/lib.go": `package lib

const Version = "1.0"
const count = 42
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "type"})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range doc.Vertices {
		if v.ID == "example.com/fixture/lib.Version" && v.Value != `"1.0"` {
			t.Fatalf("const value must be harvested: %+v", v)
		}
	}
}

// TestFieldVertices는 struct 필드가 심볼 레벨 정점과 참조 간선을 갖는지 확인한다.
// 선택(x.F)·키 리터럴(T{F: v})·위치 리터럴(T{v}) 세 경로가 모두
// 필드 도달성의 사실이어야 한다 — 하나라도 빠지면 dead가 쓰이는 필드를
// unreachable로 오보한다.
func TestFieldVertices(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Use() }
`,
		"lib/lib.go": `package lib

type Point struct {
	X     int
	Y     int
	label string
}

func Use() int {
	p := Point{1, 2}
	q := Point{X: 3}
	return p.X + q.Y
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"example.com/fixture/lib.(Point).X",
		"example.com/fixture/lib.(Point).Y",
		"example.com/fixture/lib.(Point).label",
	} {
		v, ok := doc.VertexByID(id)
		if !ok || v.Kind != graph.KindField {
			t.Fatalf("missing field vertex %s", id)
		}
	}
	// 위치 리터럴 p := Point{1,2}는 모든 필드를 쓰고, 선택 p.X는 X를 쓴다.
	refs := map[string]bool{}
	for _, e := range doc.Edges {
		if e.Kind == graph.EdgeReferences && e.From == "example.com/fixture/lib.Use" {
			refs[e.To] = true
		}
	}
	for _, f := range []string{"X", "Y", "label"} {
		id := "example.com/fixture/lib.(Point)." + f
		if !refs[id] {
			t.Fatalf("field %s must be referenced by Use (positional literal): %v", id, refs)
		}
	}
	// 타입 레벨 문서에는 필드 정점이 없다 — 멤버 단위는 심볼 레벨의 분해다.
	tdoc, err := Load(Options{Dir: dir, Level: "type"})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range tdoc.Vertices {
		if v.Kind == graph.KindField {
			t.Fatalf("type-level doc must not carry field vertices: %+v", v)
		}
	}
}

// TestFieldSelectorRef는 셀렉터 접근만 있는 필드가 참조되는지 확인한다 —
// 리터럴을 쓰지 않는 필드(외부에서 만들어진 값을 읽기만 하는 경우)도
// 사실로 남아야 한다.
func TestFieldSelectorRef(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Serve() }
`,
		"lib/lib.go": `package lib

type Cfg struct {
	Addr string
	dead int
}

func Serve() int {
	var c Cfg
	_ = c.Addr
	return 0
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	var addrRef, deadRef bool
	for _, e := range doc.Edges {
		if e.Kind != graph.EdgeReferences {
			continue
		}
		if e.To == "example.com/fixture/lib.(Cfg).Addr" {
			addrRef = true
		}
		if e.To == "example.com/fixture/lib.(Cfg).dead" {
			deadRef = true
		}
	}
	if !addrRef {
		t.Fatal("selector c.Addr must produce a references edge")
	}
	if deadRef {
		t.Fatal("unreferenced field must not gain a references edge")
	}
}

// TestKeepAnnotation은 //deadcode:keep·//gartograph:keep 표지가 붙은
// 선언이 보존 루트로 수확되는지 확인한다 — 의도적 보존이 선언 옆의
// 사실로 문서에 남아야 dead가 살린다.
func TestKeepAnnotation(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() {}
`,
		"lib/lib.go": `package lib

//deadcode:keep
func PluginEntry() {}

// gartograph:keep for the reflection registry
var Registry = map[string]int{}

//deadcode:keep
const Magic = 7

//deadcode:keep
type marker struct{}

type Meta struct {
	//deadcode:keep — written by reflection
	tag    string
	Unused int
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	roots := map[string]bool{}
	for _, r := range doc.Roots {
		roots[r] = true
	}
	for _, id := range []string{
		"example.com/fixture/lib.PluginEntry",
		"example.com/fixture/lib.Registry",
		"example.com/fixture/lib.Magic",
		"example.com/fixture/lib.marker",
		"example.com/fixture/lib.(Meta).tag",
	} {
		if !roots[id] {
			t.Fatalf("annotated symbol %s must be a retention root: %v", id, doc.Roots)
		}
	}
	if roots["example.com/fixture/lib.(Meta).Unused"] {
		t.Fatal("unannotated field must not become a root")
	}
}

// TestFieldPromotion은 승격 선택이 경유하는 임베드 필드를 참조로 긋는지
// 확인한다 — a.Name에서 Name이 임베드 Base의 필드면 Base 자체도 밟힌다.
// 경유 필드를 빼먹으면 승격으로만 쓰이는 임베드가 unreachable로 오보된다.
func TestFieldPromotion(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Use() }
`,
		"lib/lib.go": `package lib

type Base struct{ Name string }

type Wrap struct {
	Base
	extra int
}

func Use() string {
	var w Wrap
	return w.Name
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]bool{}
	for _, e := range doc.Edges {
		if e.Kind == graph.EdgeReferences && e.From == "example.com/fixture/lib.Use" {
			refs[e.To] = true
		}
	}
	for _, id := range []string{
		"example.com/fixture/lib.(Base).Name", // 도착 필드
		"example.com/fixture/lib.(Wrap).Base", // 경유 임베드 필드
	} {
		if !refs[id] {
			t.Fatalf("promoted selection must reference %s: %v", id, refs)
		}
	}
	if refs["example.com/fixture/lib.(Wrap).extra"] {
		t.Fatal("untouched field must not gain a reference")
	}
}

// TestGenericFieldRef는 제네릭 struct의 인스턴스 경유 필드 참조가
// origin 필드 정점으로 해석되는지 확인한다 — Pair[int]의 필드 객체는
// 선언의 Var와 다른 인스턴스 객체로 돌아오므로 Origin() 대응이 없으면
// 살아 있는 필드가 unreachable로 오보된다.
func TestGenericFieldRef(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Use() }
`,
		"lib/lib.go": `package lib

type Pair[T any] struct {
	Key  T
	dead bool
}

func Use() int {
	p := Pair[int]{Key: 1}
	q := Pair[string]{}
	q.Key = "x"
	return p.Key + len(q.Key)
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	var keyRef, deadRef bool
	for _, e := range doc.Edges {
		if e.Kind != graph.EdgeReferences {
			continue
		}
		if e.To == "example.com/fixture/lib.(Pair).Key" {
			keyRef = true
		}
		if e.To == "example.com/fixture/lib.(Pair).dead" {
			deadRef = true
		}
	}
	if !keyRef {
		t.Fatal("field refs through generic instances must resolve to the origin field vertex")
	}
	if deadRef {
		t.Fatal("unreferenced generic field must not gain a reference")
	}
}

// TestNestedStructFields는 위치 리터럴과 == 비교가 중첩 struct의 잎
// 필드까지 참조로 긋는지 확인한다 — 겉 필드만 밟으면 실제로 쓰인 잎
// 필드가 unreachable로 오보된다.
func TestNestedStructFields(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/lib"

func main() { lib.Use() }
`,
		"lib/lib.go": `package lib

type Inner struct {
	Leaf int
	miss bool
}

type Outer struct {
	In  Inner
	Arr [2]Inner
	Tag string
}

func Use() bool {
	var a, b Outer
	_ = Outer{In: Inner{}, Tag: "x"}
	return a == b
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]bool{}
	for _, e := range doc.Edges {
		if e.Kind == graph.EdgeReferences && e.From == "example.com/fixture/lib.Use" {
			refs[e.To] = true
		}
	}
	// a == b와 Outer{...} 위치 리터럴은 In의 잎 Leaf까지 읽고 쓴다.
	if !refs["example.com/fixture/lib.(Inner).Leaf"] {
		t.Fatalf("nested leaf field must be referenced: %v", refs)
	}
	if !refs["example.com/fixture/lib.(Inner).miss"] {
		t.Fatalf("array-element leaf field must be referenced: %v", refs)
	}
}

// TestEmbeddedFieldKeep은 임베드 필드 줄의 keep 표지가 루트로 기록되는지
// 확인한다 — Names가 비는 임베드는 이름 매칭으로는 잡히지 않는다.
func TestEmbeddedFieldKeep(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() {}
`,
		"lib/lib.go": `package lib

type Base struct{ X int }

type Wrap struct {
	//deadcode:keep
	Base
	extra int
}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	roots := map[string]bool{}
	for _, r := range doc.Roots {
		roots[r] = true
	}
	if !roots["example.com/fixture/lib.(Wrap).Base"] {
		t.Fatalf("keep on embedded field must root it: %v", doc.Roots)
	}
	if roots["example.com/fixture/lib.(Wrap).extra"] {
		t.Fatal("unannotated field must not become a root")
	}
}

// TestKeepNotSubstring은 keep 표지가 줄 첫 토큰일 때만 루트가 되는지
// 확인한다 — 지시문을 인용하는 문장 주석까지 루트로 만들면 보존 의도가
// 아닌 우연이 dead 결과를 조용히 바꾼다.
func TestKeepNotSubstring(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

func main() {}
`,
		"lib/lib.go": `package lib

// see the deadcode:keep convention in docs/contrib
func Mentioned() {}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range doc.Roots {
		if r == "example.com/fixture/lib.Mentioned" {
			t.Fatalf("quoted directive must not become a root: %v", doc.Roots)
		}
	}
}

// TestIsGeneratedFileReadsOnlyRegularFiles는 생성 표지 판정이 정규 파일만 읽는지 확인한다.
// 쓰는 쪽이 없는 FIFO는 보통의 open에서 영원히 막히므로 판정하지 않고 false다. 링크를 따라간
// 정규 파일은 그대로 판정한다 — 링크로 둔 정상 .go 파일의 generated가 바뀌면 안 된다.
func TestIsGeneratedFileReadsOnlyRegularFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe.go")
	testutil.MakeFIFO(t, fifo)
	gen := filepath.Join(dir, "gen.go")
	if err := os.WriteFile(gen, []byte(generatedHeader), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.go")
	if err := os.Symlink(gen, link); err != nil {
		t.Fatal(err)
	}
	if !isGeneratedFile(gen) || !isGeneratedFile(link) {
		t.Fatal("a generated file must stay generated, directly or through a link")
	}
	var fifoGenerated bool
	testutil.FinishWithin(t, testutil.BlockDeadline, func() { fifoGenerated = isGeneratedFile(fifo) })
	if fifoGenerated {
		t.Fatal("a FIFO is not a generated Go file")
	}
}

// generatedHeader는 생성 표지가 패키지 절 앞에 있는 파일 머리다.
const generatedHeader = "// Code generated by fixture. DO NOT EDIT.\n\npackage p\n"

// TestIsGeneratedFileHeaderLimit은 생성 표지를 찾을 때 파일 앞부분을 상한까지만 읽는지
// 확인한다 — 패키지 절이 끝내 나오지 않는 입력에서도 판정이 끝나야 한다.
func TestIsGeneratedFileHeaderLimit(t *testing.T) {
	deep := filepath.Join(t.TempDir(), "deep.go")
	padding := strings.Repeat("//\n", maxGeneratedHeader/len("//\n")+1)
	if err := os.WriteFile(deep, []byte(padding+generatedHeader), 0o644); err != nil {
		t.Fatal(err)
	}
	if isGeneratedFile(deep) {
		t.Fatalf("a marker after the first %d header bytes must not be read", maxGeneratedHeader)
	}
}
