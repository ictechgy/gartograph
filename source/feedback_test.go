package source

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

func TestFeedbackTestHelpersAreNotRunnerRoots(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod":  "module example.com/testnames\n\ngo 1.27\n",
		"main.go": "package main\nfunc main() {}\n",
		"main_test.go": `package main
import "testing"
func TestRun(t *testing.T) {}
func Test_underscore(t *testing.T) {}
func Testdata() {}
func Testmain() {}
func Benchmarking() {}
func Examplefoo() {}
func Fuzzdata() {}
`,
	})
	doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Tests: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TestRun", "Test_underscore"} {
		if !slices.Contains(doc.Roots, "example.com/testnames."+name) {
			t.Errorf("missing runner root %s", name)
		}
	}
	for _, name := range []string{"Testdata", "Testmain", "Benchmarking", "Examplefoo", "Fuzzdata"} {
		if slices.Contains(doc.Roots, "example.com/testnames."+name) {
			t.Errorf("helper became runner root %s", name)
		}
	}
}

func TestReviewTestMainAcceptsBothRunnerSignatures(t *testing.T) {
	for _, parameter := range []string{"T", "M"} {
		t.Run(parameter, func(t *testing.T) {
			dir := testutil.WriteModule(t, map[string]string{
				"go.mod":       "module example.com/testmain\n\ngo 1.27\n",
				"main.go":      "package main\nfunc main() {}\n",
				"main_test.go": "package main\nimport \"testing\"\nfunc TestMain(t *testing." + parameter + ") {}\n",
			})
			doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Tests: true})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(doc.Roots, "example.com/testmain.TestMain") {
				t.Error("valid TestMain runner entry lost")
			}
		})
	}
}

func TestFeedbackCgoCallbackAndGeneratedProvenance(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("C compiler unavailable")
	}
	t.Setenv("CGO_ENABLED", "1")
	dir := testutil.WriteModule(t, map[string]string{
		"go.mod":  "module example.com/callback\n\ngo 1.27\n",
		"main.go": "package main\nfunc main() {}\n",
		"callback.go": `package main
import "C"
//export GoCallback
func GoCallback() int { return helper() }
func helper() int { return 1 }
func Unused() {}
`,
	})
	doc := loadSymbol(t, dir)
	if !slices.Contains(doc.Roots, "example.com/callback.GoCallback") {
		t.Error("C callback is not a root")
	}
	foundGenerated := false
	for _, vertex := range doc.Vertices {
		if vertex.ID == "example.com/callback.GoCallback" && vertex.Generated {
			t.Error("user callback became generated")
		}
		if vertex.Position != nil && !strings.HasPrefix(vertex.Position.File, dir+string(filepath.Separator)) {
			foundGenerated = true
			if !vertex.Generated {
				t.Errorf("compiler infrastructure lost physical provenance: %s", vertex.ID)
			}
		}
	}
	if !foundGenerated {
		t.Fatal("cgo infrastructure was not observed")
	}
	for _, note := range doc.Limitations {
		if strings.Contains(note, "//go:linkname") {
			t.Errorf("synthetic linknames counted as user source: %s", note)
		}
	}
	if !slices.ContainsFunc(doc.Edges, func(edge graph.Edge) bool {
		return edge.From == "example.com/callback.GoCallback" && edge.To == "example.com/callback.helper" && edge.Kind == graph.EdgeCall
	}) {
		t.Error("callback outgoing dependency disappeared")
	}
}

// struct tag는 reflection으로만 읽히므로 태그가 붙은 필드는 그래프가 못 보는 접근 경로가 있다는 수확 사실이다.
// 키는 reflect.StructTag 관례로 읽는다 — 값이 정확히 "-"인 키(인코더가 건너뛰는 필드)는 빼고, 같은 키가
// 거듭되면 첫 값이 정한다(reflect.StructTag.Lookup). 관례를 벗어난 태그는 reflect처럼 거기서 읽기를 멈춘다.
func TestFeedbackFieldVerticesCarryStructTagKeys(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nfunc main() { _ = R{} }\n\n" +
			"type R struct {\n" +
			"\tA int `json:\"a\"`\n" +
			"\tB int `json:\"-\"`\n" +
			"\tc int\n" +
			"\tD int `json:\"-\" yaml:\"d\"`\n" +
			"\tE int `yaml:\"e\" json:\"e,omitempty\" xml:\"-\"`\n" +
			"\tF int `json:\"-\" json:\"f\"`\n" +
			"\tG int `json:a yaml:\"g\"`\n" +
			"\tH int `json:\"-,\"`\n" +
			"\tEmbedded `db:\"embedded\"`\n" +
			"}\n\ntype Embedded struct{}\n",
	})
	doc := loadSymbol(t, dir)
	got := map[string][]string{}
	for _, v := range doc.Vertices {
		if v.Kind == graph.KindField {
			got[v.Name] = v.TagKeys
		}
	}
	want := map[string][]string{
		"A": {"json"}, "B": nil, "c": nil, "D": {"yaml"}, "E": {"json", "yaml"},
		"F": nil, "G": nil, "H": {"json"}, "Embedded": {"db"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("field tag keys = %v, want %v", got, want)
	}
}

// --tests 없이 수확하면 _test.go 파일은 그래프 밖이라, 다른 패키지의 테스트에서만 import되는 패키지(테스트
// 헬퍼)는 모듈 안 importer가 없는 것처럼 보인다. 그 수를 실제로 세어 limitation으로 남긴다. 자기 패키지의
// 외부 테스트(pkg_test)만 import하는 라이브러리, 빌드 제약으로 빠진 테스트 파일의 import, 이미 제품 코드가
// import하는 패키지(exclude로 정점에서 뺀 패키지가 import하는 것 포함)는 세지 않는다. --tests로 테스트를
// 수확하면 조용하다.
func TestFeedbackTestOnlyImportedPackagesAreCounted(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go":    "package main\n\nimport \"example.com/fixture/lib\"\n\nfunc main() { lib.Run() }\n",
		"lib/lib.go": "package lib\n\nfunc Run() {}\n",
		"lib/lib_test.go": "package lib\n\nimport (\n\t\"testing\"\n\n\t\"example.com/fixture/internal/helper\"\n" +
			"\t\"example.com/fixture/internal/genhelper\"\n\t\"example.com/fixture/lib/shared\"\n)\n\n" +
			"func TestRun(t *testing.T) { helper.Help(); shared.S(); genhelper.Help() }\n",
		"gen/gen.go":                      "package gen\n\nimport _ \"example.com/fixture/internal/genhelper\"\n",
		"internal/genhelper/genhelper.go": "package genhelper\n\nfunc Help() {}\n",
		"lib/x_test.go": "package lib_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/fixture/internal/xhelper\"\n)\n\n" +
			"func TestX(t *testing.T) { xhelper.Help() }\n",
		"lib/never_test.go": "//go:build never\n\npackage lib\n\nimport \"example.com/fixture/internal/tagged\"\n\n" +
			"var _ = tagged.Help\n",
		"lib/shared/shared.go":           "package shared\n\nfunc S() {}\n",
		"internal/helper/helper.go":      "package helper\n\nfunc Help() {}\n",
		"internal/xhelper/xhelper.go":    "package xhelper\n\nfunc Help() {}\n",
		"internal/tagged/tagged.go":      "package tagged\n\nfunc Help() {}\n",
		"solo/solo.go":                   "package solo\n\nfunc S() {}\n",
		"solo/solo_test.go":              "package solo_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/fixture/solo\"\n)\n\nfunc TestS(t *testing.T) { solo.S() }\n",
		"cmd/tool/main.go":               "package main\n\nimport _ \"example.com/fixture/lib/shared\"\n\nfunc main() {}\n",
		"internal/helper/helper_test.go": "package helper\n",
	})
	for _, level := range []graph.Level{graph.LevelPackage, graph.LevelSymbol} {
		doc, err := Load(Options{Dir: dir, Level: level, Exclude: []string{"gen"}})
		if err != nil {
			t.Fatal(err)
		}
		var notes []string
		for _, note := range doc.Limitations {
			if strings.Contains(note, "imported only by _test.go files") {
				notes = append(notes, note)
			}
		}
		want := "2 packages are imported only by _test.go files of other packages (first: " +
			"example.com/fixture/internal/helper); test files were not harvested, so these packages may " +
			"appear orphaned and their symbols unreachable (use --tests to include)"
		if !reflect.DeepEqual(notes, []string{want}) {
			t.Fatalf("%s: test-only importer limitation = %q, want %q", level, notes, want)
		}
	}
	doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Tests: true, Exclude: []string{"gen"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range doc.Limitations {
		if strings.Contains(note, "imported only by _test.go files") {
			t.Fatalf("harvested tests must not carry the test-only importer limitation: %q", note)
		}
	}
}

// 테스트 파일 import를 읽는 go/build 문맥은 분석 대상이 고른 경로를 막히지 않게 연다 — 쓰는 쪽이 없는 FIFO와
// 디렉터리는 열지 않고 오류로 돌려준다. (지금은 go list가 같은 이름의 FIFO에서 먼저 막히지만, 문맥은 그 앞
// 단계와 무관하게 막히지 않아야 한다.)
func TestFeedbackTestImportContextRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe_test.go")
	testutil.MakeFIFO(t, fifo)
	ctxt := buildContext(Options{})
	for _, path := range []string{fifo, dir} {
		var err error
		testutil.FinishWithin(t, testutil.BlockDeadline, func() {
			var f io.ReadCloser
			if f, err = ctxt.OpenFile(path); err == nil {
				f.Close()
			}
		})
		if err == nil {
			t.Errorf("%s: non-regular file must be refused", path)
		}
	}
	regular := filepath.Join(dir, "ok_test.go")
	if err := os.WriteFile(regular, []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := ctxt.OpenFile(regular)
	if err != nil {
		t.Fatalf("regular test file must open: %v", err)
	}
	f.Close()
}
