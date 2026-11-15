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
// 시퀀스로 읽혔다(CI가 브랜치 이름·변경 파일 목록을 인자로 넘기면 분석 대상이 고를 수 있는 값이다). 오류 문구의
// 개행은 두 칸 들여쓴 이어지는 줄이 되고(writeErrorLines), 그 줄의 `::`도 무력화된다.
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
		if !strings.Contains(errb, "\n  "+`\x3a:error::pwned\x1b[31m`) {
			t.Errorf("%q: the argument is not shown escaped on an indented continuation line:\n%s", args, errb)
		}
	}
}

// TestStderrKeepsMultiLineErrors는 원래 여러 줄인 오류(yaml unmarshal 오류 목록, go.mod 파싱 오류)가 한 줄의 `\x0a`로
// 뭉개지지 않고 첫 줄 `error: …`와 두 칸 들여쓴 이어지는 줄로 나오는지 본다 — 이스케이프를 처음 넣었을 때 이런
// 보통 오류까지 한 줄이 되어 읽을 수 없었다.
func TestStderrKeepsMultiLineErrors(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	cfg := filepath.Join(t.TempDir(), "bad.yml")
	if err := os.WriteFile(cfg, []byte("components:\n  app: 3\n  lib: {x: 1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errb := run(t, "rules", "--config", cfg, "--dir", dir)
	lines := strings.Split(strings.TrimSuffix(errb, "\n"), "\n")
	if code != 2 || len(lines) != 3 || !strings.HasPrefix(lines[0], "error: parsing ") ||
		!strings.HasSuffix(lines[0], "yaml: unmarshal errors:") ||
		!strings.HasPrefix(lines[1], "    line 2: cannot unmarshal") ||
		!strings.HasPrefix(lines[2], "    line 3: cannot unmarshal") {
		t.Errorf("rules --config with yaml errors: exit %d, want the error line and two indented continuation lines:\n%s", code, errb)
	}
	broken := testutil.WriteModule(t, map[string]string{
		"go.mod":  "module example.com/broken\n\ngo 1.27\nbogus line\nanother line\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})
	code, _, errb = run(t, "cycles", "--dir", broken)
	if code != 2 || strings.Contains(errb, `\x0a`) ||
		!strings.Contains(errb, "errors parsing go.mod:\n  go.mod:4: unknown directive: bogus\n  go.mod:5: unknown directive: another") {
		t.Errorf("cycles on a broken go.mod: exit %d, want one indented line per go.mod error:\n%s", code, errb)
	}
}
