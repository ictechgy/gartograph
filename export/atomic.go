package export

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// WriteFile은 그래프·baseline 산출물을 같은 디렉터리의 고유 임시 파일로 교체한다.
// 고정된 .tmp 이름은 동시 실행이 서로의 내용을 덮거나 기존 링크의 대상을 훼손하므로
// 재사용하지 않는다. 새 파일은 소유자만 읽고 쓸 수 있고, 기존 일반 파일은 권한을 보존한다.
// rename 이전의 실패는 기존 산출물을 그대로 두고 이 호출이 만든 임시 파일만 정리한다.
func WriteFile(path string, data []byte) (err error) {
	mode := os.FileMode(0o600)
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode().IsRegular() {
			mode = info.Mode().Perm()
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspecting output %s: %w", path, statErr)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".gartograph-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary output for %s: %w", path, err)
	}
	closed, committed := false, false
	defer func() {
		if !closed {
			if closeErr := file.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("closing temporary output: %w", closeErr))
			}
		}
		if !committed {
			if removeErr := os.Remove(file.Name()); removeErr != nil {
				err = errors.Join(err, fmt.Errorf("removing temporary output: %w", removeErr))
			}
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("writing output %s: %w", path, err)
	}
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("setting output permissions %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("syncing output %s: %w", path, err)
	}
	closeErr := file.Close()
	closed = true
	if closeErr != nil {
		return fmt.Errorf("closing output %s: %w", path, closeErr)
	}
	if err := replaceOutput(file.Name(), path); err != nil {
		return fmt.Errorf("replacing output %s: %w", path, err)
	}
	committed = true
	return nil
}

// replaceOutput은 Windows에서 다른 writer의 닫히는 핸들이 잠깐 막은 교체만 재시도한다.
// 목적지를 먼저 지우지 않아 실패하면 원본이 남는다. 다른 플랫폼·오류는 곧바로 돌려준다.
var outputReplaceMu sync.Mutex

func replaceOutput(from, to string) error {
	if runtime.GOOS == "windows" {
		outputReplaceMu.Lock()
		defer outputReplaceMu.Unlock()
	}
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		err = os.Rename(from, to)
		if err == nil || runtime.GOOS != "windows" || !errors.Is(err, os.ErrPermission) {
			return err
		}
		if attempt < 7 {
			time.Sleep(time.Duration(1<<attempt) * time.Millisecond)
		}
	}
	return err
}
