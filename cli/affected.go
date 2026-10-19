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
// (git 2.24 이상 — 더 오래된 git은 이 옵션을 사용법 오류로 거부하므로 그 실패는 요구 버전을
// 담아 알린다, sinceUsageError). 분석 대상 .git 설정의 명령이 이 읽기 전용 질의에서 실행되면 안 된다
// (DECISIONS D1): gitCommand가 filter 드라이버를 빈 명령으로 덮고 fsmonitor 훅·선택 잠금을 끈다.
// 여기서는 외부 diff·textconv를 끄고(--no-ext-diff --no-textconv), 서브모듈 안에서 git을 띄우지 않는다
// (--ignore-submodules=dirty — 서브모듈 HEAD가 기록과 다르면 그 경로는 여전히 바뀐 파일이다).
// diff.autoRefreshIndex=false는 stat만 바뀐 파일을 내용 비교로 거르고 index를 다시 쓰는 단계를 끈다 —
// 그 쓰기가 post-index-change 훅을 실행한다. 그래서 stat만 바뀐 파일도 바뀐 파일로 나온다(넓히는
// 쪽, fail-safe). 격리할 수 없으면 git을 실행하지 않고 --files를 안내한다.
// --no-renames는 rename을 delete+add로 펼친다 — 옛 경로도 변경 파일이어야
// 그 패키지의 의존자가 영향 집합에 남는다. -z는 NUL 구분이라 인용되지 않은
// 파일명을 그대로 준다 — 공백·비ASCII 이름도 안전하다.
func changedFilesSince(dir, rev string) ([]string, error) {
	if err := checkSinceRevision(rev); err != nil {
		return nil, err
	}
	cmd, err := gitCommand(dir, "-c", "diff.autoRefreshIndex=false", "diff", "--name-only", "--relative",
		"--no-renames", "-z", "--no-ext-diff", "--no-textconv", "--ignore-submodules=dirty",
		"--end-of-options", rev, "--")
	if err != nil {
		return nil, fmt.Errorf("impact --since %s did not run git diff: %w; pass the changed files with --files instead",
			rev, err)
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == gitUsageExitCode {
			return nil, sinceUsageError(rev, ee.Stderr)
		}
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

// gitUsageExitCode는 git이 사용법 오류(모르는 옵션, 맞지 않는 인자 조합)로 끝낼 때의 종료 코드다.
const gitUsageExitCode = 129

// sinceUsageError는 git diff가 인자를 사용법 오류로 거부했을 때의 오류다. 고정 인자 중 git이 모를 수
// 있는 것은 --end-of-options(git 2.24 이상)뿐이고, 그 밖에는 커밋이 아닌 revision(HEAD:file 같은
// blob)이 이 오류를 낸다 — 두 원인과 해결 방향을 함께 적는다. git의 사용법 안내는 수십 줄이라 첫
// 줄만 싣는다 — 그대로 실으면 원인 문장이 안내문에 묻힌다.
func sinceUsageError(rev string, stderr []byte) error {
	said, _, _ := strings.Cut(strings.TrimSpace(string(stderr)), "\n")
	return fmt.Errorf("git diff %s rejected the arguments as a usage error (%s): impact --since needs "+
		"git 2.24 or later (it passes --end-of-options) and a revision that names commits, such as "+
		"HEAD~1 or origin/main...HEAD; upgrade git or pass the changed files with --files", rev, said)
}
