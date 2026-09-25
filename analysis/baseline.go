// 규칙 baseline 비교 — 기존 레포 도입 시 현재 위반을 "알려진 것"으로
// 합법화하고, 새 위반만 CI가 막게 한다.
// dependency-cruiser의 baseline과 go-arch-lint의 "legalize" 워크플로우가
// 증명한 도입 조건이다 — baseline 없는 --strict는 대형 레포에서 쓸 수 없다.
package analysis

import (
	"strings"

	"github.com/ictechgy/gartograph/graph"
)

// SplitBaseline은 항목을 baseline에 이미 있는 것(baselined)과
// 새 것(fresh)으로 나누고, baseline에만 남은 항목을 stale로 돌려준다.
// stale은 "더 이상 발생하지 않는 알려진 항목" — 코드가 고쳐졌으면
// baseline을 재생성하라는 신호다.
// 동일성은 key 함수가 정한다 — 위치가 아니라 사실 튜플이 단위다.
// 같은 키가 여러 번 나오면 개수로 맞춘다 — 같은 규칙의 위반이
// 두 파일에 있으면 둘은 독립적인 알려진 위반이다.
func SplitBaseline[T any](items, baseline []T, key func(T) string) (fresh, baselined, stale []T) {
	remaining := map[string]int{}
	for _, b := range baseline {
		remaining[key(b)]++
	}
	for _, v := range items {
		k := key(v)
		if remaining[k] > 0 {
			remaining[k]--
			baselined = append(baselined, v)
		} else {
			fresh = append(fresh, v)
		}
	}
	for _, b := range baseline {
		k := key(b)
		if remaining[k] > 0 {
			stale = append(stale, b)
			remaining[k]--
		}
	}
	return fresh, baselined, stale
}

// ViolationBaselineKey는 위반의 동일성을 정의한다.
// forbidden·independence 위반은 목격 경로(Path)나 끝점 정점이 달라져도
// 같은 계약 위반이므로 컴포넌트 쌍만으로 식별한다 — 경로가 바뀌었다고
// 새 위반으로 울리면 baseline이 소음이 된다.
// fileScope 위반은 지점 단위라 규칙 이름과 파일을 함께 쓴다 — 줄 번호는
// 드리프트로 흔들리고, 파일이 같으면 같은 위반으로 보는 게 정확하다.
// reason은 사유 텍스트라 동일성에 넣지 않는다.
// \x00 구분은 필드 안에 나올 수 없는 값이라 튜플 충돌이 없다.
func ViolationBaselineKey(v Violation) string {
	switch v.Rule {
	case "forbidden", "independence":
		return v.Rule + "\x00" + v.FromComponent + "\x00" + v.ToComponent
	case "fileScope":
		file := ""
		if v.Position != nil {
			file = v.Position.File
		}
		return v.Rule + "\x00" + v.Name + "\x00" + v.From + "\x00" +
			v.To + "\x00" + file
	case "limit":
		// 실제 수치는 reason에 있다 — 수치가 3→5로 변해도 같은 상한
		// 위반이므로 컴포넌트와 상한 종류만으로 식별한다.
		return v.Rule + "\x00" + v.Name + "\x00" + v.FromComponent + "\x00" + v.ToComponent
	}
	return v.Rule + "\x00" + string(v.Kind) + "\x00" + graph.CanonicalID(v.From) + "\x00" +
		graph.CanonicalID(v.To) + "\x00" + v.FromComponent + "\x00" + v.ToComponent
}

// CycleBaselineKey는 순환의 동일성을 정의한다.
// Members는 Cycles가 이미 정렬해 내놓는다 — 같은 멤버 집합은 같은
// 순환이다. 간선 목록은 멤버가 같으면 증거가 달라도 위반 사실은 같다.
// 충돌 접미사(graph.CollisionSuffix)는 뗀다 — 수확 패키지 집합만 달라진 같은 순환이
// 새 순환으로 울리지 않게.
func CycleBaselineKey(c Cycle) string {
	members := make([]string, len(c.Members))
	for i, m := range c.Members {
		members[i] = graph.CanonicalID(m)
	}
	return strings.Join(members, "\x00")
}

// FindingBaselineKey는 dead 보고의 동일성을 정의한다.
// 심볼 ID와 종류가 같으면 같은 사실이다 — 사유(reason)나 알고리즘이
// 달라져도 "도달 불가"로 보고된 대상은 같다. 충돌 접미사도 뗀다 — --tests 유무처럼
// 수확 패키지 집합만 달라 x.test가 x.test#symbol이 되어도 같은 심볼이다.
func FindingBaselineKey(f Finding) string {
	return string(f.Kind) + "\x00" + graph.CanonicalID(f.ID)
}
