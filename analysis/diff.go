// 문서 비교 질의 — 두 저장 문서 사이의 구조 차이를 보고한다.
// 영속 문서가 산출물이라는 계약 위에서, diff는 아키텍처 드리프트와
// 공개 API 변경 신호를 CI가 소비할 수 있는 형태로 만든다.
package analysis

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

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

// FieldChange는 struct 타입 정점의 필드 목록 차이다.
// unkeyed composite literal의 계약이 필드 목록·순서·타입이므로
// 필드 변화는 이름(added/removed)과 재형(declared name은 같지만
// 타입이 다름 — removed로도 잡힌다)으로 나눠 적는다.
type FieldChange struct {
	ID        string   `json:"id"`
	Added     []string `json:"added,omitempty"`
	Removed   []string `json:"removed,omitempty"`
	Retyped   []string `json:"retyped,omitempty"`
	Reordered bool     `json:"reordered,omitempty"`
}

// Diff는 두 문서 사이의 차이다.
// Breaking은 --strict가 1을 돌려줄 변경이다 — 공개 심볼이 사라지거나
// 비공개로 바뀌거나, 공개 심볼의 kind가 바뀌거나, 공개 시그니처가 타입
// 참조를 잃거나, 인터페이스가 메서드를 얻거나, struct 필드 계약이 깨진
// 경우다. 추가는 호환되는 변경이라 세지 않는다 — 인터페이스 메서드
// 추가만 예외로 breaking이다(구현자 전부가 깨진다).
type Diff struct {
	AddedVertices    []string          `json:"addedVertices,omitempty"`
	RemovedVertices  []string          `json:"removedVertices,omitempty"`
	AddedEdges       []graph.Edge      `json:"addedEdges,omitempty"`
	RemovedEdges     []graph.Edge      `json:"removedEdges,omitempty"`
	ChangedVertices  []VertexChange    `json:"changedVertices,omitempty"`
	SignatureChanges []SignatureChange `json:"signatureChanges,omitempty"`
	FieldChanges     []FieldChange     `json:"fieldChanges,omitempty"`
	Breaking         []string          `json:"breaking,omitempty"`
	Notes            []string          `json:"notes,omitempty"`
	// 양쪽 문서의 수확 limitation을 출처와 함께 싣는다 — 한쪽이 부분
	// 수확이면 diff의 제거/추가 신호가 실제 변경이 아닐 수 있다.
	OldLimitations []string `json:"oldLimitations,omitempty"`
	NewLimitations []string `json:"newLimitations,omitempty"`
}

// DiffDocuments는 old→new 문서 차이를 계산한다.
// 레벨이 다르면 정점 집합 자체가 달라 의미 없는 diff가 되므로 notes에 적는다 —
// 에러로 돌리지 않는 이유는 소비자가 차이를 보고 판단할 수 있어야 하기 때문이다.
func DiffDocuments(old, new *graph.Document) *Diff {
	d := &Diff{OldLimitations: old.Limitations, NewLimitations: new.Limitations}
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
	diffIfaceMethods(d, old, new)
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
		recordFieldChanges(d, id, oldV[id], nv)
		recordSignatureChange(d, id, oldV[id], oldSig[id], newSig[id])
	}
	for _, id := range sortedKeys(newV) {
		if _, ok := oldV[id]; !ok {
			d.AddedVertices = append(d.AddedVertices, id)
		}
	}
}

// recordVertexChanges는 kind·exported·generated 플래그의 뒤바뀜을 적는다.
// 공개 심볼의 kind 변경과 공개→비공개 전환은 소비자의 컴파일을 깨는
// breaking 변경이다 — 같은 이름이어도 `func F`가 `var F`가 되면
// 호출부가 깨진다.
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
		if ov.Exported {
			d.Breaking = append(d.Breaking, fmt.Sprintf(
				"exported symbol %s changed kind: %s -> %s", id, ov.Kind, nv.Kind))
		}
	}
	field("exported", ov.Exported, nv.Exported)
	if ov.Exported && !nv.Exported {
		d.Breaking = append(d.Breaking, fmt.Sprintf(
			"exported symbol %s became unexported", id))
	}
	field("generated", ov.Generated, nv.Generated)
	field("external", ov.External, nv.External)
	// 상수 값 변경은 소비자 코드에 인라인된 계약의 변경이다 — 컴파일은
	// 지나가도 이미 빌드된 바이너리가 다른 상수를 담고 있다는 뜻이다.
	// apidiff가 값 변경을 breaking으로 보는 이유다.
	// 어느 쪽이든 값이 비어 있으면 "몰랐다"다 — 값을 수확하기 전 형식의
	// 문서와 비교할 때 모든 상수를 변경으로 울리면 안 된다.
	if ov.Kind == graph.KindConst && ov.Value != "" && nv.Value != "" &&
		ov.Value != nv.Value {
		d.ChangedVertices = append(d.ChangedVertices, VertexChange{
			ID: id, Field: "value", From: ov.Value, To: nv.Value})
		if ov.Exported {
			d.Breaking = append(d.Breaking, fmt.Sprintf(
				"exported const %s changed value: %s -> %s", id, ov.Value, nv.Value))
		}
	}
}

// recordFieldChanges는 struct 타입의 필드 목록 차이를 적는다.
// apidiff의 판정을 따른다:
//   - 공개 필드의 제거·재형(type 변경)은 breaking이다.
//   - 모든 필드가 공개인 struct는 unkeyed literal이 가능하므로
//     필드 목록이 조금이라도 바뀌면(추가·순서 포함) breaking이다.
//   - 비공개 필드가 섞인 struct는 unkeyed literal이 원래 불가라
//     필드 추가는 호환 변경이다.
//
// 어느 쪽 문서도 필드를 수확하지 않았으면(옛 형식·비struct) 건너뛴다 —
// 모르는 것을 변경으로 울리지 않는다.
func recordFieldChanges(d *Diff, id string, ov, nv *graph.Vertex) {
	if ov.Fields == nil && nv.Fields == nil {
		return
	}
	if ov.Fields != nil && nv.Fields == nil {
		// 같은 레벨 문서에서 필드가 사라졌다는 건 struct가 아닌
		// 타입으로 바뀌었다는 뜻이다 — 필드를 쓰는 모든 코드가 깨진다.
		if ov.Exported {
			d.Breaking = append(d.Breaking, fmt.Sprintf(
				"exported type %s is no longer a struct", id))
		}
		return
	}
	if ov.Fields == nil {
		// 옛 문서에는 필드 정보가 없었다 — "생겼다"가 아니라 "몰랐다"다.
		return
	}
	oldSet := map[string]string{} // 필드명 → 타입 문자열
	for _, f := range ov.Fields {
		name, typ, _ := strings.Cut(f, ":")
		oldSet[name] = typ
	}
	newSet := map[string]string{}
	var added []string
	for _, f := range nv.Fields {
		name, typ, _ := strings.Cut(f, ":")
		newSet[name] = typ
		if _, ok := oldSet[name]; !ok {
			added = append(added, name)
		}
	}
	var removed, retyped []string
	allExported := true
	for _, f := range ov.Fields {
		name, typ, _ := strings.Cut(f, ":")
		if !isExportedName(name) {
			allExported = false
		}
		nt, ok := newSet[name]
		switch {
		case !ok:
			removed = append(removed, name)
		case nt != typ:
			retyped = append(retyped, name)
		}
	}
	reordered := !sameOrder(ov.Fields, nv.Fields)
	if len(added) == 0 && len(removed) == 0 && len(retyped) == 0 && !reordered {
		return
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(retyped)
	d.FieldChanges = append(d.FieldChanges, FieldChange{
		ID: id, Added: added, Removed: removed,
		Retyped: retyped, Reordered: reordered,
	})
	if !ov.Exported {
		// 비공개 struct의 필드 계약은 패키지 안에만 닿는다 — breaking 집계에서 뺀다.
		return
	}
	for _, name := range removed {
		if isExportedName(name) {
			d.Breaking = append(d.Breaking, fmt.Sprintf(
				"exported field %s.%s was removed", id, name))
		}
	}
	for _, name := range retyped {
		d.Breaking = append(d.Breaking, fmt.Sprintf(
			"exported field %s.%s changed type: %s -> %s",
			id, name, oldSet[name], newSet[name]))
	}
	if allExported && (len(added) > 0 || reordered) {
		d.Breaking = append(d.Breaking, fmt.Sprintf(
			"all-exported struct %s gained fields or reordered them; "+
				"unkeyed composite literals no longer compile", id))
	}
}

// sameOrder는 공통 필드의 선언 순서가 유지되는지 본다 —
// 필드 추가만 있어도 unkeyed literal 계약은 순서로 평가해야 한다.
func sameOrder(old, new []string) bool {
	names := func(l []string) []string {
		out := make([]string, len(l))
		for i, f := range l {
			out[i], _, _ = strings.Cut(f, ":")
		}
		return out
	}
	on, nn := names(old), names(new)
	set := map[string]bool{}
	for _, n := range nn {
		set[n] = true
	}
	i := 0
	for _, n := range on {
		if !set[n] {
			continue
		}
		for i < len(nn) && nn[i] != n {
			i++
		}
		if i == len(nn) {
			return false
		}
		i++
	}
	return true
}

// isExportedName은 필드·메서드 이름의 공개 여부를 본다.
func isExportedName(name string) bool {
	for _, r := range name {
		return unicode.IsUpper(r)
	}
	return false
}

// diffIfaceMethods는 인터페이스에 새 메서드가 생긴 변경을 breaking으로 잡는다.
// 추가된 contains 간선이 "양쪽 문서에 모두 있는 인터페이스 정점"에서
// "메서드 정점"으로 향할 때다 — 새 인터페이스의 메서드는 신규 API이지
// breaking이 아니므로 From이 old에 있어야 한다.
func diffIfaceMethods(d *Diff, old, new *graph.Document) {
	newV := indexVertices(new)
	oldV := indexVertices(old)
	oldE := indexEdges(old)
	newE := indexEdges(new)
	for _, k := range sortedEdgeKeys(newE) {
		e := newE[k]
		if e.Kind != graph.EdgeContains {
			continue
		}
		if _, existed := oldE[k]; existed {
			continue
		}
		tv, ok := newV[e.From]
		if !ok || !tv.Interface {
			continue
		}
		if _, inOld := oldV[e.From]; !inOld {
			continue
		}
		mv, ok := newV[e.To]
		if !ok || mv.Kind != graph.KindMethod {
			continue
		}
		if !tv.Exported {
			continue
		}
		d.Breaking = append(d.Breaking, fmt.Sprintf(
			"exported interface %s gained method %s — implementers no longer satisfy it",
			e.From, mv.Name))
	}
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
