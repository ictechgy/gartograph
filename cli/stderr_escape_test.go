package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestStderrEscapesUserArguments는 stderr 오류 문구가 사용자 인자(경로·ID·플래그 이름)와 저장 문서의 값을 텍스트
// 출력과 같은 규칙(textSafe·neutralizeWorkflowCommand)으로 거르는지 본다. 예전에는 `error: reading <경로>: open
// <경로>: …`처럼 인자를 그대로 내서, 인자 속 개행 뒤 `::error::`가 GitHub Actions 워크플로 명령으로, ESC가 터미널
// 시퀀스로 읽혔다(CI가 브랜치 이름·변경 파일 목록을 인자로 넘기면 분석 대상이 고를 수 있는 값이다).
func TestStderrEscapesUserArguments(t *testing.T) {
	const bad = "nope\n::error::pwned\x1b[31m"
	dir := testutil.WriteModule(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	// 레벨에 같은 문자열을 담은 저장 문서 — metrics가 "document is <레벨> level"로 되돌려 쓰던 값이다.
	saved := filepath.Join(t.TempDir(), "doc.json")
	doc := `{"version": 2, "tool": "gartograph", "level": "x\n::error::pwned\u001b[31m", "root": "/", "vertices": [], "edges": []}`
	if err := os.WriteFile(saved, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"dead", "--graph", bad},
		{"diff", bad, bad},
		{"rules", "--config", bad, "--dir", dir},
		{"rules", "--dir", bad},
		{"query", bad, "--dir", dir},
		{"dead", "--dir", dir, "--baseline", bad},
		{"routes", "--role", "client", "--wrappers", bad, "--dir", dir},
		{"dead", "--" + bad},
		{"metrics", "--graph", saved},
	} {
		code, _, errb := run(t, args...)
		if code == 0 {
			t.Errorf("%q: exit 0, want an error", args)
			continue
		}
		if strings.ContainsRune(errb, '\x1b') {
			t.Errorf("%q: stderr carries a raw ESC:\n%q", args, errb)
		}
		for _, line := range strings.Split(errb, "\n") {
			if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "::") {
				t.Errorf("%q: stderr line reads as a workflow command: %q", args, line)
			}
		}
		if !strings.Contains(errb, `\x0a::error::pwned\x1b[31m`) {
			t.Errorf("%q: the argument is not shown escaped on its error line:\n%s", args, errb)
		}
	}
}
