// 그래프 산출물의 파일 입출력.
// Go에는 컴파일러 인덱스 스토어가 없으므로, 이 문서가 영속 산출물이다.
package export

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ictechgy/gartograph/graph"
)

// SaveFile은 Document를 JSON 파일로 쓴다.
// 부분 쓰기가 남지 않게 임시 파일에 쓰고 rename한다.
func SaveFile(d *graph.Document, path string) error {
	data, err := JSON(d)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s: %w", path, err)
	}
	return nil
}

// LoadFile은 JSON 파일에서 Document를 읽는다.
// 더 새로운 버전의 문서는 거부한다 — 모르는 필드가 있는 문서를 읽으면
// 조용히 정보를 잃어 오독된다.
func LoadFile(path string) (*graph.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var d graph.Document
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if d.Version > graph.Version {
		return nil, fmt.Errorf(
			"%s: document version %d > supported %d — rebuild with a newer gartograph",
			path, d.Version, graph.Version)
	}
	return &d, nil
}
