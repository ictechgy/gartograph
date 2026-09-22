// Package cli는 gartograph의 명령 진입점이다.
// 종료 코드 계약: 0 정상, 1 --strict에서 위반 발견, 2 사용법·분석 오류.
// 출력은 사람이 아니라 코딩 에이전트가 읽는다는 전제로 설계한다.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"

	"github.com/ictechgy/gartograph/analysis"
	"github.com/ictechgy/gartograph/export"
	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/source"
)

// Version은 CLI가 스스로를 보고하는 버전 문자열이다.
const Version = "0.1.0-dev"

// Run은 인자를 해석해 명령을 실행하고 종료 코드를 돌려준다.
// os.Exit 대신 반환값을 쓰는 것은 종료 코드 계약을 테스트하기 위함이다.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "graph":
		return cmdGraph(args[1:], stdout, stderr)
	case "cycles":
		return cmdCycles(args[1:], stdout, stderr)
	case "query":
		return cmdQuery(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, "gartograph "+Version)
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

// usage는 명령 목록을 출력한다.
// 새 명령을 추가하면 여기와 README를 같이 고친다.
func usage(w io.Writer) {
	fmt.Fprintln(w, `gartograph — Go dependency graph tool

Usage:
  gartograph graph  [--level package] [--format json|mermaid] [flags]
  gartograph cycles [--level package] [--strict] [--format text|json] [flags]
  gartograph query  <id> [--depth N] [--format json] [flags]
  gartograph version

Flags:
  --dir PATH    module root to analyze (default ".")
  --pattern P   package pattern, repeatable (default "./...")
  --tests       include test variant packages
  --deps        include dependencies outside the main module`)
}

// flagSet는 공통 수확 플래그를 등록한다.
// 명령마다 같은 수확 옵션을 쓰므로 한 곳에서 만든다.
func flagSet(name string, stderr io.Writer) (*flag.FlagSet, *source.Options) {
	var opts source.Options
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Dir, "dir", ".", "module root to analyze")
	fs.Var((*patterns)(&opts.Patterns), "pattern", "package pattern (repeatable)")
	fs.BoolVar(&opts.Tests, "tests", false, "include test variant packages")
	fs.BoolVar(&opts.IncludeDeps, "deps", false, "include dependencies outside the main module")
	return fs, &opts
}

// patterns는 --pattern 반복 플래그용 flag.Value다.
type patterns []string

// String은 flag.Value 계약이다.
func (p *patterns) String() string { return fmt.Sprint([]string(*p)) }

// Set은 값을 누적한다.
func (p *patterns) Set(v string) error {
	*p = append(*p, v)
	return nil
}

// cmdGraph는 그래프 산출물 자체를 내보낸다.
func cmdGraph(args []string, stdout, stderr io.Writer) int {
	fs, opts := flagSet("graph", stderr)
	format := fs.String("format", "json", "output format: json|mermaid")
	level := fs.String("level", "package", "graph level: package")
	if fs.Parse(args) != nil {
		return 2
	}
	if *level != string(graph.LevelPackage) {
		fmt.Fprintf(stderr, "level %q is not implemented yet; only package\n", *level)
		return 2
	}
	doc, err := source.LoadPackageGraph(*opts)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	return emit(doc, *format, stdout, stderr)
}

// emit는 형식별 직렬화를 고른다.
// 지원하지 않는 형식은 사용법 오류(2)로 돌린다.
func emit(doc *graph.Document, format string, stdout, stderr io.Writer) int {
	var out []byte
	var err error
	switch format {
	case "json":
		out, err = export.JSON(doc)
	case "mermaid":
		out, err = export.Mermaid(doc)
	default:
		fmt.Fprintf(stderr, "unknown format %q\n", format)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	_, _ = stdout.Write(out)
	return 0
}

// cmdCycles는 순환 의존성을 찾는다.
// --strict가 켜지면 순환이 있을 때 1을 돌려준다.
func cmdCycles(args []string, stdout, stderr io.Writer) int {
	fs, opts := flagSet("cycles", stderr)
	strict := fs.Bool("strict", false, "exit 1 when cycles are found")
	format := fs.String("format", "text", "output format: text|json")
	level := fs.String("level", "package", "graph level: package")
	if fs.Parse(args) != nil {
		return 2
	}
	if *level != string(graph.LevelPackage) {
		fmt.Fprintf(stderr, "level %q is not implemented yet; only package\n", *level)
		return 2
	}
	doc, err := source.LoadPackageGraph(*opts)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	cycles := analysis.Cycles(doc)
	switch *format {
	case "json":
		out, _ := json.MarshalIndent(cycles, "", "  ")
		fmt.Fprintln(stdout, string(out))
	default:
		for _, c := range cycles {
			fmt.Fprintf(stdout, "cycle: %v\n", c.Members)
		}
	}
	if *strict && len(cycles) > 0 {
		return 1
	}
	return 0
}

// parseInterspersed는 positional 인자 사이에 낀 플래그도 파싱한다.
// Go의 flag는 첫 비플래그 인자에서 파싱을 멈추므로, `query <id> --dir .`
// 같은 호출이 동작하려면 positional을 수집하며 재파싱해야 한다.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
	return positional, nil
}

// cmdQuery는 정점 하나에 대해 이웃을 되묻는다.
// JSON은 에이전트 소비용이다 — 잘렸으면 truncated, 깊이는 depth를 싣는다.
func cmdQuery(args []string, stdout, stderr io.Writer) int {
	fs, opts := flagSet("query", stderr)
	depth := fs.Int("depth", 1, "neighbor depth")
	maxN := fs.Int("max", 0, "max neighbors per direction (0 = unlimited)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: gartograph query <vertex-id> [--depth N]")
		return 2
	}
	doc, err := source.LoadPackageGraph(*opts)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	res, err := analysis.Query(doc, positional[0], *depth, *maxN)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	sortNeighborsJSON(res)
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Fprintln(stdout, string(out))
	return 0
}

// sortNeighborsJSON은 질의 결과를 결정적으로 정렬한다.
// BFS 방문 순서는 발견 순서라, 파일로 남길 결과는 ID 순이어야 한다.
func sortNeighborsJSON(res *analysis.Neighbors) {
	sort.Slice(res.DependsOn, func(i, j int) bool {
		return res.DependsOn[i].ID < res.DependsOn[j].ID
	})
	sort.Slice(res.DependedBy, func(i, j int) bool {
		return res.DependedBy[i].ID < res.DependedBy[j].ID
	})
}
