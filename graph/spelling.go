package graph

import "strings"

// SpellingDigestMarker는 축약한 정규 타입 표기에서 앞부분과 구조 해시 사이의 표지다. fields·methods·
// typeSet·satisfies의 타입 표기 하나가 수확 상한을 넘으면 "앞부분…sha256:<구조 해시 hex 64자>"가 된다.
// 수확(source)이 붙이고 순회(analysis의 --type-edges members)가 알아본다 — 규칙이 한 곳에 있어야 두
// 쪽이 어긋나지 않는다.
const SpellingDigestMarker = "…sha256:"

// spellingDigestHexLen은 표지 뒤 구조 해시(SHA-256)의 hex 길이다.
const spellingDigestHexLen = 64

// AbbreviatedSpelling은 정규 타입 표기 하나(필드의 타입 부분 등)가 축약된 것인지 본다 — 마지막 표지
// 뒤가 소문자 hex 64자로 끝나야 한다. 축약하지 않은 표기는 이렇게 끝날 수 없다: "…"는 식별자·import
// 경로에 쓸 수 없고, struct 태그 안에 있으면 닫는 따옴표가 뒤따른다. 축약된 표기는 앞부분 밖의
// 타입도 담을 수 있다는 뜻이라, 앞부분만 보고 "이 타입은 없다"고 판정하면 안 된다.
func AbbreviatedSpelling(spelling string) bool {
	i := strings.LastIndex(spelling, SpellingDigestMarker)
	if i < 0 {
		return false
	}
	digest := spelling[i+len(SpellingDigestMarker):]
	return len(digest) == spellingDigestHexLen && strings.Trim(digest, "0123456789abcdef") == ""
}
