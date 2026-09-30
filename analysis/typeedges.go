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

// fieldIndex는 정점 종류와 타입 정점 ID → 필드(위치순)이다.
type fieldIndex struct {
	kinds  map[string]graph.VertexKind
	fields map[string][]fieldSpot
}

// newFieldIndex는 타입별 필드 목록을 모은다. 필드 소유 타입 ID가 패키지와 겹치면 문서의 타입 정점에는
// 충돌 접미사가 붙어 있어 두 형태를 다 본다.
func newFieldIndex(d *graph.Document) fieldIndex {
	idx := fieldIndex{kinds: map[string]graph.VertexKind{}, fields: map[string][]fieldSpot{}}
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
// 필드들을 돌려준다. 한 위치는 그 위치 이전에서 가장 가까운 필드 줄의 필드들(`a, b *X`는 둘 다)에
// 귀속한다. 필드에 귀속되지 않는 위치(타입 파라미터 제약, 필드 없는 타입 정의 등)가 하나라도 있거나
// 위치가 없으면 nil이다 — 그 간선은 옮기지 않는다(all과 같은 도달).
func (idx fieldIndex) fieldOwners(e graph.Edge) []string {
	if idx.kinds[e.From] != graph.KindType || len(e.Positions) == 0 ||
		(e.Kind != graph.EdgeReferences && e.Kind != graph.EdgeSignature) {
		return nil
	}
	spots := idx.fields[e.From]
	seen := map[string]bool{}
	var out []string
	for _, p := range e.Positions {
		owners := fieldsAt(spots, p)
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

// fieldsAt은 위치 p를 담는 필드 줄(p 이전에서 가장 가까운 필드 줄, 열이 p 이전인 필드)의 필드다.
func fieldsAt(spots []fieldSpot, p graph.Position) []string {
	line := -1
	for _, s := range spots {
		if s.pos.File == p.File && !positionLess(p, s.pos) {
			line = s.pos.Line
		}
	}
	if line < 0 {
		return nil
	}
	var out []string
	for _, s := range spots {
		if s.pos.File == p.File && s.pos.Line == line && !positionLess(p, s.pos) {
			out = append(out, s.id)
		}
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

// countNarrowed는 members가 실제로 좁힌 곳의 수다: 옮긴 구조 간선 (T, X) 중 순회 방향의 출발
// 끝(dependencies는 T, dependents는 X)이 root이거나 닿았는데 도착 끝은 닿지 않은 도착 정점의 수 —
// all이었다면 그 정점까지 이어졌을 것이다(깊이·출력 상한 안에서).
func countNarrowed(moved []pairKey, direction string, roots []string, rows []TraversalReached) int {
	seen := map[string]bool{}
	for _, r := range roots {
		seen[r] = true
	}
	for _, row := range rows {
		seen[row.ID] = true
	}
	missed := map[string]bool{}
	for _, m := range moved {
		from, to := m.from, m.to
		if direction == DirectionDependents {
			from, to = m.to, m.from
		}
		if seen[from] && !seen[to] {
			missed[to] = true
		}
	}
	return len(missed)
}
