package analysis

import (
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// diffDocs는 정점·간선이 주어진 두 문서를 만든다.
func diffDocs() (old, new *graph.Document) {
	old = &graph.Document{
		Level:  graph.LevelSymbol,
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "m/a", Kind: graph.KindPackage},
			{ID: "m/a.F", Kind: graph.KindFunc, Package: "m/a", Exported: true},
			{ID: "m/a.gone", Kind: graph.KindFunc, Package: "m/a", Exported: true},
			{ID: "m/a.T", Kind: graph.KindType, Package: "m/a", Exported: true},
			{ID: "m/a.U", Kind: graph.KindType, Package: "m/a", Exported: true},
		},
		Edges: []graph.Edge{
			{From: "m/a.F", To: "m/a.T", Kind: graph.EdgeSignature},
			{From: "m/a.gone", To: "m/a.U", Kind: graph.EdgeSignature},
		},
	}
	new = &graph.Document{
		Level:  graph.LevelSymbol,
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "m/a", Kind: graph.KindPackage},
			{ID: "m/a.F", Kind: graph.KindFunc, Package: "m/a", Exported: true},
			{ID: "m/a.T", Kind: graph.KindType, Package: "m/a", Exported: true},
			{ID: "m/a.U", Kind: graph.KindType, Package: "m/a", Exported: true},
			{ID: "m/a.NEW", Kind: graph.KindFunc, Package: "m/a", Exported: true},
		},
		Edges: []graph.Edge{
			{From: "m/a.F", To: "m/a.U", Kind: graph.EdgeSignature},
			{From: "m/a.F", To: "m/a.T", Kind: graph.EdgeSignature},
		},
	}
	return old, new
}

// TestDiff는 정점·간선·시그니처 차이와 breaking 신호를 확인한다.
func TestDiff(t *testing.T) {
	old, new := diffDocs()
	d := DiffDocuments(old, new)

	if len(d.AddedVertices) != 1 || d.AddedVertices[0] != "m/a.NEW" {
		t.Fatalf("added vertices: %+v", d.AddedVertices)
	}
	if len(d.RemovedVertices) != 1 || d.RemovedVertices[0] != "m/a.gone" {
		t.Fatalf("removed vertices: %+v", d.RemovedVertices)
	}
	// 공개 심볼 제거는 breaking이다.
	var removedBreaking bool
	for _, b := range d.Breaking {
		if strings.Contains(b, "m/a.gone") {
			removedBreaking = true
		}
	}
	if !removedBreaking {
		t.Fatalf("removed exported vertex must be breaking: %+v", d.Breaking)
	}
	// m/a.F의 signature 목표에 m/a.U가 추가됐다.
	if len(d.SignatureChanges) != 1 || d.SignatureChanges[0].ID != "m/a.F" ||
		len(d.SignatureChanges[0].Added) != 1 ||
		d.SignatureChanges[0].Added[0] != "m/a.U" {
		t.Fatalf("signature change on m/a.F: %+v", d.SignatureChanges)
	}
	// 간선 diff: F→T는 유지, F→U 추가, gone→U 제거.
	if len(d.AddedEdges) != 1 || len(d.RemovedEdges) != 1 {
		t.Fatalf("edges: %+v %+v", d.AddedEdges, d.RemovedEdges)
	}
}

// TestDiffLevelMismatch는 레벨이 다른 문서 비교가 notes로 보고되는지 확인한다.
// 비교 불가를 에러가 아닌 메모로 남기는 것이 소비자의 판단 재료다.
func TestDiffLevelMismatch(t *testing.T) {
	old := &graph.Document{Level: graph.LevelPackage}
	new := &graph.Document{Level: graph.LevelSymbol}
	d := DiffDocuments(old, new)
	if len(d.Notes) == 0 || !strings.Contains(d.Notes[0], "level mismatch") {
		t.Fatalf("expected level mismatch note: %+v", d.Notes)
	}
}

// TestDiffLimitations는 양쪽 문서의 수확 limitation이 출처와 함께
// diff에 실리는지 확인한다 — 부분 수확 위의 diff는 신호가 아니라
// 수확 구멍일 수 있음을 소비자가 알아야 한다.
func TestDiffLimitations(t *testing.T) {
	old := &graph.Document{Limitations: []string{"2 packages had errors"}}
	new := &graph.Document{Limitations: []string{"1 import omitted"}}
	d := DiffDocuments(old, new)
	if len(d.OldLimitations) != 1 || len(d.NewLimitations) != 1 {
		t.Fatalf("limitations must carry provenance: %+v", d)
	}
}

// TestDiffBreakingOnlyOnRemoval은 추가만 있는 diff가 breaking이 아닌지 확인한다.
func TestDiffBreakingOnlyOnRemoval(t *testing.T) {
	old := &graph.Document{
		Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "m/a.F", Kind: graph.KindFunc, Exported: true},
		},
	}
	new := &graph.Document{
		Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "m/a.F", Kind: graph.KindFunc, Exported: true},
			{ID: "m/a.G", Kind: graph.KindFunc, Exported: true},
		},
	}
	d := DiffDocuments(old, new)
	if len(d.Breaking) != 0 || len(d.AddedVertices) != 1 {
		t.Fatalf("pure additions must not be breaking: %+v", d)
	}
}
