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

// TestGeneratedAtRejectsZeroTime는 영(零) 시각 --generated-at이 모든 통로에서 사용법 오류인지 본다. 예전에는
// bridges·schema·routes가 그 값을 "주지 않음"으로 읽어 지금 시각을 조용히 실었고, reach는 그대로 실었다 — 같은
// 값을 두 통로가 다르게 읽었다. 같은 순간을 다른 오프셋으로 적어도(+09:00) 같다.
func TestGeneratedAtRejectsZeroTime(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	for _, zero := range []string{"0001-01-01T00:00:00Z", "0001-01-01T09:00:00+09:00"} {
		for _, c := range []struct {
			args []string
			code int
		}{
			{[]string{"bridges"}, 2},
			{[]string{"schema"}, 2},
			{[]string{"routes"}, 2},
			{[]string{"routes", "--role", "client"}, 2},
			{[]string{"reach", "example.com/fixture.main"}, exitUsage},
		} {
			args := append(append([]string{}, c.args...), "--dir", dir, "--generated-at", zero)
			code, stdout, errb := run(t, args...)
			if code != c.code || stdout != "" || !strings.Contains(errb, generatedAtZeroUsage) {
				t.Errorf("%v: exit %d (want %d), stdout %q, stderr %q", args, code, c.code, stdout, errb)
			}
		}
	}
	// 영 시각 바로 다음 순간은 받는다 — 거부는 "주지 않음"과 겹치는 한 값뿐이다.
	if code, _, errb := run(t, "bridges", "--dir", dir, "--generated-at", "0001-01-01T00:00:00.001Z"); code != 0 {
		t.Errorf("a non-zero early timestamp must be accepted: exit %d %s", code, errb)
	}
}
