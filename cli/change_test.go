package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
	"github.com/ictechgy/gartograph/source"
)

func changeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// 실제로 컴파일 가능한 삭제 변경에서 과거 호출자가 사라지지 않아야 한다.
func TestChangeImpactDeletedCallerCLI(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"old.go":         "package fixture\nfunc Old() int { return 1 }\n",
		"caller.go":      "package fixture\nfunc Caller() int { return Old() }\n",
		"caller_test.go": "package fixture\nimport \"testing\"\nfunc TestCaller(t *testing.T) { if Caller()!=1 {t.Fatal(\"value\")} }\n",
	})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	if err := os.Remove(filepath.Join(dir, "old.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "caller.go"), []byte("package fixture\nfunc Caller() int { return 1 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	code := Run([]string{"impact", "--dir", dir, "--base", "HEAD", "--tests"}, &out, &errs)
	if code != 0 {
		t.Fatalf("impact exit %d: %s", code, errs.String())
	}
	var report map[string]any
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report["mode"] != "patch" {
		t.Fatalf("missing patch mode: %s", out.String())
	}
	before := report["before"].(map[string]any)
	if !strings.Contains(string(mustJSON(t, before)), "example.com/fixture.Old") || !strings.Contains(string(mustJSON(t, before)), "example.com/fixture.TestCaller") {
		t.Fatalf("lost historical symbol or test: %s", out.String())
	}
	if report["complete"] != false {
		t.Fatalf("static analysis must not promise complete: %s", out.String())
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestChangeImpactInterfaceRenameAndMCP(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"impl.go":        "package fixture\ntype Service struct{}\nfunc (Service) Read() int {return 1}\n",
		"caller.go":      "package fixture\ntype Reader interface {Read() int}\nfunc Caller(r Reader) int {return r.Read()}\nvar Hook = Caller\n",
		"caller_test.go": "package fixture\nimport \"testing\"\nfunc TestCaller(t *testing.T) {if Caller(Service{})!=1 {t.Fatal(\"value\")}}\n",
	})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	old := changeGit(t, dir, "rev-parse", "HEAD")
	if err := os.Rename(filepath.Join(dir, "impl.go"), filepath.Join(dir, "renamed.go")); err != nil {
		t.Fatal(err)
	}
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "rename")
	options := source.Options{Dir: dir, Tests: true}
	report, err := runChangeImpact(options, old, "HEAD", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []changeSnapshot{report.Before, report.After} {
		hasCaller, hasTest := false, false
		for _, c := range side.Candidates {
			if c.ID == "example.com/fixture.Caller" {
				hasCaller = len(c.Via) > 0
			}
			if c.ID == "example.com/fixture.TestCaller" {
				hasTest = c.TestCandidate && len(c.Via) > 0
			}
		}
		if !hasCaller || !hasTest {
			t.Fatalf("interface caller/test missing: %+v", side)
		}
	}
	if report.Before.Files[0] != "impl.go" || report.After.Files[0] != "renamed.go" {
		t.Fatalf("rename paths: %+v", report)
	}
	srv := &mcpServer{captureOptions: &options}
	result, err := srv.runTool("gartograph_impact", json.RawMessage(`{"base":"`+old+`","head":"HEAD"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got changeReport
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	report.PreparationMillis = 0
	report.AnalysisMillis = 0
	got.PreparationMillis = 0
	got.AnalysisMillis = 0
	if !bytes.Equal(mustJSON(t, report), mustJSON(t, &got)) {
		t.Fatalf("CLI/MCP diverged:\n%s\n%s", mustJSON(t, report), mustJSON(t, &got))
	}
}

func TestChangeCaptureContentAndConfiguration(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"main.go": "package fixture\nfunc Value() int {return 1}\n", "config.json": "{\"enabled\":false}\n"})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	before, err := captureWorkingTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package fixture\nfunc Value() int {return 2}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := captureWorkingTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before.origin.ContentHash == after.origin.ContentHash {
		t.Fatal("body-only edit did not change content hash")
	}
	changeGit(t, dir, "checkout", "--", "main.go")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{\"enabled\":true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := runChangeImpact(source.Options{Dir: dir}, "HEAD", "", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.After.UnmappedFiles) != 2 || report.Complete || report.After.Origin.Kind != "working-tree" {
		t.Fatalf("unmapped config/untracked input suppressed: %+v", report)
	}
}

func TestChangeCaptureRejectsUnsafeInputs(t *testing.T) {
	for _, path := range []string{"../escape", "/root", "C:/other", "a\\b", ".git/config"} {
		if capturePath(path) == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	var out, errs bytes.Buffer
	if Run([]string{"impact", "--base", "--output=bad", "--graph", "missing"}, &out, &errs) != 2 || out.Len() != 0 {
		t.Fatalf("invalid mode produced output: %s %s", out.String(), errs.String())
	}
}

func TestChangeImpactModeOnlyGitPatch(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"value.go": "package fixture\nfunc Value() {}\n"})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	base := changeGit(t, dir, "rev-parse", "HEAD")
	changeGit(t, dir, "update-index", "--chmod=+x", "value.go")
	changeGit(t, dir, "commit", "-m", "mode only")
	report, err := runChangeImpact(source.Options{Dir: dir}, base, "HEAD", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Before.Files) != 1 || report.Before.Files[0] != "value.go" || len(report.After.Candidates) == 0 {
		t.Fatalf("mode-only patch lost: %+v", report)
	}
}
