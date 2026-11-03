package source

import (
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// localDispatchFixture는 패키지 스코프에 이름이 없는 모듈 인터페이스로만 불리는 메서드를 모은다.
// 선택적 인터페이스 단언(v, ok := x.(interface{ M() })), type switch의 이름 없는 case,
// 함수 안에 선언한 인터페이스, 이름 없는 제네릭 제약, 그 메서드 값, 비공개 메서드를 담는다.
// 외부 import가 없어 "모듈 밖 참조 생략" limitation이 생기면 안 된다 — 모듈 안 인터페이스
// 메서드를 모듈 밖 참조로 세던 회귀를 함께 잡는다.
func localDispatchFixture(t *testing.T) string {
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

type audit struct{}

func (audit) Describe() string { return "audit" }

type local struct{}

func (local) Check() bool { return true }

type pinger struct{}

func (pinger) Ping() {}

type hidden struct{}

func (hidden) secret() int { return 1 }

type unrelated struct{}

func (unrelated) Other() {}

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

func check(x any) bool {
	type checker interface{ Check() bool }
	if c, ok := x.(checker); ok {
		return c.Check()
	}
	return false
}

func call[T interface{ Ping() }](x T) { x.Ping() }

func methodValue(req any) func() error {
	if v, ok := req.(interface{ Validate() error }); ok {
		return v.Validate
	}
	return nil
}

func reveal(x any) int {
	if s, ok := x.(interface{ secret() int }); ok {
		return s.secret()
	}
	return 0
}

func main() {
	_ = handle(createUser{})
	_ = describe(audit{})
	_ = check(local{})
	call(pinger{})
	_ = methodValue(createUser{})
	_ = reveal(hidden{})
	_ = unrelated{}
}
`,
	})
}

// TestModuleLocalInterfaceDispatch는 패키지 스코프에 정점이 없는 모듈 인터페이스의 메서드 호출도
// CHA로 구현 메서드에 후보 간선을 긋는지 본다. 간선이 없으면 dead가 선택적 인터페이스 관용구로만
// 불리는 살아 있는 메서드를 unreachable로 보고하고 --strict가 실패한다.
func TestModuleLocalInterfaceDispatch(t *testing.T) {
	doc, err := Load(Options{Dir: localDispatchFixture(t), Level: graph.LevelSymbol})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	const m = "example.com/fixture"
	cases := []struct {
		from, to string
		kind     graph.EdgeKind
	}{
		{m + ".handle", m + ".(createUser).Validate", graph.EdgeCall},
		{m + ".describe", m + ".(audit).Describe", graph.EdgeCall},
		{m + ".check", m + ".(local).Check", graph.EdgeCall},
		{m + ".call", m + ".(pinger).Ping", graph.EdgeCall},
		{m + ".methodValue", m + ".(createUser).Validate", graph.EdgeReferences},
		{m + ".reveal", m + ".(hidden).secret", graph.EdgeCall},
	}
	for _, c := range cases {
		e, ok := findEdge(doc, c.from, c.to, c.kind)
		if !ok {
			t.Errorf("missing %s edge %s -> %s", c.kind, c.from, c.to)
			continue
		}
		if !e.Candidate {
			t.Errorf("%s -> %s must be a CHA candidate edge", c.from, c.to)
		}
	}
	for _, from := range []string{m + ".handle", m + ".describe", m + ".check", m + ".call"} {
		if _, ok := findEdge(doc, from, m+".(unrelated).Other", graph.EdgeCall); ok {
			t.Errorf("%s must not fan out to a type without the asserted method", from)
		}
	}
	for _, l := range doc.Limitations {
		if strings.Contains(l, "outside the module") {
			t.Errorf("module-local interface methods must not be counted as outside references: %q", l)
		}
	}
}
