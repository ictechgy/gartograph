package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// 사용자가 고른 디렉터리의 별칭과 캡처 안 원문 경로의 별칭은 다른 경계다.
func changeDirectoryAlias(t *testing.T, dir string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "repository")
	if err := os.Symlink(physical, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("directory symlink creation requires Windows privileges")
		}
		t.Fatal(err)
	}
	return alias
}

func TestChangeImpactDirectoryAliasCLI(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"value.go":     "package fixture\nfunc Value() int { return 1 }\n",
		"pkg/value.go": "package pkg\nfunc Nested() int { return 1 }\n",
	})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	for name, body := range map[string]string{
		"value.go":     "package fixture\nfunc Value() int { return 2 }\n",
		"pkg/value.go": "package pkg\nfunc Nested() int { return 2 }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mod := filepath.Join(dir, "go.mod")
	data, err := os.ReadFile(mod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mod, append(data, []byte("\n// changed module configuration\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	alias := changeDirectoryAlias(t, dir)
	for _, test := range []struct {
		name, dir, pattern, relative, symbol string
		files                                []string
	}{
		{"root", alias, "./...", ".", "example.com/fixture.Value", []string{"go.mod", "pkg/value.go", "value.go"}},
		{"subdirectory", filepath.Join(alias, "pkg"), ".", "pkg", "example.com/fixture/pkg.Nested", []string{"../go.mod", "../value.go", "value.go"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, errs bytes.Buffer
			code := Run([]string{"impact", "--dir", test.dir, "--base", "HEAD", "--pattern", test.pattern}, &out, &errs)
			if code != 0 {
				t.Fatalf("directory alias rejected: exit=%d stderr=%s", code, errs.String())
			}
			var report changeReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Scope["directory"] != test.relative || report.Complete {
				t.Fatalf("directory scope or completeness changed: %+v", report)
			}
			for _, side := range []changeSnapshot{report.Before, report.After} {
				if !reflect.DeepEqual(side.Files, test.files) || !strings.Contains(string(mustJSON(t, side)), test.symbol) {
					t.Fatalf("aliased directory changed source identity or mapping: %+v", side)
				}
			}
		})
	}
}

func TestChangeImpactDirectoryAliasKeepsSourceSymlinkGuard(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"value.go": "package fixture\nfunc Value() {}\n"})
	changeGit(t, dir, "init")
	changeGit(t, dir, "add", ".")
	changeGit(t, dir, "commit", "-m", "before")
	alias := changeDirectoryAlias(t, dir)
	outside := filepath.Join(t.TempDir(), "outside.go")
	body := []byte("package outside\n// owned sentinel\n")
	if err := os.WriteFile(outside, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.go")); err != nil {
		t.Fatal(err)
	}
	for _, head := range []string{"working", "committed"} {
		t.Run(head, func(t *testing.T) {
			args := []string{"impact", "--dir", alias, "--base", "HEAD"}
			if head == "committed" {
				changeGit(t, dir, "add", ".")
				changeGit(t, dir, "commit", "-m", "source link")
				args = append(args, "--head", "HEAD")
			}
			var out, errs bytes.Buffer
			if code := Run(args, &out, &errs); code != 2 || out.Len() != 0 || (!strings.Contains(errs.String(), "symlink") && !strings.Contains(errs.String(), "non-regular")) {
				t.Fatalf("source symlink guard changed: exit=%d stdout=%s stderr=%s", code, out.String(), errs.String())
			}
			if after, err := os.ReadFile(outside); err != nil || !bytes.Equal(after, body) {
				t.Fatalf("outside sentinel changed: %v", err)
			}
		})
	}
}
