package analysis

import (
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// deadDoc은 루트 하나(main)와 도달·미도달 심볼이 섞인 문서다.
// p.main→p.f 도달, p.Dead(공개)·p.gone(비공개) 미도달, 패키지 정점은 대상 밖.
func deadDoc() *graph.Document {
	return &graph.Document{
		Roots: []string{"p.main"},
		Vertices: []graph.Vertex{
			{ID: "p", Kind: graph.KindPackage},
			{ID: "p.main", Kind: graph.KindFunc, Package: "p"},
			{ID: "p.f", Kind: graph.KindFunc, Package: "p"},
			{ID: "p.Dead", Kind: graph.KindFunc, Package: "p", Exported: true},
			{ID: "p.gone", Kind: graph.KindFunc, Package: "p"},
		},
		Edges: []graph.Edge{
			{From: "p", To: "p.main", Kind: graph.EdgeContains},
			{From: "p", To: "p.f", Kind: graph.EdgeContains},
			{From: "p.main", To: "p.f", Kind: graph.EdgeCall},
		},
	}
}

// TestRetentionRoots는 루트 조합 규칙을 확인한다:
// 문서 루트 + retainPublic의 공개 심볼 + 추가 지정(없는 것은 unknown으로).
func TestRetentionRoots(t *testing.T) {
	roots, unknown := RetentionRoots(deadDoc(), false, []string{"p.gone", "nope"})
	if len(roots) != 2 { // p.main + p.gone
		t.Fatalf("expected 2 roots, got %v", roots)
	}
	if len(unknown) != 1 || unknown[0] != "nope" {
		t.Fatalf("expected unknown root reported, got %v", unknown)
	}

	roots, _ = RetentionRoots(deadDoc(), true, nil)
	var hasDead bool
	for _, r := range roots {
		hasDead = hasDead || r == "p.Dead"
	}
	if !hasDead {
		t.Fatalf("retainPublic must retain exported symbols, got %v", roots)
	}
}

// TestDead는 unreachable 보고가 심볼만 대상으로 하고 사유를 싣는지 확인한다.
// contains 간선은 의존이 아니므로 패키지 정점을 경유해 도달하지 않는다.
func TestDead(t *testing.T) {
	d := deadDoc()
	reachable := Reachable(d, []string{"p.main"})
	findings := Dead(d, reachable)
	if len(findings) != 2 {
		t.Fatalf("expected 2 unreachable findings, got %+v", findings)
	}
	for _, f := range findings {
		if f.State != StateUnreachable || f.Reason == "" {
			t.Fatalf("finding missing state/reason: %+v", f)
		}
	}
	// 패키지 정점은 심볼이 아니라 unreachable로 보고되지 않는다.
	for _, f := range findings {
		if f.Kind == graph.KindPackage {
			t.Fatalf("package vertex reported unreachable: %+v", f)
		}
	}
	// retainPublic 루트를 넣으면 공개 심볼은 살아 있다.
	roots, _ := RetentionRoots(d, true, nil)
	findings = Dead(d, Reachable(d, roots))
	if len(findings) != 1 || findings[0].ID != "p.gone" {
		t.Fatalf("retainPublic: expected only p.gone, got %+v", findings)
	}
}

// TestExplain은 도달 경로와 미도달 구분을 확인한다.
func TestExplain(t *testing.T) {
	d := deadDoc()
	path, found, err := Explain(d, "p.f", []string{"p.main"})
	if err != nil || !found {
		t.Fatalf("expected path to p.f: %v %v", path, err)
	}
	if len(path) != 2 || path[0] != "p.main" || path[1] != "p.f" {
		t.Fatalf("unexpected path: %v", path)
	}
	if _, found, _ := Explain(d, "p.gone", []string{"p.main"}); found {
		t.Fatal("p.gone must not have a path")
	}
	if _, _, err := Explain(d, "missing", nil); err == nil {
		t.Fatal("expected ErrNotFound for missing vertex")
	}
}
