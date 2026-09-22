// 문서 비교 질의 — 두 저장 문서 사이의 구조 차이를 보고한다.
// 영속 문서가 산출물이라는 계약 위에서, diff는 아키텍처 드리프트와
// 공개 API 변경 신호를 CI가 소비할 수 있는 형태로 만든다.
package analysis

import (
	"fmt"
	"sort"

	"github.com/ictechgy/gartograph/graph"
)

// VertexChange는 같은 ID의 정점에서 값이 바뀐 필드 하나다.
// Position 변화는 노이즈라 제외한다 — 함수가 같은 파일 안에서
// 줄만 옮겨도 드리프트로 보이면 리포트가 무뎌진다.
type VertexChange struct {
	ID    string `json:"id"`
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// SignatureChange는 공개 심볼의 signature 간선 목표 집합이 바뀐 기록이다.
// 공개 API가 새 타입을 누출하기 시작했거나(Added) 기존 참조가
// 사라졌는지(Removed)를 구분한다 — Removed가 호환성 위험 신호에 가깝다.
type SignatureChange struct {
	ID      string   `json:"id"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

// Diff는 두 문서 사이의 차이다.
// Breaking은 --strict가 1을 돌려줄 변경이다 — 공개 심볼이 사라지거나
// 공개 시그니처가 타입 참조를 잃은 경우다. 추가는 호환되는 변경이라 세지 않는다.
type Diff struct {
	AddedVertices    []string          `json:"addedVertices,omitempty"`
	RemovedVertices  []string          `json:"removedVertices,omitempty"`
	AddedEdges       []graph.Edge      `json:"addedEdges,omitempty"`
	RemovedEdges     []graph.Edge      `json:"removedEdges,omitempty"`
	ChangedVertices  []VertexChange    `json:"changedVertices,omitempty"`
	SignatureChanges []SignatureChange `json:"signatureChanges,omitempty"`
	Breaking         []string          `json:"breaking,omitempty"`
	Notes            []string          `json:"notes,omitempty"`
}

// DiffDocuments는 old→new 문서 차이를 계산한다.
// 레벨이 다르면 정점 집합 자체가 달라 의미 없는 diff가 되므로 notes에 적는다 —
// 에러로 돌리지 않는 이유는 소비자가 차이를 보고 판단할 수 있어야 하기 때문이다.
func DiffDocuments(old, new *graph.Document) *Diff {
	d := &Diff{}
	if old.Level != new.Level {
		d.Notes = append(d.Notes, fmt.Sprintf(
			"level mismatch: old=%q new=%q — vertex sets are not comparable across levels",
			old.Level, new.Level))
	}
	if old.Module != new.Module {
		d.Notes = append(d.Notes, fmt.Sprintf(
			"module mismatch: old=%q new=%q", old.Module, new.Module))
	}
	diffVertices(d, old, new)
	diffEdges(d, old, new)
	sort.Strings(d.Notes)
	return d
}

// diffVertices는 정점 집합 차이와 공개 심볼의 signature 간선 변화를 채운다.
func diffVertices(d *Diff, old, new *graph.Document) {
	oldV := indexVertices(old)
	newV := indexVertices(new)
	oldSig := signatureTargets(old)
	newSig := signatureTargets(new)

	for _, id := range sortedKeys(oldV) {
		nv, ok := newV[id]
		if !ok {
			d.RemovedVertices = append(d.RemovedVertices, id)
			if oldV[id].Exported {
				d.Breaking = append(d.Breaking,
					fmt.Sprintf("exported vertex removed: %s", id))
			}
			continue
		}
		recordVertexChanges(d, id, oldV[id], nv)
		recordSignatureChange(d, id, oldV[id], oldSig[id], newSig[id])
	}
	for _, id := range sortedKeys(newV) {
		if _, ok := oldV[id]; !ok {
			d.AddedVertices = append(d.AddedVertices, id)
		}
	}
}

// recordVertexChanges는 kind·exported·generated 플래그의 뒤바뀜을 적는다.
func recordVertexChanges(d *Diff, id string, ov, nv *graph.Vertex) {
	field := func(name string, a, b bool) {
		if a != b {
			d.ChangedVertices = append(d.ChangedVertices, VertexChange{
				ID: id, Field: name, From: fmt.Sprint(a), To: fmt.Sprint(b)})
		}
	}
	if ov.Kind != nv.Kind {
		d.ChangedVertices = append(d.ChangedVertices, VertexChange{
			ID: id, Field: "kind", From: string(ov.Kind), To: string(nv.Kind)})
	}
	field("exported", ov.Exported, nv.Exported)
	field("generated", ov.Generated, nv.Generated)
}

// recordSignatureChange는 exported 심볼의 signature 간선 목표 차이를 적는다.
// 비공개 심볼의 시그니처 변화는 API 계약과 무관해 보고하지 않는다.
func recordSignatureChange(d *Diff, id string, ov *graph.Vertex,
	oldT, newT map[string]bool) {
	if !ov.Exported {
		return
	}
	var added, removed []string
	for t := range newT {
		if !oldT[t] {
			added = append(added, t)
		}
	}
	for t := range oldT {
		if !newT[t] {
			removed = append(removed, t)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return
	}
	sort.Strings(added)
	sort.Strings(removed)
	d.SignatureChanges = append(d.SignatureChanges,
		SignatureChange{ID: id, Added: added, Removed: removed})
	for _, t := range removed {
		d.Breaking = append(d.Breaking,
			fmt.Sprintf("exported signature %s no longer references %s", id, t))
	}
}

// diffEdges는 (from,to,kind) 삼중 집합의 차이를 채운다.
// contains도 의존과 다른 사실이므로 빼지 않는다 — 문서의 모든 간선이 대상이다.
func diffEdges(d *Diff, old, new *graph.Document) {
	oldE := indexEdges(old)
	newE := indexEdges(new)
	for _, e := range sortedEdgeKeys(oldE) {
		if _, ok := newE[e]; !ok {
			d.RemovedEdges = append(d.RemovedEdges, oldE[e])
		}
	}
	for _, e := range sortedEdgeKeys(newE) {
		if _, ok := oldE[e]; !ok {
			d.AddedEdges = append(d.AddedEdges, newE[e])
		}
	}
}

// indexVertices는 정점 ID → 정점 색인이다.
func indexVertices(d *graph.Document) map[string]*graph.Vertex {
	out := make(map[string]*graph.Vertex, len(d.Vertices))
	for i := range d.Vertices {
		out[d.Vertices[i].ID] = &d.Vertices[i]
	}
	return out
}

// signatureTargets는 심볼 ID → signature 간선 목표 집합이다.
func signatureTargets(d *graph.Document) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, e := range d.Edges {
		if e.Kind != graph.EdgeSignature {
			continue
		}
		if out[e.From] == nil {
			out[e.From] = map[string]bool{}
		}
		out[e.From][e.To] = true
	}
	return out
}

// edgeKey는 간선의 집합 동일성 키다 — 위치가 아니라 관계가 단위다.
func edgeKey(e graph.Edge) string {
	return e.From + "\x00" + e.To + "\x00" + string(e.Kind)
}

// indexEdges는 간선 키 → 간선 색인이다.
func indexEdges(d *graph.Document) map[string]graph.Edge {
	out := make(map[string]graph.Edge, len(d.Edges))
	for _, e := range d.Edges {
		out[edgeKey(e)] = e
	}
	return out
}

// sortedKeys는 맵 키를 정렬해 돌려준다 — diff 출력이 실행마다 흔들리지 않게.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedEdgeKeys는 간선 색인의 키를 정렬해 돌려준다.
func sortedEdgeKeys(m map[string]graph.Edge) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
