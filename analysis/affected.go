// 파일→정점 매핑과 다중 루트 영향 질의 — "이 파일들이 바뀌면 무엇이 깨지는가".
// git diff 같은 VCS 사실은 cli가 수집하고, 여기서는 문서 위의 순수 해석만 한다.
package analysis

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ictechgy/gartograph/graph"
)

// Affected는 파일 집합에서 해석된 정점들의 역방향 전이 클로저다.
// UnmappedFiles는 어떤 정점으로도 해석되지 않은 파일이다 — .go가 아니거나,
// 삭제됐거나, 수확 범위 밖이다. 조용히 버리면 "변경 없음"으로 오독된다.
type Affected struct {
	Files         []string      `json:"files"`
	Roots         []string      `json:"roots"`
	UnmappedFiles []string      `json:"unmappedFiles,omitempty"`
	Depth         int           `json:"depth"`
	Dependers     []ImpactEntry `json:"dependers"`
	Truncated     bool          `json:"truncated,omitempty"`
	// Limitations은 수확이 보지 못한 영역이다 — 영향 분석은 닫힌 세계를
	// 가정하므로 부분 수확 위의 결과임을 소비자에게 남긴다.
	Limitations []string `json:"limitations,omitempty"`
}

// AffectedByFiles는 files에 선언된 정점과 extraRoots를 합쳐 역방향 BFS를 돌린다.
// extraRoots에 문서에 없는 정점이 있으면 ErrNotFound다 — 없는 루트를 조용히
// 버리면 소비자가 "영향 없음"으로 오독한다.
func AffectedByFiles(d *graph.Document, files, extraRoots []string,
	depth, maxEntries int) (*Affected, error) {
	roots, unmapped := VerticesForFiles(d, files)
	var missing []string
	for _, r := range extraRoots {
		if d.HasVertex(r) {
			roots = append(roots, r)
		} else {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.Join(missing, ", "))
	}
	roots = dedupeSorted(roots)
	dep, trunc := impactFrom(d, roots, depth, maxEntries)
	return &Affected{
		Files: dedupeSorted(append([]string(nil), files...)),
		Roots: roots, UnmappedFiles: unmapped,
		Depth: depth, Dependers: dep, Truncated: trunc,
		Limitations: d.Limitations,
	}, nil
}

// VerticesForFiles는 파일 경로를 정점 ID로 해석한다.
// 심볼·타입 정점은 position.file로, 패키지 정점은 파일의 디렉터리로 맞춘다 —
// 패키지 정점까지 루트에 넣어야 파일 수준 변경이 import 의존자에게도 전이된다.
// 상대 경로는 문서의 Root 기준으로 절대화한다 — Position.File은 절대 경로다.
func VerticesForFiles(d *graph.Document, files []string) (roots, unmapped []string) {
	byFile := map[string][]string{}
	for _, v := range d.Vertices {
		if v.Position != nil {
			abs := filepath.Clean(v.Position.File)
			byFile[abs] = append(byFile[abs], v.ID)
		}
	}
	seen := map[string]bool{}
	for _, f := range files {
		// .go가 아닌 파일은 정점을 선언하지 않는다 — README를 패키지
		// 정점에 매핑하면 문서 변경이 코드 영향으로 둔갑한다.
		if !strings.HasSuffix(f, ".go") {
			unmapped = append(unmapped, f)
			continue
		}
		abs := f
		if !filepath.IsAbs(abs) {
			// 상대 경로는 문서 루트 기준이다 — 수확 당시의 --dir과 같은 기준.
			abs = filepath.Join(d.Root, f)
		}
		if resolved, err := filepath.Abs(abs); err == nil {
			abs = resolved
		}
		abs = filepath.Clean(abs)
		matched := false
		for _, id := range byFile[abs] {
			if !seen[id] {
				seen[id] = true
				roots = append(roots, id)
			}
			matched = true
		}
		if pkg := packageForFile(d, abs); pkg != "" && d.HasVertex(pkg) {
			if !seen[pkg] {
				seen[pkg] = true
				roots = append(roots, pkg)
			}
			matched = true
		}
		if !matched {
			unmapped = append(unmapped, f)
		}
	}
	sort.Strings(roots)
	sort.Strings(unmapped)
	return roots, unmapped
}

// packageForFile은 파일이 속한 디렉터리의 패키지 정점 ID를 만든다.
// 루트 디렉터리의 파일은 모듈 경로 자체가 패키지 ID다.
// 기준은 ModuleDir(go.mod 위치)이다 — --dir이 모듈 하위를 가리키면
// Root 기준 상대 경로는 패키지 경로와 어긋난다. ModuleDir이 없는
// 옛 문서는 Root로 폴백한다.
// 모듈 밖의 파일(외부 의존)은 모듈 상대 경로가 성립하지 않아 ""를 돌려준다.
func packageForFile(d *graph.Document, absFile string) string {
	if d.Module == "" {
		return ""
	}
	base := d.ModuleDir
	if base == "" {
		base = d.Root
	}
	if base == "" {
		return ""
	}
	root, err := filepath.Abs(base)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, filepath.Dir(absFile))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	if rel == "." {
		return d.Module
	}
	return d.Module + "/" + filepath.ToSlash(rel)
}

// dedupeSorted는 정렬하고 중복을 제거한다 — 출력 계약의 결정성을 위해.
func dedupeSorted(ids []string) []string {
	sort.Strings(ids)
	out := ids[:0]
	for i, id := range ids {
		if i > 0 && id == ids[i-1] {
			continue
		}
		out = append(out, id)
	}
	return out
}
