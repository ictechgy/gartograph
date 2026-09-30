// routes --role server — isthmus http 도메인의 go 문서(route-decl)를 낸다.
//
// 계약은 ../isthmus의 docs/GRAPH-EXCHANGE.md "HTTP 경계" 절이 정본이다. 수확·변환 규칙은
// source.RouteFacts에 있고, 여기서는 플래그 검증·문서 쓰기·종료 코드만 다룬다(0 정상,
// 2 사용법·수확 오류 — schema·bridges와 같다).
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ictechgy/gartograph/source"
)

// cmdRoutes는 서버 라우트 선언 문서를 낸다. 클라이언트 호출(route-call)은 아직 수확하지 않는다.
func cmdRoutes(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts source.RouteOptions
	role := fs.String("role", "server", "document role: server (route declarations)")
	fs.StringVar(&opts.Harvest.Dir, "dir", ".", "module root to scan")
	fs.Var((*patterns)(&opts.Harvest.Patterns), "pattern", "package pattern (repeatable)")
	fs.StringVar(&opts.Harvest.Tags, "tags", "", "build tags to pass to the loader (comma-separated)")
	fs.StringVar(&opts.Service, "service", "", "service identity to record on the document")
	generatedAt := fs.String("generated-at", "", "fixed generatedAt timestamp (RFC 3339, UTC)")
	out := fs.String("out", "", "write the document to FILE instead of stdout")
	if fs.Parse(args) != nil {
		return 2
	}
	if err := validateRoutesFlags(*role, *generatedAt, &opts); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	// usr 확인용 그래프는 impact와 같은 exclude로 만든다(schema와 같은 이유).
	if err := applyConfigExclude(&opts.Harvest); err != nil {
		return fail(stderr, err)
	}
	doc, err := source.RouteFacts(opts, Version)
	if err != nil {
		return fail(stderr, err)
	}
	return writeDocument(doc, *out, stdout, stderr)
}

// validateRoutesFlags는 역할과 시각을 검증하고 시각을 옵션에 싣는다.
func validateRoutesFlags(role, generatedAt string, opts *source.RouteOptions) error {
	if role != "server" {
		return fmt.Errorf("--role %q is not supported; gartograph emits server route declarations only (--role server)", role)
	}
	if generatedAt == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, generatedAt)
	if err != nil {
		return fmt.Errorf("--generated-at must be an RFC 3339 timestamp such as 2026-09-30T00:00:00.000Z")
	}
	opts.GeneratedAt = t
	return nil
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
