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
// "signature"(공개 API 타입 누출), "visibleTo"(공급자가 닫은 컴포넌트),
// "forbidden"(간접 도달 금지).
// Reason은 deny 규칙에 설정된 사유다 — 에이전트가 다음 행동을 고를 정보다.
// Path는 forbidden 위반의 목격 경로다 — 간선이 아니라 도달 사실을 어겼으므로
// 어느 사슬로 닿았는지를 함께 준다. forbidden 위반은 단일 간선이 아니라
// Kind가 비어 있다.
// Position은 위반 간선의 첫 사용 지점이다 — "어디를 고치면 되나"에 답한다.
// 지점이 없는 관계(forbidden 등)나 v1 문서에서는 비어 있다.
type Violation struct {
	From          string          `json:"from"`
	To            string          `json:"to"`
	FromComponent string          `json:"fromComponent"`
	ToComponent   string          `json:"toComponent"`
	Kind          graph.EdgeKind  `json:"kind,omitempty"`
	Rule          string          `json:"rule"`
	Reason        string          `json:"reason,omitempty"`
	Path          []string        `json:"path,omitempty"`
	Position      *graph.Position `json:"position,omitempty"`
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
	// 매핑은 한 번만 해석한다 — 위반 보고와 unmapped 보고가 같은 해석을
	// 써야 한 리포트 안에서 모순이 생기지 않는다.
	mapping := MapComponents(d, cfg)
	comp := map[string]string{}
	for name, pkgs := range mapping.Components {
		for _, p := range pkgs {
			comp[p] = name
		}
	}
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
		rule, reason := ruleBroken(cfg, e.Kind, from, to)
		if rule != "" {
			rep.Violations = append(rep.Violations, Violation{
				From: e.From, To: e.To,
				FromComponent: from, ToComponent: to,
				Kind: e.Kind, Rule: rule, Reason: reason,
				Position: firstPosition(e),
			})
		}
	}
	rep.Violations = append(rep.Violations, reachViolations(d, cfg, comp)...)
	rep.Unmapped = mapping.Unmapped
	rep.UnmappedExternal = mapping.UnmappedExternal
	rep.UnmatchedComponents = mapping.UnmatchedComponents
	sort.Slice(rep.Violations, func(i, j int) bool {
		if rep.Violations[i].From != rep.Violations[j].From {
			return rep.Violations[i].From < rep.Violations[j].From
		}
		return rep.Violations[i].To < rep.Violations[j].To
	})
	return rep
}

// ruleBroken은 간선이 어긴 규칙 이름과 사유를 돌려준다. 위반이 없으면 ""다.
// deny가 가장 먼저다 — 명시 금지는 허용 목록보다 우선한다.
// signature 간선은 시그니처 규칙과 허용 목록 둘 다를 통과해야 한다 —
// 시그니처 참조도 의존이기 때문이다.
// visibleTo는 허용 목록을 통과한 뒤에 본다 — 소비자가 deps로 허용했어도
// 공급자가 닫아 두면 위반이다.
func ruleBroken(cfg *config.File, kind graph.EdgeKind, from, to string) (string, string) {
	if reason, denied := cfg.Denied(from, to); denied {
		return "deny", reason
	}
	if kind == graph.EdgeSignature && !cfg.SignatureAllowed(from, to) {
		return "signature", ""
	}
	if !cfg.Allowed(from, to) {
		return "allow", ""
	}
	if !cfg.Visible(from, to) {
		return "visibleTo", ""
	}
	return "", ""
}

// reachViolations는 도달성 계약을 검사한다 — forbidden은 한 방향,
// independent는 목록 안 모든 쌍의 양방향이다. 직접 간선만 보는
// deps/deny로는 "A가 C에 도달하면 안 됨"을 잡을 수 없다.
// 위반마다 목격 경로 하나를 실어 소비자가 사슬을 바로 볼 수 있게 한다.
func reachViolations(d *graph.Document, cfg *config.File,
	comp map[string]string) []Violation {
	if len(cfg.Forbidden) == 0 && len(cfg.Independent) == 0 {
		return nil
	}
	// 정점→컴포넌트 해석: 패키지 정점은 자기 ID로, 심볼·타입은 소속 패키지로.
	vcomp := map[string]string{}
	for _, v := range d.Vertices {
		if v.Kind == graph.KindPackage {
			vcomp[v.ID] = comp[v.ID]
		} else {
			vcomp[v.ID] = comp[v.Package]
		}
	}
	adj := graph.Adjacency(d)
	var out []Violation
	for _, rule := range cfg.Forbidden {
		if path := reachPath(adj, vcomp, rule.From, rule.To); path != nil {
			out = append(out, Violation{
				From: path[0], To: path[len(path)-1],
				FromComponent: rule.From, ToComponent: rule.To,
				Rule: "forbidden", Path: path,
			})
		}
	}
	// independent는 순서 없는 쌍 계약이다 — 어느 쪽이 먼저 선언됐는지가
	// 아니라 도달 방향이 위반을 만든다. 같은 쌍이 중복 선언돼도
	// 위반은 한 번만 보고한다.
	pairs := map[string]bool{}
	for i, a := range cfg.Independent {
		for _, b := range cfg.Independent[i+1:] {
			if a == b || pairs[a+"\x00"+b] {
				continue
			}
			pairs[a+"\x00"+b] = true
			for _, dir := range [2][2]string{{a, b}, {b, a}} {
				if path := reachPath(adj, vcomp, dir[0], dir[1]); path != nil {
					out = append(out, Violation{
						From: path[0], To: path[len(path)-1],
						FromComponent: dir[0], ToComponent: dir[1],
						Rule: "independence", Path: path,
					})
				}
			}
		}
	}
	return out
}

// reachPath는 from 컴포넌트의 어느 정점에서 to 컴포넌트의 어느 정점까지의
// 최단 의존 경로를 BFS로 찾는다. 없으면 nil이다.
// 시작점은 정렬해 둬야 실행마다 같은 목격 경로가 나온다.
func reachPath(adj map[string][]string, vcomp map[string]string,
	from, to string) []string {
	var starts []string
	for id, c := range vcomp {
		if c == from {
			starts = append(starts, id)
		}
	}
	sort.Strings(starts)
	parent := map[string]string{}
	queue := append([]string(nil), starts...)
	for _, s := range starts {
		parent[s] = ""
	}
	var hit string
	for len(queue) > 0 && hit == "" {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if _, seen := parent[next]; seen {
				continue
			}
			parent[next] = cur
			if vcomp[next] == to {
				hit = next
				break
			}
			queue = append(queue, next)
		}
	}
	if hit == "" {
		return nil
	}
	var path []string
	for cur := hit; cur != ""; cur = parent[cur] {
		path = append([]string{cur}, path...)
	}
	return path
}

// firstPosition은 간선의 첫 사용 지점을 돌려준다 — 위반 보고는
// "가장 먼저 나오는" 지점 하나를 가리키고, 전체 지점은 문서의 간선에 남는다.
func firstPosition(e graph.Edge) *graph.Position {
	if len(e.Positions) == 0 {
		return nil
	}
	p := e.Positions[0]
	return &p
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

// Mapping은 패키지 정점의 컴포넌트 해석 결과다 — 규칙이 각 패키지를
// 어느 컴포넌트로 봤는지, 어느 패키지·컴포넌트가 해석 밖인지를 담는다.
// "규칙이 왜 이 의존을 못 잡지"를 디버깅하는 mapping 명령의 산출물이다.
type Mapping struct {
	Components          map[string][]string `json:"components"`
	Unmapped            []string            `json:"unmapped,omitempty"`
	UnmappedExternal    []string            `json:"unmappedExternal,omitempty"`
	UnmatchedComponents []string            `json:"unmatchedComponents,omitempty"`
}

// MapComponents는 컴포넌트 → 패키지 정점 역방향 매핑과 미매핑 목록을 만든다.
// CheckRules와 같은 해석을 쓰되 보고 형태만 다르다 — 두 경로가 따로
// 매핑을 만들면 결과가 어긋날 수 있다.
func MapComponents(d *graph.Document, cfg *config.File) *Mapping {
	comp, used := componentMap(d, cfg)
	m := &Mapping{Components: map[string][]string{}}
	for _, v := range d.Vertices {
		if v.Kind != graph.KindPackage {
			continue
		}
		c := comp[v.ID]
		switch {
		case c != "":
			m.Components[c] = append(m.Components[c], v.ID)
		case isExternalPackage(d, v):
			m.UnmappedExternal = append(m.UnmappedExternal, v.ID)
		default:
			m.Unmapped = append(m.Unmapped, v.ID)
		}
	}
	for name := range cfg.Components {
		if !used[name] {
			m.UnmatchedComponents = append(m.UnmatchedComponents, name)
		}
		if _, ok := m.Components[name]; !ok {
			m.Components[name] = nil
		}
	}
	for _, pkgs := range m.Components {
		sort.Strings(pkgs)
	}
	sort.Strings(m.Unmapped)
	sort.Strings(m.UnmappedExternal)
	sort.Strings(m.UnmatchedComponents)
	return m
}

// isExternalPackage는 패키지 정점이 주 모듈 밖에 있는지 본다.
// 정점의 External 표시가 정본이다 — 수확 시점의 모듈 소속 사실.
// 표시가 없는 옛 문서는 경로 접두사로 폴백한다 — 모듈 정보가 없는 문서는
// 모두 내부로 본다.
func isExternalPackage(d *graph.Document, v graph.Vertex) bool {
	if v.External {
		return true
	}
	return d.Module != "" && v.ID != d.Module &&
		!strings.HasPrefix(v.ID, d.Module+"/")
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
