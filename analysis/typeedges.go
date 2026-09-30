package analysis

import (
	"sort"

	"github.com/ictechgy/gartograph/graph"
)

// 타입 간선 순회 모드다(reach·impact --format language-traversal의 --type-edges).
//
//   - all: 모든 의존 간선을 그대로 따른다(이전 동작). struct 타입 정점은 필드 선언의 타입 참조를
//     타입 정점에서 긋는다(수확 specEdges). 그래서 메서드 → 리시버 타입 → 필드 타입으로 이어져
//     같은 struct의 모든 메서드가 다른 필드 뒤의 타입(행 타입과 그 태그 컬럼)에 닿는다. 역방향도
//     필드 타입 → 컨테이너 타입 → 그 타입의 모든 메서드로 퍼진다.
//   - members: 필드 선언에서 나온 타입 구조 간선(T → X, references·signature)을 그 필드 정점의
//     간선(f → X)으로 옮긴다. 타입 정점 T에 닿아도 필드 타입까지 펼치지 않고, 필드를 실제로 쓰는
//     선언(f를 참조)만 X로 이어진다. 두 방향 모두 같은 평범한 그래프라 순회 문서의 via·roots
//     일관성(isthmus 검증)이 그대로 성립한다.
//
// members가 잃는 도달은 "필드 이름을 쓰지 않고 struct 값 전체를 넘기는" 경로뿐이다(encoding/json·ORM
// 리플렉션처럼 값 전체를 읽는 코드): 그 struct의 임베드가 아닌 필드 타입(과 그 태그)에는 닿지 않는다.
// 임베드는 embeds 간선이라 옮기지 않으므로 승격 필드·메서드는 그대로다. 타입 자신(과 그 태그)에는
// 여전히 닿는다. 필드 위치를 모르는 옛 문서(간선 위치 없음)는 옮길 간선이 없어 all과 같다.
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

// fieldSpot은 필드 정점 하나와 그 위치다.
type fieldSpot struct {
	id  string
	pos graph.Position
}

// fieldIndex는 정점 종류와 타입 정점 ID → 필드(위치순)·구조 간선 위치(위치순)이다.
type fieldIndex struct {
	kinds  map[string]graph.VertexKind
	fields map[string][]fieldSpot
	refs   map[string][]graph.Position
}

// newFieldIndex는 타입별 필드 목록을 모은다. 필드 소유 타입 ID가 패키지와 겹치면 문서의 타입 정점에는
// 충돌 접미사가 붙어 있어 두 형태를 다 본다.
func newFieldIndex(d *graph.Document) fieldIndex {
	idx := fieldIndex{kinds: map[string]graph.VertexKind{}, fields: map[string][]fieldSpot{},
		refs: map[string][]graph.Position{}}
	for _, v := range d.Vertices {
		idx.kinds[v.ID] = v.Kind
	}
	for _, v := range d.Vertices {
		if v.Kind != graph.KindField || v.Position == nil {
			continue
		}
		owner, ok := graph.MemberOwner(v.ID)
		if !ok {
			continue
		}
		if idx.kinds[owner] != graph.KindType && idx.kinds[owner+graph.CollisionSuffix] == graph.KindType {
			owner += graph.CollisionSuffix
		}
		idx.fields[owner] = append(idx.fields[owner], fieldSpot{id: v.ID, pos: *v.Position})
	}
	for _, spots := range idx.fields {
		sort.Slice(spots, func(i, j int) bool { return positionLess(spots[i].pos, spots[j].pos) })
	}
	for _, e := range d.Edges {
		if idx.kinds[e.From] == graph.KindType && (e.Kind == graph.EdgeReferences || e.Kind == graph.EdgeSignature) {
			idx.refs[e.From] = append(idx.refs[e.From], e.Positions...)
		}
	}
	for _, ps := range idx.refs {
		sort.Slice(ps, func(i, j int) bool { return positionLess(ps[i], ps[j]) })
	}
	return idx
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

// fieldOwners는 타입 선언의 구조 간선(references·signature)이 모두 필드 선언 안에서 나왔으면 그
// 필드들을 돌려준다(fieldsAt의 귀속 규칙). 필드에 귀속되지 않는 위치(타입 파라미터 제약, 필드 없는
// 타입 정의 등)가 하나라도 있거나 위치가 없으면 nil이다 — 그 간선은 옮기지 않는다(all과 같은 도달).
func (idx fieldIndex) fieldOwners(e graph.Edge) []string {
	if idx.kinds[e.From] != graph.KindType || len(e.Positions) == 0 ||
		(e.Kind != graph.EdgeReferences && e.Kind != graph.EdgeSignature) {
		return nil
	}
	spots := idx.fields[e.From]
	seen := map[string]bool{}
	var out []string
	for _, p := range e.Positions {
		owners := fieldsAt(spots, idx.refs[e.From], p)
		if len(owners) == 0 {
			return nil
		}
		for _, id := range owners {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// fieldsAt은 위치 p(타입 표현식 안의 참조)가 속할 수 있는 필드들이다. p 이전의 가장 가까운 필드 줄을
// L이라 하면, L보다 앞 줄에 있는 이 타입의 마지막 구조 참조 q 뒤부터 p까지 선언된 필드 전부다 —
// `a,\n b *X`처럼 이름이 앞 줄로 넘어간 필드(a)도 X에 귀속한다. 사이에 참조가 없는 필드(`id int`처럼
// 모듈 밖·기본 타입 필드)까지 귀속될 수 있지만 그것은 도달을 넓힐 뿐이다 — 필드 이름을 쓰는 참 도달을
// 잃지 않는 쪽을 고른다. 필드 줄이 없으면(필드 밖 위치) 빈 목록이다.
func fieldsAt(spots []fieldSpot, refs []graph.Position, p graph.Position) []string {
	line := -1
	for _, s := range spots {
		if s.pos.File == p.File && !positionLess(p, s.pos) {
			line = s.pos.Line
		}
	}
	if line < 0 {
		return nil
	}
	var after *graph.Position
	for i := range refs {
		q := refs[i]
		if q.File == p.File && q.Line < line {
			after = &refs[i]
		}
	}
	var out []string
	for _, s := range spots {
		if s.pos.File != p.File || positionLess(p, s.pos) || (after != nil && !positionLess(*after, s.pos)) {
			continue
		}
		out = append(out, s.id)
	}
	return out
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
