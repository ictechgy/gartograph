// 모듈 레벨 수확.
//
// 정점은 모듈이고 간선은 모듈 경계를 넘는 import다. go.work 워크스페이스는
// 주 모듈이 여러 개라 이 레벨에서만 구조가 보인다 — 단일 모듈 저장소에서는
// 정점 하나가 정상이다.
package source

import (
	"fmt"

	"github.com/ictechgy/gartograph/graph"
	"golang.org/x/tools/go/packages"
)

// buildModuleDocument는 도달 패키지들의 모듈 소속을 모아 모듈 그래프를 만든다.
// 패키지 간선을 모듈로 올리는 것이 아니라 모듈 소속에서 직접 만든다 —
// 같은 모듈 안의 import는 모듈 간선이 아니다.
// patternErrPaths는 패턴 오류 limitation이 이미 센 경로다(patternErrorPaths) — 로드 오류로 다시 세지 않는다.
func buildModuleDocument(root string, reachable []*packages.Package,
	includeDeps bool, patternErrPaths map[string]bool) *graph.Document {
	doc := &graph.Document{
		Version: graph.Version,
		Tool:    graph.Tool,
		Level:   graph.LevelModule,
		Root:    root,
	}
	kept := make(map[string]*packages.Package)
	var noModule, skipped int
	loadErrors := moduleLoadErrors(reachable, includeDeps, patternErrPaths)

	for _, p := range reachable {
		if p.Module == nil {
			// std의 builtin 패키지(unsafe 등)는 모듈이 없다. 풀리지 않은 import(어느 모듈도 제공하지 않는
			// 경로)도 모듈이 없지만 builtin이 아니다 — 로드 오류로 센다(moduleLoadErrors).
			if len(p.Errors) == 0 {
				noModule++
			}
			continue
		}
		if !p.Module.Main && !includeDeps {
			skipped++
			continue
		}
		kept[p.PkgPath] = p
		moduleVertex(doc, p.Module)
	}

	edgeSet := make(map[string]bool)
	for _, p := range reachable {
		if kept[p.PkgPath] == nil {
			continue
		}
		for _, imp := range p.Imports {
			other := kept[imp.PkgPath]
			if other == nil || p.Module.Path == other.Module.Path {
				continue
			}
			// 모듈 간선은 패키지 import들의 합산이라 하나의 사용 지점이 없다.
			key := p.Module.Path + "\x00" + other.Module.Path
			if !edgeSet[key] {
				edgeSet[key] = true
				doc.Edges = append(doc.Edges, graph.Edge{
					From: p.Module.Path, To: other.Module.Path, Kind: graph.EdgeImport,
				})
			}
		}
	}

	if skipped > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d packages belong to dependency modules (use --deps to include)", skipped))
	}
	if noModule > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d packages have no module (builtin); they have no module vertex", noModule))
	}
	if len(loadErrors) > 0 {
		doc.Limitation(fmt.Sprintf(
			"%d packages reported load errors; module edges through them may be incomplete", len(loadErrors)))
	}
	doc.Sort()
	return doc
}

// moduleLoadErrors는 module 문서가 셀 로드 오류 패키지의 경로 집합이다. 패키지 레벨의 "N packages reported load
// errors"와 같은 것을 센다 — 정점(모듈)에 들어가는 패키지(주 모듈, --deps면 의존 모듈)와, 모듈이 없어 정점이 될 수
// 없는 오류 패키지(풀리지 않은 import)다. 예전에는 module 레벨이 로드 오류를 하나도 세지 않아, 주 모듈 패키지의
// import를 읽지 못해도 조용했고 풀리지 않은 import는 "모듈 없는(builtin) 패키지"로 세였다. 테스트 변형은 원
// 패키지와 같은 경로라 한 번만 세고, 패턴 오류 limitation이 이미 센 경로(patternErrPaths)는 뺀다.
func moduleLoadErrors(reachable []*packages.Package, includeDeps bool,
	patternErrPaths map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, p := range reachable {
		if len(p.Errors) == 0 || patternErrPaths[p.PkgPath] {
			continue
		}
		if p.Module == nil || p.Module.Main || includeDeps {
			out[p.PkgPath] = true
		}
	}
	return out
}

// moduleVertex는 모듈 정점을 중복 없이 추가한다.
// 정점 ID는 모듈 경로다 — 버전은 정체성이 아니라 해석 결과라 빠진다.
func moduleVertex(doc *graph.Document, m *packages.Module) {
	for _, v := range doc.Vertices {
		if v.ID == m.Path {
			return
		}
	}
	doc.Vertices = append(doc.Vertices, graph.Vertex{
		ID:   m.Path,
		Kind: graph.KindModule,
		Name: m.Path,
	})
}
