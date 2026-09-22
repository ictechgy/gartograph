// impact --since/--files — git diff 또는 명시 파일 목록 기반 영향 분석.
// VCS 사실 수집은 여기서만 한다 — analysis는 문서 위의 순수 해석이다.
package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// changedFilesSince는 `git -C dir diff --name-only --relative <rev>`의
// 결과를 돌려준다. --relative로 저장소 루트가 아닌 --dir 기준 경로를 얻는다.
// rev는 그대로 git에 넘기므로 `origin/main...HEAD` 같은 범위 표기도 쓸 수 있다.
func changedFilesSince(dir, rev string) ([]string, error) {
	cmd := exec.Command("git", "-C", dir, "diff", "--name-only", "--relative", rev)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("git diff %s: %s", rev, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git diff %s: %w — is %s a git worktree?", rev, err, dir)
	}
	var files []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}
