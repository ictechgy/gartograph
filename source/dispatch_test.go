package source

import (
	"slices"
	"strings"
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

// anonDispatchFixture는 의존 모듈(replace로 붙인 example.com/dep)과 std errors가
// 이름 없는 인터페이스로만 부르는 메서드를 모은 모듈이다. 봉인(임베드 승격된
// 비공개 메서드)·패키지 스코프 별칭·파라미터 이름만 다른 두 표기를 담고,
// 대조군으로 시그니처 불일치(Wrong)·타입 파라미터(Gener)·함수 안 타입에
// 가려진 이름(Shadowed)·타입 제약 전용 인터페이스(Constrained)를 담는다.
func anonDispatchFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/anonfix\n\ngo 1.27\n\n" +
			"require example.com/dep v0.0.0\n\nreplace example.com/dep => ./dep\n",
		"dep/go.mod": "module example.com/dep\n\ngo 1.27\n",
		"dep/dep.go": `package dep

import "time"

type Token int

func Close(x any) {
	if c, ok := x.(interface{ CloseNow() error }); ok {
		_ = c.CloseNow()
	}
}

func Deadline(x any) {
	switch v := x.(type) {
	case interface{ SetDeadline(time.Time) error }:
		_ = v.SetDeadline(time.Time{})
	}
}

func Flush(f interface{ FlushAll() }) { f.FlushAll() }

func Cancel(x any) {
	type canceler interface{ CancelIt() }
	if c, ok := x.(canceler); ok {
		c.CancelIt()
	}
}

func Use(x any) {
	if u, ok := x.(interface{ UseToken(Token) }); ok {
		u.UseToken(0)
	}
}

func Private(x any) {
	if p, ok := x.(interface{ secret() }); ok {
		p.secret()
	}
}

func Gen[T any](x any) {
	if g, ok := x.(interface{ GenIt() T }); ok {
		_ = g.GenIt()
	}
}

type Base struct{}

func (Base) sealed() {}

func Seal(x any) {
	if s, ok := x.(interface {
		sealed()
		Hook()
	}); ok {
		s.Hook()
	}
}

type Aliased = interface{ AliasCall() }

func Alias(x any) {
	if a, ok := x.(Aliased); ok {
		a.AliasCall()
	}
}

func Named1(x any) {
	if h, ok := x.(interface{ Hand(ctx string) error }); ok {
		_ = h.Hand("")
	}
}

func Named2(x any) {
	if h, ok := x.(interface{ Hand(string) error }); ok {
		_ = h.Hand("")
	}
}

func Shadow(x any) {
	type Token string
	if s, ok := x.(interface{ UseShadow(Token) }); ok {
		s.UseShadow("")
	}
}

func Sum[T interface {
	~int
	Constrain()
}](v T) {
	v.Constrain()
}
`,
		"main.go": `package main

import (
	"errors"
	"time"

	"example.com/dep"
)

type Closer struct{}

func (Closer) CloseNow() error { return nil }

type Conn struct{}

func (*Conn) SetDeadline(time.Time) error { return nil }

type Wrong struct{}

func (Wrong) SetDeadline(int) error { return nil }

type Flusher struct{}

func (Flusher) FlushAll() {}

type Canc struct{}

func (Canc) CancelIt() {}

type User struct{}

func (User) UseToken(dep.Token) {}

type Gener struct{}

func (Gener) GenIt() int { return 0 }

type Mine struct{ dep.Base }

func (Mine) Hook() {}

type Al struct{}

func (Al) AliasCall() {}

type Hander struct{}

func (Hander) Hand(string) error { return nil }

type Shadowed struct{}

func (Shadowed) UseShadow(dep.Token) {}

type Constrained int

func (Constrained) Constrain() {}

type WrapErr struct{ inner error }

func (w WrapErr) Error() string { return "wrap" }
func (w WrapErr) Unwrap() error { return w.inner }

func main() {
	dep.Close(Closer{})
	dep.Deadline(&Conn{})
	dep.Deadline(Wrong{})
	dep.Flush(Flusher{})
	dep.Cancel(Canc{})
	dep.Use(User{})
	dep.Gen[int](Gener{})
	dep.Seal(Mine{})
	dep.Alias(Al{})
	dep.Named1(Hander{})
	dep.Named2(Hander{})
	dep.Shadow(Shadowed{})
	dep.Sum(Constrained(0))
	_ = errors.Is(WrapErr{}, nil)
}
`,
	})
}

// TestAnonymousInterfaceDispatchFacts는 의존 소스의 이름 없는 인터페이스
// (타입 단언·type switch·인자 타입·함수 안 선언)를 구현한 메서드에 Satisfies가
// 실리는지 확인한다. 이름은 go/types의 정규 표기(패키지 전체 경로)다.
func TestAnonymousInterfaceDispatchFacts(t *testing.T) {
	doc := loadSymbol(t, anonDispatchFixture(t))
	const m = "example.com/anonfix"
	cases := []struct{ id, iface, receiver string }{
		{m + ".(Closer).CloseNow", "interface{CloseNow() error}", m + ".Closer"},
		{m + ".(Conn).SetDeadline", "interface{SetDeadline(time.Time) error}", m + ".Conn"},
		{m + ".(Flusher).FlushAll", "interface{FlushAll()}", m + ".Flusher"},
		{m + ".(Canc).CancelIt", "interface{CancelIt()}", m + ".Canc"},
		{m + ".(User).UseToken", "interface{UseToken(example.com/dep.Token)}", m + ".User"},
		{m + ".(WrapErr).Unwrap", "interface{Unwrap() error}", m + ".WrapErr"},
		// 봉인: 비공개 메서드는 임베드한 dep.Base에서 승격된다 — 밖에서도 구현된다.
		{m + ".(Mine).Hook", "interface{Hook(); example.com/dep.sealed()}", m + ".Mine"},
		{m + ".(Al).AliasCall", "interface{AliasCall()}", m + ".Al"},
		{m + ".(Hander).Hand", "interface{Hand(string) error}", m + ".Hander"},
	}
	for _, c := range cases {
		v, ok := doc.VertexByID(c.id)
		if !ok {
			t.Fatalf("missing vertex %s", c.id)
		}
		if !slices.Contains(v.Satisfies, c.iface) || v.Receiver != c.receiver {
			t.Fatalf("%s: expected satisfies %q with receiver %s, got %v %q",
				c.id, c.iface, c.receiver, v.Satisfies, v.Receiver)
		}
		if !slices.IsSorted(v.Satisfies) {
			t.Fatalf("%s: satisfies must be sorted, got %v", c.id, v.Satisfies)
		}
	}
	// 파라미터 이름만 다른 두 표기는 같은 인터페이스다 — 이름이 하나여야 한다.
	hander, _ := doc.VertexByID(m + ".(Hander).Hand")
	if len(hander.Satisfies) != 1 {
		t.Fatalf("parameter names must not split one interface into two names, got %v", hander.Satisfies)
	}
	// 대조군 — 시그니처 불일치, 타입 파라미터(T는 모듈 메서드가 적을 수 없다),
	// 함수 안 Token에 가려진 이름(dep.Token이 아니다), 타입 제약 전용 인터페이스.
	for _, id := range []string{
		m + ".(Wrong).SetDeadline", m + ".(Gener).GenIt",
		m + ".(Shadowed).UseShadow", m + ".(Constrained).Constrain",
	} {
		v, _ := doc.VertexByID(id)
		if len(v.Satisfies) != 0 {
			t.Fatalf("%s must carry no dispatch facts, got %v", id, v.Satisfies)
		}
	}
	if !doc.AnonymousDispatch {
		t.Fatal("symbol harvest must mark that anonymous-interface facts were harvested")
	}
	if slices.ContainsFunc(doc.Limitations, func(l string) bool {
		return strings.Contains(l, "no syntax or type information")
	}) {
		t.Fatalf("every dependency here has type information, got %v", doc.Limitations)
	}
}
