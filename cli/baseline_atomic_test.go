package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBaselineDoesNotReuseTemporaryFile은 CI baseline 쓰기도 공유된 .tmp를 덮지 않게 한다.
func TestBaselineDoesNotReuseTemporaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(path+".tmp", []byte("another writer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveBaseline(path, "", nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path + ".tmp"); err != nil || string(data) != "another writer" {
		t.Fatalf("another writer's file was changed: %q, %v", data, err)
	}
	if _, err := loadBaseline(path); err != nil {
		t.Fatalf("saved baseline cannot be read: %v", err)
	}
}
