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
func buildModuleDocument(root string, reachable []*packages.Package,
	includeDeps bool) *graph.Document {
	doc := &graph.Document{
		Version: graph.Version,
		Tool:    graph.Tool,
		Level:   graph.LevelModule,
		Root:    root,
	}
	kept := make(map[string]*packages.Package)
	var noModule, skipped int

	for _, p := range reachable {
		if p.Module == nil {
			// std의 builtin 패키지(unsafe 등)는 모듈이 없다.
			noModule++
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
	doc.Sort()
	return doc
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
