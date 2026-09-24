// 외부 인터페이스 디스패치 수확.
//
// 모듈 안 인터페이스는 호출 지점이 보이므로 CHA 간선으로 구현 메서드에
// 닿는다. 모듈 밖 인터페이스(error, fmt.Stringer, flag.Value,
// encoding.TextUnmarshaler 등)는 호출 지점이 표준 라이브러리·의존 안에 있어
// 그래프에 없다. 그래서 "이 메서드는 외부 인터페이스를 구현한다"는 사실만
// 정점에 옮기고, 도달성 의미론은 analysis가 정한다.
package source

import (
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// externalIface는 모듈 밖에서 선언된, 메서드가 있는 인터페이스 하나다.
// names는 사전 필터용 메서드 이름 목록이다 — types.Implements를 모든
// 타입×인터페이스 조합에 부르지 않기 위해 이름 포함 여부부터 본다.
// hidden은 모듈 코드가 이름으로 쓸 수 없는 명명 인터페이스(비공개 이름 —
// context.stringer — 또는 internal 경로) 표시다 — satisfies
// 목록 정리(pruneHidden)의 재료다.
type externalIface struct {
	name   string
	iface  *types.Interface
	names  []string
	hidden bool
}

// markExternalDispatch는 모듈 안 구체 타입의 메서드 중 외부 인터페이스를
// 구현하는 것에 Satisfies·Receiver 사실을 싣는다. 외부 인터페이스는 export
// data의 명명 인터페이스와 의존 소스의 이름 없는 표기(anoniface.go) 둘이다.
// 심볼 레벨 전용이다 — 타입 레벨 문서에는 메서드 정점이 없다.
func (h *harvester) markExternalDispatch(internal []*packages.Package) {
	anon := scanAnonymousInterfaces(internal)
	for _, l := range anon.limitations() {
		h.doc.Limitation(l)
	}
	h.doc.AnonymousDispatch = true
	ifaces := append(externalInterfaces(internal), anon.ifaces...)
	if len(ifaces) == 0 {
		return
	}
	index := make(map[string]int, len(h.doc.Vertices))
	for i, v := range h.doc.Vertices {
		index[v.ID] = i
	}
	for _, named := range concreteTypes(internal) {
		h.markTypeDispatch(named, ifaces, index)
	}
	h.finalizeSatisfies(index, hiddenNames(ifaces))
}

// finalizeSatisfies는 정점마다 satisfies를 정리(pruneHidden)하고 정렬한다 —
// 정렬은 결정적 출력 계약이다.
func (h *harvester) finalizeSatisfies(index map[string]int, hidden map[string]bool) {
	for _, i := range index {
		v := &h.doc.Vertices[i]
		v.Satisfies = pruneHidden(v.Satisfies, hidden)
		sort.Strings(v.Satisfies)
	}
}

// hiddenNames는 비공개 명명 인터페이스 이름 집합이다.
func hiddenNames(ifaces []externalIface) map[string]bool {
	out := map[string]bool{}
	for _, ext := range ifaces {
		if ext.hidden {
			out[ext.name] = true
		}
	}
	return out
}

// pruneHidden은 hidden이 아닌 인터페이스(공개 명명·error·이름 없는 표기)가 하나라도
// 설명하는 메서드에서 hidden 명명 인터페이스를 뺀다. 비공개 인터페이스도 선언 패키지 안의
// 실제 디스패치 지점이지만, 같은 메서드를 공개 인터페이스가 이미 설명하면 도달성에
// 더하는 것이 없다(목록이 비지 않으니 리시버 규칙은 그대로다) — context.stringer·
// runtime.stringer가 fmt.Stringer 옆에 늘 붙어 triage 목록만 부풀렸다.
// 비공개만 설명하는 메서드에는 그대로 남긴다 — 빼면 그 메서드가 죽는다.
func pruneHidden(satisfies []string, hidden map[string]bool) []string {
	var visible []string
	for _, s := range satisfies {
		if !hidden[s] {
			visible = append(visible, s)
		}
	}
	if len(visible) == 0 {
		return satisfies
	}
	return visible
}

// markTypeDispatch는 한 구체 타입이 만족하는 외부 인터페이스마다
// 그 인터페이스 메서드를 구현하는 메서드 정점에 사실을 싣는다.
// 포인터 메서드 집합으로 본다 — 값·포인터 리시버 어느 쪽이 인터페이스로
// 변환돼도 외부 코드가 부를 수 있다.
func (h *harvester) markTypeDispatch(named *types.Named, ifaces []externalIface,
	index map[string]int) {
	ptr := types.NewPointer(named)
	mset := types.NewMethodSet(ptr)
	if mset.Len() == 0 {
		return
	}
	have := make(map[string]bool, mset.Len())
	for i := 0; i < mset.Len(); i++ {
		have[mset.At(i).Obj().Name()] = true
	}
	for _, ext := range ifaces {
		if !hasAllNames(have, ext.names) || !types.Implements(ptr, ext.iface) {
			continue
		}
		for j := 0; j < ext.iface.NumMethods(); j++ {
			m := ext.iface.Method(j)
			sel := mset.Lookup(m.Pkg(), m.Name())
			if sel == nil {
				continue
			}
			if fn, ok := sel.Obj().(*types.Func); ok {
				h.addSatisfies(fn, ext.name, index)
			}
		}
	}
}

// addSatisfies는 메서드 정점 하나에 외부 인터페이스 이름과 리시버를 싣는다.
// 모듈 밖 타입에서 승격된 메서드는 정점이 없어 건너뛴다 — 유령 정점 금지.
// 리시버 타입 정점이 문서에 없으면 사실을 싣지 않는다 — 가리킬 곳 없는
// Receiver는 도달성 계산에서 조용히 무시되는 유령 참조다.
func (h *harvester) addSatisfies(fn *types.Func, ifaceName string, index map[string]int) {
	if fn.Pkg() == nil {
		return
	}
	i, ok := index[objectID(fn)]
	if !ok {
		return
	}
	receiver, ok := receiverTypeID(fn)
	// 리시버 ID가 점 경로 패키지 ID와 겹치면 index는 그 패키지 정점을 가리킨다 —
	// 타입 정점이 아니므로 사실을 싣지 않는다(충돌은 limitation으로 이미 센다).
	if _, exists := index[receiver]; !ok || !exists || h.packageIDs[receiver] {
		return
	}
	v := &h.doc.Vertices[i]
	for _, s := range v.Satisfies {
		if s == ifaceName {
			return
		}
	}
	v.Satisfies = append(v.Satisfies, ifaceName)
	v.Receiver = receiver
}

// receiverTypeID는 메서드 리시버의 명명 타입 정점 ID를 만든다.
// 별칭 리시버(type A = T; func (A) M())는 Unalias로 실체 타입에,
// 제네릭 리시버는 Origin으로 원형 타입에 붙인다 — 타입 정점은 원형
// 선언 하나뿐이다. 명명 타입이 아닌 리시버는 정점이 없어 false다.
func receiverTypeID(fn *types.Func) (string, bool) {
	sig := fn.Signature()
	if sig == nil || sig.Recv() == nil {
		return "", false
	}
	t := types.Unalias(sig.Recv().Type())
	if ptr, ok := t.(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return "", false
	}
	return objectID(named.Origin().Obj()), true
}

// hasAllNames는 타입의 메서드 이름 집합이 인터페이스 메서드 이름을 전부
// 포함하는지 본다 — Implements 전의 값싼 사전 필터다.
func hasAllNames(have map[string]bool, names []string) bool {
	for _, n := range names {
		if !have[n] {
			return false
		}
	}
	return true
}

// concreteTypes는 모듈 안 패키지 스코프의 인터페이스가 아닌 명명 타입을 모은다.
func concreteTypes(internal []*packages.Package) []*types.Named {
	var out []*types.Named
	for _, p := range internal {
		if p.Types == nil {
			continue
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			if _, isIface := named.Underlying().(*types.Interface); !isIface {
				out = append(out, named)
			}
		}
	}
	return out
}

// externalInterfaces는 모듈 패키지가 전이적으로 import하는 모듈 밖 패키지의
// 패키지 스코프 인터페이스와 universe의 error를 모은다. 메서드가 없는
// 인터페이스(any)는 디스패치할 메서드가 없어 뺀다. 제네릭 인터페이스는
// 인스턴스 없이 Implements를 물을 수 없어 뺀다 — 보수적으로 "못 봄" 쪽이다.
// 비공개 인터페이스도 담는다 — 선언 패키지 안에서 디스패치할 수 있다.
func externalInterfaces(internal []*packages.Package) []externalIface {
	inModule := make(map[string]bool, len(internal))
	for _, p := range internal {
		inModule[p.PkgPath] = true
	}
	errType := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
	out := []externalIface{newExternalIface("error", errType)}
	for _, pkg := range importClosure(internal) {
		if inModule[pkg.Path()] {
			continue
		}
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok || named.TypeParams().Len() > 0 {
				continue
			}
			iface, ok := named.Underlying().(*types.Interface)
			if !ok || iface.NumMethods() == 0 {
				continue
			}
			ext := newExternalIface(pkg.Path()+"."+name, iface)
			ext.hidden = !tn.Exported() || isInternalPath(pkg.Path())
			out = append(out, ext)
		}
	}
	return out
}

// isInternalPath는 경로에 internal 세그먼트가 있는지 본다. Go import 규칙상 모듈
// 밖의 internal 패키지는 모듈 코드가 import할 수 없어, 공개 이름이어도 비공개
// 인터페이스와 같은 triage 노이즈다(internal/bisect.Writer 등).
func isInternalPath(path string) bool {
	return path == "internal" || strings.HasPrefix(path, "internal/") ||
		strings.Contains(path, "/internal/") || strings.HasSuffix(path, "/internal")
}

// newExternalIface는 사전 필터용 메서드 이름을 채운 externalIface를 만든다.
func newExternalIface(name string, iface *types.Interface) externalIface {
	names := make([]string, iface.NumMethods())
	for i := range names {
		names[i] = iface.Method(i).Name()
	}
	return externalIface{name: name, iface: iface, names: names}
}

// importClosure는 모듈 패키지에서 출발한 types 레벨 import 전이 폐포를
// 경로 순으로 돌려준다 — 순회 순서가 결과 순서를 흔들지 않게 한다.
func importClosure(internal []*packages.Package) []*types.Package {
	seen := map[string]*types.Package{}
	var queue []*types.Package
	for _, p := range internal {
		if p.Types != nil {
			queue = append(queue, p.Types)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if _, ok := seen[cur.Path()]; ok {
			continue
		}
		seen[cur.Path()] = cur
		queue = append(queue, cur.Imports()...)
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := make([]*types.Package, len(paths))
	for i, path := range paths {
		out[i] = seen[path]
	}
	return out
}
