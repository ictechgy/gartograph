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
	changeGit(t, dir, "config", "core.filemode", "false")
	staged, err := runChangeImpact(source.Options{Dir: dir}, base, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged.After.Files) != 1 || staged.After.Files[0] != "value.go" || staged.Before.Origin.ContentHash == staged.After.Origin.ContentHash {
		t.Fatalf("staged mode-only change lost: %+v", staged)
	}
	changeGit(t, dir, "commit", "-m", "mode only")
	report, err := runChangeImpact(source.Options{Dir: dir}, base, "HEAD", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Before.Files) != 1 || report.Before.Files[0] != "value.go" || len(report.After.Candidates) == 0 {
		t.Fatalf("mode-only patch lost: %+v", report)
	}
}

func TestChangeImpactRestoredUntrackedFileHasNoNetChange(t *testing.T) {
	body := "package fixture\nfunc Value() {}\n"
	dir := testutil.WriteModule(t, map[string]string{"value.go": body, "keep.go": "package fixture\n"})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "base")
	base := changeGit(t, dir, "rev-parse", "HEAD")
	changeGit(t, dir, "rm", "value.go")
	changeGit(t, dir, "commit", "-m", "delete")
	if err := os.WriteFile(filepath.Join(dir, "value.go"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := runChangeImpact(source.Options{Dir: dir}, base, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Before.Files) != 0 || len(report.After.Files) != 0 || len(report.Before.Candidates) != 0 || len(report.After.Candidates) != 0 {
		t.Fatalf("index deletion leaked into net before/after diff: %+v", report)
	}
}

func TestChangeCaptureRejectsIndexSymlinkPlaceholder(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"value.go": "package fixture\nfunc Value() {}\n"})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "base")
	blob := changeGit(t, dir, "rev-parse", "HEAD:value.go")
	changeGit(t, dir, "update-index", "--cacheinfo", "120000,"+blob+",value.go")
	_, err := captureWorkingTree(dir)
	if err == nil || !strings.Contains(err.Error(), "non-regular index") {
		t.Fatalf("index symlink placeholder accepted as regular source: %v", err)
	}
}

func TestChangeCaptureRejectsNonLocalPatterns(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"value.go": "package fixture\nfunc Value() {}\n"})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	for _, pattern := range []string{filepath.Join(dir, "value.go"), "file=" + filepath.Join(dir, "value.go"), "../...", "./../../...", "example.org/external/..."} {
		_, err := runChangeImpact(source.Options{Dir: dir, Patterns: []string{pattern}}, "HEAD", "HEAD", 1, 5)
		if err == nil || !strings.Contains(err.Error(), "repository-relative") {
			t.Errorf("pattern %q escaped capture validation: %v", pattern, err)
		}
	}
}

func TestChangeImpactSubdirectoryRetainsAncestorModuleChange(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"pkg/value.go": "package pkg\nfunc Value() {}\n"})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	file := filepath.Join(dir, "go.mod")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(data, []byte("\n// module configuration edit\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := runChangeImpact(source.Options{Dir: filepath.Join(dir, "pkg"), Patterns: []string{"."}}, "HEAD", "", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []changeSnapshot{report.Before, report.After} {
		if len(side.Files) != 1 || side.Files[0] != "../go.mod" || len(side.UnmappedFiles) != 1 || side.UnmappedFiles[0] != "../go.mod" {
			t.Fatalf("ancestor module change disappeared from subdirectory scope: %+v", side)
		}
	}
}

func TestChangeCaptureIgnoresPersistentModfile(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"value.go": "package fixture\nfunc Value() {}\n"})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	external := t.TempDir()
	modfile := filepath.Join(external, "external.mod")
	if err := os.WriteFile(modfile, []byte("module example.com/uncaptured\ngo 1.27\n"), 0600); err != nil {
		t.Fatal(err)
	}
	goenv := filepath.Join(external, "goenv")
	if err := os.WriteFile(goenv, []byte("GOFLAGS=-modfile="+filepath.ToSlash(modfile)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", goenv)
	t.Setenv("GOFLAGS", "")
	if err := os.WriteFile(filepath.Join(dir, "value.go"), []byte("package fixture\nfunc Value() { println(1) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := runChangeImpact(source.Options{Dir: dir}, "HEAD", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []changeSnapshot{report.Before, report.After} {
		found := false
		for _, candidate := range side.Candidates {
			found = found || candidate.ID == "example.com/fixture.Value"
			if strings.Contains(candidate.ID, "uncaptured") {
				t.Fatalf("persistent modfile changed captured identity: %+v", side)
			}
		}
		if !found {
			t.Fatalf("captured declaration missing: %+v", side)
		}
	}
	environment := report.Scope["loaderEnvironment"].(map[string]string)
	if environment["GOENV"] != "off" || environment["GOFLAGS"] != "" || report.Before.Workspace != "off" || report.After.Workspace != "off" {
		t.Fatalf("effective loader settings omitted: %+v", report)
	}
}

func TestChangeCaptureIgnoresAncestorWorkspace(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"value.go": "package fixture\nfunc Value() {}\n"})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "go.work"), []byte("go 1.27\nuse ./uncaptured\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, parent)
	}
	report, err := runChangeImpact(source.Options{Dir: dir}, "HEAD", "HEAD", 0, 0)
	if err != nil {
		t.Fatalf("ancestor workspace escaped capture boundary: %v", err)
	}
	if len(report.Before.Limitations) > 0 && strings.Contains(strings.Join(report.Before.Limitations, " "), "uncaptured") {
		t.Fatalf("ancestor workspace influenced captured analysis: %+v", report)
	}
}

func TestChangeCaptureUsesSnapshotWorkspaceAndIgnoresProcessFlags(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"pkg/value.go": "package pkg\nfunc Value() {}\n"})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	for name, body := range map[string]string{
		"go.work":      "go 1.27\nuse .\n",
		"pkg/value.go": "package pkg\nfunc Value() {println(1)}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOFLAGS", "-overlay="+filepath.Join(t.TempDir(), "missing-overlay.json"))
	t.Setenv("GOPACKAGESDRIVER", filepath.Join(t.TempDir(), "missing-driver"))
	t.Setenv("GO111MODULE", "off")
	report, err := runChangeImpact(source.Options{Dir: filepath.Join(dir, "pkg"), Patterns: []string{"."}}, "HEAD", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Before.Workspace != "off" || report.After.Workspace != "go.work" {
		t.Fatalf("per-snapshot workspace identity lost: %+v", report)
	}
	for _, side := range []changeSnapshot{report.Before, report.After} {
		found := false
		for _, candidate := range side.Candidates {
			found = found || candidate.ID == "example.com/fixture/pkg.Value"
		}
		if !found {
			t.Fatalf("captured subdirectory declaration missing: %+v", side)
		}
	}
}
