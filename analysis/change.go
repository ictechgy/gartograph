package analysis

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/ictechgy/gartograph/graph"
)

// ChangeCandidate는 한 스냅샷에서 변경 파일로 이어지는 검토 근거다.
// Via는 바로 앞 깊이의 의존 대상 ID이며 여러 revision의 간선을 합치지 않는다.
type ChangeCandidate struct {
	ID            string           `json:"id"`
	Kind          graph.VertexKind `json:"kind"`
	Depth         int              `json:"depth"`
	Via           []string         `json:"via,omitempty"`
	Edges         []graph.EdgeKind `json:"edges,omitempty"`
	Location      *graph.Position  `json:"location,omitempty"`
	Reason        string           `json:"reason"`
	TestCandidate bool             `json:"testCandidate,omitempty"`
}

// ChangeSide는 독립된 한 revision의 영향 후보다. Complete는 정적 분석의 완전성을 보장하지 않는다.
type ChangeSide struct {
	Files         []string          `json:"files"`
	Roots         []string          `json:"roots"`
	UnmappedFiles []string          `json:"unmappedFiles,omitempty"`
	Candidates    []ChangeCandidate `json:"candidates"`
	Depth         int               `json:"depth"`
	Max           int               `json:"max"`
	DepthLimited  bool              `json:"depthLimited"`
	OutputLimited bool              `json:"outputLimited"`
	Truncated     bool              `json:"truncated"`
	Limitations   []string          `json:"limitations"`
}

// ChangeCandidates는 변경 파일을 선언 전체에 보수적으로 매핑한 뒤 한 문서 안에서만 역순회한다.
// root도 후보로 남겨 삭제·이름 변경의 과거 선언과 직접 바뀐 테스트를 검토할 수 있게 한다.
func ChangeCandidates(d *graph.Document, files []string, depth, maxEntries int) *ChangeSide {
	byID := graph.VertexIndex(d)
	roots, unmapped := verticesForFiles(d, byID, files)
	res := &ChangeSide{Files: dedupeSorted(append([]string{}, files...)), Roots: roots,
		UnmappedFiles: unmapped, Candidates: []ChangeCandidate{}, Depth: depth, Max: maxEntries,
		Limitations: append([]string{}, d.Limitations...)}
	res.Limitations = append(res.Limitations,
		"file-wide seeds include all declarations and the package; candidates may exceed the changed declarations",
		"static review candidates do not predict test failures or authorize skipping tests",
		"reflection, external callers, runtime callback wiring, configuration and generated or excluded source may not be represented")
	if len(unmapped) > 0 {
		res.Limitations = append(res.Limitations, "unmapped changes require review; an empty candidate list is not a safety result")
	}
	in := graph.KindedIncoming(d)
	distances := map[string]int{}
	via := map[string][]string{}
	kinds := map[string][]graph.EdgeKind{}
	frontier := append([]string{}, roots...)
	for _, id := range roots {
		distances[id] = 0
	}
	for step := 1; len(frontier) > 0; step++ {
		var next []string
		for _, cur := range frontier {
			for _, neighbor := range in[cur] {
				if byID[neighbor.ID] == nil {
					continue
				}
				previous, seen := distances[neighbor.ID]
				if seen && previous != step {
					continue
				}
				if depth > 0 && step > depth {
					res.DepthLimited = true
					continue
				}
				if !seen {
					distances[neighbor.ID] = step
					next = append(next, neighbor.ID)
				}
				via[neighbor.ID] = append(via[neighbor.ID], cur)
				kinds[neighbor.ID] = append(kinds[neighbor.ID], neighbor.Kind)
			}
		}
		frontier = next
	}
	ids := make([]string, 0, len(distances))
	for id := range distances {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if distances[ids[i]] != distances[ids[j]] {
			return distances[ids[i]] < distances[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if maxEntries > 0 && len(ids) > maxEntries {
		ids = ids[:maxEntries]
		res.OutputLimited = true
	}
	for _, id := range ids {
		v := byID[id]
		c := ChangeCandidate{ID: id, Kind: v.Kind, Depth: distances[id], Via: dedupeSorted(via[id]), Edges: mergeKinds(nil, kinds[id]), Reason: "reverse-dependency"}
		if c.Depth == 0 {
			c.Reason = "changed-file declaration or package"
		}
		if v.Position != nil {
			p := *v.Position
			if rel, err := filepath.Rel(d.Root, p.File); err == nil {
				p.File = filepath.ToSlash(rel)
			}
			c.Location = &p
			c.TestCandidate = strings.HasSuffix(p.File, "_test.go") && v.Kind == graph.KindFunc && (strings.HasPrefix(v.Name, "Test") || strings.HasPrefix(v.Name, "Example") || strings.HasPrefix(v.Name, "Fuzz") || strings.HasPrefix(v.Name, "Benchmark"))
		}
		res.Candidates = append(res.Candidates, c)
	}
	res.Truncated = res.DepthLimited || res.OutputLimited
	return res
}
