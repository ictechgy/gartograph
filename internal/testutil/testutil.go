// Package testutil은 테스트용 임시 Go 모듈과 특수 파일(FIFO), 막힘 회귀 검사를 제공한다.
// source·config·cli 테스트가 같은 fixture 규약을 써야 결과를 비교할 수 있어
// 한 곳에 둔다.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// BlockDeadline은 FinishWithin에 넘기는 기한이다. 지키려는 경로는 작은 fixture라 1초 안쪽에
// 끝난다 — 기한은 "영원히 막힘"과 "바쁜 CI에서 느림"을 가르는 여유다.
const BlockDeadline = 30 * time.Second

// MakeFIFO는 path에 이름 있는 파이프를 만든다. 쓰는 쪽이 없는 FIFO는 보통의 읽기 open에서
// 영원히 막힌다 — 분석 대상이 고른 경로를 열기 전에 파일 종류를 확인하는지 검증하는 재료다.
// FIFO가 없는 windows에서는 테스트를 건너뛴다. syscall.Mkfifo 대신 mkfifo(1)을 쓰는 이유는
// 빌드 제약 없이 모든 플랫폼에서 테스트가 컴파일되게 하기 위해서다.
func MakeFIFO(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("named pipes (mkfifo) are POSIX-only")
	}
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo %s: %v\n%s", path, err, out)
	}
}

// FinishWithin은 fn이 d 안에 끝나는지 본다. 끝나지 않으면 테스트를 실패로 끝내고 fn의
// 고루틴은 남긴다 — 막힌 open·read는 취소할 수 없다. 그래서 막혀도 메모리·CPU를 쓰지 않는
// 입력(쓰는 쪽이 없는 FIFO)의 회귀 검사에만 쓴다. fn 안에서는 t.Fatal을 부르지 말고 결과를
// 바깥 변수에 담는다 — FailNow는 테스트 고루틴에서만 유효하다.
func FinishWithin(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("did not finish within %v; a read of a non-regular file (FIFO, device) probably blocked", d)
	}
}

// WriteModule은 files를 담은 임시 모듈 디렉터리를 만들고 경로를 돌려준다.
// go.mod는 files에 없으면 모듈 경로 example.com/fixture로 자동 생성한다 —
// fixture마다 go.mod를 반복해서 적지 않기 위함이다.
func WriteModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"] = "module example.com/fixture\n\ngo 1.27\n"
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}
