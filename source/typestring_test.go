package source

import (
	"slices"
	"testing"

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
`}))
	if !doc.InterfaceTypeSets {
		t.Fatal("documents must carry the interfaceTypeSets marker")
	}
	for id, want := range map[string][]string{
		"example.com/fixture/a.Number": {"~int | ~int64"},
		"example.com/fixture/a.Bytes":  {"string | ~[]uint8"},
		"example.com/fixture/a.Plain":  nil,
	} {
		v, _ := doc.VertexByID(id)
		if !slices.Equal(v.TypeSet, want) {
			t.Fatalf("%s: typeSet %q, want %q", id, v.TypeSet, want)
		}
	}
}
