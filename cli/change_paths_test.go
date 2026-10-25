package cli

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/source"
)

func TestCapturePathRejectsNonCanonicalNames(t *testing.T) {
	for _, name := range []string{".", "./value.go", "a/../value.go", "a/./value.go", "a//value.go", "a/", string([]byte{0xff})} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			if err := capturePath(name); err == nil {
				t.Fatalf("accepted noncanonical source identity %q", name)
			}
		})
	}
	for _, name := range []string{"value.go", "a/b.go", "caf\u00e9.txt", "cafe\u0301.txt"} {
		if err := capturePath(name); err != nil {
			t.Fatalf("rejected distinct canonical UTF-8 source identity %q: %v", name, err)
		}
	}
}

// Git object는 실제 filesystem의 case/Unicode 규칙과 독립적으로 서로 다른 이름을 담는다.
func captureTreeFixture(t *testing.T, files map[string][]byte) (string, string, *capturedSource) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	changeGit(t, dir, "init", "-q")
	var makeTree func(map[string][]byte) string
	makeTree = func(entries map[string][]byte) string {
		groups := map[string]map[string][]byte{}
		rows := map[string]string{}
		for name, data := range entries {
			first, rest, nested := strings.Cut(name, "/")
			if nested {
				if groups[first] == nil {
					groups[first] = map[string][]byte{}
				}
				groups[first][rest] = data
				continue
			}
			rows[first] = "100644 blob " + changeGitInput(t, dir, data, "hash-object", "-w", "--stdin") + "\t" + first + "\n"
		}
		for name, children := range groups {
			rows[name] = "040000 tree " + makeTree(children) + "\t" + name + "\n"
		}
		names := make([]string, 0, len(rows))
		for name := range rows {
			names = append(names, name)
		}
		sort.Strings(names)
		var tree strings.Builder
		for _, name := range names {
			tree.WriteString(rows[name])
		}
		return changeGitInput(t, dir, []byte(tree.String()), "mktree")
	}
	commit := changeGit(t, dir, "commit-tree", makeTree(files), "-m", "captured source identities")
	changeGit(t, dir, "update-ref", "refs/heads/main", commit)
	changeGit(t, dir, "fsck", "--no-reflogs", "--no-dangling")
	capture, err := captureRevision(dir, commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.files) != len(files) {
		t.Fatalf("Git capture lost inputs before filesystem materialization: %v", capture.files)
	}
	for name, want := range files {
		if !bytes.Equal(want, capture.files[name]) {
			t.Fatalf("captured %q differs from its Git object", name)
		}
	}
	return dir, commit, capture
}

func changeGitInput(t *testing.T, dir string, input []byte, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func captureAliasVolume(t *testing.T, first, second string) bool {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, first), []byte("case probe"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := os.Stat(filepath.Join(dir, first))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(filepath.Join(dir, second))
	return err == nil && os.SameFile(a, b)
}

func TestChangeCaptureRefusesFilesystemAliasesCLI(t *testing.T) {
	for _, test := range []struct{ name, first, second, probeFirst, probeSecond string }{
		{"case-file", "A.txt", "a.txt", "A", "a"},
		{"case-directory", "A/first.txt", "a/second.txt", "A", "a"},
		{"unicode-file", "caf\u00e9.txt", "cafe\u0301.txt", "caf\u00e9", "cafe\u0301"},
		{"unicode-directory", "caf\u00e9/first.txt", "cafe\u0301/second.txt", "caf\u00e9", "cafe\u0301"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !captureAliasVolume(t, test.probeFirst, test.probeSecond) {
				t.Skip("this filesystem represents the two spellings as distinct entries")
			}
			files := map[string][]byte{"go.mod": []byte("module example.com/fixture\n\ngo 1.27.1\n"), "value.go": []byte("package fixture\nfunc Value() {}\n"), test.first: []byte("FIRST"), test.second: []byte("SECOND")}
			dir, commit, capture := captureTreeFixture(t, files)
			hash := capture.origin.ContentHash
			side, err := analyzeCapture(capture, ".", source.Options{Dir: dir}, []string{}, 1, 5)
			var out, errs bytes.Buffer
			code := Run([]string{"impact", "--dir", dir, "--base", commit, "--head", commit}, &out, &errs)
			if err == nil || side != nil || !strings.Contains(err.Error(), "collid") {
				t.Errorf("aliased Git input was not refused before analysis: side=%+v err=%v", side, err)
			}
			if code != 2 || out.Len() != 0 || !strings.Contains(errs.String(), "collid") {
				t.Fatalf("CLI admitted aliased captured identities: exit=%d stdout=%s stderr=%s", code, out.String(), errs.String())
			}
			if capture.origin.ContentHash != hash || !bytes.Equal(capture.files[test.first], files[test.first]) || !bytes.Equal(capture.files[test.second], files[test.second]) {
				t.Fatal("rejection changed the source capture or its hash")
			}
		})
	}
}

func TestChangeCaptureKeepsDistinctCaseSensitiveNames(t *testing.T) {
	if captureAliasVolume(t, "A", "a") {
		t.Skip("the positive requires a case-sensitive filesystem")
	}
	dir, _, capture := captureTreeFixture(t, map[string][]byte{"go.mod": []byte("module example.com/fixture\n\ngo 1.27.1\n"), "value.go": []byte("package fixture\nfunc Value() {}\n"), "A/first.txt": []byte("FIRST"), "a/second.txt": []byte("SECOND")})
	if _, err := analyzeCapture(capture, ".", source.Options{Dir: dir}, []string{}, 1, 5); err != nil {
		t.Fatalf("distinct filesystem inputs were rejected: %v", err)
	}
}

func TestChangeCaptureRejectsRawNonCanonicalGitTree(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	changeGit(t, dir, "init", "-q")
	id := changeGitInput(t, dir, []byte("package fixture\n"), "hash-object", "-w", "--stdin")
	rawID, err := hex.DecodeString(id)
	if err != nil {
		t.Fatal(err)
	}
	tree := append([]byte("100644 a/../value.go\x00"), rawID...)
	treeID := changeGitInput(t, dir, tree, "hash-object", "--literally", "-t", "tree", "-w", "--stdin")
	commit := changeGit(t, dir, "commit-tree", treeID, "-m", "malformed tree fixture")
	listed := changeGit(t, dir, "ls-tree", "-rz", "--full-tree", commit)
	if !strings.Contains(listed, "a/../value.go") {
		t.Fatalf("Git did not expose the intended raw pathname: %q", listed)
	}
	if _, err := captureRevision(dir, commit); err == nil || !strings.Contains(err.Error(), "source path") {
		t.Fatalf("noncanonical raw Git identity was admitted: %v", err)
	}
	var out, errs bytes.Buffer
	if code := Run([]string{"impact", "--dir", dir, "--base", commit, "--head", commit}, &out, &errs); code != 2 || out.Len() != 0 || !strings.Contains(errs.String(), "source path") {
		t.Fatalf("CLI admitted noncanonical raw Git identity: exit=%d stdout=%s stderr=%s", code, out.String(), errs.String())
	}
}

func TestChangeCaptureRejectsDuplicateRawGitEntries(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	changeGit(t, dir, "init", "-q")
	var tree []byte
	for _, data := range []string{"package fixture\nfunc First() {}\n", "package fixture\nfunc Second() {}\n"} {
		id := changeGitInput(t, dir, []byte(data), "hash-object", "-w", "--stdin")
		rawID, err := hex.DecodeString(id)
		if err != nil {
			t.Fatal(err)
		}
		tree = append(tree, []byte("100644 value.go\x00")...)
		tree = append(tree, rawID...)
	}
	treeID := changeGitInput(t, dir, tree, "hash-object", "--literally", "-t", "tree", "-w", "--stdin")
	commit := changeGit(t, dir, "commit-tree", treeID, "-m", "duplicate tree fixture")
	listed := changeGit(t, dir, "ls-tree", "-rz", "--full-tree", commit)
	if strings.Count(listed, "value.go") != 2 {
		t.Fatalf("Git did not expose the duplicate entries: %q", listed)
	}
	if _, err := captureRevision(dir, commit); err == nil || !strings.Contains(err.Error(), "duplicate Git source path") {
		t.Fatalf("duplicate raw Git entries lost one blob without refusal: %v", err)
	}
}
