package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCaptureLocalModules(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"internal", "module example.com/m\ngo 1.27\nreplace example.com/d => ./dep\n", false},
		{"external", "module example.com/m\ngo 1.27\nreplace example.com/d => ../dep\n", true},
		{"absolute", "module example.com/m\ngo 1.27\nreplace example.com/d => " + filepath.ToSlash(filepath.Join(t.TempDir(), "outside")) + "\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ValidateCapture(dir); (err != nil) != tc.bad {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}

func TestValidateCaptureExternalWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.work"), []byte("go 1.27\nuse ../other\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCapture(dir); err == nil || !strings.Contains(err.Error(), "outside repository") {
		t.Fatalf("external workspace accepted: %v", err)
	}
}
