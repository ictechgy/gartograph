// impact --since/--files — git diff 또는 명시 파일 목록 기반 영향 분석.
// VCS 사실 수집은 여기서만 한다 — analysis는 문서 위의 순수 해석이다.
package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// checkSinceRevision은 --since 값이 git revision 자리에만 들어갈 수 있는지 본다. 비었거나
// '-'로 시작하면 git이 옵션으로 읽고(`--output=FILE`은 임의 파일을 덮어쓴다), 제어 문자는
// 로그·오류에서 다른 값으로 읽힌다(forbiddenChars, --revision과 같은 집합). 그런 값은 git을
// 실행하지 않고 사용법 오류로 돌린다 — 수확 전에도, git을 부르기 직전에도 이 함수를 지난다.
func checkSinceRevision(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") || forbiddenChars.MatchString(rev) {
		return fmt.Errorf("--since %q is not a git revision: it must be non-empty, must not start with '-', "+
			"and must not contain control characters; pass a revision such as HEAD~1 or origin/main...HEAD", rev)
	}
	return nil
}

// changedFilesSince는 `git diff --name-only --relative <rev>`의 결과를 돌려준다.
// --relative로 저장소 루트가 아닌 --dir 기준 경로를 얻는다.
// rev는 한 인자로 git에 넘기므로 `origin/main...HEAD` 같은 범위 표기도 쓸 수 있다.
// --end-of-options 뒤에 두고 끝에 --를 붙여, 검증을 지난 값도 옵션·경로로 읽히지 않게 한다
// (git 2.24 이상). gitCommand가 fsmonitor 훅·선택 잠금을 끈다 — 분석 대상 .git/config의
// core.fsmonitor 명령이 이 읽기 전용 질의에서 실행되면 안 된다. clean 필터·textconv 같은 나머지
// 설정은 여전히 git이 따른다(README: 출처를 모르는 트리에서는 --files).
// --no-renames는 rename을 delete+add로 펼친다 — 옛 경로도 변경 파일이어야
// 그 패키지의 의존자가 영향 집합에 남는다. -z는 NUL 구분이라 인용되지 않은
// 파일명을 그대로 준다 — 공백·비ASCII 이름도 안전하다.
func changedFilesSince(dir, rev string) ([]string, error) {
	if err := checkSinceRevision(rev); err != nil {
		return nil, err
	}
	cmd := gitCommand(dir, "diff", "--name-only", "--relative", "--no-renames", "-z",
		"--end-of-options", rev, "--")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("git diff %s: %s", rev, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git diff %s: %w — is %s a git worktree?", rev, err, dir)
	}
	var files []string
	for _, l := range strings.Split(string(out), "\x00") {
		if l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}
