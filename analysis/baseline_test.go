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

// TestSplitBaselineForbidden는 forbidden 위반이 목격 경로와 무관하게
// 같은 위반으로 식별되는지 확인한다 — 경로가 바뀌었다고 새 위반으로
// 울리면 baseline이 소음이 된다.
func TestSplitBaselineForbidden(t *testing.T) {
	old := Violation{Rule: "forbidden",
		FromComponent: "api", ToComponent: "db", Path: []string{"a", "b"}}
	// 같은 계약 위반인데 증인 경로가 다르다 — baselined여야 한다.
	cur := Violation{Rule: "forbidden",
		FromComponent: "api", ToComponent: "db", Path: []string{"a", "x", "b"}}
	fresh, baselined, _ := SplitBaseline([]Violation{cur}, []Violation{old})
	if len(baselined) != 1 || len(fresh) != 0 {
		t.Fatalf("forbidden must key on the component pair, not the path: %+v %+v",
			fresh, baselined)
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
