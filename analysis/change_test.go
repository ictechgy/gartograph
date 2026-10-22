package analysis

import (
	"path/filepath"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

func TestChangeCandidatesKeepSnapshotsSeparate(t *testing.T) {
	root := t.TempDir()
	vertices := []graph.Vertex{{ID: "m.Target", Kind: graph.KindFunc, Position: &graph.Position{File: filepath.Join(root, "target.go"), Line: 1}},
		{ID: "m.Middle", Kind: graph.KindFunc}, {ID: "m.Caller", Kind: graph.KindFunc}}
	before := &graph.Document{Root: root, Vertices: vertices, Edges: []graph.Edge{{From: "m.Caller", To: "m.Middle", Kind: graph.EdgeCall}}}
	after := &graph.Document{Root: root, Vertices: vertices, Edges: []graph.Edge{{From: "m.Middle", To: "m.Target", Kind: graph.EdgeCall}}}
	old := ChangeCandidates(before, []string{"target.go"}, 0, 0)
	current := ChangeCandidates(after, []string{"target.go"}, 0, 0)
	if len(old.Candidates) != 1 || len(current.Candidates) != 2 {
		t.Fatalf("mixed revision path or missing current path: old=%+v new=%+v", old, current)
	}
	if current.Candidates[1].ID != "m.Middle" || len(current.Candidates[1].Via) != 1 || current.Candidates[1].Via[0] != "m.Target" {
		t.Fatalf("missing witness: %+v", current.Candidates)
	}
}

func TestChangeCandidatesDepthAndOutputLimits(t *testing.T) {
	d := impactDoc()
	d.Root = t.TempDir()
	d.Vertices[3].Position = &graph.Position{File: filepath.Join(d.Root, "target.go"), Line: 1}
	limited := ChangeCandidates(d, []string{"target.go"}, 1, 0)
	if !limited.DepthLimited || !limited.Truncated || limited.OutputLimited {
		t.Fatalf("depth boundary hidden: %+v", limited)
	}
	capped := ChangeCandidates(d, []string{"target.go"}, 0, 1)
	if capped.DepthLimited || !capped.OutputLimited || !capped.Truncated || len(capped.Candidates) != 1 {
		t.Fatalf("output boundary hidden: %+v", capped)
	}
	full := ChangeCandidates(d, []string{"target.go"}, 3, 0)
	if full.Truncated {
		t.Fatalf("exhausted graph is not depth-limited: %+v", full)
	}
	legacy, err := FindImpact(d, "pkg.c", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !legacy.DepthLimited || legacy.OutputLimited || legacy.Truncated || legacy.Mode != "snapshot" || legacy.Freshness != "unverified" {
		t.Fatalf("legacy metadata or compatibility: %+v", legacy)
	}
}
