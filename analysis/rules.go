// 레이어 규칙 질의 — import 간선이 .gartograph.yml의 컴포넌트 규칙을 어기는지 검사한다.
package analysis

import (
	"sort"
	"strings"

	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/graph"
)

// Violation은 규칙 위반 하나다 — 실제 간선이 evidence다.
type Violation struct {
	From          string         `json:"from"`
	To            string         `json:"to"`
	FromComponent string         `json:"fromComponent"`
	ToComponent   string         `json:"toComponent"`
	Kind          graph.EdgeKind `json:"kind"`
}

// CheckRules는 패키지 레벨 import 간선을 컴포넌트 규칙과 대조한다.
// 어떤 컴포넌트에도 매핑되지 않은 패키지는 unmapped로 돌려준다 —
// 매핑 구멍은 "규칙 무관"이 아니라 "규칙이 모르는 영역"이다.
func CheckRules(d *graph.Document, cfg *config.File) (violations []Violation, unmapped []string) {
	comp := componentMap(d, cfg)
	for _, e := range d.Edges {
		if e.Kind != graph.EdgeImport {
			continue
		}
		from, to := comp[e.From], comp[e.To]
		if from == "" || to == "" {
			continue
		}
		if !cfg.Allowed(from, to) {
			violations = append(violations, Violation{
				From: e.From, To: e.To,
				FromComponent: from, ToComponent: to, Kind: e.Kind,
			})
		}
	}
	unmapped = unmappedPackages(d, comp)
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].From != violations[j].From {
			return violations[i].From < violations[j].From
		}
		return violations[i].To < violations[j].To
	})
	return violations, unmapped
}

// componentMap은 패키지 정점을 컴포넌트로 해석한다.
// 모듈 경로 접두사를 벗겨 상대 경로로 패턴과 맞춘다.
func componentMap(d *graph.Document, cfg *config.File) map[string]string {
	out := make(map[string]string)
	for _, v := range d.Vertices {
		if v.Kind != graph.KindPackage {
			continue
		}
		rel := relPath(v.ID, d.Module)
		if c, ok := cfg.ComponentOf(rel); ok {
			out[v.ID] = c
		}
	}
	return out
}

// unmappedPackages는 어떤 컴포넌트에도 속하지 않은 패키지 정점을 모은다.
func unmappedPackages(d *graph.Document, comp map[string]string) []string {
	var out []string
	for _, v := range d.Vertices {
		if v.Kind == graph.KindPackage && comp[v.ID] == "" {
			out = append(out, v.ID)
		}
	}
	sort.Strings(out)
	return out
}

// relPath는 패키지 경로를 모듈 상대 경로로 바꾼다.
// Root가 모듈 경로일 때만 접두사를 벗긴다 — 파일시스템 경로가 아니라
// 모듈 경로 기준으로 맞춰야 설정 파일이 이식 가능하다.
func relPath(pkgPath, modulePath string) string {
	if modulePath == "" {
		return pkgPath
	}
	if pkgPath == modulePath {
		return "."
	}
	return strings.TrimPrefix(pkgPath, modulePath+"/")
}
