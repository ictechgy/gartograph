package graph

import (
	"strings"
	"testing"
)

// TestAbbreviatedSpelling은 축약 표기를 알아보는 계약을 고정한다 — 마지막 표지 뒤가 소문자 hex
// 64자일 때만 축약이다. 표지만 있거나(태그 안의 같은 글자), 해시 길이·대소문자가 다르거나, 표지 뒤에
// 다른 것이 이어지면 아니다.
func TestAbbreviatedSpelling(t *testing.T) {
	digest := strings.Repeat("0123456789abcdef", 4)
	for spelling, want := range map[string]bool{
		"struct{a struct{a " + SpellingDigestMarker + digest:       true,
		"(" + SpellingDigestMarker + digest:                        true,
		"struct{a int; b int}":                                     false,
		"struct{a int \"x:…sha256:\"}":                             false,
		"struct{a int \"" + SpellingDigestMarker + digest + "\"}":  false,
		"x" + SpellingDigestMarker + digest[:63]:                   false,
		"x" + SpellingDigestMarker + strings.ToUpper(digest):       false,
		"x" + SpellingDigestMarker + digest + "}":                  false,
		"x" + SpellingDigestMarker + digest + SpellingDigestMarker: false,
	} {
		if got := AbbreviatedSpelling(spelling); got != want {
			t.Errorf("AbbreviatedSpelling(%q) = %v, want %v", spelling, got, want)
		}
	}
}
