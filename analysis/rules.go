// 레이어 규칙 질의 — import 간선이 .gartograph.yml의 컴포넌트 규칙을 어기는지 검사한다.
package analysis

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/graph"
)

// Violation은 규칙 위반 하나다 — 실제 간선이 evidence다.
// Rule은 어긴 규칙 종류다: "allow"(허용 목록에 없음), "deny"(명시 금지),
// "signature"(공개 API 타입 누출), "visibleTo"(공급자가 닫은 컴포넌트),
// "forbidden"(간접 도달 금지), "independence"(독립 선언된 컴포넌트 간 도달),
// "fileScope"(import 지점의 파일 패턴 위반),
// "stability"(자기보다 불안정한 컴포넌트로의 의존).
// Name은 fileScope 위반이 어긴 규칙의 이름이다 — 같은 to 컴포넌트를
// 다른 파일 패턴으로 막는 규칙이 여럿일 때 구분자다.
// Reason은 deny·fileRules 규칙에 설정된 사유다 — 에이전트가 다음 행동을
// 고를 정보다.
// Path는 forbidden·independence 위반의 목격 경로다 — 간선이 아니라 도달
// 사실을 어겼으므로 어느 사슬로 닿았는지를 함께 준다. 그 위반들은 단일
// 간선이 아니라 Kind가 비어 있다.
// Position은 위반 간선의 첫 사용 지점이다 — "어디를 고치면 되나"에 답한다.
// fileScope 위반은 실제로 매칭된 지점을 가리킨다. 지점이 없는 관계
// (forbidden 등)나 v1 문서에서는 비어 있다.
type Violation struct {
	From          string          `json:"from"`
	To            string          `json:"to"`
	FromComponent string          `json:"fromComponent"`
	ToComponent   string          `json:"toComponent"`
	Kind          graph.EdgeKind  `json:"kind,omitempty"`
	Rule          string          `json:"rule"`
	Name          string          `json:"name,omitempty"`
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
// FileScopeUnchecked는 fileRules의 to 컴포넌트로 향하면서 사용 지점이
// 없어 파일 스코프를 검사할 수 없는 import 간선 수다 — v1 문서나 위치를
// 싣지 않는 수확의 신호로, limitation으로 승격된다.
type RuleReport struct {
	Violations          []Violation `json:"violations"`
	Unmapped            []string    `json:"unmapped,omitempty"`
	UnmappedExternal    []string    `json:"unmappedExternal,omitempty"`
	UnmatchedComponents []string    `json:"unmatchedComponents,omitempty"`
	FileScopeUnchecked  int         `json:"fileScopeUnchecked,omitempty"`
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
			// fileRules는 허용 목록과 별개의 축이다 — deps가 허용한 의존도
			// import 지점의 파일이 규칙에 걸리면 위반이다.
			fv, unchecked := fileViolations(d, cfg, e, from, to)
			rep.Violations = append(rep.Violations, fv...)
			rep.FileScopeUnchecked += unchecked
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
	rep.Violations = append(rep.Violations, stabilityViolations(d, cfg, comp)...)
	rep.Violations = append(rep.Violations, limitViolations(d, cfg, comp)...)
	rep.Unmapped = mapping.Unmapped
	rep.UnmappedExternal = mapping.UnmappedExternal
	rep.UnmatchedComponents = mapping.UnmatchedComponents
	slices.SortStableFunc(rep.Violations, compareViolations)
	return rep
}

// compareViolations는 같은 간선의 여러 규칙·위치도 결정적으로 정렬한다.
func compareViolations(a, b Violation) int {
	for _, pair := range [][2]string{{a.From, b.From}, {a.To, b.To}, {a.Rule, b.Rule},
		{string(a.Kind), string(b.Kind)}, {a.Name, b.Name}, {a.Reason, b.Reason},
		{a.FromComponent, b.FromComponent}, {a.ToComponent, b.ToComponent}} {
		if order := strings.Compare(pair[0], pair[1]); order != 0 {
			return order
		}
	}
	if order := slices.Compare(a.Path, b.Path); order != 0 {
		return order
	}
	if a.Position == nil {
		if b.Position == nil {
			return 0
		}
		return -1
	}
	if b.Position == nil {
		return 1
	}
	return cmp.Or(strings.Compare(a.Position.File, b.Position.File),
		cmp.Compare(a.Position.Line, b.Position.Line), cmp.Compare(a.Position.Column, b.Position.Column))
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

// fileViolations는 import 간선 하나를 fileRules와 대조한다.
// 간선의 사용 지점 파일이 규칙의 from 패턴에 맞으면 위반이다 — 지점마다
// 별도 위반을 내서 "어느 파일을 고치면 되나"를 정확히 가리킨다.
// 대상 컴포넌트로 향하는데 지점이 없는 간선은 검사 불가라 수를 세어
// 돌려준다 — 조용히 통과하면 "규칙이 지켜졌다"는 착각을 만든다.
func fileViolations(d *graph.Document, cfg *config.File, e graph.Edge,
	fromComp, toComp string) (violations []Violation, unchecked int) {
	if len(cfg.FileRules) == 0 || toComp == "" {
		return nil, 0
	}
	var applies bool
	for _, r := range cfg.FileRules {
		if r.To == toComp {
			applies = true
			break
		}
	}
	if !applies {
		return nil, 0
	}
	if len(e.Positions) == 0 {
		return nil, 1
	}
	for i := range e.Positions {
		p := e.Positions[i]
		rel := relFile(d, p.File)
		for _, r := range cfg.FileRules {
			if r.To != toComp || !config.MatchFile(r.From, rel) {
				continue
			}
			pos := p
			violations = append(violations, Violation{
				From: e.From, To: e.To,
				FromComponent: fromComp, ToComponent: toComp,
				Kind: e.Kind, Rule: "fileScope",
				Name: r.Name, Reason: r.Reason, Position: &pos,
			})
		}
	}
	return violations, 0
}

// relFile은 절대 파일 경로를 그 파일이 속한 모듈 기준 상대 경로로 바꾼다 — 설정의 파일 패턴이
// 수확 머신의 절대 경로가 아니라 이식 가능한 경로에 맞도록. 기준은 fileModule이 고른다(go.work
// 워크스페이스면 파일을 담는 워크스페이스 모듈, 아니면 ModuleDir, 그것도 없으면 Root).
func relFile(d *graph.Document, abs string) string {
	_, base := fileModule(d, abs)
	if rel, err := filepath.Rel(base, abs); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(abs)
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

// stabilityViolations는 안정성 방향 계약을 검사한다 — stability가 켜지면
// 컴포넌트는 자기보다 불안정한 컴포넌트에 의존할 수 없다.
// dependency-cruiser의 moreUnstable 규칙과 같은 계약이다.
// 불안정성은 metrics와 같은 Ce/(Ca+Ce)다 — 같은 수치가 규칙과 메트릭에서
// 다른 값을 내면 보고가 거짓말이 된다. 위반에는 두 수치를 사유로 싣는다 —
// 어느 쪽을 안정화해야 하는지가 다음 행동이다.
func stabilityViolations(d *graph.Document, cfg *config.File,
	comp map[string]string) []Violation {
	if !cfg.Stability {
		return nil
	}
	inDeg, outDeg := componentDegrees(d, comp)
	inst := func(c string) float64 {
		ca, ce := len(inDeg[c]), len(outDeg[c])
		if ca+ce == 0 {
			return 0
		}
		return float64(ce) / float64(ca+ce)
	}
	var out []Violation
	for _, e := range d.Edges {
		if e.Kind != graph.EdgeImport {
			continue
		}
		fu, tu := comp[e.From], comp[e.To]
		if fu == "" || tu == "" || fu == tu {
			continue
		}
		if inst(tu) <= inst(fu) {
			continue
		}
		out = append(out, Violation{
			From: e.From, To: e.To,
			FromComponent: fu, ToComponent: tu,
			Kind: e.Kind, Rule: "stability",
			Reason: fmt.Sprintf("depends on a less stable component (I %.3g > %.3g)",
				inst(tu), inst(fu)),
			Position: firstPosition(e),
		})
	}
	return out
}

// componentDegrees는 컴포넌트 단위의 들어오는/나가는 의존 집합을 센다.
// 같은 컴포넌트 안의 의존은 결합이 아니라 제외하고, 같은 쌍의 간선이
// 여러 개여도 컴포넌트 쌍은 하나로 센다 — stability와 limits가 같은
// 수치를 보아야 보고가 거짓말하지 않는다.
func componentDegrees(d *graph.Document, comp map[string]string) (inDeg, outDeg map[string]map[string]bool) {
	inDeg, outDeg = map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, e := range d.Edges {
		if e.Kind != graph.EdgeImport {
			continue
		}
		fu, tu := comp[e.From], comp[e.To]
		if fu == "" || tu == "" || fu == tu {
			continue
		}
		if outDeg[fu] == nil {
			outDeg[fu] = map[string]bool{}
		}
		if inDeg[tu] == nil {
			inDeg[tu] = map[string]bool{}
		}
		outDeg[fu][tu] = true
		inDeg[tu][fu] = true
	}
	return inDeg, outDeg
}

// limitViolations는 컴포넌트의 의존 수 상한을 검사한다.
// 상한 초과는 어느 한 간선의 위반이 아니라 컴포넌트 덩치의 사실이다 —
// From/To 정점은 비우고 컴포넌트와 실제 수치를 사유로 싣는다.
// Name에는 어긴 상한 종류(maxIn/maxOut)를 넣는다 — 같은 컴포넌트에
// 두 상한이 걸릴 때의 구분자이고 baseline 동일성의 재료다.
func limitViolations(d *graph.Document, cfg *config.File,
	comp map[string]string) []Violation {
	if len(cfg.Limits) == 0 {
		return nil
	}
	inDeg, outDeg := componentDegrees(d, comp)
	var out []Violation
	for _, r := range cfg.Limits {
		if r.MaxOut != nil && len(outDeg[r.Component]) > *r.MaxOut {
			out = append(out, Violation{
				FromComponent: r.Component,
				Rule:          "limit",
				Name:          "maxOut",
				Reason: fmt.Sprintf("depends on %d components; limit maxOut is %d",
					len(outDeg[r.Component]), *r.MaxOut),
			})
		}
		if r.MaxIn != nil && len(inDeg[r.Component]) > *r.MaxIn {
			out = append(out, Violation{
				ToComponent: r.Component,
				Rule:        "limit",
				Name:        "maxIn",
				Reason: fmt.Sprintf("depended on by %d components; limit maxIn is %d",
					len(inDeg[r.Component]), *r.MaxIn),
			})
		}
	}
	return out
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
// 패키지가 속한 모듈의 경로 접두사를 벗겨 상대 경로로 패턴과 맞춘다(packageModule) — go.work
// 워크스페이스에서는 패키지마다 자기 워크스페이스 모듈이 기준이라 exclude와 같은 패키지를 가리킨다.
// 모듈 밖의 패키지는 접두사가 벗겨지지 않아 전체 import 경로가 그대로 패턴과 맞는다 —
// `github.com/aws/**` 같은 컴포넌트가 --deps 수확에서 vendor 규칙이 된다.
// 두 번째 반환값은 실제로 한 정점 이상에 매칭된 컴포넌트 집합이다.
func componentMap(d *graph.Document, cfg *config.File) (map[string]string, map[string]bool) {
	out := make(map[string]string)
	used := make(map[string]bool)
	for _, v := range d.Vertices {
		if v.Kind != graph.KindPackage {
			continue
		}
		rel := graph.ModuleRelative(v.ID, packageModule(d, v))
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
// 워크스페이스 문서(isWorkspaceDocument)의 External은 빠짐없이 수확한 사실이라 그대로 믿는다 — 형제
// 워크스페이스 모듈의 패키지는 문서 Module 접두사가 없어도 외부가 아니다.
// 그 밖의 문서에서 표시가 없으면 경로 접두사로 폴백한다(External을 싣기 전의 옛 문서) — 모듈 정보가
// 없는 문서는 모두 내부로 본다.
func isExternalPackage(d *graph.Document, v graph.Vertex) bool {
	if v.External {
		return true
	}
	if isWorkspaceDocument(d) {
		return false
	}
	return d.Module != "" && v.ID != d.Module &&
		!strings.HasPrefix(v.ID, d.Module+"/")
}

// isWorkspaceDocument는 문서가 패키지 정점의 모듈 소속(graph.Document.WorkspaceModules)을 담았는지
// 본다 — 둘 이상의 주 모듈을 수확한 go.work 문서만 싣는다. 없는 문서(단일 모듈, 그 사실을 수확하기 전의
// 옛 문서)는 문서 Module 하나로 해석한다 — 옛 문서의 결과가 그대로여야 하기 때문이다.
func isWorkspaceDocument(d *graph.Document) bool {
	return len(d.WorkspaceModules) > 0
}

// packageModule은 패키지 정점의 패턴 기준 모듈 경로다 — graph.ModuleRelative에 넘기는 값이다.
// 워크스페이스 문서는 수확 사실을 그대로 쓴다: 외부 패키지는 ""(전체 import 경로로 맞춘다), 형제
// 워크스페이스 모듈의 패키지는 정점의 Module, 나머지는 문서 Module이다. exclude(source.excludeMatch)와
// 같은 기준이다. 그 밖의 문서는 지금처럼 모든 패키지를 문서 Module 기준으로 벗긴다 — 접두사가 맞는
// 패키지만 벗겨지므로 외부 패키지는 전체 경로로 남는다.
func packageModule(d *graph.Document, v graph.Vertex) string {
	switch {
	case !isWorkspaceDocument(d):
		return d.Module
	case v.External:
		return ""
	case v.Module != "":
		return v.Module
	default:
		return d.Module
	}
}

// fileModule은 파일이 속한 모듈의 경로와 go.mod 디렉터리를 고른다 — 파일→패키지 해석(packageForFile)과
// fileRules의 파일 경로(relFile)가 같은 기준을 쓴다. 워크스페이스 문서에서는 파일을 담는 워크스페이스 모듈
// 디렉터리 중 가장 깊은 것이다(다른 워크스페이스 모듈 디렉터리 안에 놓인 모듈도 맞게 고른다). 담는 모듈이
// 없거나 워크스페이스 문서가 아니면 문서 Module과 ModuleDir(없으면 Root)이다 — 예전 기준 그대로다.
func fileModule(d *graph.Document, abs string) (modulePath, dir string) {
	modulePath, dir = d.Module, d.ModuleDir
	if dir == "" {
		dir = d.Root
	}
	depth := -1
	for _, m := range d.WorkspaceModules {
		if len(m.Dir) > depth && containsFile(m.Dir, abs) {
			modulePath, dir, depth = m.Path, m.Dir, len(m.Dir)
		}
	}
	return modulePath, dir
}

// containsFile은 절대 경로 abs가 dir 아래에 있는지 어휘적으로 본다(심링크를 풀지 않는다 — 저장 문서의
// 경로는 이 머신에 없을 수 있다). 상대 경로를 만들 수 없으면(다른 볼륨, 빈 dir·상대 dir) 담지 않는다.
func containsFile(dir, abs string) bool {
	rel, err := filepath.Rel(dir, abs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
