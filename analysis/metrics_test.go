package analysis

import (
	"testing"

	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/graph"
)

// metricsDoc은 web→svc→db 직선과 임포터 없는 util 패키지를 가진 문서다.
func metricsDoc() *graph.Document {
	return &graph.Document{
		Module: "example.com/m",
		Vertices: []graph.Vertex{
			{ID: "example.com/m/web", Kind: graph.KindPackage, Name: "web"},
			{ID: "example.com/m/svc", Kind: graph.KindPackage, Name: "svc"},
			{ID: "example.com/m/db", Kind: graph.KindPackage, Name: "db"},
			{ID: "example.com/m/util", Kind: graph.KindPackage, Name: "util"},
			{ID: "example.com/m", Kind: graph.KindPackage, Name: "main"},
		},
		Edges: []graph.Edge{
			{From: "example.com/m/web", To: "example.com/m/svc", Kind: graph.EdgeImport},
			{From: "example.com/m/svc", To: "example.com/m/db", Kind: graph.EdgeImport},
			{From: "example.com/m", To: "example.com/m/web", Kind: graph.EdgeImport},
		},
	}
}

// metricsCfg는 web/svc/db/util을 컴포넌트로 매핑한다 — main은 컴포넌트 밖.
func metricsCfg() *config.File {
	return &config.File{Components: map[string][]string{
		"web": {"web"}, "svc": {"svc"}, "db": {"db"}, "util": {"util"},
	}}
}

// metricOf는 이름으로 컴포넌트 메트릭을 찾는다.
func metricOf(rep *MetricsReport, name string) ComponentMetric {
	for _, m := range rep.Components {
		if m.Name == name {
			return m
		}
	}
	return ComponentMetric{Name: name + ":missing"}
}

// TestMetricsCaCe는 컴포넌트 단위 결합도를 확인한다.
// svc는 web이 의존하고 db에 의존하므로 Ca=1, Ce=1, I=0.5다.
func TestMetricsCaCe(t *testing.T) {
	rep := Metrics(metricsDoc(), metricsCfg())
	svc := metricOf(rep, "svc")
	if svc.Afferent != 1 || svc.Efferent != 1 {
		t.Fatalf("svc coupling: %+v", svc)
	}
	if svc.Instability == nil || *svc.Instability != 0.5 {
		t.Fatalf("svc instability must be 0.5: %+v", svc)
	}
	// db는 흡입만 한다 — Ca=1, Ce=0, I=0(최안정).
	db := metricOf(rep, "db")
	if db.Afferent != 1 || db.Efferent != 0 ||
		db.Instability == nil || *db.Instability != 0 {
		t.Fatalf("db coupling: %+v", db)
	}
	// util은 아무와도 결합이 없다 — 불안정성은 정의되지 않아 키가 빠진다.
	util := metricOf(rep, "util")
	if util.Instability != nil {
		t.Fatalf("isolated component must have no instability: %+v", util)
	}
	// main 패키지는 컴포넌트에 매핑되지 않아 unmapped다.
	if len(rep.Unmapped) != 1 || rep.Unmapped[0] != "example.com/m" {
		t.Fatalf("main pkg must be unmapped: %+v", rep.Unmapped)
	}
}

// TestMetricsOrphans는 임포터 없는 내부 패키지만 orphan으로 보고한다.
// main 패키지는 진입점이라 제외된다 — 보존 루트를 orphan으로 울리면 안 된다.
func TestMetricsOrphans(t *testing.T) {
	rep := Metrics(metricsDoc(), metricsCfg())
	if len(rep.Orphans) != 1 || rep.Orphans[0] != "example.com/m/util" {
		t.Fatalf("only util is an orphan: %+v", rep.Orphans)
	}
}

// TestMetricsRootExclusion은 보존 루트 소속 패키지가 orphan에서
// 빠지는지 확인한다 — 루트가 심볼 ID일 때는 그 패키지를 뽑아야 한다.
func TestMetricsRootExclusion(t *testing.T) {
	d := metricsDoc()
	// util 안의 init이 보존 루트다 — util은 orphan 후보에서 빠져야 한다.
	d.Roots = []string{"example.com/m/util.init"}
	d.Vertices = append(d.Vertices,
		graph.Vertex{ID: "example.com/m/util.init", Kind: graph.KindFunc,
			Package: "example.com/m/util"})
	rep := Metrics(d, metricsCfg())
	if len(rep.Orphans) != 0 {
		t.Fatalf("root-bearing package must not be an orphan: %+v", rep.Orphans)
	}
	// 루트 ID가 문서에 없는 정점이면 그대로 쓴다 — 잘못된 입력을 추리지 않는다.
	d.Roots = []string{"example.com/m/ghost"}
	rep = Metrics(d, metricsCfg())
	if len(rep.Orphans) != 1 {
		t.Fatalf("unknown root id must not hide the orphan: %+v", rep.Orphans)
	}
}

// TestMetricsBlankRootKeepsOrphan은 빈 식별자 루트(pkg._)가 패키지를 orphan에서
// 빼지 않는지 확인한다 — var _ I = (*T)(nil)은 컴파일 타임 단언이지 패키지를
// 남기겠다는 표지가 아니다(init·keep과 다르다).
func TestMetricsBlankRootKeepsOrphan(t *testing.T) {
	d := metricsDoc()
	d.Roots = []string{"example.com/m/util._"}
	d.Vertices = append(d.Vertices,
		graph.Vertex{ID: "example.com/m/util._", Kind: graph.KindVar, Name: "_",
			Package: "example.com/m/util"})
	rep := Metrics(d, metricsCfg())
	if len(rep.Orphans) != 1 || rep.Orphans[0] != "example.com/m/util" {
		t.Fatalf("a blank-declaration root must not hide the orphan: %+v", rep.Orphans)
	}
}

// TestMetricsNoConfig는 설정 없이 패키지 단위로 계산되는지 확인한다.
func TestMetricsNoConfig(t *testing.T) {
	rep := Metrics(metricsDoc(), nil)
	// 컴포넌트 단위가 패키지다 — main도 단위가 된다.
	svc := metricOf(rep, "example.com/m/svc")
	if svc.Afferent != 1 || svc.Efferent != 1 {
		t.Fatalf("per-package svc coupling: %+v", svc)
	}
	if len(rep.Components) != 5 {
		t.Fatalf("per-package units: %+v", rep.Components)
	}
}

// TestMetricsAbstractnessDistance는 인터페이스 비율(A)과 주 계열 거리(D)를 확인한다.
// D = |A+I−1| — 타입 정점이 없는 단위는 정의되지 않아 키가 빠진다.
func TestMetricsAbstractnessDistance(t *testing.T) {
	d := metricsDoc()
	// svc에 인터페이스 하나와 구체 타입 하나를 둔다 — A = 1/2 = 0.5.
	// I = 0.5이므로 D = |0.5+0.5−1| = 0.
	d.Vertices = append(d.Vertices,
		graph.Vertex{ID: "example.com/m/svc.Store", Kind: graph.KindType,
			Package: "example.com/m/svc", Interface: true},
		graph.Vertex{ID: "example.com/m/svc.svcImpl", Kind: graph.KindType,
			Package: "example.com/m/svc"},
		// db는 전부 구체다 — A = 0, I = 0이므로 D = 1(완전 구체·완전 안정).
		graph.Vertex{ID: "example.com/m/db.Row", Kind: graph.KindType,
			Package: "example.com/m/db"},
	)
	rep := Metrics(d, metricsCfg())
	svc := metricOf(rep, "svc")
	if svc.Abstractness == nil || *svc.Abstractness != 0.5 {
		t.Fatalf("svc A must be 0.5: %+v", svc)
	}
	if svc.Distance == nil || *svc.Distance != 0 {
		t.Fatalf("svc D must be 0 (on the main sequence): %+v", svc)
	}
	db := metricOf(rep, "db")
	if db.Abstractness == nil || *db.Abstractness != 0 {
		t.Fatalf("db A must be 0 (all concrete): %+v", db)
	}
	if db.Distance == nil || *db.Distance != 1 {
		t.Fatalf("db D must be 1 (concrete stable zone): %+v", db)
	}
	// web은 타입 정점이 없다 — A/D는 정의되지 않아 키가 빠진다.
	web := metricOf(rep, "web")
	if web.Abstractness != nil || web.Distance != nil {
		t.Fatalf("type-less component must omit A/D: %+v", web)
	}
	// I가 정의되지 않은 고립 단위는 D도 정의되지 않는다 — |A+?−1|은 계산 불가.
	d.Vertices = append(d.Vertices,
		graph.Vertex{ID: "example.com/m/util.Helper", Kind: graph.KindType,
			Package: "example.com/m/util"})
	rep = Metrics(d, metricsCfg())
	util := metricOf(rep, "util")
	if util.Abstractness == nil || *util.Abstractness != 0 {
		t.Fatalf("util A must be 0: %+v", util)
	}
	if util.Distance != nil {
		t.Fatalf("isolated component must omit D (I undefined): %+v", util)
	}
}

// TestMetricsDistanceUnrounded는 D가 반올림 전의 A·I로 계산되는지
// 확인한다 — A=I=1/3이면 실제 D는 |2/3−1| = 0.333이지만 반올림된
// 피연산자(0.333+0.333)로 계산하면 0.334가 나온다.
func TestMetricsDistanceUnrounded(t *testing.T) {
	d := &graph.Document{
		Module: "m",
		Vertices: []graph.Vertex{
			{ID: "m/a", Kind: graph.KindPackage, Name: "a"},
			{ID: "m/b", Kind: graph.KindPackage, Name: "b"},
			{ID: "m/c", Kind: graph.KindPackage, Name: "c"},
			{ID: "m/d", Kind: graph.KindPackage, Name: "d"},
			{ID: "m/c.Iface", Kind: graph.KindType, Package: "m/c", Interface: true},
			{ID: "m/c.S1", Kind: graph.KindType, Package: "m/c"},
			{ID: "m/c.S2", Kind: graph.KindType, Package: "m/c"},
		},
		Edges: []graph.Edge{
			{From: "m/a", To: "m/c", Kind: graph.EdgeImport},
			{From: "m/b", To: "m/c", Kind: graph.EdgeImport},
			{From: "m/c", To: "m/d", Kind: graph.EdgeImport},
		},
	}
	cfg := &config.File{Components: map[string][]string{
		"a": {"a"}, "b": {"b"}, "c": {"c"}, "d": {"d"},
	}}
	// c: Ca=2, Ce=1 → I=1/3. 타입 3 중 인터페이스 1 → A=1/3.
	rep := Metrics(d, cfg)
	c := metricOf(rep, "c")
	if c.Distance == nil || *c.Distance != 0.333 {
		t.Fatalf("D must be computed from unrounded operands (0.333): %+v", c)
	}
}
