package graph

import "strings"

// CollisionSuffix는 패키지 경로와 겹치는 심볼 ID 뒤에 붙는 접미사다.
// 패키지 정점 ID는 import 경로 그대로라 점이 든 경로(example.com/m/x.y)는 패키지 x의
// 심볼 y의 ID("pkgpath.Name")와 같아진다 — --tests의 테스트 main 패키지 x.test와
// 함수 test도 그렇다. '#'는 import 경로에 쓸 수 없는 문자라 접미사 ID는 어떤 패키지와도
// 겹치지 않는다. 겹치지 않는 ID는 그대로다.
// 수확(source)이 붙이고 비교(analysis의 diff·baseline)가 뗀다 — 규칙이 한 곳에 있어야
// 두 쪽이 어긋나지 않는다.
const CollisionSuffix = "#symbol"

// CanonicalID는 충돌 접미사를 뗀 심볼 ID다. 접미사는 수확된 패키지 집합에 따라
// 붙고 떨어지므로(형제 x.y/ 디렉터리 추가, --tests 유무), 같은 심볼을 문서 사이에서
// 짝지을 때는 이 값을 쓴다. 한 문서 안에서는 패키지와 겹칠 수 있어 정점 색인 키로
// 단독으로 쓰면 안 된다 — 범주(패키지·심볼)와 함께 쓴다.
func CanonicalID(id string) string {
	return strings.TrimSuffix(id, CollisionSuffix)
}

// MemberOwner는 메서드·필드 정점 ID("pkgpath.(Recv).Name")에서 소유 타입의 ID
// ("pkgpath.Recv")를 돌려준다. import 경로에는 괄호를 쓸 수 없어 첫 ".("가 경로와
// 리시버의 경계다. 소유 타입 ID가 패키지와 겹치면 문서에는 CollisionSuffix가 붙어
// 있다 — 조회하는 쪽이 두 형태를 다 본다. 멤버 ID가 아니면 false다.
func MemberOwner(id string) (string, bool) {
	open := strings.Index(id, ".(")
	if open < 0 {
		return "", false
	}
	rest := id[open+2:]
	closeIdx := strings.Index(rest, ").")
	if closeIdx <= 0 {
		return "", false
	}
	return id[:open] + "." + rest[:closeIdx], true
}
