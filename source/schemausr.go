// persistence 사실의 symbol.usr 귀속.
//
// isthmus trace는 핸들러 순회의 도달 집합과 relation-use 사실을 symbol.usr의 정확한
// 문자열 일치로 잇는다. 그래서 usr는 `impact`·`reach`가 쓰는 심볼 정점 ID여야 하고,
// 귀속 규칙은 심볼 수확(declEdges·specEdges)이 간선 출발점을 정하는 규칙과 같아야 한다 —
// 수확이 어떤 선언 안의 참조를 X에서 긋는다면, 같은 자리의 SQL 사실도 X의 사실이다.
package source

import (
	"go/ast"
	"go/types"
	"strings"

	"github.com/ictechgy/gartograph/graph"
	"golang.org/x/tools/go/packages"
)

// symbolIDs는 usr 확인용 심볼 그래프의 색인이다.
type symbolIDs struct {
	// symbols는 심볼 레벨 정점(type·func·method·field·var·const) ID 집합이다 —
	// 패키지 정점은 usr로 싣지 않는다(순회가 함수에서 패키지로 가는 의존 간선을 긋지 않아
	// trace에서 이어지지 않는다).
	symbols map[string]bool
	// packages는 패키지 정점 ID다 — 수확과 같은 충돌 접미사(disambiguate)를 붙이는 재료.
	packages map[string]bool
}

// newSymbolIDs는 문서에서 색인을 만든다.
func newSymbolIDs(doc *graph.Document) symbolIDs {
	ids := symbolIDs{symbols: map[string]bool{}, packages: map[string]bool{}}
	for _, v := range doc.Vertices {
		switch v.Kind {
		case graph.KindPackage, graph.KindModule:
			ids.packages[v.ID] = true
		default:
			ids.symbols[v.ID] = true
		}
	}
	return ids
}

// objectVertexID는 객체의 정점 ID를 수확과 같은 규칙으로 만든다.
func (ids symbolIDs) objectVertexID(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	return disambiguate(objectID(obj), ids.packages)
}

// declPart는 같은 귀속으로 훑을 노드 하나다.
type declPart struct {
	node  ast.Node
	owner string
}

// declParts는 최상위 선언을 귀속 단위로 나눈다.
//   - 함수·메서드: 그 정점(본문 안 클로저·지역 타입 포함). 빈 함수(func _)는 정점이 없다.
//   - 타입 선언: 타입 정점(필드 태그 포함). 빈 타입(type _)은 정점이 없다.
//   - 변수·상수: 이름과 값의 개수가 같으면 값마다 그 이름의 정점, 아니면(var a, b = f())
//     첫 이름의 정점. 선언 타입은 첫 이름에 귀속한다. 빈 식별자(_)는 패키지 초기화
//     루트(pkg._)다 — 그 식은 초기화 때 실행되고, 수확도 모듈 심볼을 쓰는 빈 선언을
//     그 정점에서 긋는다.
//   - import: 귀속 없음.
func declParts(scan *schemaScan, p *packages.Package, decl ast.Decl) []declPart {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return []declPart{{node: d, owner: funcOwner(scan.ids, p, d)}}
	case *ast.GenDecl:
		var parts []declPart
		for _, spec := range d.Specs {
			parts = append(parts, specParts(scan.ids, p, spec)...)
		}
		return parts
	}
	return nil
}

// funcOwner는 함수 선언의 정점 ID 후보다.
func funcOwner(ids symbolIDs, p *packages.Package, d *ast.FuncDecl) string {
	if d.Name.Name == "_" || p.TypesInfo == nil {
		return ""
	}
	return ids.objectVertexID(p.TypesInfo.Defs[d.Name])
}

// specParts는 선언 스펙 하나를 귀속 단위로 나눈다.
func specParts(ids symbolIDs, p *packages.Package, spec ast.Spec) []declPart {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		owner := ""
		if s.Name.Name != "_" && p.TypesInfo != nil {
			owner = ids.objectVertexID(p.TypesInfo.Defs[s.Name])
		}
		return []declPart{{node: s, owner: owner}}
	case *ast.ValueSpec:
		return valueParts(ids, p, s)
	}
	// ImportSpec — 경로 문자열은 선언이 아니다.
	return []declPart{{node: spec, owner: ""}}
}

// valueParts는 변수·상수 스펙을 이름별 귀속으로 나눈다.
func valueParts(ids symbolIDs, p *packages.Package, s *ast.ValueSpec) []declPart {
	owners := make([]string, len(s.Names))
	for i, name := range s.Names {
		owners[i] = valueOwner(ids, p, name)
	}
	first := ""
	if len(owners) > 0 {
		first = owners[0]
	}
	var parts []declPart
	if s.Type != nil {
		parts = append(parts, declPart{node: s.Type, owner: first})
	}
	for i, v := range s.Values {
		owner := first
		if len(s.Values) == len(s.Names) {
			owner = owners[i]
		}
		parts = append(parts, declPart{node: v, owner: owner})
	}
	return parts
}

// valueOwner는 변수·상수 이름 하나의 정점 ID 후보다.
func valueOwner(ids symbolIDs, p *packages.Package, name *ast.Ident) string {
	if name.Name == "_" {
		return p.PkgPath + "._"
	}
	if p.TypesInfo == nil {
		return ""
	}
	return ids.objectVertexID(p.TypesInfo.Defs[name])
}

// attachSymbols는 후보 ID가 심볼 정점인 사실에만 symbol을 싣고 나머지를 센다.
// 후보가 정점이 아닌 경우: exclude로 뺀 패키지, 타입 정보가 없는 패키지, 빈 함수·타입,
// 모듈 심볼을 쓰지 않는 빈 선언(pkg._ 정점이 없음), import 경로.
func (s *schemaScan) attachSymbols() {
	for i := range s.list {
		fact := &s.list[i]
		if !s.ids.symbols[fact.owner] {
			s.missingUsrs++
			continue
		}
		fact.Symbol = &FactSymbol{QualifiedName: qualifiedName(fact.owner), Usr: fact.owner}
	}
}

// qualifiedName은 정점 ID의 사람이 읽는 짧은 이름이다 — import 경로의 마지막 요소부터다
// ("example.com/m/store.(Repo).List" → "store.(Repo).List"). 식별자에는 '/'가 올 수
// 없어 마지막 '/' 뒤는 항상 패키지 이름부터 시작한다.
func qualifiedName(id string) string {
	return id[strings.LastIndex(id, "/")+1:]
}
