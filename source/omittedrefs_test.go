package source

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// omittedRefsFixture는 모듈 상수·함수와 모듈 밖 타입을 함께 참조하는 두 패키지 모듈이다. type 레벨에서 lib.N은
// 상수라 정점이 없고, --pattern .이면 lib 패키지 전체가 심볼 정점이 없다 — 둘 다 모듈 안 심볼이다. var z =
// lib.G(three)는 호출을 실행하고 모듈 상수를 써서 초기화 루트(pkg._)도 같은 식을 훑는다 — 그 순회는 다시 세지 않는다.
func omittedRefsFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"lib/lib.go": "package lib\n\nconst N = 2\n\nfunc F() {}\n\nfunc G(n int) int { return n }\n\n" +
			"type T struct{}\n\nfunc (T) M() {}\n",
		"main.go": "package main\n\nimport (\n\t\"io\"\n\n\t\"example.com/fixture/lib\"\n)\n\n" +
			"type A [lib.N]int\n\ntype R struct{ io.Reader }\n\nconst three = 3\n\nvar z = lib.G(three)\n\n" +
			"func main() {\n\tlib.F()\n\tlib.T{}.M()\n\tvar a A\n\tvar r R\n\t_, _, _ = a, r, z\n}\n",
	})
}

// omittedCounts는 문서의 두 생략 참조 수(모듈 밖, 정점 없는 모듈 심볼)다. 문장이 없으면 0이다.
func omittedCounts(t *testing.T, doc *graph.Document) (outside, module int) {
	t.Helper()
	for _, l := range doc.Limitations {
		var n int
		switch {
		case strings.Contains(l, "references to symbols outside the module were omitted"):
			if _, err := fmt.Sscanf(l, "%d references", &n); err != nil {
				t.Fatalf("unparsable limitation %q: %v", l, err)
			}
			outside = n
		case strings.Contains(l, "references to module symbols that have no vertex in this document were omitted"):
			if _, err := fmt.Sscanf(l, "%d references", &n); err != nil {
				t.Fatalf("unparsable limitation %q: %v", l, err)
			}
			module = n
		}
	}
	return outside, module
}

// TestOmittedModuleReferencesAreNotOutside는 정점이 없어 긋지 못한 참조를 대상 패키지의 모듈 소속으로 가르는지
// 확인한다. 예전에는 type 레벨의 모듈 상수(type A [lib.N]int의 lib.N)와 --pattern 밖 모듈 패키지의 심볼(lib.F)을
// "references to symbols outside the module"로 셌다 — 모듈 안 심볼이라 거짓 문장이었다. 이제 그 참조는 정점 없는
// 모듈 심볼 수로 따로 세고, 모듈 밖 수에는 io.Reader(embeds·references 두 사실)만 남는다.
func TestOmittedModuleReferencesAreNotOutside(t *testing.T) {
	dir := omittedRefsFixture(t)
	cases := []struct {
		name            string
		opts            Options
		outside, module int
	}{
		// type 레벨: lib.N 하나가 모듈 심볼(상수 정점 없음), io.Reader 임베드가 embeds·references 둘이다.
		{"type level", Options{Dir: dir, Level: graph.LevelType}, 2, 1},
		// symbol 레벨, 모든 패키지: 모듈 심볼은 전부 정점이라 모듈 쪽 수가 없다.
		{"symbol level", Options{Dir: dir, Level: graph.LevelSymbol}, 2, 0},
		// --pattern .: lib은 패키지 정점만 있고 심볼 정점이 없다 — lib.N 참조, lib.F·lib.G 호출(각각 call·references),
		// lib.T 참조와 메서드 호출 lib.T{}.M()(call·references — 간선 쪽 계수는 정점 ID의 패키지 경로로 가른다)이 모듈
		// 쪽이다. 초기화 루트의 lib.G 재방문은 세지 않는다.
		{"pattern outside lib", Options{Dir: dir, Level: graph.LevelSymbol, Patterns: []string{"."}}, 2, 8},
	}
	for _, c := range cases {
		doc, err := Load(c.opts)
		if err != nil {
			t.Fatal(err)
		}
		outside, module := omittedCounts(t, doc)
		if outside != c.outside || module != c.module {
			t.Errorf("%s: outside=%d module=%d, want outside=%d module=%d (limitations %q)",
				c.name, outside, module, c.outside, c.module, doc.Limitations)
		}
	}
}
