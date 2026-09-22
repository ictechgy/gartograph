// Package export는 Document를 바깥 형식으로 직렬화한다.
// 출력은 결정적이어야 한다 — 같은 입력이 같은 바이트가 아니면
// 리포트 diff와 캐시가 무의미해진다.
package export

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ictechgy/gartograph/graph"
)

// JSON은 Document를 결정적 JSON으로 직렬화한다.
// 호출 전에 Sort로 정규화했다고 가정하고, Go의 encoding/json은
// 맵 키를 정렬하므로 구조만 정렬돼 있으면 바이트가 안정적이다.
func JSON(d *graph.Document) ([]byte, error) {
	d.Sort()
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding graph document: %w", err)
	}
	return append(out, '\n'), nil
}

// Mermaid는 Document를 mermaid flowchart로 직렬화한다.
// 정점 ID의 `/`·`.` 같은 문자는 mermaid 식별자가 될 수 없어
// 안정적인 짧은 ID로 바꾸고 라벨에 원래 ID를 싣는다.
func Mermaid(d *graph.Document) ([]byte, error) {
	d.Sort()
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	ids := mermaidIDs(d)
	for _, v := range d.Vertices {
		fmt.Fprintf(&b, "  %s[%s]\n", ids[v.ID], escapeLabel(v.ID))
	}
	for _, e := range d.Edges {
		if e.Kind == graph.EdgeContains {
			continue
		}
		fmt.Fprintf(&b, "  %s -->|%s| %s\n", ids[e.From], e.Kind, ids[e.To])
	}
	return []byte(b.String()), nil
}

// mermaidIDs는 정점 ID를 mermaid 안전 식별자로 매핑한다.
// 정렬된 정점 순서로 n0, n1... 을 부여하므로 같은 입력은 같은 ID다.
func mermaidIDs(d *graph.Document) map[string]string {
	ids := make(map[string]string, len(d.Vertices))
	for i, v := range d.Vertices {
		ids[v.ID] = fmt.Sprintf("n%d", i)
	}
	return ids
}

// escapeLabel은 라벨 안의 큰따옴표를 mermaid가 먹는 형태로 감싼다.
func escapeLabel(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `#quot;`) + `"`
}
