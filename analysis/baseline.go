// 규칙 baseline 비교 — 기존 레포 도입 시 현재 위반을 "알려진 것"으로
// 합법화하고, 새 위반만 CI가 막게 한다.
// dependency-cruiser의 baseline과 go-arch-lint의 "legalize" 워크플로우가
// 증명한 도입 조건이다 — baseline 없는 --strict는 대형 레포에서 쓸 수 없다.
package analysis

// SplitBaseline은 위반을 baseline에 이미 있는 것(baselined)과
// 새 것(fresh)으로 나누고, baseline에만 남은 항목을 stale로 돌려준다.
// stale은 "더 이상 발생하지 않는 알려진 위반" — 코드가 고쳐졌으면
// baseline을 재생성하라는 신호다.
// 동일성은 baselineKey가 정한다 — 위치가 아니라 규칙·간선 튜플이 단위다.
func SplitBaseline(violations, baseline []Violation) (fresh, baselined, stale []Violation) {
	remaining := map[string]int{}
	for _, b := range baseline {
		remaining[baselineKey(b)]++
	}
	for _, v := range violations {
		k := baselineKey(v)
		if remaining[k] > 0 {
			remaining[k]--
			baselined = append(baselined, v)
		} else {
			fresh = append(fresh, v)
		}
	}
	for _, b := range baseline {
		k := baselineKey(b)
		if remaining[k] > 0 {
			stale = append(stale, b)
			remaining[k]--
		}
	}
	return fresh, baselined, stale
}

// baselineKey는 위반의 동일성을 정의한다.
// forbidden 위반은 목격 경로(Path)가 달라져도 같은 계약 위반이므로
// 컴포넌트 쌍만으로 식별한다 — 경로가 바뀌었다고 새 위반으로 울리면
// baseline이 소음이 된다. reason은 사유 텍스트라 동일성에 넣지 않는다.
// \x00 구분은 필드 안에 나올 수 없는 값이라 튜플 충돌이 없다.
func baselineKey(v Violation) string {
	if v.Rule == "forbidden" {
		return v.Rule + "\x00" + v.FromComponent + "\x00" + v.ToComponent
	}
	return v.Rule + "\x00" + string(v.Kind) + "\x00" + v.From + "\x00" +
		v.To + "\x00" + v.FromComponent + "\x00" + v.ToComponent
}
