package analysis

import (
	"strings"
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

// TestDeadExported는 보고가 심볼의 공개 여부 사실을 싣는지 확인한다 —
// 공개 unreachable(외부 호출자·플러그인이 쓸 수 있음)과 비공개
// unreachable(저장소 안에서 닫혀 있음)의 triage 분리 재료다.
func TestDeadExported(t *testing.T) {
	findings := Dead(deadDoc(), Reachable(deadDoc(), []string{"p.main"}))
	var pub, priv *Finding
	for i := range findings {
		switch findings[i].ID {
		case "p.Dead":
			pub = &findings[i]
		case "p.gone":
			priv = &findings[i]
		}
	}
	if pub == nil || !pub.Exported {
		t.Fatalf("exported finding must carry exported=true: %+v", findings)
	}
	if priv == nil || priv.Exported {
		t.Fatalf("unexported finding must carry exported=false: %+v", findings)
	}
}

// TestDeadField는 필드 정점이 unreachable 보고의 대상이 되는지 확인한다 —
// 멤버 레벨 분석의 종단 계약이다.
func TestDeadField(t *testing.T) {
	d := deadDoc()
	d.Vertices = append(d.Vertices,
		graph.Vertex{ID: "p.(T).Used", Kind: graph.KindField,
			Package: "p", Exported: true},
		graph.Vertex{ID: "p.(T).stale", Kind: graph.KindField, Package: "p"})
	d.Edges = append(d.Edges,
		graph.Edge{From: "p.f", To: "p.(T).Used", Kind: graph.EdgeReferences})
	findings := Dead(d, Reachable(d, []string{"p.main"}))
	var stale bool
	for _, f := range findings {
		if f.ID == "p.(T).Used" {
			t.Fatal("referenced field must be reachable")
		}
		if f.ID == "p.(T).stale" {
			stale = true
		}
	}
	if !stale {
		t.Fatalf("unreferenced field must be reported: %+v", findings)
	}
}

// dispatchDoc은 외부 인터페이스 디스패치 사실이 있는 문서다.
// main→Use→T(참조). T.Error는 error를 구현(리시버 T 도달), T.Helper는 무관,
// U.String은 fmt.Stringer를 구현하지만 리시버 U가 도달하지 않는다.
// T.Error→T references가 있어 간선으로 그었다면 순환이 되는 모양이다.
func dispatchDoc() *graph.Document {
	return &graph.Document{
		Roots: []string{"p.main"},
		Vertices: []graph.Vertex{
			{ID: "p.main", Kind: graph.KindFunc, Package: "p"},
			{ID: "p.Use", Kind: graph.KindFunc, Package: "p"},
			{ID: "p.T", Kind: graph.KindType, Package: "p"},
			{ID: "p.(T).Error", Kind: graph.KindMethod, Package: "p",
				Satisfies: []string{"error"}, Receiver: "p.T"},
			{ID: "p.(T).Helper", Kind: graph.KindMethod, Package: "p"},
			{ID: "p.U", Kind: graph.KindType, Package: "p"},
			{ID: "p.(U).String", Kind: graph.KindMethod, Package: "p",
				Satisfies: []string{"fmt.Stringer"}, Receiver: "p.U"},
		},
		Edges: []graph.Edge{
			{From: "p.main", To: "p.Use", Kind: graph.EdgeCall},
			{From: "p.Use", To: "p.T", Kind: graph.EdgeReferences},
			{From: "p.(T).Error", To: "p.T", Kind: graph.EdgeReferences},
		},
	}
}

// TestReachableExternalDispatch는 외부 인터페이스를 구현한 메서드가
// 리시버 타입이 도달할 때만 도달하고, 무관한 메서드는 그대로 보고되며,
// 보고된 메서드에 Satisfies 사실이 실리는지 확인한다.
func TestReachableExternalDispatch(t *testing.T) {
	d := dispatchDoc()
	reachable := Reachable(d, []string{"p.main"})
	if !reachable["p.(T).Error"] {
		t.Fatal("method satisfying an external interface must be reachable via its reachable receiver")
	}
	if reachable["p.(T).Helper"] {
		t.Fatal("method without external dispatch facts must not be reached through its receiver")
	}
	if reachable["p.(U).String"] {
		t.Fatal("external dispatch must not revive a method whose receiver is unreachable")
	}
	findings := Dead(d, reachable)
	var orphan *Finding
	for i := range findings {
		if findings[i].ID == "p.(U).String" {
			orphan = &findings[i]
		}
	}
	if orphan == nil || len(orphan.Satisfies) != 1 || orphan.Satisfies[0] != "fmt.Stringer" {
		t.Fatalf("unreachable method must carry its satisfies fact, got %+v", findings)
	}
}

// TestExplainExternalDispatch는 --explain 경로가 리시버 타입을 거쳐
// 메서드에 닿는지 확인한다 — 도달 판정과 설명이 같은 인접 맵을 써야 한다.
func TestExplainExternalDispatch(t *testing.T) {
	path, found, err := Explain(dispatchDoc(), "p.(T).Error", []string{"p.main"})
	if err != nil || !found {
		t.Fatalf("expected a path, got found=%v err=%v", found, err)
	}
	want := []string{"p.main", "p.Use", "p.T", "p.(T).Error"}
	if strings.Join(path, " ") != strings.Join(want, " ") {
		t.Fatalf("expected path %v, got %v", want, path)
	}
}

// TestReachAdjacencyIgnoresMissingReceiver는 문서에 없는 리시버를 가리키는
// 사실이 유령 인접을 만들지 않는지 확인한다 — 손으로 고친 저장 문서 방어.
func TestReachAdjacencyIgnoresMissingReceiver(t *testing.T) {
	d := dispatchDoc()
	d.Vertices[3].Receiver = "p.Gone"
	if _, ok := ReachAdjacency(d)["p.Gone"]; ok {
		t.Fatal("missing receiver must not appear in the reach adjacency")
	}
}

// TestSharedIgnoresExternalDispatch는 shared가 의존 간선만 따라 path와
// 같은 답을 내는지 확인한다 — 외부 디스패치는 dead·explain 전용 규칙이다.
func TestSharedIgnoresExternalDispatch(t *testing.T) {
	d := dispatchDoc()
	res, err := Shared(d, []string{"p.main", "p.Use"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range res.Shared {
		if id == "p.(T).Error" {
			t.Fatalf("shared must not include dispatch-only methods, got %v", res.Shared)
		}
	}
	if _, found := IsExternalDispatch(d, "p.T", "p.(T).Error"); !found {
		t.Fatal("receiver→method hop must be recognised as external dispatch")
	}
	if _, found := IsExternalDispatch(d, "p.Use", "p.T"); found {
		t.Fatal("a real document edge must not be reported as external dispatch")
	}
}
