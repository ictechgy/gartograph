package graph

import "testing"

// TestMethodOwner는 멤버 ID에서 소유 타입 ID를 뽑는 계약을 고정한다 — import 경로에는
// 괄호가 없어 첫 ".("가 경계이고, 점이 든 경로도 그대로 남는다.
func TestMethodOwner(t *testing.T) {
	for id, want := range map[string]string{
		"m/a.(I).Do":                "m/a.I",
		"gopkg.in/yaml.v3.(Node).X": "gopkg.in/yaml.v3.Node",
		"m/x.y.(T).F":               "m/x.y.T",
	} {
		if got, ok := MethodOwner(id); !ok || got != want {
			t.Fatalf("%s: got %q %v, want %q", id, got, ok, want)
		}
	}
	for _, id := range []string{"m/a.F", "m/a", "m/a.()x"} {
		if _, ok := MethodOwner(id); ok {
			t.Fatalf("%s is not a member ID", id)
		}
	}
}
