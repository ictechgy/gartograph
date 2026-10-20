// init 스캐폴딩 — 수확된 패키지 그래프에서 초기 .gartograph.yml을 만든다.
// deps는 실제 관찰된 import를 그대로 허용 목록으로 옮긴다 — 첫 검사부터
// 통과하는 설정이어야 "현실을 기록하고 점진적으로 조이는" 도입이 성립한다.
package analysis

import (
	"fmt"
	"sort"

	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/graph"
)

// ScaffoldConfig는 문서의 내부 패키지 구조에서 규칙 파일 초안을 만든다.
// 패키지 하나가 컴포넌트 하나다 — 디렉터리로 뭉뚱그리면 실제 경계가
// 사라지므로, 굵은 컴포넌트로 묶는 편집은 사용자가 한다.
// 외부 패키지(--deps 수확분)는 컴포넌트를 만들지 않는다 — vendor 규칙은
// 사용자가 의도를 알고 적어야 할 영역이다.
// go.work 워크스페이스의 형제 모듈 패키지는 내부라 컴포넌트를 만들고, 패턴은 rules와 같이 패키지가 속한
// 모듈 기준 상대 경로다. 그래서 두 모듈의 같은 상대 경로(a/util·b/util)는 같은 패턴에 맞는다 — 컴포넌트를
// 둘 두면 한쪽은 아무 패키지에도 매칭되지 않으므로 한 컴포넌트로 묶는다.
func ScaffoldConfig(d *graph.Document) *config.File {
	comps := map[string][]string{}
	unit := map[string]string{}      // 패키지 정점 ID → 컴포넌트명
	byPattern := map[string]string{} // 패턴 → 컴포넌트명
	for _, v := range d.Vertices {
		if v.Kind != graph.KindPackage || isExternalPackage(d, v) {
			continue
		}
		rel := graph.ModuleRelative(v.ID, packageModule(d, v))
		name, pattern := rel, rel+"/**"
		if rel == "." {
			// 모듈 루트 패키지 — 경로 "."는 재귀 패턴으로 표현할 수 없어
			// 정확 일치로 둔다.
			name, pattern = "root", "."
		}
		if existing, ok := byPattern[pattern]; ok {
			unit[v.ID] = existing
			continue
		}
		// 이름은 컴포넌트 키라 유일해야 한다 — 모듈 루트가 "root"를 쓰면
		// 같은 이름의 root/ 패키지가 밀려나는 식의 충돌을 접미사로 피한다.
		for i := 2; ; i++ {
			if _, taken := comps[name]; !taken {
				break
			}
			name = fmt.Sprintf("%s-%d", rel, i)
			if rel == "." {
				name = fmt.Sprintf("root-%d", i)
			}
		}
		comps[name] = []string{pattern}
		byPattern[pattern] = name
		unit[v.ID] = name
	}
	deps := map[string][]string{}
	seen := map[string]map[string]bool{}
	for _, e := range d.Edges {
		if e.Kind != graph.EdgeImport {
			continue
		}
		f, t := unit[e.From], unit[e.To]
		if f == "" || t == "" || f == t {
			continue
		}
		if seen[f] == nil {
			seen[f] = map[string]bool{}
		}
		seen[f][t] = true
	}
	for name := range comps {
		var tos []string
		for t := range seen[name] {
			tos = append(tos, t)
		}
		sort.Strings(tos)
		deps[name] = tos
	}
	return &config.File{Components: comps, Deps: deps}
}
