// 레이어 규칙 질의 — import 간선이 .gartograph.yml의 컴포넌트 규칙을 어기는지 검사한다.
package analysis

import (
	"sort"
	"strings"

	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/graph"
)

// Violation은 규칙 위반 하나다 — 실제 간선이 evidence다.
// Rule은 어긴 규칙 종류다: "allow"(허용 목록에 없음), "deny"(명시 금지),
// "signature"(공개 API 타입 누출).
type Violation struct {
	From          string         `json:"from"`
	To            string         `json:"to"`
	FromComponent string         `json:"fromComponent"`
	ToComponent   string         `json:"toComponent"`
	Kind          graph.EdgeKind `json:"kind"`
	Rule          string         `json:"rule"`
}

// CheckRules는 import 간선을 컴포넌트 규칙과 대조하고, 문서가 심볼 레벨이면
// signature 규칙도 검사한다. 어떤 컴포넌트에도 매핑되지 않은 패키지는
// unmapped로 돌려준다 — 매핑 구멍은 "규칙 무관"이 아니라 "규칙이 모르는 영역"이다.
func CheckRules(d *graph.Document, cfg *config.File) (violations []Violation, unmapped []string) {
	comp := componentMap(d, cfg)
	vmap := vertexMap(d)
	for _, e := range d.Edges {
		var from, to string
		switch e.Kind {
		case graph.EdgeImport:
			from, to = comp[e.From], comp[e.To]
		case graph.EdgeSignature:
			// signature 규칙은 설정된 컴포넌트의 exported 심볼에서만 검사한다.
			if len(cfg.Signature) == 0 {
				continue
			}
			src, ok := vmap[e.From]
			if !ok || !src.Exported {
				continue
			}
			from, to = comp[src.Package], comp[vmap[e.To].Package]
		default:
			continue
		}
		if from == "" || to == "" {
			continue
		}
		rule := ruleBroken(cfg, e.Kind, from, to)
		if rule != "" {
			violations = append(violations, Violation{
				From: e.From, To: e.To,
				FromComponent: from, ToComponent: to,
				Kind: e.Kind, Rule: rule,
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

// ruleBroken은 간선이 어긴 규칙 이름을 돌려준다. 위반이 없으면 ""다.
// deny가 가장 먼저다 — 명시 금지는 허용 목록보다 우선한다.
// signature 간선은 시그니처 규칙과 허용 목록 둘 다를 통과해야 한다 —
// 시그니처 참조도 의존이기 때문이다.
func ruleBroken(cfg *config.File, kind graph.EdgeKind, from, to string) string {
	if cfg.Denied(from, to) {
		return "deny"
	}
	if kind == graph.EdgeSignature && !cfg.SignatureAllowed(from, to) {
		return "signature"
	}
	if !cfg.Allowed(from, to) {
		return "allow"
	}
	return ""
}

// vertexMap은 정점 ID로 정점을 찾는 인덱스다.
func vertexMap(d *graph.Document) map[string]graph.Vertex {
	out := make(map[string]graph.Vertex, len(d.Vertices))
	for _, v := range d.Vertices {
		out[v.ID] = v
	}
	return out
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
