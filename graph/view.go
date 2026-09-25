package graph

import "sort"

// vertexKindsForLevel는 레벨별 정점 집합이다.
// symbol은 가장 세밀한 레벨이라 type을 포함한다.
var vertexKindsForLevel = map[Level]map[VertexKind]bool{
	LevelModule:  {KindModule: true},
	LevelPackage: {KindPackage: true},
	LevelType:    {KindType: true},
	LevelSymbol: {
		KindType: true, KindFunc: true, KindMethod: true,
		KindField: true, KindVar: true, KindConst: true,
	},
}

// edgeKindsForLevel는 레벨별 의존 간선 집합이다.
// 패키지 순환은 컴파일러가 막으므로 실전 순환 검사는 type·symbol 레벨이다.
var edgeKindsForLevel = map[Level][]EdgeKind{
	LevelModule:  {EdgeImport},
	LevelPackage: {EdgeImport},
	LevelType:    {EdgeEmbeds, EdgeImplements, EdgeReferences},
	LevelSymbol:  {EdgeCall, EdgeImplements, EdgeEmbeds, EdgeReferences},
}

// Rank는 레벨의 세밀함 순서다 — 문서가 요청한 레벨을 담았는지 비교할 때 쓴다.
func (l Level) Rank() int {
	switch l {
	case LevelModule:
		return 0
	case LevelPackage:
		return 1
	case LevelType:
		return 2
	case LevelSymbol:
		return 3
	default:
		return -1
	}
}

// View는 Document를 레벨별 부분 그래프로 투영한다.
// 정점은 레벨의 종류 집합으로, 간선은 양 끝이 살아 있고 레벨의 간선 종류에
// 해당하는 것만 남긴다 — 소유(contains) 간선은 어느 레벨에서도 의존이 아니다.
func (d *Document) View(l Level) (*Document, error) {
	vk, ok := vertexKindsForLevel[l]
	if !ok {
		return nil, &UnknownLevelError{Value: string(l)}
	}
	ek := edgeKindsForLevel[l]

	out := &Document{
		Version:     d.Version,
		Tool:        d.Tool,
		Level:       l,
		Root:        d.Root,
		Module:      d.Module,
		Roots:       d.Roots,
		Limitations: d.Limitations,
		// 수확 사실이라 투영해도 그대로다.
		AnonymousDispatch:   d.AnonymousDispatch,
		InterfaceMethodSets: d.InterfaceMethodSets,
	}
	alive := make(map[string]bool)
	for _, v := range d.Vertices {
		if vk[v.Kind] {
			alive[v.ID] = true
			out.Vertices = append(out.Vertices, v)
		}
	}
	for _, e := range d.Edges {
		if !alive[e.From] || !alive[e.To] {
			continue
		}
		for _, k := range ek {
			if e.Kind == k {
				out.Edges = append(out.Edges, e)
				break
			}
		}
	}
	out.Sort()
	return out, nil
}

// Symbols는 심볼 레벨 정점(type·func·method·field·var·const)만 골라 돌려준다.
// dead 질의처럼 패키지·모듈이 아닌 정점만 대상으로 할 때 쓴다.
func (d *Document) Symbols() []Vertex {
	var out []Vertex
	for _, v := range d.Vertices {
		switch v.Kind {
		case KindType, KindFunc, KindMethod, KindField, KindVar, KindConst:
			out = append(out, v)
		}
	}
	return out
}

// RootIDs는 문서에 실린 보존 루트 중 실제 정점이 있는 것만 돌려준다.
// 문서가 잘렸거나 루트가 생략된 경우를 질의 쪽에서 매번 검사하지 않게 한다.
func (d *Document) RootIDs() []string {
	var out []string
	for _, r := range d.Roots {
		if d.HasVertex(r) {
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}
