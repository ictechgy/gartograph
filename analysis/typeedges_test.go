package analysis

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// membersDoc은 문서의 필드 구조 간선을 필드 간선으로 옮긴 복사본이다 — 구현과 따로, 규칙을 문서 수준에서
// 다시 적용해 기존 root별 BFS 오라클(oracleTraverse)에 넣기 위한 것이다.
func membersDoc(d *graph.Document) *graph.Document {
	idx := newFieldIndex(d)
	out := *d
	out.Edges = nil
	for _, e := range d.Edges {
		owners := idx.fieldOwners(e)
		if owners == nil {
			out.Edges = append(out.Edges, e)
			continue
		}
		for _, f := range owners {
			moved := e
			moved.From = f
			out.Edges = append(out.Edges, moved)
		}
	}
	return &out
}

// randomMembersDoc는 필드·구조 간선 위치를 섞은 무작위 문서다(필드 줄, 타입 선언 줄, 위치 없음).
func randomMembersDoc(rng *rand.Rand) *graph.Document {
	d := &graph.Document{Version: graph.Version, Level: graph.LevelSymbol, DispatchEvidence: true}
	var types, members []string
	for t := 0; t < 4; t++ {
		id := fmt.Sprintf("p.T%d", t)
		line := 100 * (t + 1)
		d.Vertices = append(d.Vertices, graph.Vertex{ID: id, Kind: graph.KindType,
			Position: &graph.Position{File: "a.go", Line: line, Column: 6}})
		typeAt := len(d.Vertices) - 1
		types = append(types, id)
		for f := 0; f < 1+rng.Intn(3); f++ {
			fid := fmt.Sprintf("p.(T%d).f%d", t, f)
			d.Vertices = append(d.Vertices, graph.Vertex{ID: fid, Kind: graph.KindField,
				Position: &graph.Position{File: "a.go", Line: line + 1 + f, Column: 2}})
			members = append(members, fid)
			// 필드 타입은 무작위 타입(또는 모듈 밖 타입)이다 — 구조 간선은 아래에서 필드 줄 위치로 긋는다.
			typ := "int"
			if rng.Intn(3) != 0 {
				typ = fmt.Sprintf("*p.T%d", rng.Intn(4))
			}
			d.Vertices[typeAt].Fields = append(d.Vertices[typeAt].Fields, fmt.Sprintf("f%d:%s", f, typ))
		}
		mid := fmt.Sprintf("p.(T%d).m", t)
		d.Vertices = append(d.Vertices, graph.Vertex{ID: mid, Kind: graph.KindMethod})
		members = append(members, mid)
		d.Edges = append(d.Edges, graph.Edge{From: mid, To: id, Kind: graph.EdgeReferences})
	}
	pick := func(list []string) string { return list[rng.Intn(len(list))] }
	for i := 0; i < 12; i++ {
		from := pick(types)
		line := 100*(1+int(from[len(from)-1]-'0')) + rng.Intn(4)
		var positions []graph.Position
		if rng.Intn(6) != 0 {
			positions = []graph.Position{{File: "a.go", Line: line, Column: 10}}
		}
		d.Edges = append(d.Edges, graph.Edge{From: from, To: pick(types), Kind: graph.EdgeReferences, Positions: positions})
	}
	all := append(append([]string(nil), types...), members...)
	for i := 0; i < 14; i++ {
		d.Edges = append(d.Edges, graph.Edge{From: pick(members), To: pick(all), Kind: graph.EdgeCall,
			Candidate: rng.Intn(5) == 0})
	}
	return d
}

// TestTypeEdgesMembersOracle는 members 순회가 "필드 구조 간선을 필드로 옮긴 문서"의 root별 BFS
// 오라클과 같은지(도달·depth·via·roots·등급) 무작위 문서로 대조한다.
func TestTypeEdgesMembersOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for trial := 0; trial < 300; trial++ {
		d := randomMembersDoc(rng)
		for _, direction := range []string{DirectionDependencies, DirectionDependents} {
			roots := []string{d.Vertices[rng.Intn(len(d.Vertices))].ID, d.Vertices[rng.Intn(len(d.Vertices))].ID}
			if roots[0] == roots[1] {
				roots = roots[:1]
			}
			req := TraversalRequest{Roots: roots, Direction: direction, MaxDepth: 1 + rng.Intn(6),
				MaxReached: MaxTraversalReached, TypeEdges: TypeEdgesMembers}
			got := Traverse(d, req)
			plain := req
			plain.TypeEdges = ""
			want, _ := oracleTraverse(membersDoc(d), plain)
			if fmt.Sprint(got.Reached) != fmt.Sprint(want.Reached) {
				t.Fatalf("trial %d %s roots %v:\n got %v\nwant %v", trial, direction, roots, got.Reached, want.Reached)
			}
		}
	}
}

// demoDoc은 공유 Handler struct 데모다: handleHealth는 리시버만, handleUsers는 users 필드와
// UserStore.List를 쓴다. User는 태그 컬럼을 가진 행 타입이다.
func demoDoc() *graph.Document {
	pos := func(line, col int) *graph.Position { return &graph.Position{File: "app.go", Line: line, Column: col} }
	at := func(line, col int) []graph.Position { return []graph.Position{*pos(line, col)} }
	return &graph.Document{
		Version: graph.Version, Level: graph.LevelSymbol, DispatchEvidence: true,
		Vertices: []graph.Vertex{
			{ID: "app.User", Kind: graph.KindType, Position: pos(3, 6)},
			{ID: "app.UserStore", Kind: graph.KindType, Position: pos(8, 6)},
			{ID: "app.(UserStore).List", Kind: graph.KindMethod, Position: pos(10, 21)},
			{ID: "app.Handler", Kind: graph.KindType, Position: pos(15, 6),
				Fields: []string{"users:*app.UserStore", "name:string"}},
			{ID: "app.(Handler).users", Kind: graph.KindField, Position: pos(16, 2)},
			{ID: "app.(Handler).name", Kind: graph.KindField, Position: pos(17, 2)},
			{ID: "app.(Handler).handleHealth", Kind: graph.KindMethod, Position: pos(20, 19)},
			{ID: "app.(Handler).handleUsers", Kind: graph.KindMethod, Position: pos(22, 19)},
			{ID: "app.(Handler).handleName", Kind: graph.KindMethod, Position: pos(24, 19)},
		},
		Edges: []graph.Edge{
			{From: "app.(UserStore).List", To: "app.UserStore", Kind: graph.EdgeReferences, Positions: at(10, 10)},
			{From: "app.(UserStore).List", To: "app.User", Kind: graph.EdgeSignature, Positions: at(10, 30)},
			{From: "app.Handler", To: "app.UserStore", Kind: graph.EdgeReferences, Positions: at(16, 8)},
			{From: "app.(Handler).handleHealth", To: "app.Handler", Kind: graph.EdgeReferences, Positions: at(20, 10)},
			{From: "app.(Handler).handleUsers", To: "app.Handler", Kind: graph.EdgeReferences, Positions: at(22, 10)},
			{From: "app.(Handler).handleUsers", To: "app.(Handler).users", Kind: graph.EdgeReferences, Positions: at(22, 60)},
			{From: "app.(Handler).handleUsers", To: "app.(UserStore).List", Kind: graph.EdgeCall, Positions: at(22, 70)},
			{From: "app.(Handler).handleName", To: "app.Handler", Kind: graph.EdgeReferences, Positions: at(24, 10)},
			{From: "app.(Handler).handleName", To: "app.(Handler).name", Kind: graph.EdgeReferences, Positions: at(24, 60)},
		},
	}
}

// reachedIDs는 순회 결과의 정점 ID 집합이다.
func reachedIDs(res *TraversalResult) map[string]bool {
	out := map[string]bool{}
	for _, r := range res.Reached {
		out[r.ID] = true
	}
	return out
}

// TestTypeEdgesDemo는 members가 /api/health 핸들러를 users 행 타입에서 떼고, users 핸들러의 참 도달은
// 지키는지 본다(두 방향). all은 이전 동작(과대 근사)이다.
func TestTypeEdgesDemo(t *testing.T) {
	d := demoDoc()
	run := func(root, direction, mode string) map[string]bool {
		return reachedIDs(Traverse(d, TraversalRequest{Roots: []string{root}, Direction: direction,
			MaxDepth: MaxTraversalDepth, MaxReached: MaxTraversalReached, TypeEdges: mode}))
	}
	health := run("app.(Handler).handleHealth", DirectionDependencies, TypeEdgesMembers)
	if health["app.UserStore"] || health["app.User"] || !health["app.Handler"] {
		t.Errorf("members: handleHealth reached %v; want Handler only", health)
	}
	if all := run("app.(Handler).handleHealth", DirectionDependencies, TypeEdgesAll); !all["app.UserStore"] {
		t.Errorf("all keeps the previous over-approximation: %v", all)
	}
	users := run("app.(Handler).handleUsers", DirectionDependencies, TypeEdgesMembers)
	for _, want := range []string{"app.(Handler).users", "app.UserStore", "app.(UserStore).List", "app.User"} {
		if !users[want] {
			t.Errorf("members lost true reach %s from handleUsers: %v", want, users)
		}
	}
	impact := run("app.UserStore", DirectionDependents, TypeEdgesMembers)
	if impact["app.(Handler).handleHealth"] || impact["app.(Handler).handleName"] || impact["app.Handler"] ||
		!impact["app.(Handler).handleUsers"] || !impact["app.(Handler).users"] {
		t.Errorf("members impact of UserStore = %v; want the users field and handleUsers, not the container or health/name", impact)
	}
	if all := run("app.UserStore", DirectionDependents, TypeEdgesAll); !all["app.(Handler).handleHealth"] {
		t.Errorf("all keeps the previous container spread: %v", all)
	}
	res := Traverse(d, TraversalRequest{Roots: []string{"app.(Handler).handleHealth"}, Direction: DirectionDependencies,
		MaxDepth: MaxTraversalDepth, MaxReached: MaxTraversalReached, TypeEdges: TypeEdgesMembers})
	if res.TypeEdgesNarrowed != 1 {
		t.Errorf("TypeEdgesNarrowed = %d, want 1 (UserStore, not reached from Handler)", res.TypeEdgesNarrowed)
	}
}

// TestTypeEdgesMultiNameField는 `a, b *X`처럼 한 줄의 여러 필드가 모두 X에 귀속되는지 본다.
func TestTypeEdgesMultiNameField(t *testing.T) {
	d := demoDoc()
	for i := range d.Vertices {
		switch d.Vertices[i].ID {
		case "app.(Handler).name":
			d.Vertices[i].Position = &graph.Position{File: "app.go", Line: 16, Column: 9}
		case "app.Handler":
			d.Vertices[i].Fields = []string{"users:*app.UserStore", "name:*app.UserStore"}
		}
	}
	for i, e := range d.Edges {
		if e.From == "app.Handler" {
			d.Edges[i].Positions = []graph.Position{{File: "app.go", Line: 16, Column: 16}}
		}
	}
	got := reachedIDs(Traverse(d, TraversalRequest{Roots: []string{"app.(Handler).handleName"},
		Direction: DirectionDependencies, MaxDepth: MaxTraversalDepth, MaxReached: MaxTraversalReached,
		TypeEdges: TypeEdgesMembers}))
	if !got["app.UserStore"] {
		t.Errorf("name shares the users declaration line; reading it must reach UserStore: %v", got)
	}
}

// TestTypeEdgesNestedField는 역방향에서 겹겹이 담긴 필드를 이름으로 읽는 선언까지 닿고, 필드를
// 이름으로 쓰지 않는 컨테이너 값 사용(encoding/json에 값 전체를 넘기기)은 all에서만 닿는지 본다 —
// members의 알려진 손실이다.
func TestTypeEdgesNestedField(t *testing.T) {
	pos := func(line, col int) *graph.Position { return &graph.Position{File: "a.go", Line: line, Column: col} }
	at := func(line, col int) []graph.Position { return []graph.Position{*pos(line, col)} }
	d := &graph.Document{Version: graph.Version, Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "p.User", Kind: graph.KindType, Position: pos(1, 6)},
			{ID: "p.Resp", Kind: graph.KindType, Position: pos(5, 6), Fields: []string{"U:p.User"}},
			{ID: "p.(Resp).U", Kind: graph.KindField, Position: pos(6, 2)},
			{ID: "p.Env", Kind: graph.KindType, Position: pos(10, 6), Fields: []string{"R:p.Resp"}},
			{ID: "p.(Env).R", Kind: graph.KindField, Position: pos(11, 2)},
			{ID: "p.name", Kind: graph.KindFunc, Position: pos(20, 6)},
			{ID: "p.encode", Kind: graph.KindFunc, Position: pos(30, 6)},
		},
		Edges: []graph.Edge{
			{From: "p.Resp", To: "p.User", Kind: graph.EdgeReferences, Positions: at(6, 4)},
			{From: "p.Env", To: "p.Resp", Kind: graph.EdgeReferences, Positions: at(11, 4)},
			{From: "p.name", To: "p.(Env).R", Kind: graph.EdgeReferences, Positions: at(21, 4)},
			{From: "p.name", To: "p.(Resp).U", Kind: graph.EdgeReferences, Positions: at(21, 8)},
			{From: "p.encode", To: "p.Env", Kind: graph.EdgeSignature, Positions: at(30, 14)},
		},
	}
	impact := func(mode string) map[string]bool {
		return reachedIDs(Traverse(d, TraversalRequest{Roots: []string{"p.User"}, Direction: DirectionDependents,
			MaxDepth: MaxTraversalDepth, MaxReached: MaxTraversalReached, TypeEdges: mode}))
	}
	members, all := impact(TypeEdgesMembers), impact(TypeEdgesAll)
	if !members["p.(Resp).U"] || !members["p.name"] {
		t.Errorf("members impact of User lost the named field use: %v", members)
	}
	if members["p.encode"] || !all["p.encode"] {
		t.Errorf("whole-value use: members %v, all %v (documented members loss)", members, all)
	}
}

// TestTypeEdgesWithoutFieldLists는 필드 목록(Vertex.Fields)이 없는 옛 문서에서 members가 all과 같은
// 도달인지 본다.
func TestTypeEdgesWithoutFieldLists(t *testing.T) {
	d := demoDoc()
	for i := range d.Vertices {
		d.Vertices[i].Fields = nil
	}
	for _, direction := range []string{DirectionDependencies, DirectionDependents} {
		for _, root := range []string{"app.(Handler).handleHealth", "app.UserStore", "app.User"} {
			req := TraversalRequest{Roots: []string{root}, Direction: direction, MaxDepth: MaxTraversalDepth,
				MaxReached: MaxTraversalReached}
			all := Traverse(d, req)
			req.TypeEdges = TypeEdgesMembers
			members := Traverse(d, req)
			if fmt.Sprint(reachedIDs(all)) != fmt.Sprint(reachedIDs(members)) {
				t.Errorf("%s %s: members %v, all %v", direction, root, reachedIDs(members), reachedIDs(all))
			}
		}
	}
}

// TestTypeEdgesReviewCases는 리뷰가 찾은 경우를 고정한다: 이름이 앞 줄로 넘어간 필드(`a,\n b *X`)도
// X에 귀속하고, members가 all보다 더 닿는 곳(도달하지 않은 타입의 필드를 읽는 코드)은 좁힌 수로
// 세지 않으며, 깊이 상한 밖은 세지 않는다.
func TestTypeEdgesReviewCases(t *testing.T) {
	pos := func(line, col int) *graph.Position { return &graph.Position{File: "a.go", Line: line, Column: col} }
	at := func(line, col int) []graph.Position { return []graph.Position{*pos(line, col)} }
	d := &graph.Document{Version: graph.Version, Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "p.S", Kind: graph.KindType, Position: pos(1, 6), Fields: []string{"y:p.Y", "a:*p.X", "b:*p.X"}},
			{ID: "p.(S).y", Kind: graph.KindField, Position: pos(2, 2)},
			{ID: "p.(S).a", Kind: graph.KindField, Position: pos(3, 2)},
			{ID: "p.(S).b", Kind: graph.KindField, Position: pos(4, 2)},
			{ID: "p.Y", Kind: graph.KindType, Position: pos(10, 6)},
			{ID: "p.X", Kind: graph.KindType, Position: pos(12, 6), Fields: []string{"u:[]p.U"}},
			{ID: "p.(X).u", Kind: graph.KindField, Position: pos(13, 2)},
			{ID: "p.U", Kind: graph.KindType, Position: pos(15, 6)},
			{ID: "p.h", Kind: graph.KindFunc, Position: pos(20, 6)},
			{ID: "p.g", Kind: graph.KindFunc, Position: pos(30, 6)},
		},
		Edges: []graph.Edge{
			{From: "p.S", To: "p.Y", Kind: graph.EdgeReferences, Positions: at(2, 4)},
			{From: "p.S", To: "p.X", Kind: graph.EdgeReferences, Positions: at(4, 5)},
			{From: "p.X", To: "p.U", Kind: graph.EdgeReferences, Positions: at(13, 4)},
			{From: "p.h", To: "p.(S).a", Kind: graph.EdgeReferences, Positions: at(21, 4)},
			{From: "p.g", To: "p.(X).u", Kind: graph.EdgeReferences, Positions: at(31, 4)},
		},
	}
	run := func(root string, depth int) (*TraversalResult, map[string]bool) {
		res := Traverse(d, TraversalRequest{Roots: []string{root}, Direction: DirectionDependencies,
			MaxDepth: depth, MaxReached: MaxTraversalReached, TypeEdges: TypeEdgesMembers})
		return res, reachedIDs(res)
	}
	res, got := run("p.h", MaxTraversalDepth)
	if !got["p.X"] || got["p.Y"] || res.TypeEdgesNarrowed != 0 {
		t.Errorf("a (named on the line before `b *X`) must reach X but not Y: %v narrowed %d", got, res.TypeEdgesNarrowed)
	}
	res, got = run("p.g", MaxTraversalDepth)
	if !got["p.U"] || res.TypeEdgesNarrowed != 0 {
		t.Errorf("members reaching more than all is not narrowing: %v narrowed %d", got, res.TypeEdgesNarrowed)
	}
	d.Edges = append(d.Edges, graph.Edge{From: "p.g", To: "p.X", Kind: graph.EdgeSignature, Positions: at(30, 12)})
	if res, _ = run("p.g", 1); res.TypeEdgesNarrowed != 0 {
		t.Errorf("depth 1 all-mode does not reach U either; narrowed %d", res.TypeEdgesNarrowed)
	}
}

// TestTypeEdgesExternalFieldNotAttributed는 모듈 밖 타입 필드(`db *sql.DB`)를 읽어도 다음 줄 필드의
// 타입(`last []Order`)에 닿지 않는지 본다 — 필드 목록의 정규 타입으로 귀속한다(e2e에서 위치 기반
// 귀속이 db까지 묶어 orders 컬럼을 다시 끌어온 회귀).
func TestTypeEdgesExternalFieldNotAttributed(t *testing.T) {
	pos := func(line, col int) *graph.Position { return &graph.Position{File: "a.go", Line: line, Column: col} }
	d := &graph.Document{Version: graph.Version, Level: graph.LevelSymbol,
		Vertices: []graph.Vertex{
			{ID: "p.Order", Kind: graph.KindType, Position: pos(1, 6)},
			{ID: "p.Store", Kind: graph.KindType, Position: pos(5, 6),
				Fields: []string{"db:*database/sql.DB", "last:[]p.Order"}},
			{ID: "p.(Store).db", Kind: graph.KindField, Position: pos(6, 2)},
			{ID: "p.(Store).last", Kind: graph.KindField, Position: pos(7, 2)},
			{ID: "p.list", Kind: graph.KindFunc, Position: pos(10, 6)},
		},
		Edges: []graph.Edge{
			{From: "p.Store", To: "p.Order", Kind: graph.EdgeReferences, Positions: []graph.Position{*pos(7, 9)}},
			{From: "p.list", To: "p.(Store).db", Kind: graph.EdgeReferences, Positions: []graph.Position{*pos(11, 4)}},
		},
	}
	got := reachedIDs(Traverse(d, TraversalRequest{Roots: []string{"p.list"}, Direction: DirectionDependencies,
		MaxDepth: MaxTraversalDepth, MaxReached: MaxTraversalReached, TypeEdges: TypeEdgesMembers}))
	if got["p.Order"] {
		t.Errorf("reading db must not reach Order: %v", got)
	}
}
