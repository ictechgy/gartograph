package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestBridgeFactsGeneratedAt는 bridges·schema의 --generated-at이 routes·reach와 같은 문법(RFC 3339, 계약의 UTC
// 밀리초로 정규화)으로 generatedAt을 고정해 같은 입력이 같은 바이트가 되는지, 틀린 값은 수확·--out 쓰기 전에
// 같은 문구의 사용법 오류(2)인지 본다. 예전에는 두 명령에 이 플래그가 없어 실행마다 generatedAt이 달랐다.
func TestBridgeFactsGeneratedAt(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport \"database/sql\"\n\nfunc main() {}\n\n" +
			"func q(db *sql.DB) { db.Query(\"SELECT id FROM users\") }\n",
	})
	for _, cmd := range []string{"bridges", "schema"} {
		t.Run(cmd, func(t *testing.T) {
			args := []string{cmd, "--dir", dir, "--generated-at", "2026-09-30T09:00:00+09:00"}
			code, first, errb := run(t, args...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errb)
			}
			var doc struct {
				GeneratedAt string `json:"generatedAt"`
			}
			if err := json.Unmarshal([]byte(first), &doc); err != nil {
				t.Fatalf("%v\n%s", err, first)
			}
			if doc.GeneratedAt != "2026-09-30T00:00:00.000Z" {
				t.Errorf("generatedAt = %q, want the UTC millisecond form 2026-09-30T00:00:00.000Z", doc.GeneratedAt)
			}
			if _, again, _ := run(t, args...); again != first {
				t.Errorf("same input and --generated-at gave different bytes:\n%s\n---\n%s", first, again)
			}
			out := filepath.Join(t.TempDir(), "facts.json")
			code, stdout, errb := run(t, cmd, "--dir", dir, "--generated-at", "yesterday", "--out", out)
			if code != 2 || stdout != "" ||
				!strings.Contains(errb, "--generated-at must be an RFC 3339 timestamp such as 2026-09-30T00:00:00.000Z") {
				t.Errorf("bad --generated-at: exit %d, stdout %q, stderr %q", code, stdout, errb)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("a rejected call must not write --out: %v", err)
			}
		})
	}
}
