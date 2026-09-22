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
