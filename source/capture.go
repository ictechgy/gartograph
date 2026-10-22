package source

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// ReadCaptureFile은 캡처 중 파일 종류가 바뀌어도 FIFO에서 멈추거나 장치를 읽지 않는다.
func ReadCaptureFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.OpenFile(path, readFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("cannot capture non-regular source %s", path)
	}
	return io.ReadAll(io.LimitReader(f, maxBytes+1))
}

// ValidateCapture는 복제한 소스가 원 작업 트리나 다른 revision을 로드하지 않게 한다.
// 저장소 밖의 로컬 모듈은 해시로 고정하지 못하므로 부분 성공 대신 준비 방법을 알린다.
func ValidateCapture(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() != "go.mod" && entry.Name() != "go.work" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		check := func(local string) error {
			if filepath.IsAbs(local) {
				return fmt.Errorf("captured %s refers to absolute local module %q; prepare a repository-relative module", entry.Name(), local)
			}
			rel, relErr := filepath.Rel(root, filepath.Join(filepath.Dir(path), local))
			if relErr != nil || !filepath.IsLocal(rel) {
				return fmt.Errorf("captured %s refers outside repository: %q; include local dependencies in the repository", entry.Name(), local)
			}
			return nil
		}
		if entry.Name() == "go.mod" {
			file, parseErr := modfile.Parse(path, data, nil)
			if parseErr != nil {
				return parseErr
			}
			for _, replace := range file.Replace {
				if replace.New.Version == "" {
					if err := check(replace.New.Path); err != nil {
						return err
					}
				}
			}
		} else {
			file, parseErr := modfile.ParseWork(path, data, nil)
			if parseErr != nil {
				return parseErr
			}
			for _, use := range file.Use {
				if err := check(use.Path); err != nil {
					return err
				}
			}
			for _, replace := range file.Replace {
				if replace.New.Version == "" {
					if err := check(replace.New.Path); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}
