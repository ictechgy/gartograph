package analysis

import (
	"slices"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// diffDocs는 정점·간선이 주어진 두 문서를 만든다.
func diffDocs() (old, new *graph.Document) {
	old = &graph.Document{
		Level:  graph.LevelSymbol,
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "m/a", Kind: graph.KindPackage},
			{ID: "m/a.F", Kind: graph.KindFunc, Package: "m/a", Exported: true},
			{ID: "m/a.gone", Kind: graph.KindFunc, Package: "m/a", Exported: true},
			{ID: "m/a.T", Kind: graph.KindType, Package: "m/a", Exported: true},
			{ID: "m/a.U", Kind: graph.KindType, Package: "m/a", Exported: true},
		},
		Edges: []graph.Edge{
			{From: "m/a.F", To: "m/a.T", Kind: graph.EdgeSignature},
			{From: "m/a.gone", To: "m/a.U", Kind: graph.EdgeSignature},
		},
	}
	new = &graph.Document{
		Level:  graph.LevelSymbol,
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "m/a", Kind: graph.KindPackage},
			{ID: "m/a.F", Kind: graph.KindFunc, Package: "m/a", Exported: true},
			{ID: "m/a.T", Kind: graph.KindType, Package: "m/a", Exported: true},
			{ID: "m/a.U", Kind: graph.KindType, Package: "m/a", Exported: true},
			{ID: "m/a.NEW", Kind: graph.KindFunc, Package: "m/a", Exported: true},
		},
		Edges: []graph.Edge{
			{From: "m/a.F", To: "m/a.U", Kind: graph.EdgeSignature},
			{From: "m/a.F", To: "m/a.T", Kind: graph.EdgeSignature},
		},
	}
	return old, new
}

// TestDiff는 정점·간선·시그니처 차이와 breaking 신호를 확인한다.
func TestDiff(t *testing.T) {
	old, new := diffDocs()
	d := DiffDocuments(old, new)

	if len(d.AddedVertices) != 1 || d.AddedVertices[0] != "m/a.NEW" {
		t.Fatalf("added vertices: %+v", d.AddedVertices)
	}
	if len(d.RemovedVertices) != 1 || d.RemovedVertices[0] != "m/a.gone" {
		t.Fatalf("removed vertices: %+v", d.RemovedVertices)
	}
	// 공개 심볼 제거는 breaking이다.
	var removedBreaking bool
	for _, b := range d.Breaking {
		if strings.Contains(b, "m/a.gone") {
			removedBreaking = true
		}
	}
	if !removedBreaking {
		t.Fatalf("removed exported vertex must be breaking: %+v", d.Breaking)
	}
	// m/a.F의 signature 목표에 m/a.U가 추가됐다.
	if len(d.SignatureChanges) != 1 || d.SignatureChanges[0].ID != "m/a.F" ||
		len(d.SignatureChanges[0].Added) != 1 ||
		d.SignatureChanges[0].Added[0] != "m/a.U" {
		t.Fatalf("signature change on m/a.F: %+v", d.SignatureChanges)
	}
	// 간선 diff: F→T는 유지, F→U 추가, gone→U 제거.
	if len(d.AddedEdges) != 1 || len(d.RemovedEdges) != 1 {
		t.Fatalf("edges: %+v %+v", d.AddedEdges, d.RemovedEdges)
	}
}

// TestDiffLevelMismatch는 레벨이 다른 문서 비교가 notes로 보고되는지 확인한다.
// 비교 불가를 에러가 아닌 메모로 남기는 것이 소비자의 판단 재료다.
func TestDiffLevelMismatch(t *testing.T) {
	old := &graph.Document{Level: graph.LevelPackage}
	new := &graph.Document{Level: graph.LevelSymbol}
	d := DiffDocuments(old, new)
	if len(d.Notes) == 0 || !strings.Contains(d.Notes[0], "level mismatch") {
		t.Fatalf("expected level mismatch note: %+v", d.Notes)
	}
}

// TestDiffLimitations는 양쪽 문서의 수확 limitation이 출처와 함께
// diff에 실리는지 확인한다 — 부분 수확 위의 diff는 신호가 아니라
// 수확 구멍일 수 있음을 소비자가 알아야 한다.
func TestDiffLimitations(t *testing.T) {
	old := &graph.Document{Limitations: []string{"2 packages had errors"}}
	new := &graph.Document{Limitations: []string{"1 import omitted"}}
	d := DiffDocuments(old, new)
	if len(d.OldLimitations) != 1 || len(d.NewLimitations) != 1 {
		t.Fatalf("limitations must carry provenance: %+v", d)
	}
}

// TestDiffBreakingOnlyOnRemoval은 추가만 있는 diff가 breaking이 아닌지 확인한다.
func TestDiffBreakingOnlyOnRemoval(t *testing.T) {
	old := &graph.Document{
		Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "m/a.F", Kind: graph.KindFunc, Exported: true},
		},
	}
	new := &graph.Document{
		Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "m/a.F", Kind: graph.KindFunc, Exported: true},
			{ID: "m/a.G", Kind: graph.KindFunc, Exported: true},
		},
	}
	d := DiffDocuments(old, new)
	if len(d.Breaking) != 0 || len(d.AddedVertices) != 1 {
		t.Fatalf("pure additions must not be breaking: %+v", d)
	}
}

// hasBreaking은 breaking 목록에 substr을 포함하는 항목이 있는지 본다.
func hasBreaking(d *Diff, substr string) bool {
	for _, b := range d.Breaking {
		if strings.Contains(b, substr) {
			return true
		}
	}
	return false
}

// TestDiffKindChangeBreaking은 공개 심볼의 kind 변경이 breaking인지 확인한다 —
// `func F`가 `var F`로 바뀌면 이름은 같아도 호출부가 깨진다.
func TestDiffKindChangeBreaking(t *testing.T) {
	old := &graph.Document{Vertices: []graph.Vertex{
		{ID: "m/a.F", Kind: graph.KindFunc, Exported: true},
	}}
	new := &graph.Document{Vertices: []graph.Vertex{
		{ID: "m/a.F", Kind: graph.KindVar, Exported: true},
	}}
	d := DiffDocuments(old, new)
	if !hasBreaking(d, "changed kind") {
		t.Fatalf("kind change on exported symbol must be breaking: %+v", d.Breaking)
	}
}

// TestDiffUnexportBreaking은 공개→비공개 전환이 breaking인지 확인한다.
func TestDiffUnexportBreaking(t *testing.T) {
	old := &graph.Document{Vertices: []graph.Vertex{
		{ID: "m/a.F", Kind: graph.KindFunc, Exported: true},
	}}
	new := &graph.Document{Vertices: []graph.Vertex{
		{ID: "m/a.F", Kind: graph.KindFunc},
	}}
	d := DiffDocuments(old, new)
	if !hasBreaking(d, "became unexported") {
		t.Fatalf("exported->unexported must be breaking: %+v", d.Breaking)
	}
	// 반대 방향(비공개→공개)은 새 API일 뿐 breaking이 아니다.
	d2 := DiffDocuments(new, old)
	if hasBreaking(d2, "became unexported") {
		t.Fatalf("unexported->exported must not be breaking: %+v", d2.Breaking)
	}
}

// TestDiffInterfaceGainedMethod는 인터페이스의 메서드 추가가 breaking인지
// 확인한다 — 모듈 안에 구현체가 없어도 소비자의 구현체가 깨진다.
// 새 인터페이스의 메서드는 신규 API이지 breaking이 아니어야 한다.
func TestDiffInterfaceGainedMethod(t *testing.T) {
	old := &graph.Document{
		Vertices: []graph.Vertex{
			{ID: "m/a.I", Kind: graph.KindType, Exported: true, Interface: true},
			{ID: "m/a.(I).Do", Kind: graph.KindMethod, Package: "m/a", Exported: true},
		},
		// 수확기는 메서드 contains를 패키지에서 긋는다 — 판정은 이 모양에서 돌아야 한다.
		Edges: []graph.Edge{
			{From: "m/a", To: "m/a.(I).Do", Kind: graph.EdgeContains},
		},
	}
	new := &graph.Document{
		Vertices: []graph.Vertex{
			{ID: "m/a.I", Kind: graph.KindType, Exported: true, Interface: true},
			{ID: "m/a.(I).Do", Kind: graph.KindMethod, Package: "m/a", Exported: true},
			{ID: "m/a.(I).Run", Kind: graph.KindMethod, Package: "m/a", Exported: true},
			// 새 인터페이스 — 이것의 메서드는 breaking이 아니다.
			{ID: "m/a.J", Kind: graph.KindType, Exported: true, Interface: true},
			{ID: "m/a.(J).New", Kind: graph.KindMethod, Package: "m/a", Exported: true},
		},
		Edges: []graph.Edge{
			{From: "m/a", To: "m/a.(I).Do", Kind: graph.EdgeContains},
			{From: "m/a", To: "m/a.(I).Run", Kind: graph.EdgeContains},
			{From: "m/a", To: "m/a.(J).New", Kind: graph.EdgeContains},
		},
	}
	d := DiffDocuments(old, new)
	if !hasBreaking(d, "gained method") {
		t.Fatalf("interface method addition must be breaking: %+v", d.Breaking)
	}
	if hasBreaking(d, "m/a.J") {
		t.Fatalf("a brand-new interface is new API, not breaking: %+v", d.Breaking)
	}
}

// TestDiffStructFields는 struct 필드 계약의 breaking 분류를 확인한다 —
// apidiff 규칙: 공개 필드 제거·재형은 항상 breaking, 모든 필드가 공개인
// struct의 추가·순서 변경은 unkeyed literal을 깨므로 breaking,
// 비공개 필드가 섞인 struct의 추가는 호환이다.
func TestDiffStructFields(t *testing.T) {
	mk := func(fields ...string) graph.Vertex {
		return graph.Vertex{ID: "m/a.S", Kind: graph.KindType,
			Exported: true, Fields: fields}
	}
	// 순수 추가, 모든 필드 공개 — unkeyed literal이 깨진다.
	d := DiffDocuments(
		&graph.Document{Vertices: []graph.Vertex{mk("A:int", "B:string")}},
		&graph.Document{Vertices: []graph.Vertex{mk("A:int", "B:string", "C:bool")}})
	if !hasBreaking(d, "unkeyed composite literals") || len(d.FieldChanges) != 1 {
		t.Fatalf("all-exported field addition must be breaking: %+v", d)
	}
	// 비공개 필드가 있으면 unkeyed literal이 원래 불가 — 추가는 호환.
	d = DiffDocuments(
		&graph.Document{Vertices: []graph.Vertex{mk("A:int", "x:string")}},
		&graph.Document{Vertices: []graph.Vertex{mk("A:int", "x:string", "C:bool")}})
	if len(d.Breaking) != 0 || len(d.FieldChanges) != 1 {
		t.Fatalf("addition to mixed struct must be compatible: %+v", d)
	}
	// 공개 필드 제거·재형은 비공개 필드가 있어도 breaking.
	d = DiffDocuments(
		&graph.Document{Vertices: []graph.Vertex{mk("A:int", "B:string", "x:bool")}},
		&graph.Document{Vertices: []graph.Vertex{mk("A:int64", "x:bool")}})
	if !hasBreaking(d, "changed type") || !hasBreaking(d, "was removed") {
		t.Fatalf("retyped and removed exported fields must be breaking: %+v", d.Breaking)
	}
	// 옛 문서에 필드가 없으면(옛 형식 수확) "몰랐다"다 — 변경으로 울리지 않는다.
	old := mk()
	old.Fields = nil
	d = DiffDocuments(
		&graph.Document{Vertices: []graph.Vertex{old}},
		&graph.Document{Vertices: []graph.Vertex{mk("A:int")}})
	if len(d.FieldChanges) != 0 || len(d.Breaking) != 0 {
		t.Fatalf("missing old fields must not be read as a change: %+v", d)
	}
}

// TestConstValueBreaking은 공개 상수의 값 변경이 breaking인지 확인한다 —
// 상수는 소비자 코드에 인라인되므로 재컴파일 없이 이미 빌드된 바이너리가
// 다른 상수를 담는다. 값을 모르는 옛 문서와의 비교는 "몰랐다"다.
func TestConstValueBreaking(t *testing.T) {
	mk := func(exported bool, value string) graph.Vertex {
		return graph.Vertex{ID: "m/lib.K", Kind: graph.KindConst,
			Exported: exported, Value: value}
	}
	// 공개 상수 값 변경은 breaking.
	d := DiffDocuments(
		&graph.Document{Vertices: []graph.Vertex{mk(true, `"1.0"`)}},
		&graph.Document{Vertices: []graph.Vertex{mk(true, `"2.0"`)}})
	if !hasBreaking(d, "changed value") {
		t.Fatalf("exported const value change must be breaking: %+v", d)
	}
	// 비공개 상수의 값 변경은 API 계약이 아니다 — 변경 기록은 남지만
	// breaking은 아니다.
	d = DiffDocuments(
		&graph.Document{Vertices: []graph.Vertex{mk(false, `"1.0"`)}},
		&graph.Document{Vertices: []graph.Vertex{mk(false, `"2.0"`)}})
	if len(d.Breaking) != 0 {
		t.Fatalf("unexported const value change is not breaking: %+v", d)
	}
	// 값을 수확하기 전 형식의 문서와 비교하면 "몰랐다" — 모든 상수가
	// 변경으로 울리면 안 된다.
	d = DiffDocuments(
		&graph.Document{Vertices: []graph.Vertex{mk(true, "")}},
		&graph.Document{Vertices: []graph.Vertex{mk(true, `"2.0"`)}})
	if len(d.Breaking) != 0 {
		t.Fatalf("old doc without values must not flag every const: %+v", d)
	}
}

// TestDiffPairsCollisionSuffix는 형제 디렉터리(x.V2/)가 생겨 심볼 x.V2의 ID가
// x.V2#symbol로 바뀌어도 diff가 같은 심볼로 짝짓는지 확인한다 — 공개 API는 그대로인데
// "제거·kind 변경" breaking을 내면 --strict가 거짓으로 실패한다. 새 패키지 x.V2는
// 추가로 보고된다.
func TestDiffPairsCollisionSuffix(t *testing.T) {
	sym := graph.Vertex{ID: "m/x.V2", Kind: graph.KindFunc, Package: "m/x", Name: "V2", Exported: true}
	old := &graph.Document{Level: graph.LevelSymbol, Vertices: []graph.Vertex{
		{ID: "m/x", Kind: graph.KindPackage}, sym,
	}, Edges: []graph.Edge{{From: "m/x", To: "m/x.V2", Kind: graph.EdgeContains}}}
	moved := sym
	moved.ID = "m/x.V2" + graph.CollisionSuffix
	new := &graph.Document{Level: graph.LevelSymbol, Vertices: []graph.Vertex{
		{ID: "m/x", Kind: graph.KindPackage}, {ID: "m/x.V2", Kind: graph.KindPackage}, moved,
	}, Edges: []graph.Edge{{From: "m/x", To: moved.ID, Kind: graph.EdgeContains}}}
	d := DiffDocuments(old, new)
	if len(d.Breaking) != 0 || len(d.RemovedVertices) != 0 || len(d.ChangedVertices) != 0 {
		t.Fatalf("suffix-only ID change must not look like a removal: %+v", d)
	}
	if len(d.AddedVertices) != 1 || d.AddedVertices[0] != "m/x.V2" {
		t.Fatalf("only the new package is added: %v", d.AddedVertices)
	}
	if len(d.AddedEdges) != 0 || len(d.RemovedEdges) != 0 {
		t.Fatalf("the same contains edge must pair across the suffix: +%v -%v", d.AddedEdges, d.RemovedEdges)
	}
}

// TestDiffSignatureTargetsUseActualIDs는 signature 목표가 접미사를 사이에 두고 짝지어지되
// 각 문서의 실제 ID로 보고되는지 확인한다 — 정규 ID로 보고하면 형제 x.U/가 있을 때 그
// 이름이 패키지 정점을 가리켜 소비자가 엉뚱한 정점을 따라간다.
func TestDiffSignatureTargetsUseActualIDs(t *testing.T) {
	f := graph.Vertex{ID: "m/x.F", Kind: graph.KindFunc, Package: "m/x", Exported: true}
	tt := graph.Vertex{ID: "m/x.T", Kind: graph.KindType, Package: "m/x", Exported: true}
	u := graph.Vertex{ID: "m/x.U" + graph.CollisionSuffix, Kind: graph.KindType, Package: "m/x", Exported: true}
	old := &graph.Document{Level: graph.LevelSymbol, Vertices: []graph.Vertex{f, tt},
		Edges: []graph.Edge{{From: f.ID, To: tt.ID, Kind: graph.EdgeSignature}}}
	new := &graph.Document{Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{f, tt, u, {ID: "m/x.U", Kind: graph.KindPackage}},
		Edges: []graph.Edge{{From: f.ID, To: tt.ID, Kind: graph.EdgeSignature},
			{From: f.ID, To: u.ID, Kind: graph.EdgeSignature}}}
	d := DiffDocuments(old, new)
	if len(d.SignatureChanges) != 1 || !slices.Equal(d.SignatureChanges[0].Added, []string{u.ID}) {
		t.Fatalf("added target must be reported by the new document's ID: %+v", d.SignatureChanges)
	}
	d = DiffDocuments(new, old)
	if !slices.ContainsFunc(d.Breaking, func(b string) bool {
		return strings.HasSuffix(b, "no longer references "+u.ID)
	}) {
		t.Fatalf("removed target must be reported by the old document's ID: %v", d.Breaking)
	}
}

// ifaceDoc는 인터페이스 판정 테스트용 문서다 — 수확 모양대로 메서드 contains는
// 패키지에서 나간다.
func ifaceDoc(vs []graph.Vertex, es ...graph.Edge) *graph.Document {
	doc := &graph.Document{Level: graph.LevelSymbol, Vertices: vs, Edges: es}
	for _, v := range vs {
		if v.Kind == graph.KindMethod {
			doc.Edges = append(doc.Edges, graph.Edge{From: v.Package, To: v.ID, Kind: graph.EdgeContains})
		}
	}
	return doc
}

// TestDiffInterfaceBreakingExact는 인터페이스 판정의 조건을 하나씩 고정한다 — 부분
// 문자열만 보면 조건을 지워도 테스트가 통과한다(변이 테스트로 확인된 공백).
func TestDiffInterfaceBreakingExact(t *testing.T) {
	iface := func(id string, exported bool) graph.Vertex {
		return graph.Vertex{ID: id, Kind: graph.KindType, Package: "m/a", Interface: true, Exported: exported}
	}
	method := func(id, name string) graph.Vertex {
		return graph.Vertex{ID: id, Kind: graph.KindMethod, Package: "m/a", Name: name, Exported: true}
	}
	concrete := graph.Vertex{ID: "m/a.S", Kind: graph.KindType, Package: "m/a", Exported: true}
	base := []graph.Vertex{iface("m/a.I", true), method("m/a.(I).Do", "Do"),
		iface("m/a.p", false), method("m/a.(p).Do", "Do"), concrete,
		iface("m/a.B"+graph.CollisionSuffix, true), {ID: "m/a.B", Kind: graph.KindPackage}}
	old := ifaceDoc(base)
	if d := DiffDocuments(old, ifaceDoc(base)); len(d.Breaking) != 0 {
		t.Fatalf("unchanged documents: existing methods are not gained: %v", d.Breaking)
	}
	grown := append(append([]graph.Vertex(nil), base...),
		method("m/a.(I).Run", "Run"), method("m/a.(p).Run", "Run"),
		method("m/a.(S).Run", "Run"), method("m/a.(B).Run", "Run"))
	want := []string{
		"exported interface m/a.B#symbol gained method Run — implementers no longer satisfy it",
		"exported interface m/a.I gained method Run — implementers no longer satisfy it",
	}
	if d := DiffDocuments(old, ifaceDoc(grown)); !slices.Equal(d.Breaking, want) {
		t.Fatalf("only exported interfaces present before may break:\n got %q\nwant %q", d.Breaking, want)
	}
}

// TestDiffInterfaceBreakingViaEmbedding은 임베드로 늘어난 메서드 집합도 breaking으로
// 잡는지 확인한다 — 비공개 임베드 인터페이스가 메서드를 얻거나, 기존 공개 인터페이스가
// 새로 인터페이스를 임베드하면 공개 인터페이스의 구현자가 깨진다.
func TestDiffInterfaceBreakingViaEmbedding(t *testing.T) {
	iface := func(id string, exported bool) graph.Vertex {
		return graph.Vertex{ID: id, Kind: graph.KindType, Package: "m/a", Interface: true, Exported: exported}
	}
	m := func(id, name string) graph.Vertex {
		return graph.Vertex{ID: id, Kind: graph.KindMethod, Package: "m/a", Name: name}
	}
	embedsJ := graph.Edge{From: "m/a.I", To: "m/a.j", Kind: graph.EdgeEmbeds}
	vs := []graph.Vertex{iface("m/a.I", true), iface("m/a.j", false), m("m/a.(j).M", "M"),
		iface("m/a.K", true), m("m/a.(K).Z", "Z")}
	old := ifaceDoc(vs, embedsJ)
	grown := ifaceDoc(append(append([]graph.Vertex(nil), vs...), m("m/a.(j).N", "N")), embedsJ)
	d := DiffDocuments(old, grown)
	if !slices.Contains(d.Breaking,
		"exported interface m/a.I gained method N via embedded m/a.j — implementers no longer satisfy it") {
		t.Fatalf("a method gained by an embedded interface breaks the embedder: %v", d.Breaking)
	}
	embedsK := graph.Edge{From: "m/a.I", To: "m/a.K", Kind: graph.EdgeEmbeds}
	// 비공개 인터페이스(j)와 새 인터페이스(L)가 K를 임베드하는 것은 공개 계약 변경이 아니다.
	jEmbedsK := graph.Edge{From: "m/a.j", To: "m/a.K", Kind: graph.EdgeEmbeds}
	lEmbedsK := graph.Edge{From: "m/a.L", To: "m/a.K", Kind: graph.EdgeEmbeds}
	withL := append(append([]graph.Vertex(nil), vs...), iface("m/a.L", true))
	d = DiffDocuments(old, ifaceDoc(withL, embedsJ, embedsK, jEmbedsK, lEmbedsK))
	if !slices.Equal(d.Breaking, []string{
		"exported interface m/a.I now embeds m/a.K — implementers no longer satisfy it"}) {
		t.Fatalf("only an existing exported interface newly embedding breaks implementers: %v", d.Breaking)
	}
}
