package source

import (
	"fmt"
	"go/token"
	"go/types"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestInterfaceMethodsCanonical은 인터페이스 methods 사실이 표기 차이에 흔들리지 않는지
// 확인한다 — 별칭(any·type U = T)·byte/uint8·타입 파라미터 이름이 달라도 같은 항목이고,
// 다른 패키지의 비공개 메서드는 경로로 한정되며, 항목은 정렬된다.
func TestInterfaceMethodsCanonical(t *testing.T) {
	methodsOf := func(src string) []string {
		doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{"a/a.go": src}))
		v, ok := doc.VertexByID("example.com/fixture/a.I")
		if !ok || !doc.InterfaceMethodSets {
			t.Fatalf("interface vertex with method-set marker expected, got %+v", v)
		}
		return v.Methods
	}
	a := methodsOf("package a\n\ntype T struct{}\n\ntype I[X any] interface {\n\tZ(x interface{}) []uint8\n\tGet() X\n\tUse(t T, r rune)\n}\n")
	b := methodsOf("package a\n\ntype T struct{}\n\ntype U = T\n\ntype I[E any] interface {\n\tZ(v any) []byte\n\tGet() E\n\tUse(u U, r int32)\n}\n")
	want := []string{"Get() P0", "Use(example.com/fixture/a.T, int32)", "Z(interface{}) []uint8"}
	if !slices.Equal(a, want) || !slices.Equal(b, want) {
		t.Fatalf("spelling differences must not change entries:\n a %q\n b %q\nwant %q", a, b, want)
	}
	c := methodsOf("package a\n\nimport \"io\"\n\ntype I interface {\n\tio.Reader\n\tseal()\n}\n")
	if !slices.Equal(c, []string{"Read([]uint8) (int, error)", "example.com/fixture/a.seal()"}) {
		t.Fatalf("embedded external methods and qualified unexported methods expected, got %q", c)
	}
}

// TestInterfaceTypeSets는 제약 인터페이스의 타입 원소가 typeSet 사실로 실리는지 확인한다 —
// 원소마다 한 항목, 유니언 항은 정렬된 정규 표기라 순서·별칭(byte/uint8) 차이에 흔들리지
// 않는다. 메서드만 있는 인터페이스는 typeSet이 없다.
func TestInterfaceTypeSets(t *testing.T) {
	doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{"a/a.go": `package a

type Number interface{ ~int64 | ~int }

type Bytes interface {
	~[]byte | string
	Len() int
}

type Plain interface{ Do() }

type signed interface{ ~int | ~int64 }
type float interface{ ~float64 }

type Wrapped interface{ signed }

type Either interface{ signed | float }

type Eq interface {
	comparable
	error
}

// 틸드 없는 단일 항 인터페이스(유니언이 아니라 타입 자체로 기록된다)와 임베드 사슬도
// 유니언 항으로 펼친다.
type myInt interface{ int }
type Single interface{ myInt | ~string }

type integer interface{ signed }
type Chained interface{ integer | ~string }
`}))
	if !doc.InterfaceTypeSets {
		t.Fatal("documents must carry the interfaceTypeSets marker")
	}
	for id, want := range map[string][]string{
		"example.com/fixture/a.Number": {"~int | ~int64"},
		"example.com/fixture/a.Bytes":  {"string | ~[]uint8"},
		"example.com/fixture/a.Plain":  nil,
		// 비공개 제약을 임베드하면 그 원소를 펼친다.
		"example.com/fixture/a.Wrapped": {"~int | ~int64"},
		// 유니언의 인터페이스 항도 펼친다.
		"example.com/fixture/a.Either": {"~float64 | ~int | ~int64"},
		// comparable은 특수 항목, 메서드만 있는 error 임베드는 원소가 없다.
		"example.com/fixture/a.Eq":      {"comparable"},
		"example.com/fixture/a.Single":  {"int | ~string"},
		"example.com/fixture/a.Chained": {"~int | ~int64 | ~string"},
	} {
		v, _ := doc.VertexByID(id)
		if !slices.Equal(v.TypeSet, want) {
			t.Fatalf("%s: typeSet %q, want %q", id, v.TypeSet, want)
		}
	}
}

// abbreviationPattern은 축약 표기의 꼬리다 — 앞부분 뒤에 "…sha256:"과 구조 해시 hex 64자.
var abbreviationPattern = regexp.MustCompile(`…sha256:[0-9a-f]{64}$`)

// maxAbbreviationBytes는 축약 표기 하나의 최대 길이다(앞부분 + 표지 + 해시).
const maxAbbreviationBytes = abbreviationPrefixBytes + len(graph.SpellingDigestMarker) + 64

// aliasChain은 별칭 struct 사슬(A0 = leaf, Ai = struct{ a, b A(i-1) })을 go/types API로 직접 만들고
// 마지막 별칭을 돌려준다. 타입 검사기는 이 사슬의 검증에서 깊이마다 두 배로 느려져(깊이 28에서 약
// 2초) 소스 fixture로는 수확기 비용만 잴 수 없다. 정규 표기는 깊이마다 두 배다(별칭은 이름 경계가
// 아니라 펼쳐진다).
func aliasChain(leaf types.Type, depth int) types.Type {
	pkg := types.NewPackage("example.com/chain", "chain")
	prev := types.Type(types.NewAlias(types.NewTypeName(token.NoPos, pkg, "A0", nil), leaf))
	for i := 1; i <= depth; i++ {
		st := types.NewStruct([]*types.Var{
			types.NewField(token.NoPos, pkg, "a", prev, false),
			types.NewField(token.NoPos, pkg, "b", prev, false),
		}, nil)
		prev = types.NewAlias(types.NewTypeName(token.NoPos, pkg, fmt.Sprintf("A%d", i), nil), st)
	}
	return prev
}

// aliasChainSpelling은 aliasChain의 정규 표기를 문자열로 직접 이어 만든다(작은 깊이 전용 — 길이가
// 깊이마다 두 배다).
func aliasChainSpelling(leaf string, depth int) string {
	s := leaf
	for i := 0; i < depth; i++ {
		s = "struct{a " + s + "; b " + s + "}"
	}
	return s
}

// aliasChainPrefix는 aliasChain 정규 표기의 처음 abbreviationPrefixBytes바이트다. 이어 붙인 문자열의
// 앞부분은 조각들의 앞부분만으로 정해지므로 깊이마다 잘라도 정확하다(깊은 사슬도 잴 수 있다).
func aliasChainPrefix(leaf string, depth int) string {
	s := leaf
	for i := 0; i < depth; i++ {
		s = "struct{a " + s + "; b " + s + "}"
		s = s[:min(len(s), abbreviationPrefixBytes)]
	}
	return s
}

// TestSpellingCapBoundsAliasChains는 별칭 struct 사슬의 정규 표기가 상한에서 축약돼 크기가 묶이는지
// 확인한다 — 표기는 깊이 n에서 17·2^n−14바이트라 축약하지 않으면 깊이 30에서 18GB다. 축약 표기는
// 원래 표기의 처음 abbreviationPrefixBytes바이트에 구조 해시를 붙인 것이고, 계산 비용은 깊이에
// 선형이다(12→24에서 할당이 두 배 남짓 — 메모가 없으면 2^12배). 상한 이하(깊이 11, 약 34KiB)는
// 그대로 적힌다. 순서가 안전판이다: 상한이 없으면 깊이 16에서, 메모가 없으면 할당 비교에서 멈춰
// 깊이 30을 돌지 않는다.
func TestSpellingCapBoundsAliasChains(t *testing.T) {
	if text, abbreviated := newTypeSpeller().canonicalType(aliasChain(types.Typ[types.Int], 11)); abbreviated ||
		text != aliasChainSpelling("int", 11) {
		t.Fatalf("a %d-byte spelling is under the cap and must stay whole, got abbreviated=%v %.200q",
			len(aliasChainSpelling("int", 11)), abbreviated, text)
	}
	bounded := func(depth int) bool {
		return t.Run(fmt.Sprintf("depth%d", depth), func(t *testing.T) {
			text, abbreviated := newTypeSpeller().canonicalType(aliasChain(types.Typ[types.Int], depth))
			if !abbreviated || len(text) > maxAbbreviationBytes || !abbreviationPattern.MatchString(text) {
				t.Fatalf("spelling must be abbreviated to at most %d bytes, got abbreviated=%v len=%d %.300q",
					maxAbbreviationBytes, abbreviated, len(text), text)
			}
			want := aliasChainPrefix("int", depth)
			if len(want) != abbreviationPrefixBytes || !strings.HasPrefix(text, want+graph.SpellingDigestMarker) {
				t.Fatalf("abbreviation must keep the first %d bytes of the spelling %q, got %q",
					abbreviationPrefixBytes, want, text)
			}
		})
	}
	if !bounded(16) {
		return
	}
	allocs := func(depth int) float64 {
		chain := aliasChain(types.Typ[types.Int], depth)
		return testing.AllocsPerRun(3, func() { newTypeSpeller().canonicalType(chain) })
	}
	if small, large := allocs(12), allocs(24); large > 3*small {
		t.Fatalf("spelling cost must be linear in the chain depth: depth 12 %.0f allocs, depth 24 %.0f", small, large)
	}
	bounded(30)
}

// TestSpellingCapBoundary는 상한이 표기 바이트 수에 정확히 걸리는지 확인한다 — maxSpellingBytes
// 바이트는 그대로, 한 바이트 더 길면 축약이다. 길이는 struct 태그로 맞춘다("struct{a int "태그"}"는
// 태그보다 16바이트 길다).
func TestSpellingCapBoundary(t *testing.T) {
	pkg := types.NewPackage("example.com/edge", "edge")
	tagged := func(total int) types.Type {
		tag := strings.Repeat("x", total-16)
		return types.NewStruct([]*types.Var{types.NewField(token.NoPos, pkg, "a", types.Typ[types.Int], false)},
			[]string{tag})
	}
	whole, abbreviated := newTypeSpeller().canonicalType(tagged(maxSpellingBytes))
	if abbreviated || len(whole) != maxSpellingBytes || whole != "struct{a int "+strconv.Quote(strings.Repeat("x", maxSpellingBytes-16))+"}" {
		t.Fatalf("a spelling of exactly %d bytes must stay whole, got abbreviated=%v len=%d", maxSpellingBytes, abbreviated, len(whole))
	}
	over, abbreviated := newTypeSpeller().canonicalType(tagged(maxSpellingBytes + 1))
	if !abbreviated || !strings.HasPrefix(over, whole[:abbreviationPrefixBytes]+graph.SpellingDigestMarker) ||
		!abbreviationPattern.MatchString(over) {
		t.Fatalf("one byte over the cap must abbreviate, got abbreviated=%v %.300q", abbreviated, over)
	}
}

// TestAbbreviationIdentity는 축약이 diff의 문자열 비교 계약을 지키는지 확인한다 — 같은 타입(따로 만든
// 같은 구조, 다른 수확, 별칭과 그 실체, byte와 uint8)은 같은 축약이고, 앞부분이 같아도 어디서든
// 구조가 다르면(맨 안쪽 잎, 맨 바깥 필드 이름) 다른 축약이다.
func TestAbbreviationIdentity(t *testing.T) {
	spell := func(typ types.Type) string {
		text, abbreviated := newTypeSpeller().canonicalType(typ)
		if !abbreviated {
			t.Fatalf("a chain over the cap must be abbreviated, got %.200q", text)
		}
		return text
	}
	spell(aliasChain(types.Typ[types.Int], 16)) // 상한(깊이 12)을 넘는 작은 사슬부터 — 축약이 없으면 깊이 30은 메모리를 다 쓴다
	base := spell(aliasChain(types.Typ[types.Int], 30))
	shared := newTypeSpeller()
	chain := aliasChain(types.Typ[types.Int], 30)
	again, _ := shared.canonicalType(chain)
	actual, _ := shared.canonicalType(types.Unalias(chain))
	if again != base || actual != base {
		t.Fatalf("the same type must get the same abbreviation:\n %q\n %q\n %q", base, again, actual)
	}
	if spell(aliasChain(types.Typ[types.Byte], 30)) != spell(aliasChain(types.Typ[types.Uint8], 30)) {
		t.Fatal("byte and uint8 spell the same, so their abbreviations must match")
	}
	pkg := types.NewPackage("example.com/chain", "chain")
	inner := aliasChain(types.Typ[types.Int], 29)
	renamed := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, pkg, "a", inner, false),
		types.NewField(token.NoPos, pkg, "c", inner, false),
	}, nil)
	prefix := base[:abbreviationPrefixBytes]
	for name, typ := range map[string]types.Type{
		"innermost leaf":       aliasChain(types.Typ[types.Int64], 30),
		"outermost field name": renamed,
	} {
		other := spell(typ)
		if !strings.HasPrefix(other, prefix) || other == base {
			t.Fatalf("%s: different types must differ in the hash even with the same prefix:\n %q\n %q", name, base, other)
		}
	}
}

// TestSpellingCycleGuard는 타입 그래프에 순환이 있어도 표기가 유한하게 끝나는지 확인한다. go/types는
// 순환 별칭을 invalid type으로 끊고 공개 API로는 순환 타입을 만들 수 없어 표기 조각 함수를 바꿔
// 끼운다. 계산 중인 타입을 다시 만나면 끝없는 표기로 보고 결정적으로 축약해야 하고, 조각 함수는 길이·
// 해시를 잴 때 타입마다 한 번, 앞부분을 펼 때 앞부분 바이트만큼만 불린다. 조각 함수의 안전판은 가드가
// 없을 때 순회를 끊어 테스트가 멈추거나 메모리를 다 쓰지 않고 실패하게 한다.
func TestSpellingCycleGuard(t *testing.T) {
	x, y := types.Typ[types.Int], types.Typ[types.String]
	spell := func() (string, bool, int) {
		s := newTypeSpeller()
		calls := 0
		s.parts = func(typ types.Type) []any {
			calls++
			if calls > 10000 {
				return []any{"!"}
			}
			switch typ {
			case x:
				return []any{"*", y}
			case y:
				return []any{"[]", x}
			}
			return typeParts(typ)
		}
		text, abbreviated := s.canonicalType(x)
		return text, abbreviated, calls
	}
	text, abbreviated, calls := spell()
	again, _, _ := spell()
	want := strings.Repeat("*[]", abbreviationPrefixBytes/3) + "*"
	if calls > 1000 || !abbreviated || !strings.HasPrefix(text, want+graph.SpellingDigestMarker) ||
		!abbreviationPattern.MatchString(text) || again != text {
		t.Fatalf("a cyclic type must end as a deterministic abbreviation after a bounded walk, got %d part calls, "+
			"abbreviated=%v %q / %q", calls, abbreviated, text, again)
	}
}

// funcChain은 함수 별칭 사슬(F0 = leaf, Fi = func(F(i-1), F(i-1)))을 go/types API로 직접 만든다. 정규
// 표기와 타입 성분 경로 수가 깊이마다 두 배다.
func funcChain(leaf types.Type, depth int) types.Type {
	pkg := types.NewPackage("example.com/chain", "chain")
	prev := types.Type(types.NewAlias(types.NewTypeName(token.NoPos, pkg, "F0", nil), leaf))
	for i := 1; i <= depth; i++ {
		params := types.NewTuple(types.NewParam(token.NoPos, pkg, "", prev), types.NewParam(token.NoPos, pkg, "", prev))
		sig := types.NewSignatureType(nil, nil, nil, params, nil, false)
		prev = types.NewAlias(types.NewTypeName(token.NoPos, pkg, fmt.Sprintf("F%d", i), nil), sig)
	}
	return prev
}

// TestMentionsUnnameableIsLinear는 이름 없는 인터페이스 거르기(mentionsUnnameable)가 공유된 하위
// 타입을 한 번만 보는지 확인한다 — 답은 같고(사슬 맨 안쪽의 타입 파라미터도 찾는다) 비용은 깊이에
// 선형이다(n→2n에서 할당이 두 배 남짓 — 경로마다 다시 보면 2^n배). 함수 별칭 사슬은 타입 검사기가
// 선형이라 이 순회가 수확 전체를 멈추던 곳이다.
func TestMentionsUnnameableIsLinear(t *testing.T) {
	tparam := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), types.NewInterfaceType(nil, nil))
	if !mentionsUnnameable(funcChain(tparam, 12)) || mentionsUnnameable(funcChain(types.Typ[types.Int], 12)) {
		t.Fatal("a type parameter at the bottom of the chain is unnameable; a plain int chain is not")
	}
	allocs := func(depth int) float64 {
		chain := funcChain(types.Typ[types.Int], depth)
		return testing.AllocsPerRun(3, func() { mentionsUnnameable(chain) })
	}
	if small, large := allocs(10), allocs(20); large > 3*small {
		t.Fatalf("mentionsUnnameable must be linear in the chain depth: depth 10 %.0f allocs, depth 20 %.0f", small, large)
	}
}

// chainSource는 별칭 사슬 선언 소스다 — name0 = int, nameI = shape(name(I-1)). shape는 "%[1]s"로 앞
// 별칭을 두 번 쓴다(예: "struct{ a, b %[1]s }", "func(%[1]s, %[1]s)").
func chainSource(name, shape string, depth int) string {
	lines := []string{fmt.Sprintf("type %s0 = int", name)}
	for i := 1; i <= depth; i++ {
		lines = append(lines, fmt.Sprintf("type %s%d = %s", name, i, fmt.Sprintf(shape, fmt.Sprintf("%s%d", name, i-1))))
	}
	return strings.Join(lines, "\n") + "\n"
}

// abbreviationFixture는 상한을 넘는 별칭 struct 사슬(깊이 14, 정규 표기 약 272KiB)이 fields·methods·
// typeSet·satisfies에 하나 이상씩 들어가는 모듈이다. 의존 모듈(replace)의 이름 없는 인터페이스가
// satisfies 이름이 된다. 깊이 10 사슬(약 17KiB)은 상한 아래라 그대로 적혀야 한다. tests면 같은
// 패키지의 _test.go를 더해 테스트 변형이 같은 선언을 다시 훑게 한다.
func abbreviationFixture(t *testing.T, tests bool) string {
	t.Helper()
	const pair = "struct{ a, b %[1]s }"
	files := map[string]string{
		"go.mod": "module example.com/abbrfix\n\ngo 1.27\n\n" +
			"require example.com/dep v0.0.0\n\nreplace example.com/dep => ./dep\n",
		"dep/go.mod": "module example.com/dep\n\ngo 1.27\n",
		"dep/dep.go": "package dep\n\n" + chainSource("A", pair, 14) + `
func Use(x any) {
	if t, ok := x.(interface{ Take(A14) }); ok {
		t.Take(A14{})
	}
}
`,
		"a/a.go": "package a\n\nimport \"example.com/dep\"\n\n" + chainSource("B", pair, 14) + `
type Root struct {
	X B14
	Y B14
	Z int
	S B10
}

type I interface {
	M(B14) int
	N()
}

type C interface{ B14 | int }

type Taker struct{}

func (Taker) Take(dep.A14) {}
`,
	}
	if tests {
		files["a/a_test.go"] = "package a\n\nimport \"testing\"\n\nfunc TestRoot(t *testing.T) { _ = Root{} }\n"
	}
	return testutil.WriteModule(t, files)
}

// abbreviatedEntries는 문서의 fields·methods·typeSet·satisfies 항목 중 축약 표지가 든 것의 수다 —
// limitation이 센 수와 맞춰 보는 실제 수다(fixture에는 표지 글자를 담은 태그가 없다).
func abbreviatedEntries(doc *graph.Document) int {
	n := 0
	for _, v := range doc.Vertices {
		for _, list := range [][]string{v.Fields, v.Methods, v.TypeSet, v.Satisfies} {
			for _, entry := range list {
				if strings.Contains(entry, graph.SpellingDigestMarker) {
					n++
				}
			}
		}
	}
	return n
}

// abbreviationLimitation은 축약 항목 수 limitation 문구다.
func abbreviationLimitation(n int) string {
	return fmt.Sprintf("%d fields/methods/typeSet/satisfies entries abbreviate a type spelling over 65536 bytes "+
		"as its first 256 bytes followed by …sha256: and a structural hash", n)
}

// TestAbbreviatedFacts는 상한을 넘는 표기가 수확 문서의 모든 사실 종류에서 같은 축약으로 적히고,
// limitation이 문서에 실린 축약 항목을 정확히 세는지 확인한다 — 테스트 변형이 같은 선언을 다시 훑어도
// 두 번 세지 않는다. 필드 이름과 메서드 이름은 축약 앞에 남고, 이름 없는 인터페이스 이름(satisfies)은
// 인터페이스 표기 하나로 축약되며, 상한 아래의 긴 표기는 그대로이고, 두 번 수확해도 바이트가 같다.
func TestAbbreviatedFacts(t *testing.T) {
	full := aliasChainSpelling("int", 14)
	abbreviated := full[:abbreviationPrefixBytes] + graph.SpellingDigestMarker
	signature := ("(" + full)[:abbreviationPrefixBytes] + graph.SpellingDigestMarker
	for _, tests := range []bool{false, true} {
		dir := abbreviationFixture(t, tests)
		doc, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Tests: tests})
		if err != nil {
			t.Fatal(err)
		}
		root, _ := doc.VertexByID("example.com/abbrfix/a.Root")
		if len(root.Fields) != 4 || root.Fields[0] != "X:"+root.Fields[1][2:] || root.Fields[2] != "Z:int" ||
			!strings.HasPrefix(root.Fields[0], "X:"+abbreviated) || !abbreviationPattern.MatchString(root.Fields[0]) ||
			root.Fields[3] != "S:"+aliasChainSpelling("int", 10) {
			t.Fatalf("tests=%v: Root fields must abbreviate the depth-14 chain the same way and keep depth 10, got %.400q",
				tests, root.Fields)
		}
		iface, _ := doc.VertexByID("example.com/abbrfix/a.I")
		if len(iface.Methods) != 2 || !strings.HasPrefix(iface.Methods[0], "M"+signature) ||
			!abbreviationPattern.MatchString(iface.Methods[0]) || iface.Methods[1] != "N()" {
			t.Fatalf("tests=%v: method entries must keep the name and abbreviate the signature, got %.400q", tests, iface.Methods)
		}
		constraint, _ := doc.VertexByID("example.com/abbrfix/a.C")
		if len(constraint.TypeSet) != 1 || !strings.HasPrefix(constraint.TypeSet[0], "int | "+abbreviated) {
			t.Fatalf("tests=%v: union terms must abbreviate the long term, got %.400q", tests, constraint.TypeSet)
		}
		take, _ := doc.VertexByID("example.com/abbrfix/a.(Taker).Take")
		name := ("interface{Take(" + full)[:abbreviationPrefixBytes] + graph.SpellingDigestMarker
		if len(take.Satisfies) != 1 || !strings.HasPrefix(take.Satisfies[0], name) ||
			!abbreviationPattern.MatchString(take.Satisfies[0]) {
			t.Fatalf("tests=%v: the anonymous interface name must abbreviate as one spelling, got %.400q",
				tests, take.Satisfies)
		}
		if got := abbreviatedEntries(doc); got != 5 || !slices.Contains(doc.Limitations, abbreviationLimitation(got)) {
			t.Fatalf("tests=%v: the limitation must count the %d abbreviated entries (want 5), got %q", tests, got, doc.Limitations)
		}
		again, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Tests: tests})
		if err != nil {
			t.Fatal(err)
		}
		if second, _ := again.VertexByID("example.com/abbrfix/a.Root"); !slices.Equal(second.Fields, root.Fields) {
			t.Fatalf("tests=%v: abbreviations must be identical across harvests", tests)
		}
	}
	quiet := loadSymbol(t, testutil.WriteModule(t, map[string]string{"a/a.go": "package a\n\n" +
		"type B0 = int\ntype B1 = struct{ a, b B0 }\n\ntype Root struct{ S B1 }\n"}))
	if slices.ContainsFunc(quiet.Limitations, func(l string) bool { return strings.Contains(l, "abbreviate") }) {
		t.Fatalf("nothing was abbreviated, so no limitation may appear: %q", quiet.Limitations)
	}
}

// TestFuncAliasChainHarvestIsBounded는 함수 별칭 사슬(Fi = func(F(i-1), F(i-1)))의 심볼 수확이 깊이
// 64에서도 묶인 크기로 끝나는지 확인한다. 이 모양은 타입 검사기가 선형이라 지수 비용이 수확기에만
// 있었다 — 정규 표기(fields·methods·satisfies)는 깊이마다 두 배였고, 의존 소스의 이름 없는 인터페이스
// 거르기(mentionsUnnameable)는 경로 수만큼 돌았다. 깊이 16(표기 약 700KiB)을 먼저 본다 — 상한이 없으면
// 깊이 64는 끝나지 않는다.
func TestFuncAliasChainHarvestIsBounded(t *testing.T) {
	const pair = "func(%[1]s, %[1]s)"
	for _, depth := range []int{16, 64} {
		top := fmt.Sprintf("%d", depth)
		dir := testutil.WriteModule(t, map[string]string{
			"go.mod": "module example.com/funcfix\n\ngo 1.27\n\n" +
				"require example.com/dep v0.0.0\n\nreplace example.com/dep => ./dep\n",
			"dep/go.mod": "module example.com/dep\n\ngo 1.27\n",
			"dep/dep.go": "package dep\n\n" + chainSource("F", pair, depth) + `
func Use(x any) {
	if t, ok := x.(interface{ Take(F` + top + `) }); ok {
		t.Take(nil)
	}
}
`,
			"a/a.go": "package a\n\nimport \"example.com/dep\"\n\n" + chainSource("G", pair, depth) + `
type Root struct{ X G` + top + ` }

type I interface{ M(G` + top + `) }

type Taker struct{}

func (Taker) Take(dep.F` + top + `) {}
`,
		})
		doc := loadSymbol(t, dir)
		root, _ := doc.VertexByID("example.com/funcfix/a.Root")
		iface, _ := doc.VertexByID("example.com/funcfix/a.I")
		take, _ := doc.VertexByID("example.com/funcfix/a.(Taker).Take")
		if root == nil || iface == nil || take == nil || len(root.Fields) != 1 || len(iface.Methods) != 1 ||
			len(take.Satisfies) != 1 {
			t.Fatalf("depth %d: Root field, I method, and Take satisfies expected, got %+v %+v %+v", depth, root, iface, take)
		}
		for _, entry := range []string{root.Fields[0], iface.Methods[0], take.Satisfies[0]} {
			if !strings.Contains(entry, graph.SpellingDigestMarker) || len(entry) > maxAbbreviationBytes+len("interface{Take}") {
				t.Fatalf("depth %d: every entry must be abbreviated and bounded, got len=%d %.300q", depth, len(entry), entry)
			}
		}
		if !slices.Contains(doc.Limitations, abbreviationLimitation(3)) {
			t.Fatalf("depth %d: the limitation must count the 3 abbreviated entries, got %q", depth, doc.Limitations)
		}
	}
}

// taggedStruct는 정규 표기가 정확히 total바이트인 struct 타입이다("struct{a int "태그"}"는 태그보다 16바이트
// 길다). last는 태그의 마지막 글자다 — 길이가 같고 구조만 다른 타입을 만든다.
func taggedStruct(total int, last byte) types.Type {
	pkg := types.NewPackage("example.com/edge", "edge")
	tag := strings.Repeat("x", total-17) + string(last)
	return types.NewStruct([]*types.Var{types.NewField(token.NoPos, pkg, "a", types.Typ[types.Int], false)},
		[]string{tag})
}

// unionOf는 틸드 없는 항들의 유니언이다.
func unionOf(terms ...types.Type) *types.Union {
	out := make([]*types.Term, len(terms))
	for i, term := range terms {
		out[i] = types.NewTerm(false, term)
	}
	return types.NewUnion(out)
}

// TestUnionEntryCap은 유니언 항목 하나에도 상한이 걸리는지 확인한다 — 항 하나하나는 상한 아래여도 이은
// 길이가 maxSpellingBytes를 넘으면 항목 전체를 앞부분과 구조 해시로 축약한다. 정확히 상한이면 그대로이고,
// 구조가 같은 항(두 인터페이스 항을 펼쳐 겹친 항)은 한 번만 센다. 같은 유니언은 같은 축약, 앞부분이 같아도
// 항이 다르면 다른 축약이다.
func TestUnionEntryCap(t *testing.T) {
	spell := func(u *types.Union) (string, bool) { return newTypeSpeller().unionEntry(u) }
	text := func(typ types.Type) string {
		s, _ := newTypeSpeller().canonicalType(typ)
		return s
	}
	joined := func(a, b types.Type) string {
		texts := []string{text(a), text(b)}
		slices.Sort(texts)
		return texts[0] + " | " + texts[1]
	}
	small := taggedStruct(100, 'x')
	exact := taggedStruct(maxSpellingBytes-103, 'x')
	if got, abbreviated := spell(unionOf(small, exact)); abbreviated || len(got) != maxSpellingBytes ||
		got != joined(small, exact) {
		t.Fatalf("a union of exactly %d bytes must stay whole, got abbreviated=%v len=%d", maxSpellingBytes,
			abbreviated, len(got))
	}
	over := taggedStruct(maxSpellingBytes-102, 'x')
	got, abbreviated := spell(unionOf(small, over))
	if !abbreviated || len(got) > maxAbbreviationBytes || !abbreviationPattern.MatchString(got) ||
		!strings.HasPrefix(got, joined(small, over)[:abbreviationPrefixBytes]+graph.SpellingDigestMarker) {
		t.Fatalf("one byte over the cap must abbreviate the whole entry, got abbreviated=%v %.300q", abbreviated, got)
	}
	// 틸드("~")도 한 바이트로 센다 — 틸드 항은 표기 순서에서 뒤로 간다("s" < "~").
	tilde := func(long types.Type) *types.Union {
		return types.NewUnion([]*types.Term{types.NewTerm(true, small), types.NewTerm(false, long)})
	}
	edge := taggedStruct(maxSpellingBytes-104, 'x')
	if whole, abbreviated := spell(tilde(edge)); abbreviated || whole != text(edge)+" | ~"+text(small) {
		t.Fatalf("a tilde union of exactly %d bytes must stay whole, got abbreviated=%v len=%d", maxSpellingBytes,
			abbreviated, len(whole))
	}
	if _, abbreviated := spell(tilde(taggedStruct(maxSpellingBytes-103, 'x'))); !abbreviated {
		t.Fatal("the tilde must count toward the cap")
	}
	again, _ := spell(unionOf(taggedStruct(100, 'x'), taggedStruct(maxSpellingBytes-102, 'x')))
	other, _ := spell(unionOf(small, taggedStruct(maxSpellingBytes-102, 'y')))
	if again != got || other == got || !strings.HasPrefix(other, got[:abbreviationPrefixBytes]) {
		t.Fatalf("the same union must abbreviate the same way and a different one differently:\n %q\n %q\n %q",
			got, again, other)
	}
	// 두 인터페이스 항이 따로 만든 같은 구조의 항을 펼친다 — 그 항을 두 번 세면 상한을 넘는다.
	ifaceTerm := func(a, b types.Type) types.Type {
		return types.NewInterfaceType(nil, []types.Type{unionOf(a, b)}).Complete()
	}
	merged, abbreviated := spell(unionOf(ifaceTerm(taggedStruct(40000, 'x'), types.Typ[types.Int]),
		ifaceTerm(taggedStruct(40000, 'x'), types.Typ[types.String])))
	if abbreviated || merged != "int | string | "+text(taggedStruct(40000, 'x')) {
		t.Fatalf("terms with the same structure must count once, got abbreviated=%v %.200q", abbreviated, merged)
	}
}

// TestUnionEntryAbbreviationIsCheap은 상한을 넘는 유니언을 축약할 때 항을 끝까지 펴지 않는지 확인한다 — 항
// 100개(go/types의 유니언 한계)의 표기 길이가 8배(깊이 8 사슬 약 4KiB → 깊이 11 약 34KiB)가 돼도 항목 하나를
// 만드는 할당은 두 배를 넘지 않는다(앞부분만 편다). 항을 다 펴서 이으면 할당도 표기 길이만큼 8배가 된다.
func TestUnionEntryAbbreviationIsCheap(t *testing.T) {
	allocated := func(depth int) uint64 {
		chain := aliasChain(types.Typ[types.Int], depth)
		terms := make([]types.Type, 100)
		for i := range terms {
			terms[i] = types.NewArray(chain, int64(i+1))
		}
		u := unionOf(terms...)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		text, abbreviated := newTypeSpeller().unionEntry(u)
		runtime.ReadMemStats(&after)
		if !abbreviated || len(text) > maxAbbreviationBytes || !strings.HasPrefix(text, "[100]struct{a struct{") {
			t.Fatalf("depth %d: a 100-term union over the cap must abbreviate, got abbreviated=%v %.300q",
				depth, abbreviated, text)
		}
		return after.TotalAlloc - before.TotalAlloc
	}
	if short, long := allocated(8), allocated(11); long > 2*short {
		t.Fatalf("abbreviating must not spell every term: %d bytes for 4 KiB terms, %d bytes for 34 KiB terms", short, long)
	}
}

// TestJoinedSpellingsAreCapped는 표기를 이어 만드는 두 항목도 상한을 지키는지 수확 문서에서 확인한다 — 항이
// 각각 상한 아래(약 34KiB)인 유니언의 typeSet 항목과, 서명이 각각 상한 아래인 의존 소스 이름 없는 인터페이스의
// satisfies 이름이다. 수정 전에는 항목 하나가 항·메서드 수만큼 길어졌고, satisfies 이름은 구현 메서드마다
// 실려 문서가 메서드 수의 제곱으로 커졌다. 축약 항목은 limitation이 센다.
func TestJoinedSpellingsAreCapped(t *testing.T) {
	const pair = "struct{ a, b %[1]s }"
	doc := loadSymbol(t, testutil.WriteModule(t, map[string]string{
		"go.mod": "module example.com/joinfix\n\ngo 1.27\n\n" +
			"require example.com/dep v0.0.0\n\nreplace example.com/dep => ./dep\n",
		"dep/go.mod": "module example.com/dep\n\ngo 1.27\n",
		"dep/dep.go": "package dep\n\n" + chainSource("A", pair, 11) + `
func Use(x any) {
	if t, ok := x.(interface{ M0(A11); M1(A11); M2(A11) }); ok {
		t.M0(A11{})
	}
}
`,
		"a/a.go": "package a\n\nimport \"example.com/dep\"\n\n" + chainSource("B", pair, 11) + `
type U interface{ [1]B11 | [2]B11 | [3]B11 }

type T struct{}

func (T) M0(dep.A11) {}
func (T) M1(dep.A11) {}
func (T) M2(dep.A11) {}
`,
	}))
	full := aliasChainSpelling("int", 11)
	u, ok := doc.VertexByID("example.com/joinfix/a.U")
	if !ok {
		t.Fatal("vertex example.com/joinfix/a.U expected")
	}
	if len(u.TypeSet) != 1 || len(u.TypeSet[0]) > maxAbbreviationBytes ||
		!strings.HasPrefix(u.TypeSet[0], ("[1]" + full)[:abbreviationPrefixBytes]+graph.SpellingDigestMarker) ||
		!abbreviationPattern.MatchString(u.TypeSet[0]) {
		t.Fatalf("a union of three long terms must abbreviate as one entry, got %.400q", u.TypeSet)
	}
	name := ("interface{M0(" + full)[:abbreviationPrefixBytes] + graph.SpellingDigestMarker
	var names []string
	for _, m := range []string{"M0", "M1", "M2"} {
		v, ok := doc.VertexByID("example.com/joinfix/a.(T)." + m)
		if !ok {
			t.Fatalf("method vertex %s expected", m)
		}
		if len(v.Satisfies) != 1 || len(v.Satisfies[0]) > maxAbbreviationBytes ||
			!strings.HasPrefix(v.Satisfies[0], name) || !abbreviationPattern.MatchString(v.Satisfies[0]) {
			t.Fatalf("%s: the anonymous interface name must abbreviate as one spelling, got %.400q", m, v.Satisfies)
		}
		names = append(names, v.Satisfies[0])
	}
	if names[0] != names[1] || names[1] != names[2] || !slices.Contains(doc.Limitations, abbreviationLimitation(4)) {
		t.Fatalf("the three methods must carry the same name and the limitation must count 4 entries: %q %q",
			names, doc.Limitations)
	}
}
