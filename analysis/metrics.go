// 결합도 메트릭 — 컴포넌트(또는 설정이 없으면 패키지) 단위의
// afferent/efferent coupling과 불안정성을 계산한다.
// dependency-cruiser의 metrics가 증명한 축이다 — 추이를 추적하는 숫자를
// 에이전트에게 주면 "이 변경이 구조를 얼마나 흔들었나"를 수치로 볼 수 있다.
package analysis

import (
	"math"
	"sort"

	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/graph"
)

// ComponentMetric은 컴포넌트 하나의 결합도다.
// Afferent(Ca)는 나를 의존하는 다른 컴포넌트 수, Efferent(Ce)는 내가
// 의존하는 다른 컴포넌트 수다. Instability는 Ce/(Ca+Ce) — 0에 가까울수록
// 안정(남이 나를 많이 씀), 1에 가까울수록 불안정(나만 남을 씀).
// Ca+Ce가 0이면 정의되지 않아 키가 빠진다 — 0은 "안정"이라는 뜻이 있어
// 고립과 섞으면 안 된다.
// Abstractness(A)는 컴포넌트 안 인터페이스 타입의 비율이다 — Robert Martin의
// 추상성 지표를 Go로 옮기면 인터페이스가 추상 타입의 전부다. Distance는
// |A+I−1| — 주 계열(main sequence)에서 떨어진 정도로, 0에 가까울수록
// 안정·추상 균형이 잡힌 컴포넌트다. 둘 다 타입 정점이 있는 문서
// (type·symbol 레벨)에서만 계산되고, 타입이 하나도 없는 컴포넌트는
// 정의되지 않아 키가 빠진다 — 0은 "전부 구체"라는 뜻이 있어 구분해야 한다.
type ComponentMetric struct {
	Name         string   `json:"name"`
	Packages     []string `json:"packages"`
	Afferent     int      `json:"afferent"`
	Efferent     int      `json:"efferent"`
	Instability  *float64 `json:"instability,omitempty"`
	Abstractness *float64 `json:"abstractness,omitempty"`
	Distance     *float64 `json:"distance,omitempty"`
}

// MetricsReport는 결합도 질의의 결과다.
// Orphans는 모듈 안에서 임포터가 없는 내부 패키지다 — main 패키지와
// 문서의 보존 루트는 진입점이므로 제외한다. dead(심볼 도달성)와는 다른
// 사실이다: 패키지가 살아 있어도 아무도 import하지 않을 수 있다.
// Unmapped는 설정이 있을 때 컴포넌트에 속하지 않은 내부 패키지다.
type MetricsReport struct {
	Components []ComponentMetric `json:"components"`
	Orphans    []string          `json:"orphans,omitempty"`
	Unmapped   []string          `json:"unmapped,omitempty"`
}

// Metrics는 패키지 정점과 import 간선만으로 결합도를 계산한다.
// cfg가 nil이면 패키지 하나가 곧 하나의 단위다 — .gartograph.yml 없는
// 저장소에서도 같은 질의가 답해야 한다.
// 심볼 레벨 문서에서도 패키지 정점의 import 간선은 그대로 있으므로
// 어느 레벨의 문서든 같은 결과를 낸다.
func Metrics(d *graph.Document, cfg *config.File) *MetricsReport {
	unit := map[string]string{} // 패키지 정점 ID → 단위(컴포넌트 또는 패키지 자신)
	var unmapped []string
	if cfg != nil {
		m := MapComponents(d, cfg)
		for name, pkgs := range m.Components {
			for _, p := range pkgs {
				unit[p] = name
			}
		}
		unmapped = m.Unmapped
	}
	roots := rootSet(d)
	isPkg := map[string]bool{}
	for _, v := range d.Vertices {
		if v.Kind == graph.KindPackage {
			isPkg[v.ID] = true
			if cfg == nil {
				unit[v.ID] = v.ID
			}
		}
	}
	// 컴포넌트 단위 간선으로 환산한다 — 같은 단위 안의 의존은 결합이 아니다.
	// orphan 판정용 임포터 수는 단위와 무관하게 실제 패키지 간선에서 센다.
	inDeg, outDeg := map[string]map[string]bool{}, map[string]map[string]bool{}
	importers := map[string]int{}
	for _, e := range d.Edges {
		if e.Kind != graph.EdgeImport {
			continue
		}
		if isPkg[e.To] {
			importers[e.To]++
		}
		fu, tu := unit[e.From], unit[e.To]
		if fu == "" || tu == "" || fu == tu {
			continue
		}
		if outDeg[fu] == nil {
			outDeg[fu] = map[string]bool{}
		}
		if inDeg[tu] == nil {
			inDeg[tu] = map[string]bool{}
		}
		outDeg[fu][tu] = true
		inDeg[tu][fu] = true
	}
	// orphan은 임포터가 하나도 없는 내부 패키지다 — 매핑 여부와 무관하게
	// 사실을 보고한다. unmapped 패키지도 orphan이 될 수 있다.
	var orphans []string
	for _, v := range d.Vertices {
		if v.Kind != graph.KindPackage {
			continue
		}
		if isInternalOrphanCandidate(d, v, roots) && importers[v.ID] == 0 {
			orphans = append(orphans, v.ID)
		}
	}
	sort.Strings(orphans)

	// 추상성 분모는 단위별 타입 정점 수다 — 타입은 패키지 소속이므로
	// v.Package를 단위로 환산한다. 외부 패키지의 타입은 수확되지 않아
	// 자연스럽게 제외된다.
	typeCount, ifaceCount := map[string]int{}, map[string]int{}
	for _, v := range d.Vertices {
		if v.Kind != graph.KindType {
			continue
		}
		if u := unit[v.Package]; u != "" {
			typeCount[u]++
			if v.Interface {
				ifaceCount[u]++
			}
		}
	}

	names := map[string]bool{}
	for _, u := range unit {
		names[u] = true
	}
	if cfg != nil {
		for name := range cfg.Components {
			names[name] = true // 매칭 0인 컴포넌트도 보여야 구멍이 보인다
		}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	rep := &MetricsReport{Unmapped: unmapped, Orphans: orphans}
	for _, n := range sorted {
		var pkgs []string
		if cfg != nil {
			for p, u := range unit {
				if u == n {
					pkgs = append(pkgs, p)
				}
			}
			sort.Strings(pkgs)
		} else {
			pkgs = []string{n}
		}
		ca, ce := len(inDeg[n]), len(outDeg[n])
		m := ComponentMetric{Name: n, Packages: pkgs, Afferent: ca, Efferent: ce}
		var iRaw float64
		if ca+ce > 0 {
			iRaw = float64(ce) / float64(ca+ce)
			i := math.Round(iRaw*1000) / 1000
			m.Instability = &i
		}
		// 타입이 하나도 없으면 추상성은 정의되지 않는다 — 0으로 채우면
		// "전부 구체 타입"이라는 다른 사실과 구분할 수 없다.
		if typeCount[n] > 0 {
			aRaw := float64(ifaceCount[n]) / float64(typeCount[n])
			a := math.Round(aRaw*1000) / 1000
			m.Abstractness = &a
			// distance는 반올림 전 값으로 계산해 한 번만 반올림한다 —
			// 반올림된 피연산자로 계산하면 경계값 근처에서 실제 순서와
			// 어긋난 D가 나올 수 있다.
			if ca+ce > 0 {
				dist := math.Round(math.Abs(aRaw+iRaw-1)*1000) / 1000
				m.Distance = &dist
			}
		}
		rep.Components = append(rep.Components, m)
	}
	return rep
}

// rootSet은 문서에 기록된 보존 루트의 패키지 ID 집합이다.
// 루트는 심볼 ID일 수 있으므로 패키지 부분을 뽑는다.
func rootSet(d *graph.Document) map[string]bool {
	out := map[string]bool{}
	for _, r := range d.Roots {
		if v, ok := d.VertexByID(r); ok && v.Package != "" {
			out[v.Package] = true
		} else {
			out[r] = true
		}
	}
	return out
}

// isInternalOrphanCandidate는 orphan 후보인지 본다 — 내부 패키지이면서
// main 패키지(진입점)도 보존 루트 소속도 아닌 것.
// 외부 패키지는 모듈 안에서의 임포터 유무가 orphan의 뜻을 갖지 않는다.
func isInternalOrphanCandidate(d *graph.Document, v graph.Vertex, roots map[string]bool) bool {
	if isExternalPackage(d, v) || v.Name == "main" || roots[v.ID] {
		return false
	}
	return true
}
