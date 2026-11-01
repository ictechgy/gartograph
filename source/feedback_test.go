package source

import (
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
