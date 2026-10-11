package export

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// TestSaveFileDoesNotReuseTemporaryPaths는 이전 실행의 파일·링크를 건드리지 않게 한다.
func TestSaveFileDoesNotReuseTemporaryPaths(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint(symlink), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "graph.json")
			sentinel := filepath.Join(dir, "sentinel")
			if err := os.WriteFile(sentinel, []byte("keep me"), 0o600); err != nil {
				t.Fatal(err)
			}
			if symlink {
				if err := os.Symlink(sentinel, path+".tmp"); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else if err := os.WriteFile(path+".tmp", []byte("keep me"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := SaveFile(&graph.Document{Version: graph.Version, Module: "saved"}, path); err != nil {
				t.Fatal(err)
			}
			for _, original := range []string{sentinel, path + ".tmp"} {
				data, err := os.ReadFile(original)
				if err != nil || string(data) != "keep me" {
					t.Fatalf("unrelated file changed: %q, %v", data, err)
				}
			}
			back, err := LoadFile(path)
			if err != nil || back.Module != "saved" {
				t.Fatalf("saved document = %+v, %v", back, err)
			}
		})
	}
}

// TestWriteFileConcurrentは同じ宛先への同時書き込みが全て完全な一文書を残すことを確かめる。
func TestWriteFileConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "graph.json")
	const writers = 32
	var workers sync.WaitGroup
	errors := make(chan error, writers)
	for index := range writers {
		workers.Go(func() {
			body := bytes.Repeat([]byte{byte('a' + index)}, 64*1024)
			errors <- WriteFile(path, body)
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 64*1024 {
		t.Fatalf("partial output: length %d, error %v", len(data), err)
	}
	if !bytes.Equal(data, bytes.Repeat(data[:1], len(data))) {
		t.Fatal("concurrent documents were interleaved")
	}
	assertNoTemporaryOutputs(t, dir)
}

// TestWriteFileFailurePreservesDestination는 rename 실패가 기존 디렉터리나 내용을 지우지 않게 한다.
func TestWriteFileFailurePreservesDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "destination")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new")); err == nil {
		t.Fatal("replacing a directory must fail")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("existing destination changed: %v", err)
	}
	assertNoTemporaryOutputs(t, dir)
	if err := WriteFile(filepath.Join(dir, "missing", "graph.json"), nil); err == nil {
		t.Fatal("missing parent must fail")
	}
}

// TestWriteFilePermissions는 새 산출물이 비공개이고 기존 제한 권한이 완화되지 않게 한다.
func TestWriteFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	path := filepath.Join(t.TempDir(), "graph.json")
	for _, mode := range []os.FileMode{0o600, 0o640} {
		if mode != 0o600 {
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
		}
		if err := WriteFile(path, []byte("saved")); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions = %v, %v; want %v", info, err, mode)
		}
	}
}

// assertNoTemporaryOutputs는 성공·실패 이후 이 호출의 임시 산출물이 남지 않는지 확인한다.
func assertNoTemporaryOutputs(t *testing.T, dir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, ".gartograph-*.tmp"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("temporary outputs remain: %v, %v", paths, err)
	}
}
