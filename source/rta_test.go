package source

import (
	"slices"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestRTARootsEverySyntheticInit는 RTA가 초기화 루트(pkg._) 없이도 모든 패키지의
// 합성 init을 루트로 삼는지 확인한다 — 문서에서 pkg._를 걷어내 CHA 쪽 장치와 떼어
// 본다. 합성 init은 패키지 변수 초기화식을 실행하므로 rreg가 도달해야 하고, explain
// 인접 맵에는 순회용 ID(pkg#init)로 실린다.
func TestRTARootsEverySyntheticInit(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport _ \"example.com/fixture/r\"\n\nfunc main() {}\n",
		"r/r.go":  "package r\n\nvar X = rreg()\n\nfunc rreg() int { return 1 }\n",
	})
	doc := loadSymbol(t, dir)
	const blank, target = "example.com/fixture/r._", "example.com/fixture/r.rreg"
	doc.Vertices = slices.DeleteFunc(doc.Vertices, func(v graph.Vertex) bool { return v.ID == blank })
	roots := map[string]bool{}
	for _, r := range doc.Roots {
		if r != blank {
			roots[r] = true
		}
	}
	opts := Options{Dir: dir, Level: graph.LevelSymbol}
	reach, err := RTAReachable(opts, doc, roots)
	if err != nil {
		t.Fatal(err)
	}
	if !reach[target] {
		t.Fatal("rta must root every synthetic init, not only packages with an init-root vertex")
	}
	adj, _, err := RTAAdjacency(opts, doc, roots)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(adj["example.com/fixture/r"+PackageInitSuffix], target) {
		t.Fatalf("synthetic init without an init root must appear as pkg#init, adj=%v", adj)
	}
}
