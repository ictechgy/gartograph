// 문서 비교 질의 — 두 저장 문서 사이의 구조 차이를 보고한다.
// 영속 문서가 산출물이라는 계약 위에서, diff는 아키텍처 드리프트와
// 공개 API 변경 신호를 CI가 소비할 수 있는 형태로 만든다.
package analysis

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
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
	diffTypeSets(d, old, new)
	sort.Strings(d.Notes)
	return d
}

// diffVertices는 정점 집합 차이와 공개 심볼의 signature 간선 변화를 채운다.
func diffVertices(d *Diff, old, new *graph.Document) {
	oldV := indexVertices(old)
	newV := indexVertices(new)
	oldSig := signatureTargets(old)
	newSig := signatureTargets(new)

	for _, k := range sortedKeys(oldV) {
		ov := oldV[k]
		nv, ok := newV[k]
		if !ok {
			d.RemovedVertices = append(d.RemovedVertices, ov.ID)
			if ov.Exported && !stillInMethodSet(ov, old, newV) {
				d.Breaking = append(d.Breaking,
					fmt.Sprintf("exported vertex removed: %s", ov.ID))
			}
			continue
		}
		// 짝지어진 정점은 새 문서의 ID로 보고한다 — 소비자가 지금 찾을 수 있는 이름이다.
		recordVertexChanges(d, nv.ID, ov, nv)
		recordFieldChanges(d, nv.ID, ov, nv)
		recordSignatureChange(d, nv.ID, ov, oldSig[k], newSig[k])
	}
	for _, k := range sortedKeys(newV) {
		if _, ok := oldV[k]; !ok {
			d.AddedVertices = append(d.AddedVertices, newV[k].ID)
		}
	}
	sort.Strings(d.RemovedVertices)
	sort.Strings(d.AddedVertices)
}

// stillInMethodSet은 사라진 인터페이스 메서드 정점이 여전히 그 인터페이스의 메서드
// 집합에 있는지 본다 — 선언 메서드를 임베드한 인터페이스로 옮기는 리팩터는 메서드 집합이
// 그대로라 호환이다. 두 문서 모두 메서드 집합 사실이 있을 때만 판단한다.
func stillInMethodSet(ov *graph.Vertex, old *graph.Document, newV map[string]*graph.Vertex) bool {
	if ov.Kind != graph.KindMethod || !old.InterfaceMethodSets {
		return false
	}
	owner, ok := graph.MemberOwner(ov.ID)
	if !ok {
		return false
	}
	nv := newV["sym\x00"+owner]
	return nv != nil && nv.Interface && methodEntriesByName(nv.Methods)[ov.Name] != ""
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

// diffTypeSets는 공개 제약 인터페이스의 타입 집합 변경을 breaking으로 잡는다 — 좁히면
// 소비자의 인스턴스화가, 넓히면 그 제약을 쓰는 소비자 제네릭 본문(허용 연산)이 깨질 수 있다.
// 두 문서 모두 표시(InterfaceTypeSets)가 있어야 비교한다 — 옛 문서의 부재는 "몰랐다"다.
func diffTypeSets(d *Diff, old, new *graph.Document) {
	if old.InterfaceTypeSets != new.InterfaceTypeSets {
		d.Notes = append(d.Notes, "one document lacks interface type sets (interfaceTypeSets) — "+
			"constraint type sets were not compared; re-harvest both to compare them")
	}
	if !old.InterfaceTypeSets || !new.InterfaceTypeSets {
		return
	}
	oldV := indexVertices(old)
	newV := indexVertices(new)
	for _, k := range sortedKeys(newV) {
		nv, ov := newV[k], oldV[k]
		if ov == nil || !ov.Interface || !nv.Interface || !nv.Exported || slices.Equal(ov.TypeSet, nv.TypeSet) {
			continue
		}
		d.Breaking = append(d.Breaking, fmt.Sprintf("exported constraint %s changed type set: %s -> %s — "+
			"instantiations or generic code may no longer compile", nv.ID, renderTypeSet(ov.TypeSet),
			renderTypeSet(nv.TypeSet)))
	}
}

// renderTypeSet은 타입 집합 항목(교집합)을 따옴표로 감싸 "; "로 잇는다 — 항목 안에도
// 공백과 " | "가 있어 경계를 보이게 한다. 항목이 없으면 제약이 없다는 뜻(any)이지 공집합이
// 아니다.
func renderTypeSet(set []string) string {
	if len(set) == 0 {
		return "any (no type elements)"
	}
	quoted := make([]string, len(set))
	for i, e := range set {
		quoted[i] = strconv.Quote(e)
	}
	return strings.Join(quoted, "; ")
}

// diffIfaceMethods는 공개 인터페이스의 메서드 집합이 늘어난 변경을 breaking으로 잡는다
// — 모듈 밖 구현자가 더 이상 인터페이스를 만족하지 못한다. 세 경로가 있다: 직접 선언한
// 메서드 추가, 임베드한(비공개일 수도 있는) 인터페이스의 메서드 추가, 새 인터페이스 임베드.
// 새 메서드 정점의 소유자는 ID("pkgpath.(I).M")에서 찾는다 — 수확기는 메서드 contains를
// 패키지에서 긋기 때문에, 인터페이스 타입에서 나가는 contains를 찾으면 실제 문서에서 이
// 판정이 한 번도 작동하지 않는다. 새 인터페이스는 신규 API라 옛 문서에도 있어야 breaking이다.
// 두 문서 모두 인터페이스 메서드 집합 사실(InterfaceMethodSets)을 가지면 그 사실로
// 판정한다 — 모듈 밖 인터페이스 임베드(io.Reader)까지 보인다. 옛 문서가 끼면 정점으로
// 판정하고, 그때 모듈 밖 임베드는 외부 간선이 생략돼 보이지 않는다.
func diffIfaceMethods(d *Diff, old, new *graph.Document) {
	if old.InterfaceMethodSets && new.InterfaceMethodSets {
		d.Breaking = append(d.Breaking, methodSetBreaking(old, new)...)
		return
	}
	oldV := indexVertices(old)
	newByID := verticesByID(new)
	embedders := interfaceEmbedders(new, newByID)
	var out []string
	gained := gainedInterfaceMethods(oldV, newByID, indexVertices(new))
	for _, owner := range sortedKeys(gained) {
		for _, name := range gained[owner] {
			out = append(out, gainedBreaking(owner, name, embedders, newByID, oldV)...)
		}
	}
	out = append(out, newEmbedBreaking(old, new, newByID, oldV)...)
	sort.Strings(out)
	d.Breaking = append(d.Breaking, slices.Compact(out)...)
}

// methodSetBreaking은 양쪽 문서에 있는 공개 인터페이스의 메서드 집합 사실을 비교한다.
// 공개 인터페이스가 인터페이스가 아니게 된 변경도 여기서 잡는다 — 필드 사실이 없던
// 쪽이라 recordFieldChanges는 "몰랐다"로 넘긴다.
func methodSetBreaking(old, new *graph.Document) []string {
	oldV := indexVertices(old)
	newV := indexVertices(new)
	ctx := methodSetContext{old: verticesByID(old), new: verticesByID(new),
		embeds: interfaceEmbeds(new), symbols: new.Level == graph.LevelSymbol}
	var out []string
	for _, k := range sortedKeys(newV) {
		nv, ov := newV[k], oldV[k]
		switch {
		case ov == nil || !ov.Interface || !ov.Exported:
		case !nv.Interface:
			out = append(out, fmt.Sprintf("exported type %s is no longer an interface", nv.ID))
		default:
			out = append(out, ctx.changes(ov, nv)...)
		}
	}
	sort.Strings(out)
	return out
}

// methodSetContext는 메서드 집합 비교가 문구를 만들 때 보는 두 문서의 색인이다.
// symbols가 거짓(type 레벨)이면 메서드 정점이 없어 선언·임베드를 가를 수 없다.
type methodSetContext struct {
	old, new map[string]*graph.Vertex
	embeds   map[string][]string // 인터페이스 ID → 새 문서에서 임베드한 인터페이스 ID들
	symbols  bool
}

// changes는 한 인터페이스의 메서드 추가·서명 변경·(임베드로 인한) 제거 문구를 만든다.
// 추가·서명 변경은 모듈 밖 구현자를, 제거는 호출자를 깬다. 선언 메서드 제거는 정점
// 제거가 이미 보고하므로 여기서는 임베드로 빠진 것만 적는다.
func (c methodSetContext) changes(ov, nv *graph.Vertex) []string {
	before := methodEntriesByName(ov.Methods)
	after := methodEntriesByName(nv.Methods)
	var out []string
	for _, entry := range nv.Methods {
		name := methodEntryName(entry)
		prev, had := before[name]
		switch {
		case !had:
			out = append(out, c.gained(nv, name))
		case prev != entry:
			out = append(out, fmt.Sprintf("exported interface %s changed method %s signature — "+
				"implementers no longer satisfy it", nv.ID, shortMethodName(nv, name)))
		}
	}
	for _, entry := range ov.Methods {
		if name := methodEntryName(entry); after[name] == "" && !declaredIn(c.old, ov, name) {
			out = append(out, fmt.Sprintf("exported interface %s lost method %s via embedding — "+
				"callers no longer compile", nv.ID, shortMethodName(ov, name)))
		}
	}
	return out
}

// gained는 메서드 추가 문구다 — 직접 선언이면 그대로, 모듈 안 인터페이스 임베드로
// 들어왔으면 그 인터페이스를, 모듈 밖 임베드면 "via embedding"을 적는다. type 레벨은
// 선언 여부를 알 수 없어 구분 없이 적는다.
func (c methodSetContext) gained(nv *graph.Vertex, name string) string {
	short := shortMethodName(nv, name)
	how := ""
	switch {
	case !c.symbols || declaredIn(c.new, nv, name):
	case c.embeddedSource(nv.ID, name) != "":
		how = " via embedded " + c.embeddedSource(nv.ID, name)
	default:
		how = " via embedding"
	}
	return fmt.Sprintf("exported interface %s gained method %s%s — implementers no longer satisfy it",
		nv.ID, short, how)
}

// embeddedSource는 인터페이스가 임베드한 모듈 안 인터페이스 중 그 메서드를 가진 것의 ID다.
func (c methodSetContext) embeddedSource(ifaceID, name string) string {
	for _, id := range c.embeds[ifaceID] {
		if v := c.new[id]; v != nil && methodEntriesByName(v.Methods)[name] != "" {
			return id
		}
	}
	return ""
}

// declaredIn은 인터페이스가 그 메서드를 직접 선언했는지(정점 pkgpath.(I).M이 있는지) 본다.
// 비공개 메서드 항목은 "pkgpath.name"이라 같은 패키지일 때만 짧은 이름으로 찾는다.
func declaredIn(byID map[string]*graph.Vertex, iface *graph.Vertex, name string) bool {
	short := shortMethodName(iface, name)
	if strings.Contains(short, ".") {
		return false // 다른 패키지의 비공개 메서드 — 임베드로만 들어온다
	}
	return byID[iface.Package+".("+iface.Name+")."+short] != nil
}

// shortMethodName은 같은 패키지의 비공개 메서드 항목("pkgpath.seal")을 짧은 이름으로 줄인다.
func shortMethodName(iface *graph.Vertex, name string) string {
	return strings.TrimPrefix(name, iface.Package+".")
}

// interfaceEmbeds는 인터페이스 ID → 그 인터페이스가 임베드한 인터페이스 ID들이다.
func interfaceEmbeds(d *graph.Document) map[string][]string {
	byID := verticesByID(d)
	out := map[string][]string{}
	for _, e := range d.Edges {
		from, to := byID[e.From], byID[e.To]
		if e.Kind == graph.EdgeEmbeds && from != nil && to != nil && from.Interface && to.Interface {
			out[e.From] = append(out[e.From], e.To)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// methodEntriesByName은 메서드 집합 항목을 이름 → 항목으로 색인한다.
func methodEntriesByName(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		out[methodEntryName(e)] = e
	}
	return out
}

// methodEntryName은 "Name(params) results" 항목의 이름 부분이다.
func methodEntryName(entry string) string {
	if i := strings.Index(entry, "("); i >= 0 {
		return entry[:i]
	}
	return entry
}

// gainedInterfaceMethods는 옛 문서에 없던 메서드 정점을 소유 인터페이스별로 모은다.
// 비공개 인터페이스도 모은다 — 공개 인터페이스에 임베드돼 있으면 그쪽을 깬다.
func gainedInterfaceMethods(oldV map[string]*graph.Vertex, newByID map[string]*graph.Vertex,
	newV map[string]*graph.Vertex) map[string][]string {
	out := map[string][]string{}
	for _, k := range sortedKeys(newV) {
		v := newV[k]
		if v.Kind != graph.KindMethod || oldV[k] != nil {
			continue
		}
		if owner := ownerVertex(newByID, v.ID); owner != nil && owner.Interface {
			out[owner.ID] = append(out[owner.ID], v.Name)
		}
	}
	return out
}

// gainedBreaking은 인터페이스 owner가 얻은 메서드가 깨는 공개 인터페이스들의 문구를
// 만든다 — owner 자신과, owner를 (전이적으로) 임베드한 인터페이스. 재귀 대신 worklist다.
func gainedBreaking(owner, name string, embedders map[string][]string,
	newByID, oldV map[string]*graph.Vertex) []string {
	var out []string
	seen := map[string]bool{owner: true}
	for queue := []string{owner}; len(queue) > 0; queue = queue[1:] {
		id := queue[0]
		if v := newByID[id]; v != nil && v.Exported && oldV[vertexKey(*v)] != nil {
			out = append(out, gainedMessage(id, owner, name))
		}
		for _, e := range embedders[id] {
			if !seen[e] {
				seen[e] = true
				queue = append(queue, e)
			}
		}
	}
	return out
}

// gainedMessage는 메서드 추가 breaking 문구다 — 임베드를 거쳤으면 그 경로를 밝힌다.
func gainedMessage(iface, owner, name string) string {
	if iface == owner {
		return fmt.Sprintf("exported interface %s gained method %s — implementers no longer satisfy it",
			iface, name)
	}
	return fmt.Sprintf("exported interface %s gained method %s via embedded %s — "+
		"implementers no longer satisfy it", iface, name, owner)
}

// interfaceEmbedders는 임베드된 인터페이스 ID → 그것을 임베드한 인터페이스 ID들이다.
func interfaceEmbedders(d *graph.Document, byID map[string]*graph.Vertex) map[string][]string {
	out := map[string][]string{}
	for _, e := range d.Edges {
		if from := byID[e.From]; e.Kind == graph.EdgeEmbeds && from != nil && from.Interface {
			out[e.To] = append(out[e.To], e.From)
		}
	}
	return out
}

// newEmbedBreaking은 옛 문서에 있던 공개 인터페이스가 새로 인터페이스를 임베드한
// 변경의 문구를 만든다 — 임베드된 메서드 전부가 구현자에게 새 요구다.
func newEmbedBreaking(old, new *graph.Document, newByID, oldV map[string]*graph.Vertex) []string {
	oldE := indexEdges(old)
	var out []string
	for _, e := range new.Edges {
		from, to := newByID[e.From], newByID[e.To]
		if e.Kind != graph.EdgeEmbeds || oldE[edgeKey(e)].Kind != "" || from == nil || to == nil ||
			!from.Interface || !to.Interface || !from.Exported || oldV[vertexKey(*from)] == nil {
			continue
		}
		out = append(out, fmt.Sprintf("exported interface %s now embeds %s — implementers no longer satisfy it",
			from.ID, to.ID))
	}
	return out
}

// ownerVertex는 멤버 정점의 소유 타입 정점을 찾는다. 소유 타입 ID가 패키지 경로와
// 겹치면 문서에는 충돌 접미사가 붙은 형태로 있다.
func ownerVertex(byID map[string]*graph.Vertex, memberID string) *graph.Vertex {
	owner, ok := graph.MemberOwner(memberID)
	if !ok {
		return nil
	}
	if v := byID[owner]; v != nil && v.Kind == graph.KindType {
		return v
	}
	return byID[owner+graph.CollisionSuffix]
}

// recordSignatureChange는 exported 심볼의 signature 간선 목표 차이를 적는다.
// 비공개 심볼의 시그니처 변화는 API 계약과 무관해 보고하지 않는다.
func recordSignatureChange(d *Diff, id string, ov *graph.Vertex,
	oldT, newT map[string]string) {
	if !ov.Exported {
		return
	}
	added := targetsOnlyIn(newT, oldT)   // 새 문서의 ID
	removed := targetsOnlyIn(oldT, newT) // 옛 문서의 ID
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

// targetsOnlyIn은 a에만 있는 목표를 a 문서의 실제 ID로 돌려준다.
func targetsOnlyIn(a, b map[string]string) []string {
	var out []string
	for canonical, actual := range a {
		if _, ok := b[canonical]; !ok {
			out = append(out, actual)
		}
	}
	return out
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

// vertexKey는 문서 사이에서 같은 정점을 짝짓는 키다. 심볼은 충돌 접미사를 뗀
// ID로 맞춘다 — 형제 x.y/ 디렉터리나 --tests 유무처럼 수확 패키지 집합만 달라
// x.y가 x.y#symbol이 되어도 같은 심볼이다. 한 문서 안에서 패키지 x.y와 심볼
// x.y#symbol이 함께 있을 수 있어 범주를 키에 넣는다.
func vertexKey(v graph.Vertex) string {
	if v.Kind == graph.KindPackage || v.Kind == graph.KindModule {
		return "pkg\x00" + v.ID
	}
	return "sym\x00" + graph.CanonicalID(v.ID)
}

// indexVertices는 vertexKey → 정점 색인이다(문서 사이 비교용).
func indexVertices(d *graph.Document) map[string]*graph.Vertex {
	out := make(map[string]*graph.Vertex, len(d.Vertices))
	for i := range d.Vertices {
		out[vertexKey(d.Vertices[i])] = &d.Vertices[i]
	}
	return out
}

// verticesByID는 정점 ID → 정점 색인이다(한 문서 안의 간선 끝 조회용).
func verticesByID(d *graph.Document) map[string]*graph.Vertex {
	out := make(map[string]*graph.Vertex, len(d.Vertices))
	for i := range d.Vertices {
		out[d.Vertices[i].ID] = &d.Vertices[i]
	}
	return out
}

// signatureTargets는 심볼 vertexKey → (정규 ID → 그 문서의 실제 목표 ID)다.
// signature 간선은 심볼에서 심볼(타입)로 향한다. 비교는 정규 ID로 하되 보고는 실제
// ID로 한다 — 정규 ID는 형제 x.U/가 있으면 패키지 정점을 가리킨다.
func signatureTargets(d *graph.Document) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, e := range d.Edges {
		if e.Kind != graph.EdgeSignature {
			continue
		}
		from := "sym\x00" + graph.CanonicalID(e.From)
		if out[from] == nil {
			out[from] = map[string]string{}
		}
		out[from][graph.CanonicalID(e.To)] = e.To
	}
	return out
}

// edgeKey는 간선의 집합 동일성 키다 — 위치가 아니라 관계가 단위다.
// 끝점은 충돌 접미사를 뗀다(vertexKey와 같은 이유). 간선 종류가 끝점의 범주를
// 가른다 — import는 패키지끼리, contains는 패키지에서 심볼로, 나머지는 심볼끼리라
// 접미사를 떼도 다른 관계와 겹치지 않는다.
func edgeKey(e graph.Edge) string {
	return graph.CanonicalID(e.From) + "\x00" + graph.CanonicalID(e.To) + "\x00" + string(e.Kind)
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
