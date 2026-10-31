package source

import (
	"os/exec"
	"path/filepath"
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
