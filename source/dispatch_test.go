package source

import (
	"slices"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// dispatchFixture는 모듈 밖 인터페이스로만 불리는 메서드를 모은 모듈이다.
// flag.Value(fs.Var), error(반환값), encoding.TextUnmarshaler(json이 부름),
// 임베딩 승격 fmt.Stringer, 그리고 대조군(외부 인터페이스와 무관한 메서드)을 담는다.
func dispatchFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/dispfix\n\ngo 1.27\n",
		"main.go": `package main

import (
	"encoding/json"
	"flag"

	"example.com/dispfix/lib"
)

func main() {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	var p lib.Patterns
	fs.Var(&p, "pattern", "repeatable")
	var cfg lib.Config
	_ = json.Unmarshal([]byte("{}"), &cfg)
	_, _ = lib.Parse("x")
	_ = lib.Wrapper{}
}
`,
		"lib/lib.go": `package lib

type Patterns []string

func (p *Patterns) Set(v string) error { *p = append(*p, v); return nil }
func (p *Patterns) String() string     { return "" }
func (p *Patterns) Helper()            {}

type Level int

func (l *Level) UnmarshalText(b []byte) error { *l = Level(len(b)); return nil }

type Config struct{ Level Level }

type ParseError struct{ Input string }

func (e *ParseError) Error() string { return quote(e.Input) }

func quote(s string) string { return "'" + s + "'" }

func Parse(s string) (int, error) { return 0, &ParseError{Input: s} }

type Named struct{}

func (Named) String() string { return "named" }

type Wrapper struct{ Named }

type Orphan struct{}

func (Orphan) String() string { return "orphan" }

type Real struct{}

type Al = Real

func (Al) Error() string { return "al" }

type Box[T any] struct{ V T }

func (b Box[T]) String() string { return "box" }
`,
	})
}

// TestExternalDispatchFacts는 외부 인터페이스를 구현한 메서드 정점에
// Satisfies·Receiver 사실이 실리고, 무관한 메서드에는 실리지 않는지 확인한다.
func TestExternalDispatchFacts(t *testing.T) {
	doc := loadSymbol(t, dispatchFixture(t))
	const lib = "example.com/dispfix/lib"
	cases := []struct {
		id, iface, receiver string
	}{
		{lib + ".(Patterns).Set", "flag.Value", lib + ".Patterns"},
		{lib + ".(Patterns).String", "fmt.Stringer", lib + ".Patterns"},
		{lib + ".(Level).UnmarshalText", "encoding.TextUnmarshaler", lib + ".Level"},
		{lib + ".(ParseError).Error", "error", lib + ".ParseError"},
		// 승격 메서드는 선언 타입(Named) 아래에 사실이 실린다.
		{lib + ".(Named).String", "fmt.Stringer", lib + ".Named"},
		// 별칭 리시버는 실체 타입, 제네릭 리시버는 원형 타입 정점을 가리킨다.
		{lib + ".(Real).Error", "error", lib + ".Real"},
		{lib + ".(Box).String", "fmt.Stringer", lib + ".Box"},
	}
	for _, c := range cases {
		v, ok := doc.VertexByID(c.id)
		if !ok {
			t.Fatalf("missing vertex %s", c.id)
		}
		if !slices.Contains(v.Satisfies, c.iface) {
			t.Fatalf("%s: expected satisfies %s, got %v", c.id, c.iface, v.Satisfies)
		}
		if !slices.IsSorted(v.Satisfies) {
			t.Fatalf("%s: satisfies must be sorted for determinism, got %v", c.id, v.Satisfies)
		}
		if v.Receiver != c.receiver {
			t.Fatalf("%s: expected receiver %s, got %q", c.id, c.receiver, v.Receiver)
		}
	}
	helper, _ := doc.VertexByID(lib + ".(Patterns).Helper")
	if len(helper.Satisfies) != 0 || helper.Receiver != "" {
		t.Fatalf("unrelated method must carry no dispatch facts, got %+v", helper)
	}
}

// TestExternalDispatchAddsNoEdges는 외부 디스패치가 문서 간선이 아니라
// 정점 사실로만 남는지 확인한다 — 타입→메서드 간선은 메서드→리시버
// references와 맞물려 cycles에 가짜 2-순환을 만든다.
func TestExternalDispatchAddsNoEdges(t *testing.T) {
	doc := loadSymbol(t, dispatchFixture(t))
	const lib = "example.com/dispfix/lib"
	for _, e := range doc.Edges {
		if e.From == lib+".Patterns" && e.To == lib+".(Patterns).Set" {
			t.Fatalf("external dispatch must not become an edge: %+v", e)
		}
	}
}

// TestExternalDispatchTypeLevel은 타입 레벨 문서에 메서드 정점이 없어
// 디스패치 사실을 싣지 않는지 확인한다.
func TestExternalDispatchTypeLevel(t *testing.T) {
	doc, err := Load(Options{Dir: dispatchFixture(t), Level: graph.LevelType})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range doc.Vertices {
		if len(v.Satisfies) != 0 {
			t.Fatalf("type-level document must carry no dispatch facts, got %+v", v)
		}
	}
}
