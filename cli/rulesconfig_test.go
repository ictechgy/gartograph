package cli

import (
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestRulesRejectsUnknownConfigKeys는 규칙 파일의 오타 키가 종료 2인지 확인한다. 예전에는 yaml이 모르는 키를
// 버려 `forbiden:`(forbidden 오타)가 금지 규칙을 통째로 끄고도 `rules --strict`가 "0 violations"로 0을
// 돌려줬다 — 같은 설정에서 철자를 고치면 위반 1건으로 1이 나온다. 모든 수확 명령이 exclude를 읽으려고 같은
// 파일을 열므로 `exclud:`도 graph를 종료 2로 멈춘다(빼려던 패키지를 아무 표시 없이 수확하지 않는다).
func TestRulesRejectsUnknownConfigKeys(t *testing.T) {
	files := map[string]string{
		"app/app.go": "package app\n\nimport _ \"example.com/fixture/mid\"\n",
		"mid/mid.go": "package mid\n\nimport _ \"example.com/fixture/db\"\n",
		"db/db.go":   "package db\n",
	}
	const rules = "components: {app: [app], mid: [mid], db: [db]}\ndeps: {app: [mid], mid: [db], db: []}\n"
	files[".gartograph.yml"] = rules + "forbidden:\n  - {from: app, to: db}\n"
	dir := testutil.WriteModule(t, files)
	if code, out, errb := run(t, "rules", "--dir", dir, "--strict"); code != 1 ||
		!strings.Contains(out, "violation[forbidden]") {
		t.Fatalf("spelled forbidden: want exit 1 with the violation, got %d\n%s%s", code, out, errb)
	}

	files[".gartograph.yml"] = rules + "forbiden:\n  - {from: app, to: db}\n"
	dir = testutil.WriteModule(t, files)
	code, out, errb := run(t, "rules", "--dir", dir, "--strict")
	if code != 2 || out != "" || !strings.Contains(errb, `line 3: unknown key "forbiden"`) ||
		!strings.Contains(errb, "forbidden") {
		t.Fatalf("misspelled forbidden: want exit 2 naming the key, got %d\nstdout:\n%s\nstderr:\n%s", code, out, errb)
	}

	files[".gartograph.yml"] = rules + "exclud: [db]\n"
	dir = testutil.WriteModule(t, files)
	if code, _, errb := run(t, "graph", "--dir", dir); code != 2 || !strings.Contains(errb, `unknown key "exclud"`) {
		t.Fatalf("misspelled exclude: want graph to exit 2 naming the key, got %d\n%s", code, errb)
	}
}
