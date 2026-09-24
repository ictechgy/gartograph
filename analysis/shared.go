// 공통 도달 집합 질의 — "이 루트들이 같이 끌어오는 것"의 답이다.
// goda의 shared가 증명한 축이다 — 여러 진입점의 의존 교집합을 보면
// 공유 하부구조가 무엇인지, 어느 루트가 뭘 혼자 끌어오는지가 나뉜다.
package analysis

import (
	"fmt"
	"sort"

	"github.com/ictechgy/gartograph/graph"
)

// SharedResult는 여러 루트의 도달 집합 교집합·차집이다.
// Shared는 모든 루트에서 도달 가능한 정점, Only는 루트별로 혼자만
// 도달하는 정점이다 — "공통 부품"과 "이 루트만의 비용"을 나눠 준다.
// 루트 자신이 다른 루트에서 도달 가능하면 shared에도 들어간다 —
// 도달성 집합의 의미를 그대로 따른다.
type SharedResult struct {
	Roots  []string            `json:"roots"`
	Shared []string            `json:"shared"`
	Only   map[string][]string `json:"only,omitempty"`
	// Limitations은 수확이 보지 못한 영역이다 — shared/only가 부분
	// 수확의 산물일 수 있음을 소비자에게 남긴다.
	Limitations []string `json:"limitations,omitempty"`
}

// Shared는 각 루트에서 의존 간선을 따라 도달 가능한 정점 집합의
// 교집합과 루트별 고유분을 계산한다. 루트가 문서에 없으면
// ErrNotFound다 — "교집합이 비었다"와 "루트가 없다"는 다른 사실이다.
func Shared(d *graph.Document, roots []string) (*SharedResult, error) {
	for _, id := range roots {
		if !d.HasVertex(id) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
	}
	reach := make(map[string]map[string]bool, len(roots))
	count := map[string]int{} // 정점 → 도달하는 루트 수
	for _, r := range roots {
		reach[r] = DependencyReachable(d, []string{r})
		for id := range reach[r] {
			count[id]++
		}
	}
	res := &SharedResult{Roots: roots, Limitations: d.Limitations}
	var shared []string
	for id, n := range count {
		if n == len(roots) {
			shared = append(shared, id)
		}
	}
	sort.Strings(shared)
	res.Shared = shared
	for _, r := range roots {
		var only []string
		for id := range reach[r] {
			if count[id] < len(roots) {
				only = append(only, id)
			}
		}
		if len(only) > 0 {
			sort.Strings(only)
			if res.Only == nil {
				res.Only = map[string][]string{}
			}
			res.Only[r] = only
		}
	}
	return res, nil
}
