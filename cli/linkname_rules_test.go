package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestLinknameDependencyRunsOneWay는 //go:linkname이 본문 없는 선언 → 본문 한 방향 의존만 만드는지
// rules와 query로 본다. 검토에서 양방향 후보 간선 때문에 forbidden {from: low, to: high} 규칙이
// low.hidden -> high.pulled 거짓 위반(exit 1)을 냈고, query low.hidden이 high.pulled에 의존한다고 답했다.
// 실제로는 high가 low의 구현을 당겨 쓴다.
func TestLinknameDependencyRunsOneWay(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "example.com/fixture/high"

func main() { high.Run() }
`,
		"high/high.go": `package high

import (
	_ "unsafe"

	_ "example.com/fixture/low"
)

//go:linkname pulled example.com/fixture/low.hidden
func pulled() int

func Run() { _ = pulled() }
`,
		"high/empty.s": "",
		"low/low.go": `package low

func hidden() int { return 42 }
`,
	})
	saved := filepath.Join(t.TempDir(), "g.json")
	if code, _, errb := run(t, "graph", "--dir", dir, "--level", "symbol", "--out", saved); code != 0 {
		t.Fatalf("graph: %d %s", code, errb)
	}
	// 묶음은 확정 호출이 아니라 후보 관계로 남아 순환 근거에서 빠진다 — 그 수를 CHA 팬아웃과 구분해 알린다.
	code, cyclesOut, errb := run(t, "cycles", "--graph", saved, "--format", "json")
	if code != 0 || !strings.Contains(cyclesOut,
		"1 candidate dependency relations (CHA interface-dispatch fan-out or //go:linkname bindings)") {
		t.Errorf("cycles must count the linkname binding as a candidate relation: %d %s%s", code, cyclesOut, errb)
	}
	cfg := filepath.Join(t.TempDir(), "rules.yml")
	rules := "components:\n  low: [\"low\"]\n  high: [\"high\"]\n  app: [\".\"]\n" +
		"deps:\n  app: [high]\n  high: [low]\nforbidden:\n  - {from: low, to: high}\n"
	if err := os.WriteFile(cfg, []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errb := run(t, "rules", "--graph", saved, "--config", cfg, "--strict"); code != 0 {
		t.Errorf("low does not depend on high through the linkname; rules must pass: %d\n%s%s", code, out, errb)
	}
	code, out, errb := run(t, "query", "--graph", saved, "example.com/fixture/low.hidden")
	if code != 0 {
		t.Fatalf("query: %d %s", code, errb)
	}
	var q struct {
		DependsOn []struct {
			ID string `json:"id"`
		} `json:"dependsOn"`
		DependedBy []struct {
			ID string `json:"id"`
		} `json:"dependedBy"`
	}
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		t.Fatal(err)
	}
	for _, n := range q.DependsOn {
		if strings.HasSuffix(n.ID, "high.pulled") {
			t.Errorf("low.hidden must not depend on high.pulled: %s", out)
		}
	}
	found := false
	for _, n := range q.DependedBy {
		found = found || strings.HasSuffix(n.ID, "high.pulled")
	}
	if !found {
		t.Errorf("high.pulled depends on low.hidden through the linkname: %s", out)
	}
}
