package analysis

import (
	"reflect"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// TestSplitBaseline은 위반의 fresh/baselined/stale 3분할을 확인한다.
// baseline에만 남은 항목은 stale이다 — 고쳐진 위반이 조용히
// baseline에 쌓이면 재생성 시점을 놓친다.
func TestSplitBaseline(t *testing.T) {
	known := Violation{From: "a", To: "b", Kind: graph.EdgeImport,
		FromComponent: "ca", ToComponent: "cb", Rule: "allow"}
	fixed := Violation{From: "x", To: "y", Kind: graph.EdgeImport, Rule: "deny"}
	novel := Violation{From: "p", To: "q", Kind: graph.EdgeImport, Rule: "allow"}

	fresh, baselined, stale := SplitBaseline(
		[]Violation{known, novel}, []Violation{known, fixed}, ViolationBaselineKey)

	if len(fresh) != 1 || !reflect.DeepEqual(fresh[0], novel) {
		t.Fatalf("fresh: %+v", fresh)
	}
	if len(baselined) != 1 || !reflect.DeepEqual(baselined[0], known) {
		t.Fatalf("baselined: %+v", baselined)
	}
	if len(stale) != 1 || !reflect.DeepEqual(stale[0], fixed) {
		t.Fatalf("stale: %+v", stale)
	}
}

// TestSplitBaselineForbidden는 forbidden 위반이 목격 경로와 무관하게
// 같은 위반으로 식별되는지 확인한다 — 경로가 바뀌었다고 새 위반으로
// 울리면 baseline이 소음이 된다.
func TestSplitBaselineForbidden(t *testing.T) {
	old := Violation{Rule: "forbidden",
		FromComponent: "api", ToComponent: "db", Path: []string{"a", "b"}}
	// 같은 계약 위반인데 증인 경로가 다르다 — baselined여야 한다.
	cur := Violation{Rule: "forbidden",
		FromComponent: "api", ToComponent: "db", Path: []string{"a", "x", "b"}}
	fresh, baselined, _ := SplitBaseline([]Violation{cur}, []Violation{old}, ViolationBaselineKey)
	if len(baselined) != 1 || len(fresh) != 0 {
		t.Fatalf("forbidden must key on the component pair, not the path: %+v %+v",
			fresh, baselined)
	}
}

// TestSplitBaselineEmpty는 baseline이 없을 때 전부 fresh인지 확인한다.
func TestSplitBaselineEmpty(t *testing.T) {
	v := Violation{From: "a", To: "b", Kind: graph.EdgeImport}
	fresh, baselined, stale := SplitBaseline([]Violation{v}, nil, ViolationBaselineKey)
	if len(fresh) != 1 || len(baselined) != 0 || len(stale) != 0 {
		t.Fatalf("empty baseline: %+v %+v %+v", fresh, baselined, stale)
	}
}

// TestSplitBaselineIndependence는 independence 위반이 forbidden처럼
// 컴포넌트 쌍으로 식별되는지 확인한다 — 목격 경로의 끝점 정점이
// 바뀌어도 같은 계약 위반이다.
func TestSplitBaselineIndependence(t *testing.T) {
	old := Violation{Rule: "independence", From: "m/x", To: "m/y",
		FromComponent: "x", ToComponent: "y", Path: []string{"m/x", "m/y"}}
	cur := Violation{Rule: "independence", From: "m/x2", To: "m/y2",
		FromComponent: "x", ToComponent: "y", Path: []string{"m/x2", "m/z", "m/y2"}}
	fresh, baselined, _ := SplitBaseline([]Violation{cur}, []Violation{old},
		ViolationBaselineKey)
	if len(baselined) != 1 || len(fresh) != 0 {
		t.Fatalf("independence must key on the component pair: %+v %+v",
			fresh, baselined)
	}
}

// TestSplitBaselineFileScope는 fileScope 위반이 규칙 이름과 파일로
// 식별되는지 확인한다 — 같은 간선의 다른 파일 위반은 별개 항목이고,
// 줄 번호 이동은 같은 위반이다.
func TestSplitBaselineFileScope(t *testing.T) {
	old := Violation{Rule: "fileScope", Name: "r", From: "m/web", To: "m/th",
		Position: &graph.Position{File: "/m/web/web.go", Line: 3}}
	same := Violation{Rule: "fileScope", Name: "r", From: "m/web", To: "m/th",
		Position: &graph.Position{File: "/m/web/web.go", Line: 40}}
	other := Violation{Rule: "fileScope", Name: "r", From: "m/web", To: "m/th",
		Position: &graph.Position{File: "/m/web/other.go", Line: 3}}
	fresh, baselined, _ := SplitBaseline([]Violation{same, other},
		[]Violation{old}, ViolationBaselineKey)
	if len(baselined) != 1 || len(fresh) != 1 {
		t.Fatalf("fileScope keys on rule name + file, not line: %+v %+v",
			fresh, baselined)
	}
}

// TestCycleFindingBaselineKey는 cycles·dead baseline의 동일성 키를 확인한다.
func TestCycleFindingBaselineKey(t *testing.T) {
	c1 := Cycle{Members: []string{"a", "b"}}
	c2 := Cycle{Members: []string{"a", "b"}, Edges: []graph.Edge{
		{From: "a", To: "b", Kind: graph.EdgeCall}}}
	if CycleBaselineKey(c1) != CycleBaselineKey(c2) {
		t.Fatal("same member set is the same cycle regardless of edge evidence")
	}
	f1 := Finding{ID: "m/a.f", Kind: graph.KindFunc, Reason: "cha"}
	f2 := Finding{ID: "m/a.f", Kind: graph.KindFunc, Reason: "rta"}
	if FindingBaselineKey(f1) != FindingBaselineKey(f2) {
		t.Fatal("same symbol unreachable is the same fact across algos")
	}
}

// TestBaselineKeysIgnoreCollisionSuffix는 --tests 유무처럼 수확 패키지 집합만 달라
// 심볼 ID에 #symbol이 붙고 떨어져도 baseline이 같은 항목으로 보는지 확인한다.
func TestBaselineKeysIgnoreCollisionSuffix(t *testing.T) {
	plain := Finding{ID: "m/x.test", Kind: graph.KindFunc}
	suffixed := Finding{ID: "m/x.test" + graph.CollisionSuffix, Kind: graph.KindFunc}
	if FindingBaselineKey(plain) != FindingBaselineKey(suffixed) {
		t.Fatal("dead baseline must pair a symbol across the collision suffix")
	}
	if CycleBaselineKey(Cycle{Members: []string{"m/x.a", "m/x.test"}}) !=
		CycleBaselineKey(Cycle{Members: []string{"m/x.a", "m/x.test" + graph.CollisionSuffix}}) {
		t.Fatal("cycle baseline must pair members across the collision suffix")
	}
}
