// Package testutil은 테스트용 임시 Go 모듈을 만든다.
// source·cli 테스트가 같은 fixture 규약을 써야 결과를 비교할 수 있어
// 한 곳에 둔다.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteModule은 files를 담은 임시 모듈 디렉터리를 만들고 경로를 돌려준다.
// go.mod는 files에 없으면 모듈 경로 example.com/fixture로 자동 생성한다 —
// fixture마다 go.mod를 반복해서 적지 않기 위함이다.
func WriteModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"] = "module example.com/fixture\n\ngo 1.27\n"
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}
