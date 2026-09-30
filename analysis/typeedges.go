package analysis

import (
	"strings"

	"github.com/ictechgy/gartograph/graph"
)

// 타입 간선 순회 모드다(reach·impact --format language-traversal의 --type-edges).
//
//   - all: 모든 의존 간선을 그대로 따른다(이전 동작). struct 타입 정점은 필드 선언의 타입 참조를
//     타입 정점에서 긋는다(수확 specEdges). 그래서 메서드 → 리시버 타입 → 필드 타입으로 이어져
//     같은 struct의 모든 메서드가 다른 필드 뒤의 타입(행 타입과 그 태그 컬럼)에 닿는다. 역방향도
//     필드 타입 → 컨테이너 타입 → 그 타입의 모든 메서드로 퍼진다.
//   - members: 필드 선언에서 나온 타입 구조 간선(T → X, references·signature)을 그 필드 정점의
//     간선(f → X)으로 옮긴다. 어느 필드인지는 타입 정점의 필드 목록(정규 타입 문자열)으로 정한다. 타입 정점 T에 닿아도 필드 타입까지 펼치지 않고, 필드를 실제로 쓰는
//     선언(f를 참조)만 X로 이어진다. 두 방향 모두 같은 평범한 그래프라 순회 문서의 via·roots
//     일관성(isthmus 검증)이 그대로 성립한다.
//
// members가 잃는 도달은 "필드 이름을 쓰지 않고 struct 값 전체를 넘기는" 경로뿐이다(encoding/json·ORM
// 리플렉션처럼 값 전체를 읽는 코드): 그 struct의 임베드가 아닌 필드 타입(과 그 태그)에는 닿지 않는다.
// 임베드는 embeds 간선이라 옮기지 않으므로 승격 필드·메서드는 그대로다. 타입 자신(과 그 태그)에는
// 여전히 닿는다. 필드 목록이 없는 옛 문서는 옮길 간선이 없어 all과 같다.
const (
	TypeEdgesMembers = "members"
	TypeEdgesAll     = "all"
)

// virtualEdge는 순회 인접 목록을 만들 재료 간선이다(원래 방향).
type virtualEdge struct {
	from, to  string
	kind      graph.EdgeKind
	candidate bool
}

// fieldIndex는 정점 종류, struct 타입 정점의 필드 목록(Vertex.Fields, "이름:정규 타입"), 타입 정점
// 위치다.
type fieldIndex struct {
	kinds  map[string]graph.VertexKind
	fields map[string][]string
	pos    map[string]*graph.Position
	first  map[string]graph.Position // 타입의 첫 필드 정점 위치(타입 머리 판정용)
}

// newFieldIndex는 정점 색인을 만든다.
func newFieldIndex(d *graph.Document) fieldIndex {
	idx := fieldIndex{kinds: map[string]graph.VertexKind{}, fields: map[string][]string{},
		pos: map[string]*graph.Position{}, first: map[string]graph.Position{}}
	for _, v := range d.Vertices {
		idx.kinds[v.ID] = v.Kind
		idx.pos[v.ID] = v.Position
		if v.Kind == graph.KindType && len(v.Fields) > 0 {
			idx.fields[v.ID] = v.Fields
		}
	}
	for _, v := range d.Vertices {
		if v.Kind != graph.KindField || v.Position == nil {
			continue
		}
		owner, ok := graph.MemberOwner(v.ID)
		if !ok {
			continue
		}
		owner = idx.typeID(owner)
		if cur, seen := idx.first[owner]; !seen || positionLess(*v.Position, cur) {
			idx.first[owner] = *v.Position
		}
	}
	return idx
}

// typeID는 멤버 ID에서 얻은 소유 타입 ID를 문서의 정점 ID로 맞춘다(패키지와 겹치면 충돌 접미사).
func (idx fieldIndex) typeID(owner string) string {
	if idx.kinds[owner] != graph.KindType && idx.kinds[owner+graph.CollisionSuffix] == graph.KindType {
		return owner + graph.CollisionSuffix
	}
	return owner
}

// positionLess는 (파일, 줄, 열) 순서다.
func positionLess(a, b graph.Position) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column < b.Column
}

// fieldOwners는 struct 타입 T의 구조 간선 T → X(references·signature)를 필드로 옮길 수 있으면 그
// 필드 정점들을 돌려준다: 필드 목록에서 정규 타입이 X를 담는 필드(`a, b *X`는 둘 다, `m map[K]X`,
// `Box[X]`, `struct{ x X }`도)다. 간선 위치가 첫 필드보다 앞(타입 파라미터 제약 등 타입 머리)이거나,
// X를 담는 필드가 없거나, 필드 목록·정점이 없는 옛 문서면 nil이다 — 그 간선은 옮기지 않는다(all과 같은
// 도달).
func (idx fieldIndex) fieldOwners(e graph.Edge) []string {
	fields := idx.fields[e.From]
	if idx.kinds[e.From] != graph.KindType || len(fields) == 0 ||
		(e.Kind != graph.EdgeReferences && e.Kind != graph.EdgeSignature) {
		return nil
	}
	if first, ok := idx.first[e.From]; ok {
		for _, p := range e.Positions {
			if positionLess(p, first) {
				return nil
			}
		}
	}
	target := graph.CanonicalID(e.To)
	member := graph.CanonicalID(e.From)
	// 타입 정점 ID는 "경로.이름"이다(선언 타입이라 타입 인자가 붙지 않는다). 점이 없는 ID는 모양을 모르므로
	// 옮기지 않는다.
	pkgEnd := strings.LastIndex(member, ".")
	if pkgEnd < 0 {
		return nil
	}
	var out []string
	for _, f := range fields {
		name, typ, ok := strings.Cut(f, ":")
		if !ok || !mentionsType(typ, target) {
			continue
		}
		id := member[:pkgEnd] + ".(" + member[pkgEnd+1:] + ")." + name
		if idx.kinds[id] == graph.KindField {
			out = append(out, id)
		}
	}
	return out
}

// mentionsType은 정규 타입 문자열이 타입 ID("경로.이름")를 한 토큰으로 담는지 본다 — 앞뒤가 경로·
// 식별자 문자면 다른 타입(`api.OrderStore`의 `api.Order`)이다.
func mentionsType(typ, id string) bool {
	for from := 0; ; {
		i := strings.Index(typ[from:], id)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(id)
		if (start == 0 || !isPathByte(typ[start-1])) && (end == len(typ) || !isIdentByte(typ[end])) {
			return true
		}
		from = start + 1
	}
}

// isIdentByte는 Go 식별자 바이트인지 본다.
func isIdentByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

// isPathByte는 import 경로·식별자 바이트인지 본다.
func isPathByte(c byte) bool {
	return isIdentByte(c) || c == '.' || c == '/' || c == '-' || c == '~'
}

// traversalEdges는 모드에 맞는 재료 간선이다(contains 제외). members는 필드 구조 간선을 필드 정점의
// 간선으로 옮긴다. 두 번째 값은 옮긴 간선의 (타입, 필드 타입) 쌍이다 — 좁힌 곳을 세는 재료다.
func traversalEdges(d *graph.Document, typeEdges string) ([]virtualEdge, []pairKey) {
	members := typeEdges == TypeEdgesMembers
	var idx fieldIndex
	if members {
		idx = newFieldIndex(d)
	}
	out := make([]virtualEdge, 0, len(d.Edges))
	var moved []pairKey
	for _, e := range d.Edges {
		if e.Kind == graph.EdgeContains {
			continue
		}
		var owners []string
		if members {
			owners = idx.fieldOwners(e)
		}
		if owners == nil {
			out = append(out, virtualEdge{from: e.From, to: e.To, kind: e.Kind, candidate: e.Candidate})
			continue
		}
		moved = append(moved, pairKey{e.From, e.To})
		for _, f := range owners {
			out = append(out, virtualEdge{from: f, to: e.To, kind: e.Kind, candidate: e.Candidate})
		}
	}
	return out, moved
}

// countNarrowed는 members 때문에 닿지 않은 정점 수다: 같은 root·깊이 상한으로 all 그래프를 한 번에
// 넓혀(다중 출발 BFS — 도달 집합은 root별 순회의 합집합과 같다) 닿는 정점 중 members 순회가 싣지 않은
// 것. members가 오히려 더 닿는 경우(도달하지 않은 타입의 필드를 읽는 코드가 그 필드 타입에 닿음)는
// 세지 않는다 — --type-edges all로 다시 돌렸을 때 더해지는 것만 센다.
func countNarrowed(d *graph.Document, direction string, roots []string, maxDepth int, rows []TraversalReached) int {
	adj, _ := buildTraversalAdjacency(d, direction, false, TypeEdgesAll)
	isRoot := map[string]bool{}
	for _, r := range roots {
		isRoot[r] = true
	}
	reached := map[string]bool{}
	for _, row := range rows {
		reached[row.ID] = true
	}
	seen := map[string]bool{}
	frontier := append([]string(nil), roots...)
	for _, r := range roots {
		seen[r] = true
	}
	missed := 0
	for level := 1; level <= maxDepth && len(frontier) > 0; level++ {
		var next []string
		for _, node := range frontier {
			for _, nb := range adj.next[node] {
				if seen[nb] {
					continue
				}
				seen[nb] = true
				next = append(next, nb)
				if !isRoot[nb] && !reached[nb] {
					missed++
				}
			}
		}
		frontier = next
	}
	return missed
}
