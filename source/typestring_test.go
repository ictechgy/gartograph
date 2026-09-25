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
