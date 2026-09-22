// 규칙 baseline 비교 — 기존 레포 도입 시 현재 위반을 "알려진 것"으로
// 합법화하고, 새 위반만 CI가 막게 한다.
// dependency-cruiser의 baseline과 go-arch-lint의 "legalize" 워크플로우가
// 증명한 도입 조건이다 — baseline 없는 --strict는 대형 레포에서 쓸 수 없다.
package analysis

// SplitBaseline은 위반을 baseline에 이미 있는 것(baselined)과
// 새 것(fresh)으로 나누고, baseline에만 남은 항목을 stale로 돌려준다.
// stale은 "더 이상 발생하지 않는 알려진 위반" — 코드가 고쳐졌으면
// baseline을 재생성하라는 신호다.
// Violation은 전 필드가 문자열이라 직접 맵 키가 된다 — 위치가 아니라
// (from,to,kind,rule) 튜플이 동일성 단위다.
func SplitBaseline(violations, baseline []Violation) (fresh, baselined, stale []Violation) {
	remaining := map[Violation]int{}
	for _, b := range baseline {
		remaining[b]++
	}
	for _, v := range violations {
		if remaining[v] > 0 {
			remaining[v]--
			baselined = append(baselined, v)
		} else {
			fresh = append(fresh, v)
		}
	}
	for _, b := range baseline {
		if remaining[b] > 0 {
			stale = append(stale, b)
			remaining[b]--
		}
	}
	return fresh, baselined, stale
}
