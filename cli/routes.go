// routes --role server|client — isthmus http 도메인의 go 문서(route-decl·route-call)를 낸다.
//
// 계약은 ../isthmus의 docs/GRAPH-EXCHANGE.md "HTTP 경계" 절과 docs/HTTP-WRAPPERS.md가 정본이다.
// 수확·변환 규칙은 source.RouteFacts(서버)·source.ClientRouteFacts(클라이언트)에 있고, 여기서는 플래그
// 검증·문서 쓰기·종료 코드만 다룬다(0 정상, 2 사용법·수확 오류 — schema·bridges와 같다).
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

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
	fs.SetOutput(stderr)
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
		fmt.Fprintln(stderr, "error:", err)
		return 2
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
			fmt.Fprintln(stderr, "error:", err)
			return 2
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
	if f.generatedAt == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, f.generatedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("--generated-at must be an RFC 3339 timestamp such as 2026-09-30T00:00:00.000Z")
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
		if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
			return fail(stderr, fmt.Errorf("writing %s: %w", out, err))
		}
		return 0
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}
