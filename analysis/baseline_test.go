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
		[]Violation{known, novel}, []Violation{known, fixed})

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

// TestSplitBaselineEmpty는 baseline이 없을 때 전부 fresh인지 확인한다.
func TestSplitBaselineEmpty(t *testing.T) {
	v := Violation{From: "a", To: "b", Kind: graph.EdgeImport}
	fresh, baselined, stale := SplitBaseline([]Violation{v}, nil)
	if len(fresh) != 1 || len(baselined) != 0 || len(stale) != 0 {
		t.Fatalf("empty baseline: %+v %+v %+v", fresh, baselined, stale)
	}
}
