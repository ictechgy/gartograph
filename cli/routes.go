// routes --role server|client — isthmus http 도메인의 go 문서(route-decl·route-call)를 낸다.
//
// 계약은 ../isthmus의 docs/GRAPH-EXCHANGE.md "HTTP 경계" 절과 docs/HTTP-WRAPPERS.md가 정본이다.
// 수확·변환 규칙은 source.RouteFacts(서버)·source.ClientRouteFacts(클라이언트)에 있고, 여기서는 플래그
// 검증·문서 쓰기·종료 코드만 다룬다(0 정상, 2 사용법·수확 오류 — schema·bridges와 같다).
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/ictechgy/gartograph/export"
	"github.com/ictechgy/gartograph/source"
)

// routesFlags는 routes 명령의 플래그 값이다.
type routesFlags struct {
	role, generatedAt, out, wrappers, service string
	harvest                                   source.Options
}

// cmdRoutes는 서버 라우트 선언(--role server) 또는 클라이언트 호출(--role client) 문서를 낸다.
func cmdRoutes(args []string, stdout, stderr io.Writer) int {
	var f routesFlags
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	setFlagOutput(fs, stderr)
	fs.StringVar(&f.role, "role", "server", "document role: server (route declarations) or client (route calls)")
	fs.StringVar(&f.harvest.Dir, "dir", ".", "module root to scan")
	fs.Var((*patterns)(&f.harvest.Patterns), "pattern", "package pattern (repeatable)")
	fs.StringVar(&f.harvest.Tags, "tags", "", "build tags to pass to the loader (comma-separated)")
	fs.StringVar(&f.service, "service", "", "service identity to record on the document")
	fs.StringVar(&f.wrappers, "wrappers", "", "isthmus http-wrappers v1 file (client role only)")
	fs.StringVar(&f.generatedAt, "generated-at", "", "fixed generatedAt timestamp (RFC 3339, UTC)")
	fs.StringVar(&f.out, "out", "", "write the document to FILE instead of stdout")
	if fs.Parse(args) != nil {
		return 2
	}
	generated, err := validateRoutesFlags(f)
	if err != nil {
		return fail(stderr, err)
	}
	// usr 확인용 그래프는 impact와 같은 exclude로 만든다(schema와 같은 이유).
	if err := applyConfigExclude(&f.harvest); err != nil {
		return fail(stderr, err)
	}
	if f.role == "client" {
		return clientRoutes(f, generated, stdout, stderr)
	}
	doc, err := source.RouteFacts(source.RouteOptions{Harvest: f.harvest, Service: f.service, GeneratedAt: generated}, Version)
	if err != nil {
		return fail(stderr, err)
	}
	return writeDocument(doc, f.out, stdout, stderr)
}

// clientRoutes는 route-call 문서를 낸다. 래퍼 선언 파일의 오류는 사용법 오류(2)다.
func clientRoutes(f routesFlags, generated time.Time, stdout, stderr io.Writer) int {
	opts := source.ClientRouteOptions{Harvest: f.harvest, Service: f.service, GeneratedAt: generated}
	if f.wrappers != "" {
		file, err := source.LoadWrapperFile(f.wrappers)
		if err != nil {
			return fail(stderr, err)
		}
		opts.Wrappers = file
	}
	doc, err := source.ClientRouteFacts(opts, Version)
	if err != nil {
		return fail(stderr, err)
	}
	return writeDocument(doc, f.out, stdout, stderr)
}

// validateRoutesFlags는 역할·플래그 조합과 시각을 검증한다.
func validateRoutesFlags(f routesFlags) (time.Time, error) {
	if f.role != "server" && f.role != "client" {
		return time.Time{}, fmt.Errorf("--role %q is not supported; use --role server (route declarations) or --role client (route calls)", f.role)
	}
	if f.wrappers != "" && f.role != "client" {
		return time.Time{}, fmt.Errorf("--wrappers declares client HTTP wrappers; it needs --role client")
	}
	return parseFactsGeneratedAt(f.generatedAt)
}

// generatedAtUsage는 --generated-at 값이 틀렸을 때의 문구다 — routes·bridges·schema(종료 2)와 reach·impact
// 순회 문서(종료 64)가 같은 문법과 같은 문구를 쓴다.
const generatedAtUsage = "--generated-at must be an RFC 3339 timestamp such as 2026-09-30T00:00:00.000Z"

// generatedAtZeroUsage는 --generated-at이 영(零) 시각(0001-01-01T00:00:00Z와 같은 순간)일 때의 문구다.
const generatedAtZeroUsage = "--generated-at must not be the zero time 0001-01-01T00:00:00Z, which stands for " +
	"\"not set\"; pass the timestamp to record"

// parseFactsGeneratedAt은 bridge-facts 문서(routes·bridges·schema)의 --generated-at을 읽는다. 빈 값은 영(零)
// 시각이고 문서는 추출 시각(지금)을 싣는다. 정규화(UTC 밀리초)는 문서를 만드는 source가 한다.
func parseFactsGeneratedAt(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return parseTimestamp(value)
}

// parseTimestamp는 비어 있지 않은 --generated-at 값 하나를 읽는다(RFC 3339). 영 시각은 거부한다 — source가 영
// 시각을 "주지 않음"(지금)으로 읽어, 그 값을 준 bridges·schema·routes는 조용히 지금 시각을 실었고 reach는 그대로
// 실었다. 두 통로가 같은 값을 다르게 읽지 않게 둘 다 사용법 오류로 막는다.
func parseTimestamp(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.New(generatedAtUsage)
	}
	if t.IsZero() {
		return time.Time{}, errors.New(generatedAtZeroUsage)
	}
	return t, nil
}

// writeDocument는 문서를 JSON으로 파일이나 표준 출력에 쓴다.
func writeDocument(doc any, out string, stdout, stderr io.Writer) int {
	data, err := marshalReport(doc)
	if err != nil {
		return fail(stderr, err)
	}
	if out != "" {
		if err := export.WriteFile(out, append(data, '\n')); err != nil {
			return fail(stderr, fmt.Errorf("writing %s: %w", out, err))
		}
		return 0
	}
	if err := emitBytes(stdout, append(data, '\n')); err != nil {
		return fail(stderr, err)
	}
	return 0
}
