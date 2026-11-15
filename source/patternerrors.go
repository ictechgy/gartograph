package source

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// isPatternError는 packages.Load가 패턴 자체의 오류로 돌려준 가짜 패키지인지 본다 — 없는 디렉터리
// (`./interal/...`, `./nonexistent`), 주 모듈이 없는 --dir(`directory prefix . does not contain main module`),
// 어느 모듈도 제공하지 않는 import 경로, 읽지 못한 replace 대상·하위 디렉터리. go list -e는 이런 패턴마다 ID가
// 패턴 문자열이고 이름·파일·모듈 없이 오류만 담은 패키지를 낸다 — 같은 패턴이 고른 실제 패키지가 있으면 그것도
// 함께 낸다(`example.com/...`의 replace 대상이 없으면 주 모듈 패키지와 오류가 같이 온다). 주 모듈의 실제
// 패키지(빌드 제약으로 파일이 다 빠진 디렉터리 포함)는 모듈이 있고, 표준 라이브러리 패키지는 이름과 파일이 있어
// 여기에 들지 않는다. 적재 루트(packages.Load의 반환값)에만 쓴다 — 의존 쪽의 빠진 import는 그 import를 쓰는
// 패키지의 오류로 이미 센다.
func isPatternError(p *packages.Package) bool {
	return len(p.Errors) > 0 && p.Name == "" && p.Module == nil &&
		len(p.GoFiles)+len(p.CompiledGoFiles)+len(p.OtherFiles)+len(p.IgnoredFiles) == 0
}

// splitPatternErrors는 적재 루트에서 패턴 오류 가짜 패키지를 떼어 낸다. 둘째 값은 ID순이다 — 그 패키지가
// 정점이 되면(--deps) 이름 없는 유령 정점이 되고, 모듈 레벨에서는 "모듈 없는 패키지"로 잘못 세였다.
func splitPatternErrors(roots []*packages.Package) (kept, errs []*packages.Package) {
	for _, p := range roots {
		if isPatternError(p) {
			errs = append(errs, p)
			continue
		}
		kept = append(kept, p)
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].ID < errs[j].ID })
	return kept, errs
}

// patternErrorSummary는 패턴 오류 수와 첫 오류 문구다(`N package patterns reported errors from go list
// (first: …)`). 본 것만 쓴다 — 오류를 낸 패턴이 실제 패키지도 골랐는지는 go list가 알려 주지 않아 "아무것도
// 적재하지 못했다"고 단정하지 않는다. errs는 splitPatternErrors의 둘째 값이다.
func patternErrorSummary(errs []*packages.Package) string {
	return fmt.Sprintf("%d package patterns reported errors from go list (first: %s)",
		len(errs), firstErrorLine(errs[0].Errors[0].Msg))
}

// firstErrorLine은 go list 오류 문구의 첫 줄이다 — 둘째 줄부터는 `go get` 안내 같은 덧붙임이다. 첫 줄이
// 다음 줄을 소개하는 `:`로 끝나면 그 소개 절(`…; to add it:`)도 뗀다 — 다음 줄 없이 남으면 뜻이 끊긴다.
func firstErrorLine(msg string) string {
	line := strings.TrimSpace(strings.SplitN(msg, "\n", 2)[0])
	intro, ok := strings.CutSuffix(line, ":")
	if !ok {
		return line
	}
	if i := strings.LastIndex(intro, "; "); i >= 0 {
		return intro[:i]
	}
	return intro
}

// patternErrorLimitation은 수확 문서·보고의 패턴 오류 limitation이다. 예전에는 keep이 가짜 패키지를 버린 뒤 로드
// 오류를 세어, 패턴 오타 하나로 아무것도 보지 않은 cycles --strict·dead --strict가 limitation 없이 성공했다.
// roots는 패턴 오류를 뺀 적재 루트 수다. 0이면 아무 패키지도 적재하지 않았다는 것까지 본 사실이라 그렇게 쓰고,
// 아니면 다른 패키지는 수확됐으므로(같은 패턴이 고른 것일 수도 있다) 오류가 가린 몫만 말한다.
func patternErrorLimitation(errs []*packages.Package, roots int) string {
	if roots == 0 {
		return patternErrorSummary(errs) + "; no packages were loaded, so nothing was harvested — check --pattern and --dir"
	}
	return patternErrorSummary(errs) + "; packages they could not resolve were not analyzed"
}

// noMatchLimitation은 오류 없이 패키지를 하나도 고르지 않은 수확의 limitation이다(Go 파일이 없는 디렉터리의
// `./docs/...` — go list는 경고만 하고 아무것도 돌려주지 않는다). 패턴은 받은 그대로 공백으로 잇는다.
func noMatchLimitation(patterns []string) string {
	return fmt.Sprintf("no packages matched the patterns (%s); nothing was harvested — check --pattern and --dir",
		joinedPatterns(patterns))
}

// joinedPatterns는 수확 패턴(없으면 기본 ./...)을 공백으로 이은 문구다 — limitation과 오류가 같은 표기를 쓴다.
func joinedPatterns(patterns []string) string {
	return strings.Join(defaultPatterns(patterns), " ")
}

// defaultPatterns는 패턴이 없을 때 load가 쓰는 기본 패턴(./...)을 채운다.
func defaultPatterns(patterns []string) []string {
	if len(patterns) == 0 {
		return []string{"./..."}
	}
	return patterns
}
