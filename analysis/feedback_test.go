package analysis

import (
	"reflect"
	"slices"
	"testing"

	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/graph"
)

// 후보 디스패치는 도달성을 넓혀도 확정 순환의 증거가 될 수 없다.
func TestFeedbackCandidateCallsDoNotProveCycles(t *testing.T) {
	d := &graph.Document{
		Vertices: []graph.Vertex{{ID: "a"}, {ID: "b"}},
		Edges: []graph.Edge{
			{From: "a", To: "b", Kind: graph.EdgeCall, Candidate: true},
			{From: "b", To: "a", Kind: graph.EdgeCall},
		},
	}
	if cycles := Cycles(d); len(cycles) != 0 {
		t.Fatalf("candidate dispatch proved a cycle: %+v", cycles)
	}
	if !reflect.DeepEqual(graph.Adjacency(d)["a"], []string{"b"}) {
		t.Fatal("candidate reachability must remain available")
	}
	d.Edges = append(d.Edges, graph.Edge{From: "a", To: "b", Kind: graph.EdgeCall})
	d.Sort()
	if cycles := Cycles(d); len(cycles) != 1 || len(cycles[0].Edges) != 2 {
		t.Fatalf("confirmed calls must still prove the cycle: %+v", cycles)
	}
}

// 같은 from/to를 가진 여러 위치·규칙의 출력은 입력 순서에 좌우되지 않는다.
func TestFeedbackRuleTieOrderIsDeterministic(t *testing.T) {
	positions := make([]graph.Position, 16)
	for i := range positions {
		positions[i] = graph.Position{File: "/m/web/web.go", Line: i + 1, Column: 2}
	}
	d := &graph.Document{
		Module:   "example.com/m",
		Vertices: []graph.Vertex{{ID: "example.com/m/web", Kind: graph.KindPackage}, {ID: "example.com/m/db", Kind: graph.KindPackage}},
		Edges:    []graph.Edge{{From: "example.com/m/web", To: "example.com/m/db", Kind: graph.EdgeImport, Positions: positions}},
	}
	cfg := &config.File{
		Components: map[string][]string{"web": {"web"}, "db": {"db"}},
		Deps:       map[string][]string{"web": {"db"}},
		FileRules:  []config.FileRule{{Name: "z", From: "*", To: "db"}, {Name: "a", From: "*", To: "db"}},
	}
	first := CheckRules(d, cfg)
	slices.Reverse(d.Edges[0].Positions)
	slices.Reverse(cfg.FileRules)
	second := CheckRules(d, cfg)
	if len(first.Violations) < 16 || !reflect.DeepEqual(first, second) {
		t.Fatalf("rule order depends on input: first=%+v, second=%+v", first.Violations, second.Violations)
	}
}

// 생성 파일 출신 정점의 보고는 그 출처 사실을 그대로 싣는다 — 소비자가 손으로 쓴 코드와
// 생성기(cgo 등) 산출물을 보고만 보고 가른다. CHA·RTA 두 판정 모두 같은 사실을 옮긴다.
func TestFeedbackDeadFindingsCarryGeneratedProvenance(t *testing.T) {
	d := &graph.Document{
		Roots: []string{"p.main"},
		Vertices: []graph.Vertex{
			{ID: "p.main", Kind: graph.KindFunc, Package: "p"},
			{ID: "p._Cgo_use", Kind: graph.KindFunc, Package: "p", Generated: true},
			{ID: "p._Cgo_always_false", Kind: graph.KindVar, Package: "p", Generated: true},
			{ID: "p.handwritten", Kind: graph.KindFunc, Package: "p"},
		},
	}
	reach := Reachable(d, d.Roots)
	for name, findings := range map[string][]Finding{
		"cha": Dead(d, reach),
		"rta": DeadRTA(d, reach, map[string]bool{"p.main": true}),
	} {
		got := map[string]bool{}
		for _, f := range findings {
			got[f.ID] = f.Generated
		}
		want := map[string]bool{"p._Cgo_use": true, "p._Cgo_always_false": true, "p.handwritten": false}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: generated provenance = %v, want %v", name, got, want)
		}
	}
}

// 필드 보고는 정점의 struct tag 키를 그대로 싣는다 — 직렬화·ORM이 reflection으로 읽는 필드를
// 소비자가 보고만 보고 알아본다. 보고 범위는 그대로다.
func TestFeedbackDeadFieldFindingsCarryTagKeys(t *testing.T) {
	d := &graph.Document{
		Roots: []string{"p.main"},
		Vertices: []graph.Vertex{
			{ID: "p.main", Kind: graph.KindFunc, Package: "p"},
			{ID: "p.(R).A", Kind: graph.KindField, Package: "p", Exported: true, TagKeys: []string{"json"}},
			{ID: "p.(R).c", Kind: graph.KindField, Package: "p"},
		},
	}
	reach := Reachable(d, d.Roots)
	for name, findings := range map[string][]Finding{
		"cha": Dead(d, reach),
		"rta": DeadRTA(d, reach, map[string]bool{"p.main": true}),
	} {
		got := map[string][]string{}
		for _, f := range findings {
			got[f.ID] = f.TagKeys
		}
		want := map[string][]string{"p.(R).A": {"json"}, "p.(R).c": nil}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: tag keys = %v, want %v", name, got, want)
		}
	}
}
