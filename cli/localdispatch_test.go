package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// optionalInterfaceModule은 선택적 인터페이스 단언과 type switch의 이름 없는 case로만 불리는
// 메서드를 담는다. 검토에서 기본 CHA dead가 두 메서드를 unreachable로 보고하고 --strict가 실패하던
// 재현 입력(Validate·Describe)이다. Unused는 대조군 — 아무 인터페이스도 부르지 않는다.
func optionalInterfaceModule(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"main.go": `package main

type createUser struct{ Name string }

func (c createUser) Validate() error {
	if c.Name == "" {
		return nil
	}
	return nil
}

func (createUser) Unused() {}

type audit struct{}

func (audit) Describe() string { return "audit" }

func handle(req any) error {
	if v, ok := req.(interface{ Validate() error }); ok {
		return v.Validate()
	}
	return nil
}

func describe(x any) string {
	switch d := x.(type) {
	case interface{ Describe() string }:
		return d.Describe()
	}
	return ""
}

func main() {
	_ = handle(createUser{})
	_ = describe(audit{})
}
`,
	})
}

// TestDeadModuleLocalInterfaceDispatch는 선택적 인터페이스 관용구로만 불리는 메서드와 그 메서드가
// 읽는 필드를 CHA와 RTA 모두 살리는지, 아무도 부르지 않는 메서드는 CHA에서 계속 보고하는지 본다.
// RTA의 필드 판정은 CHA 그래프 도달성을 쓰므로 Name은 팬아웃 간선이 생겨야 살아난다.
func TestDeadModuleLocalInterfaceDispatch(t *testing.T) {
	dir := optionalInterfaceModule(t)
	for _, algo := range []string{"cha", "rta"} {
		_, out, errb := run(t, "dead", "--dir", dir, "--algo", algo)
		for _, alive := range []string{"(createUser).Validate", "(audit).Describe", "(createUser).Name"} {
			if strings.Contains(out, alive) {
				t.Errorf("%s: %s is called through a module-local interface and must be reachable:\n%s%s",
					algo, alive, out, errb)
			}
		}
		// RTA는 인터페이스로 변환된 타입(createUser → any)의 메서드를 보수적으로 살리므로
		// 대조군은 CHA에서만 본다 — CHA 팬아웃이 단언한 메서드 이름 밖으로 퍼지지 않는지가 요점이다.
		if algo == "cha" && !strings.Contains(out, "(createUser).Unused") {
			t.Errorf("%s: a method no interface calls must still be reported:\n%s", algo, out)
		}
	}
}

// TestDeadOldDocumentWithoutModuleLocalDispatch는 모듈 안 이름 없는 인터페이스 팬아웃 이전 문서로
// dead를 돌리면 메서드 보고에 재수확 권고가 붙는지 확인한다 — 그 문서에는 팬아웃 간선이 없다.
func TestDeadOldDocumentWithoutModuleLocalDispatch(t *testing.T) {
	dir := optionalInterfaceModule(t)
	code, out, errb := run(t, "graph", "--level", "symbol", "--format", "json", "--dir", dir)
	if code != 0 {
		t.Fatalf("graph failed: %d %s", code, errb)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["moduleLocalDispatch"] != true {
		t.Fatalf("symbol harvest must mark module-local dispatch: %v", doc["moduleLocalDispatch"])
	}
	const note = "lacks the moduleLocalDispatch marker"
	fresh := filepath.Join(t.TempDir(), "new.json")
	raw, _ := json.Marshal(doc)
	if err := os.WriteFile(fresh, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := run(t, "dead", "--graph", fresh); strings.Contains(out, note) {
		t.Fatalf("a current document must not get the re-harvest note: %s", out)
	}
	delete(doc, "moduleLocalDispatch")
	old := filepath.Join(t.TempDir(), "old.json")
	raw, _ = json.Marshal(doc)
	if err := os.WriteFile(old, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := run(t, "dead", "--graph", old); !strings.Contains(out, note) {
		t.Fatalf("an old document with method findings must get the re-harvest note: %s", out)
	}
}
