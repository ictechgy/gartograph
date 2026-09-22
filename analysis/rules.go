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

// RuleReport는 규칙 검사 결과다.
// Violations는 규칙을 어긴 실제 간선, Unmapped는 모듈 내부인데
// 컴포넌트 미매핑인 패키지, UnmappedExternal은 --deps로 들어온
// 외부 패키지 중 미매핑이다 — "규칙이 모르는 내부 영역"과
// "규칙이 굳이 매핑하지 않은 바깥"은 다른 사실이라 섞지 않는다.
// UnmatchedComponents는 어떤 패키지 정점에도 매칭되지 않은 컴포넌트다 —
// 오타·stale·"외부 패턴인데 --deps 없이 수확"의 신호다.
type RuleReport struct {
	Violations          []Violation `json:"violations"`
	Unmapped            []string    `json:"unmapped,omitempty"`
	UnmappedExternal    []string    `json:"unmappedExternal,omitempty"`
	UnmatchedComponents []string    `json:"unmatchedComponents,omitempty"`
}

// CheckRules는 import 간선을 컴포넌트 규칙과 대조하고, 문서가 심볼 레벨이면
// signature 규칙도 검사한다. 어떤 컴포넌트에도 매핑되지 않은 패키지는
// unmapped로 돌려준다 — 매핑 구멍은 "규칙 무관"이 아니라 "규칙이 모르는 영역"이다.
func CheckRules(d *graph.Document, cfg *config.File) *RuleReport {
	comp, used := componentMap(d, cfg)
	vmap := vertexMap(d)
	rep := &RuleReport{}
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
			rep.Violations = append(rep.Violations, Violation{
				From: e.From, To: e.To,
				FromComponent: from, ToComponent: to,
				Kind: e.Kind, Rule: rule,
			})
		}
	}
	for _, v := range d.Vertices {
		if v.Kind != graph.KindPackage || comp[v.ID] != "" {
			continue
		}
		if isExternalPackage(d, v.ID) {
			rep.UnmappedExternal = append(rep.UnmappedExternal, v.ID)
		} else {
			rep.Unmapped = append(rep.Unmapped, v.ID)
		}
	}
	for name := range cfg.Components {
		if !used[name] {
			rep.UnmatchedComponents = append(rep.UnmatchedComponents, name)
		}
	}
	sort.Slice(rep.Violations, func(i, j int) bool {
		if rep.Violations[i].From != rep.Violations[j].From {
			return rep.Violations[i].From < rep.Violations[j].From
		}
		return rep.Violations[i].To < rep.Violations[j].To
	})
	sort.Strings(rep.Unmapped)
	sort.Strings(rep.UnmappedExternal)
	sort.Strings(rep.UnmatchedComponents)
	return rep
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
// 모듈 경로 접두사를 벗겨 상대 경로로 패턴과 맞춘다. 모듈 밖의 패키지는
// 접두사가 벗겨지지 않아 전체 import 경로가 그대로 패턴과 맞는다 —
// `github.com/aws/**` 같은 컴포넌트가 --deps 수확에서 vendor 규칙이 된다.
// 두 번째 반환값은 실제로 한 정점 이상에 매칭된 컴포넌트 집합이다.
func componentMap(d *graph.Document, cfg *config.File) (map[string]string, map[string]bool) {
	out := make(map[string]string)
	used := make(map[string]bool)
	for _, v := range d.Vertices {
		if v.Kind != graph.KindPackage {
			continue
		}
		rel := relPath(v.ID, d.Module)
		if c, ok := cfg.ComponentOf(rel); ok {
			out[v.ID] = c
			used[c] = true
		}
	}
	return out, used
}

// isExternalPackage는 패키지 정점이 주 모듈 밖에 있는지 본다.
// --deps로 수확된 의존 패키지만 여기 해당한다 — 모듈 정보가 없는 문서는
// 모두 내부로 본다.
func isExternalPackage(d *graph.Document, pkgID string) bool {
	return d.Module != "" && pkgID != d.Module &&
		!strings.HasPrefix(pkgID, d.Module+"/")
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
