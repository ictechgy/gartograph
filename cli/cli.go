// Package cli는 gartograph의 명령 진입점이다.
// 종료 코드 계약: 0 정상, 1 --strict에서 위반 발견, 2 사용법·분석 오류.
// 출력은 사람이 아니라 코딩 에이전트가 읽는다는 전제로 설계한다.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ictechgy/gartograph/analysis"
	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/export"
	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/source"
)

// Version은 CLI가 스스로를 보고하는 버전 문자열이다. 릴리스는
// -ldflags "-X .../cli.Version=<태그>"로 이 값을 덮어쓴다.
var Version = "0.7.0"

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
	case "dead":
		return cmdDead(args[1:], stdout, stderr)
	case "rules":
		return cmdRules(args[1:], stdout, stderr)
	case "query":
		return cmdQuery(args[1:], stdout, stderr)
	case "impact":
		return cmdImpact(args[1:], stdout, stderr)
	case "path":
		return cmdPath(args[1:], stdout, stderr)
	case "shared":
		return cmdShared(args[1:], stdout, stderr)
	case "diff":
		return cmdDiff(args[1:], stdout, stderr)
	case "metrics":
		return cmdMetrics(args[1:], stdout, stderr)
	case "mapping":
		return cmdMapping(args[1:], stdout, stderr)
	case "init":
		return cmdInit(args[1:], stdout, stderr)
	case "bridges":
		return cmdBridges(args[1:], stdout, stderr)
	case "schema":
		return cmdSchema(args[1:], stdout, stderr)
	case "unused-deps":
		return cmdUnusedDeps(args[1:], stdout, stderr)
	case "mcp":
		return cmdMcp(args[1:], stdout, stderr)
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
  gartograph graph  [--level package|type|symbol] [--format json|mermaid|dot] [--out FILE] [flags]
  gartograph cycles [--level package|type|symbol] [--strict] [--format text|json|sarif]
                    [--baseline FILE | --write-baseline FILE] [flags]
  gartograph dead   [--retain-public] [--root ID]... [--explain ID] [--strict]
                    [--format text|json|sarif]
                    [--baseline FILE | --write-baseline FILE] [flags]
  gartograph rules  [--config FILE] [--strict] [--format text|json|sarif]
                    [--baseline FILE | --write-baseline FILE] [flags]
  gartograph query  <id> [--depth N] [--max N] [--level L] [flags]
  gartograph impact <id> [--depth N] [--max N] [--level L] [flags]
  gartograph impact --since <git-rev>|--files F... [--depth N] [flags]
  gartograph path   <from-id> <to-id> [--level L] [flags]
  gartograph shared <id> <id> [more ids...] [--level L] [flags]
  gartograph diff   <old.json> <new.json> [--strict] [--format text|json]
  gartograph metrics [--config FILE] [--format text|json] [flags]
  gartograph mapping [--config FILE] [--format text|json] [flags]
  gartograph init   [--dir PATH]  scaffold .gartograph.yml from observed imports
  gartograph bridges [--dir PATH] [--out FILE]  isthmus bridge-facts (platform "go")
  gartograph schema  [--dir PATH] [--out FILE]  isthmus persistence relation-uses (platform "go")
  gartograph unused-deps [--strict] [--format text|json] [flags]
  gartograph mcp    serve the graph over MCP stdio [flags]
  gartograph version

Harvest flags (graph, cycles, dead, rules, query):
  --dir PATH    module root to analyze (default ".")
  --pattern P   package pattern, repeatable (default "./...")
  --tests       include test variant packages
  --deps        include dependencies outside the main module
  --goos/--goarch  harvest for a different target platform (conditional files)
  --graph FILE  read a saved graph document instead of harvesting

ID commands (query, impact, path, shared) harvest at --level symbol by
default so type, function, and method IDs resolve; --level package is faster.`)
}

// flagSet는 공통 수확 플래그를 등록한다.
// 명령마다 같은 수확 옵션을 쓰므로 한 곳에서 만든다.
func flagSet(name string, stderr io.Writer) (*flag.FlagSet, *source.Options, *string) {
	var opts source.Options
	var graphPath string
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Dir, "dir", ".", "module root to analyze")
	fs.Var((*patterns)(&opts.Patterns), "pattern", "package pattern (repeatable)")
	fs.BoolVar(&opts.Tests, "tests", false, "include test variant packages")
	fs.BoolVar(&opts.IncludeDeps, "deps", false, "include dependencies outside the main module")
	fs.StringVar(&opts.Tags, "tags", "", "build tags to pass to the loader (comma-separated)")
	fs.StringVar(&opts.GOOS, "goos", "", "target GOOS for conditional files (default: host)")
	fs.StringVar(&opts.GOARCH, "goarch", "", "target GOARCH for conditional files (default: host)")
	fs.StringVar(&graphPath, "graph", "", "read a saved graph document instead of harvesting")
	return fs, &opts, &graphPath
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

// stringsFlag는 --root 같은 반복 문자열 플래그다.
type stringsFlag []string

// String은 flag.Value 계약이다.
func (s *stringsFlag) String() string { return fmt.Sprint([]string(*s)) }

// Set은 값을 누적한다.
func (s *stringsFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
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

// loadDoc는 --graph가 있으면 파일에서, 없으면 수확해서 Document를 얻는다.
// 저장 문서를 읽을 때 수확 플래그는 무시된다 — 두 입력 경로가 섞이면
// 어느 쪽이 쓰였는지 불분명해진다.
// 수확 경로에서는 .gartograph.yml의 exclude도 함께 적용한다 — 수확 범위를
// 좁히는 설정이 명령마다 다르게 동작하면 같은 저장소가 다른 그래프가 된다.
func loadDoc(opts *source.Options, graphPath string) (*graph.Document, error) {
	if graphPath != "" {
		return export.LoadFile(graphPath)
	}
	if err := applyConfigExclude(opts); err != nil {
		return nil, err
	}
	return source.Load(*opts)
}

// applyConfigExclude는 .gartograph.yml이 있으면 그 exclude 패턴을 수확
// 옵션에 싣는다. 파일이 없으면 아무것도 하지 않는다 — exclude는 선택
// 설정이지 필수가 아니다. 있는데 깨진 파일은 에러다 — 제외 범위를 모른 채
// 수확하면 소비자가 "없다"를 "뺐다"와 구분할 수 없다.
func applyConfigExclude(opts *source.Options) error {
	path, ok := config.Find(opts.Dir)
	if !ok {
		return nil
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	opts.Exclude = append(opts.Exclude, cfg.Exclude...)
	return nil
}

// requireLevel은 문서가 want 레벨 이상을 담았는지 확인한다.
// 부족하면 어떻게 다시 수확할지를 메시지에 담는다.
func requireLevel(doc *graph.Document, want graph.Level) error {
	if doc.Level.Rank() < want.Rank() {
		return fmt.Errorf("document level is %q; re-harvest with --level %s",
			doc.Level, want)
	}
	return nil
}

// idLevelFlag는 정점 ID를 받는 명령(query·impact·path·shared)의 --level을 등록한다.
// 기본이 symbol인 이유: ID가 타입·함수·메서드를 가리킬 수 있는데 package
// 레벨로 수확하면 그 정점이 문서에 없어 "vertex not found"가 된다.
// 패키지 ID의 이웃·경로·집합은 symbol 문서에서도 같다 — Adjacency·Incoming이
// contains를 빼서 패키지 정점에는 import 간선만 닿는다. 다만 limitations에는
// symbol 수확이 실제로 못 본 영역(모듈 밖 심볼 참조 수 등)이 더해진다 —
// 그 문서의 사실이라 거르지 않는다. package는 큰 저장소에서 수확을 빠르게
// 하려는 선택지로 남긴다. --graph와 함께면 다른 수확 플래그처럼 무시된다.
func idLevelFlag(fs *flag.FlagSet) *string {
	return fs.String("level", string(graph.LevelSymbol),
		"harvest level: module|package|type|symbol (ignored with --graph)")
}

// loadIDDoc는 ID 명령의 --level을 해석해 문서를 얻는다.
// 레벨 오류는 수확 전에 드러나야 한다 — 잘못된 값이 기본 레벨로 새면
// 사용자는 자기가 고른 레벨의 답이라고 오독한다.
func loadIDDoc(opts *source.Options, graphPath, level string) (*graph.Document, error) {
	lvl, err := graph.ParseLevel(level)
	if err != nil {
		return nil, err
	}
	opts.Level = lvl
	return loadDoc(opts, graphPath)
}

// levelHint는 symbol보다 거친 문서에서 정점을 못 찾은 오류에 레벨 사실과
// 효과 있는 해결책을 덧붙인다. 그 문서에 ID가 없는 것은 "코드에 없다"가
// 아니라 "그 레벨이라 못 봤다"일 수 있다 — 둘을 구분하지 않으면 소비자가
// 존재하는 코드를 없다고 믿는다. 저장 문서(--graph)에는 --level이 듣지 않으므로
// 다시 저장하는 길을 알린다 — 무시되는 플래그를 권하면 같은 오류가 되풀이된다.
func levelHint(doc *graph.Document, graphPath string, err error) error {
	if !errors.Is(err, analysis.ErrNotFound) || doc.Level.Rank() >= graph.LevelSymbol.Rank() {
		return err
	}
	remedy := "re-run with --level symbol"
	if graphPath != "" {
		remedy = "save the document with 'gartograph graph --level symbol --out FILE' or drop --graph"
	}
	return fmt.Errorf("%w (document is %s level; finer-grained IDs are absent: %s)",
		err, doc.Level, remedy)
}

// fail은 에러를 출력하고 종료 코드 2를 돌려준다.
func fail(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "error:", err)
	return 2
}

// cmdGraph는 그래프 산출물 자체를보낸다.
func cmdGraph(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("graph", stderr)
	format := fs.String("format", "json", "output format: json|mermaid|dot")
	level := fs.String("level", string(graph.LevelPackage), "harvest level: package|type|symbol")
	out := fs.String("out", "", "also write the document to FILE")
	if fs.Parse(args) != nil {
		return 2
	}
	if *graphPath != "" {
		fmt.Fprintln(stderr, "--graph is meaningless for the graph command; it produces the document")
		return 2
	}
	lvl, err := graph.ParseLevel(*level)
	if err != nil {
		return fail(stderr, err)
	}
	opts.Level = lvl
	// 수확 경로는 loadDoc 하나다 — exclude 같은 설정 적용이 명령마다
	// 다르면 같은 저장소가 다른 그래프를 낸다.
	doc, err := loadDoc(opts, "")
	if err != nil {
		return fail(stderr, err)
	}
	if *out != "" {
		if err := export.SaveFile(doc, *out); err != nil {
			return fail(stderr, err)
		}
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
	case "dot":
		out, err = export.DOT(doc)
	default:
		fmt.Fprintf(stderr, "unknown format %q\n", format)
		return 2
	}
	if err != nil {
		return fail(stderr, err)
	}
	_, _ = stdout.Write(out)
	return 0
}

// cyclesReport는 cycles 명령의 JSON 출력 형식이다.
// Baselined는 baseline에 이미 있어 넘어간 순환, StaleBaseline은
// 더 이상 발생하지 않아 baseline 재생성이 필요한 항목이다.
type cyclesReport struct {
	Cycles        []analysis.Cycle `json:"cycles"`
	Baselined     []analysis.Cycle `json:"baselined,omitempty"`
	StaleBaseline []analysis.Cycle `json:"staleBaseline,omitempty"`
	Limitations   []string         `json:"limitations,omitempty"`
}

// cmdCycles는 순환 의존성을 찾는다.
// --level은 문서를 어느 레벨로 투영할지 고른다 — 패키지 순환은 컴파일러가
// 막으므로 실전 검사는 type·symbol 레벨이다.
// --strict가 켜지면 순환이 있을 때 1을 돌려준다.
// --baseline은 알려진 순환을 걸러 새 순환만 남기고,
// --write-baseline은 현재 순환 전부를 새 baseline으로 저장한다.
func cmdCycles(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("cycles", stderr)
	strict := fs.Bool("strict", false, "exit 1 when cycles are found")
	format := fs.String("format", "text", "output format: text|json|sarif")
	level := fs.String("level", string(graph.LevelPackage), "view level: package|type|symbol")
	baselinePath := fs.String("baseline", "", "baseline file of known cycles")
	writeBaseline := fs.String("write-baseline", "", "write all current cycles to FILE")
	if fs.Parse(args) != nil {
		return 2
	}
	lvl, err := graph.ParseLevel(*level)
	if err != nil {
		return fail(stderr, err)
	}
	// 질의 레벨을 담을 수 있게 수확 레벨을 맞춘다 — 저장 문서를 읽을 때는 무시된다.
	opts.Level = lvl
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	if err := requireLevel(doc, lvl); err != nil {
		return fail(stderr, err)
	}
	view, err := doc.View(lvl)
	if err != nil {
		return fail(stderr, err)
	}
	cycles := analysis.Cycles(view)

	// baseline과의 비교는 보고 전에 — baselined는 strict·SARIF 어느 쪽으로도
	// 새어 나가면 안 된다.
	all := cycles
	var baselined, stale []analysis.Cycle
	if *baselinePath != "" {
		base, err := loadCyclesBaseline(*baselinePath)
		if err != nil {
			return fail(stderr, err)
		}
		cycles, baselined, stale = analysis.SplitBaseline(
			cycles, base.Cycles, analysis.CycleBaselineKey)
	}
	if *writeBaseline != "" {
		if err := saveCyclesBaseline(*writeBaseline, all); err != nil {
			return fail(stderr, err)
		}
	}
	limitations := append([]string(nil), doc.Limitations...)
	if len(stale) > 0 {
		limitations = append(limitations, fmt.Sprintf(
			"%d baseline cycles no longer occur; regenerate the baseline", len(stale)))
	}
	sort.Strings(limitations)

	switch *format {
	case "json":
		if err := emitJSON(stdout, cyclesReport{
			Cycles: cycles, Baselined: baselined,
			StaleBaseline: stale, Limitations: limitations,
		}); err != nil {
			return fail(stderr, err)
		}
	case "sarif":
		out, err := cyclesSARIF(cycles)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, string(out))
	case "text":
		for _, c := range cycles {
			fmt.Fprintf(stdout, "cycle: %v\n", c.Members)
		}
		fmt.Fprintf(stdout, "%d cycles (%d baselined)\n", len(cycles), len(baselined))
		if len(limitations) > 0 {
			fmt.Fprintln(stdout, "limitations:")
			for _, l := range limitations {
				fmt.Fprintf(stdout, "  - %s\n", l)
			}
		}
	default:
		fmt.Fprintf(stderr, "unknown format %q\n", *format)
		return 2
	}
	if *strict && len(cycles) > 0 {
		return 1
	}
	return 0
}

// deadReport는 dead 명령의 JSON 출력 형식이다.
// 루트 목록을 함께 실어 "무엇에서 도달하지 못했나"를 소비자가 스스로 판단하게 한다.
type deadReport struct {
	Algorithm     string             `json:"algorithm"`
	Roots         []string           `json:"roots"`
	UnknownRoots  []string           `json:"unknownRoots,omitempty"`
	Unreachable   []analysis.Finding `json:"unreachable"`
	Baselined     []analysis.Finding `json:"baselined,omitempty"`
	StaleBaseline []analysis.Finding `json:"staleBaseline,omitempty"`
	Limitations   []string           `json:"limitations,omitempty"`
}

// cmdDead는 보존 루트에서 도달 불가능한 심볼을 보고한다.
// 도달성은 그래프 사실이고 삭제 판정은 어디에도 없다.
// --baseline은 알려진 보고를 걸러 새 보고만 남기고,
// --write-baseline은 현재 보고 전부를 새 baseline으로 저장한다.
func cmdDead(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("dead", stderr)
	retainPublic := fs.Bool("retain-public", false,
		"retain all exported symbols — use for libraries without main")
	var extraRoots stringsFlag
	fs.Var(&extraRoots, "root", "additional retention root vertex ID (repeatable)")
	explain := fs.String("explain", "",
		"show a reachability path for vertex ID (on the RTA call graph when --algo rta)")
	format := fs.String("format", "text", "output format: text|json|sarif")
	strict := fs.Bool("strict", false, "exit 1 when unreachable symbols exist")
	algo := fs.String("algo", "cha",
		"reachability algorithm: cha (harvested graph) | rta (SSA-based, source only)")
	baselinePath := fs.String("baseline", "", "baseline file of known unreachable findings")
	writeBaseline := fs.String("write-baseline", "", "write all current findings to FILE")
	if fs.Parse(args) != nil {
		return 2
	}
	if *algo != "cha" && *algo != "rta" {
		fmt.Fprintf(stderr, "unknown algo %q: want cha|rta\n", *algo)
		return 2
	}
	// RTA는 SSA로 다시 분석한다 — 저장 문서에는 호출 정밀도의 재료가 없다.
	if *algo == "rta" && *graphPath != "" {
		fmt.Fprintln(stderr,
			"dead --algo rta requires source harvesting — it cannot run on a saved graph (--graph)")
		return 2
	}
	opts.Level = graph.LevelSymbol
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	if err := requireLevel(doc, graph.LevelSymbol); err != nil {
		return fail(stderr, err)
	}
	roots, unknown := analysis.RetentionRoots(doc, *retainPublic, extraRoots)

	if *explain != "" {
		return explainDead(doc, *explain, roots, *algo, *opts, stdout, stderr)
	}

	limitations := append([]string(nil), doc.Limitations...)
	for _, u := range unknown {
		limitations = append(limitations,
			fmt.Sprintf("retention root %q is not in the graph", u))
	}
	if len(doc.Roots) == 0 && !*retainPublic {
		limitations = append(limitations,
			"no main or init roots found; exported API may appear unreachable (use --retain-public)")
	}
	reachable := analysis.Reachable(doc, roots)
	findings := analysis.Dead(doc, reachable)
	if *algo == "rta" {
		rootSet := make(map[string]bool, len(roots))
		for _, r := range roots {
			rootSet[r] = true
		}
		rtaSet, err := source.RTAReachable(*opts, doc, rootSet)
		if err != nil {
			return fail(stderr, err)
		}
		findings = analysis.DeadRTA(doc, reachable, rtaSet)
		limitations = append(limitations,
			"rta under-approximates: methods reachable only via reflection or uninstantiated types may appear unreachable")
	}
	if *algo != "rta" && hasMethodFinding(findings) {
		limitations = append(limitations, externalDispatchLimitation(doc))
	}
	// RTA는 모든 합성 init을 루트로 삼아 이 표시와 무관하다.
	if *algo != "rta" {
		limitations = append(limitations, initializerRootsLimitation(doc, findings)...)
	}
	if hasFieldFinding(findings) {
		limitations = append(limitations,
			"field reachability counts named accesses (x.F, T{F: v}, positional literals); "+
				"reflection, serialization (encoding/json, gob), and whole-struct copies are invisible to this graph")
	}

	// baseline과의 비교는 보고 전에 — baselined는 strict·SARIF 어느 쪽으로도
	// 새어 나가면 안 된다.
	all := findings
	var baselined, stale []analysis.Finding
	if *baselinePath != "" {
		base, err := loadDeadBaseline(*baselinePath)
		if err != nil {
			return fail(stderr, err)
		}
		findings, baselined, stale = analysis.SplitBaseline(
			findings, base.Findings, analysis.FindingBaselineKey)
	}
	if *writeBaseline != "" {
		if err := saveDeadBaseline(*writeBaseline, all); err != nil {
			return fail(stderr, err)
		}
	}
	if len(stale) > 0 {
		limitations = append(limitations, fmt.Sprintf(
			"%d baseline findings no longer occur; regenerate the baseline", len(stale)))
	}
	sort.Strings(limitations)

	switch *format {
	case "json":
		if err := emitJSON(stdout, deadReport{
			Algorithm: *algo,
			Roots:     roots, UnknownRoots: unknown,
			Unreachable: findings, Baselined: baselined,
			StaleBaseline: stale, Limitations: limitations,
		}); err != nil {
			return fail(stderr, err)
		}
	case "sarif":
		out, err := deadSARIF(findings)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, string(out))
	case "text":
		for _, f := range findings {
			exported := ""
			if f.Exported {
				exported = " (exported)"
			}
			fmt.Fprintf(stdout, "unreachable %s: %s%s\n", f.Kind, f.ID, exported)
		}
		fmt.Fprintf(stdout, "%d unreachable symbols (%d baselined, %d retention roots)\n",
			len(findings), len(baselined), len(roots))
		if len(limitations) > 0 {
			fmt.Fprintln(stdout, "limitations:")
			for _, l := range limitations {
				fmt.Fprintf(stdout, "  - %s\n", l)
			}
		}
	default:
		fmt.Fprintf(stderr, "unknown format %q\n", *format)
		return 2
	}
	if *strict && len(findings) > 0 {
		return 1
	}
	return 0
}

// hasMethodFinding은 unreachable 보고에 메서드가 있는지 확인한다.
// 메서드가 하나라도 있으면 외부 인터페이스 디스패치의 blind spot을 밝힐 가치가 있다.
func hasMethodFinding(findings []analysis.Finding) bool {
	for _, f := range findings {
		if f.Kind == graph.KindMethod {
			return true
		}
	}
	return false
}

// hasFieldFinding은 unreachable 보고에 필드가 있는지 확인한다.
// 필드는 이름으로 고른 접근만 그래프에 남으므로, 보고가 나오면
// reflection·직렬화 같은 보이지 않는 접근 경로를 밝혀야 한다.
func hasFieldFinding(findings []analysis.Finding) bool {
	for _, f := range findings {
		if f.Kind == graph.KindField {
			return true
		}
	}
	return false
}

// initializerRootsLimitation은 초기화 루트 표시(initializerRoots)가 없는 문서의 CHA dead
// 보고에 붙는 재수확 권고다. "이전 문서"라고 단정하지 않고 표시가 없다는 사실만 말한다 —
// 표시 도입 전 개발 빌드가 만든 문서에는 pkg._가 일부 있을 수 있다. 보고가 없으면 조용하다.
func initializerRootsLimitation(doc *graph.Document, findings []analysis.Finding) []string {
	if len(findings) == 0 || doc.InitializerRoots {
		return nil
	}
	return []string{"this document lacks the initializerRoots marker (harvested before package-initialization " +
		"roots were complete): symbols used only by blank declarations or calling variable initializers may " +
		"appear unreachable — re-harvest"}
}

// externalDispatchLimitation은 CHA dead의 메서드 보고에 붙는 외부 디스패치
// 한계 문구를 고른다. 문구는 문서가 실제로 수확한 사실만큼만 말한다 —
// 이름 없는 인터페이스 수확 표시(AnonymousDispatch)가 없는 옛 문서에도 명명
// 인터페이스의 satisfies는 있어서, 그 존재만으로 "이름 없는 것도 셌다"고 하면 거짓이다.
// RTA는 SSA 전체 프로그램으로 외부 호출까지 보므로 이 문구가 없다.
func externalDispatchLimitation(doc *graph.Document) string {
	const invisible = "dispatch via reflection or generic interfaces is invisible to this graph"
	if doc.AnonymousDispatch {
		return "methods implementing interfaces declared outside the module (named, or anonymous " +
			"literals in dependency source such as errors' interface{ Unwrap() error }) count as " +
			"reachable while their receiver type is reachable; " + invisible
	}
	if hasSatisfies(doc) {
		return "methods implementing named interfaces declared outside the module count as reachable " +
			"while their receiver type is reachable; this document predates anonymous-interface facts " +
			"(e.g. errors' interface{ Unwrap() error }), so methods called only through them may appear " +
			"unreachable — re-harvest; " + invisible
	}
	return "no vertex carries external-dispatch facts (satisfies); methods called only through " +
		"interfaces declared outside the module may appear unreachable — re-harvest if this document predates them"
}

// hasSatisfies는 문서에 외부 디스패치 사실을 실은 정점이 하나라도 있는지 본다.
func hasSatisfies(doc *graph.Document) bool {
	for _, v := range doc.Vertices {
		if len(v.Satisfies) > 0 {
			return true
		}
	}
	return false
}

// explainDead는 한 정점이 왜 살아 있는지(또는 왜 못 찾았는지) 보여준다.
// --algo rta면 RTA 호출 그래프 위에서 설명한다 — CHA 수확 그래프의 경로를
// 보여주면 "RTA가 왜 죽였다/살렸다"의 답이 아니라 다른 알고리즘의 말이 된다.
func explainDead(doc *graph.Document, id string, roots []string, algo string,
	opts source.Options, stdout, stderr io.Writer) int {
	if algo == "rta" {
		rootSet := make(map[string]bool, len(roots))
		for _, r := range roots {
			rootSet[r] = true
		}
		adj, rtaReach, err := source.RTAAdjacency(opts, doc, rootSet)
		if err != nil {
			return fail(stderr, err)
		}
		if !doc.HasVertex(id) {
			return fail(stderr, fmt.Errorf("%w: %s", analysis.ErrNotFound, id))
		}
		adj = analysis.WithAbstractCalls(doc, adj, analysis.Reachable(doc, roots), rtaReach)
		path, found := analysis.ExplainAdjacency(adj, id, source.ExplainRoots(doc, roots))
		if !found {
			fmt.Fprintf(stdout,
				"no path from %d retention roots to %s (rta call graph)\n", len(roots), id)
			return 0
		}
		for i, p := range path {
			switch {
			case i == 0 && strings.HasSuffix(p, source.PackageInitSuffix):
				fmt.Fprintf(stdout, "root: %s (package initializer; not a graph vertex)\n", p)
			case i == 0:
				fmt.Fprintf(stdout, "root: %s\n", p)
			case strings.HasSuffix(p, source.PackageInitSuffix):
				// 문서 정점이 아니다 — 표시하지 않으면 소비자가 없는 정점을 찾는다.
				fmt.Fprintf(stdout, "  -> %s (package initializer; not a graph vertex)\n", p)
			default:
				fmt.Fprintf(stdout, "  -> %s\n", p)
			}
		}
		return 0
	}
	path, found, err := analysis.Explain(doc, id, roots)
	if err != nil {
		return fail(stderr, err)
	}
	if !found {
		fmt.Fprintf(stdout, "no path from %d retention roots to %s\n", len(roots), id)
		return 0
	}
	for i, p := range path {
		if i == 0 {
			fmt.Fprintf(stdout, "root: %s\n", p)
			continue
		}
		// 합성 걸음은 문서 간선이 없다 — 표시하지 않으면 소비자가 간선을 찾다 실패한다.
		if ifaces, ok := analysis.IsExternalDispatch(doc, path[i-1], p); ok {
			fmt.Fprintf(stdout, "  -> %s (external dispatch: %s)\n", p, strings.Join(ifaces, " | "))
		} else {
			fmt.Fprintf(stdout, "  -> %s\n", p)
		}
	}
	return 0
}

// rulesReport는 rules 명령의 JSON 출력 형식이다.
// Baselined는 baseline에 이미 있어 넘어간 위반, StaleBaseline은
// 더 이상 발생하지 않아 baseline 재생성이 필요한 항목이다.
// UnmappedExternal은 --deps로 들어온 외부 패키지 중 컴포넌트 미매핑,
// UnmatchedComponents는 어느 패키지에도 매칭되지 않은 컴포넌트다.
type rulesReport struct {
	Violations          []analysis.Violation `json:"violations"`
	Baselined           []analysis.Violation `json:"baselined,omitempty"`
	StaleBaseline       []analysis.Violation `json:"staleBaseline,omitempty"`
	Unmapped            []string             `json:"unmapped,omitempty"`
	UnmappedExternal    []string             `json:"unmappedExternal,omitempty"`
	UnmatchedComponents []string             `json:"unmatchedComponents,omitempty"`
	Limitations         []string             `json:"limitations,omitempty"`
}

// cmdRules는 .gartograph.yml의 컴포넌트 의존 규칙을 검사한다.
// --baseline은 알려진 위반을 걸러 새 위반만 남기고,
// --write-baseline은 현재 위반 전부를 새 baseline으로 저장한다.
func cmdRules(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("rules", stderr)
	configPath := fs.String("config", "", "rules file (default: .gartograph.yml in --dir)")
	format := fs.String("format", "text", "output format: text|json|sarif")
	strict := fs.Bool("strict", false, "exit 1 when violations exist")
	baselinePath := fs.String("baseline", "", "baseline file of known violations")
	writeBaseline := fs.String("write-baseline", "", "write all current violations to FILE")
	if fs.Parse(args) != nil {
		return 2
	}
	// 형식 검증은 부수 효과(--write-baseline 쓰기)보다 먼저다 —
	// 거부될 호출이 기존 baseline을 덮어쓰면 안 된다.
	switch *format {
	case "text", "json", "sarif":
	default:
		fmt.Fprintf(stderr, "unknown format %q\n", *format)
		return 2
	}
	cfgPath := *configPath
	if cfgPath == "" {
		found, ok := config.Find(opts.Dir)
		if !ok {
			fmt.Fprintf(stderr, "error: no .gartograph.yml found in %s\n", opts.Dir)
			return 2
		}
		cfgPath = found
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fail(stderr, err)
	}
	// signature 규칙은 signature 간선이 있어야 검사된다 — 패키지 레벨로
	// 수확하면 규칙이 조용히 통과하므로 수확 레벨을 올린다.
	if len(cfg.Signature) > 0 && *graphPath == "" {
		opts.Level = graph.LevelSymbol
	}
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	rep := analysis.CheckRules(doc, cfg)
	violations := rep.Violations
	unmapped := rep.Unmapped

	// baseline과의 비교는 보고 전에 한다 — baselined는 "알려진 위반"이라
	// strict·SARIF 어느 쪽으로도 새어 나가면 안 된다.
	all := violations
	var baselined, stale []analysis.Violation
	if *baselinePath != "" {
		base, err := loadBaseline(*baselinePath)
		if err != nil {
			return fail(stderr, err)
		}
		violations, baselined, stale = analysis.SplitBaseline(
			violations, base.Violations, analysis.ViolationBaselineKey)
	}
	if *writeBaseline != "" {
		// 새 baseline은 분할 전의 현재 위반 전부를 담는다.
		if err := saveBaseline(*writeBaseline, all); err != nil {
			return fail(stderr, err)
		}
	}

	limitations := append([]string(nil), doc.Limitations...)
	if len(cfg.Signature) > 0 && doc.Level.Rank() < graph.LevelSymbol.Rank() {
		limitations = append(limitations,
			"signature rules configured but document is below symbol level; signature checks skipped")
	}
	if len(unmapped) > 0 {
		limitations = append(limitations, fmt.Sprintf(
			"%d packages match no component; rules did not check them", len(unmapped)))
	}
	if len(rep.UnmappedExternal) > 0 {
		limitations = append(limitations, fmt.Sprintf(
			"%d external packages match no component; component patterns can cover them",
			len(rep.UnmappedExternal)))
	}
	if len(rep.UnmatchedComponents) > 0 {
		limitations = append(limitations, fmt.Sprintf(
			"components %v matched no packages — typo, stale, or external pattern without --deps",
			rep.UnmatchedComponents))
	}
	if rep.FileScopeUnchecked > 0 {
		limitations = append(limitations, fmt.Sprintf(
			"%d import edges to fileRules targets carry no positions; file scope could not be checked (re-harvest for positions)",
			rep.FileScopeUnchecked))
	}
	if len(stale) > 0 {
		limitations = append(limitations, fmt.Sprintf(
			"%d baseline violations no longer occur; regenerate the baseline", len(stale)))
	}
	sort.Strings(limitations)

	switch *format {
	case "json":
		if err := emitJSON(stdout, rulesReport{
			Violations: violations, Baselined: baselined, StaleBaseline: stale,
			Unmapped: unmapped, UnmappedExternal: rep.UnmappedExternal,
			UnmatchedComponents: rep.UnmatchedComponents, Limitations: limitations,
		}); err != nil {
			return fail(stderr, err)
		}
	case "sarif":
		out, err := rulesSARIF(violations)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, string(out))
	case "text":
		for _, v := range violations {
			label := v.Rule
			if v.Name != "" {
				label = v.Rule + ":" + v.Name
			}
			fmt.Fprintf(stdout, "violation[%s]: %s (%s) -> %s (%s)\n",
				label, v.From, v.FromComponent, v.To, v.ToComponent)
			if v.Position != nil {
				fmt.Fprintf(stdout, "  at: %s:%d:%d\n",
					v.Position.File, v.Position.Line, v.Position.Column)
			}
			if v.Reason != "" {
				fmt.Fprintf(stdout, "  reason: %s\n", v.Reason)
			}
			if len(v.Path) > 0 {
				fmt.Fprintf(stdout, "  path: %s\n", strings.Join(v.Path, " -> "))
			}
		}
		fmt.Fprintf(stdout, "%d violations (%d baselined, %d packages unmapped, %d external unmapped)\n",
			len(violations), len(baselined), len(unmapped), len(rep.UnmappedExternal))
		if len(limitations) > 0 {
			fmt.Fprintln(stdout, "limitations:")
			for _, l := range limitations {
				fmt.Fprintf(stdout, "  - %s\n", l)
			}
		}
	default:
		fmt.Fprintf(stderr, "unknown format %q\n", *format)
		return 2
	}
	if *strict && len(violations) > 0 {
		return 1
	}
	return 0
}

// cmdQuery는 정점 하나에 대해 이웃을 되묻는다.
// JSON은 에이전트 소비용이다 — 잘렸으면 truncated, 깊이는 depth를 싣는다.
func cmdQuery(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("query", stderr)
	depth := fs.Int("depth", 1, "neighbor depth")
	maxN := fs.Int("max", 0, "max neighbors per direction (0 = unlimited)")
	level := idLevelFlag(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: gartograph query <vertex-id> [--depth N]")
		return 2
	}
	doc, err := loadIDDoc(opts, *graphPath, *level)
	if err != nil {
		return fail(stderr, err)
	}
	res, err := analysis.Query(doc, positional[0], *depth, *maxN)
	if err != nil {
		return fail(stderr, levelHint(doc, *graphPath, err))
	}
	sortNeighborsJSON(res)
	if err := emitJSON(stdout, res); err != nil {
		return fail(stderr, err)
	}
	return 0
}

// cmdImpact는 정점의 역방향 전이 클로저를 본다 — 이걸 바꾸면 무엇이 깨지는가.
// query가 양방향 1~N홉 이웃을 보는 것과 달리 의존자 방향만, 거리를 싣고 모은다.
// --since/--files가 있으면 파일 집합 모드다 — 바뀐 파일에 선언된 정점들이
// 루트가 되고, positional 정점 ID도 함께 줄 수 있다.
func cmdImpact(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("impact", stderr)
	depth := fs.Int("depth", 0, "max reverse-dependency depth (0 = full transitive closure)")
	maxN := fs.Int("max", 0, "max dependers to report (0 = unlimited)")
	since := fs.String("since", "", "git revision to diff for changed files (e.g. HEAD~1, origin/main...HEAD)")
	var files stringsFlag
	fs.Var(&files, "files", "changed file path relative to --dir (repeatable)")
	level := idLevelFlag(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) > 1 {
		fmt.Fprintln(stderr,
			"usage: gartograph impact [<vertex-id>] [--since REV | --files F...] [--depth N]")
		return 2
	}
	fileMode := *since != "" || len(files) > 0
	if len(positional) == 0 && !fileMode {
		fmt.Fprintln(stderr,
			"usage: gartograph impact [<vertex-id>] [--since REV | --files F...] [--depth N]")
		return 2
	}
	// 파일→정점 해석은 심볼 위치가 있어야 정확하다 — 새로 수확할 때는
	// --level과 상관없이 가장 세밀한 레벨을 고른다. 저장 문서(--graph)는
	// 있는 레벨 그대로 쓴다.
	if fileMode {
		// 덮어쓰기 전에 검증한다 — 오타가 파일 모드에서만 조용히 통과하면
		// ID 모드와 같은 플래그의 계약이 갈린다.
		if _, err := graph.ParseLevel(*level); err != nil {
			return fail(stderr, err)
		}
		*level = string(graph.LevelSymbol)
	}
	doc, err := loadIDDoc(opts, *graphPath, *level)
	if err != nil {
		return fail(stderr, err)
	}
	if fileMode {
		changed := append([]string(nil), files...)
		if *since != "" {
			gitFiles, err := changedFilesSince(opts.Dir, *since)
			if err != nil {
				return fail(stderr, err)
			}
			changed = append(changed, gitFiles...)
		}
		res, err := analysis.AffectedByFiles(doc, changed, positional, *depth, *maxN)
		if err != nil {
			return fail(stderr, levelHint(doc, *graphPath, err))
		}
		if err := emitJSON(stdout, res); err != nil {
			return fail(stderr, err)
		}
		return 0
	}
	res, err := analysis.FindImpact(doc, positional[0], *depth, *maxN)
	if err != nil {
		return fail(stderr, levelHint(doc, *graphPath, err))
	}
	if err := emitJSON(stdout, res); err != nil {
		return fail(stderr, err)
	}
	return 0
}

// cmdPath는 두 정점 사이의 최단 의존 경로를 찾는다.
// "왜 A가 B를 아는가"에 답하는 명령 — 경로가 없으면 found:false가
// 그래프 사실로 돌아가고, 정점이 없으면 사용법이 아닌 분석 오류(2)다.
func cmdPath(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("path", stderr)
	level := idLevelFlag(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 2 {
		fmt.Fprintln(stderr, "usage: gartograph path <from-id> <to-id>")
		return 2
	}
	doc, err := loadIDDoc(opts, *graphPath, *level)
	if err != nil {
		return fail(stderr, err)
	}
	res, err := analysis.Path(doc, positional[0], positional[1])
	if err != nil {
		return fail(stderr, levelHint(doc, *graphPath, err))
	}
	if err := emitJSON(stdout, res); err != nil {
		return fail(stderr, err)
	}
	return 0
}

// cmdShared는 여러 루트의 공통 도달 집합을 보고한다.
// "이 두 진입점이 같이 끌어오는 것" — 공유 부품의 경계를 보는 질의다.
// 출력은 JSON만이다 — 집합 목록은 에이전트 소비가 상정이다.
func cmdShared(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("shared", stderr)
	level := idLevelFlag(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) < 2 {
		fmt.Fprintln(stderr, "usage: gartograph shared <id> <id> [more ids...]")
		return 2
	}
	doc, err := loadIDDoc(opts, *graphPath, *level)
	if err != nil {
		return fail(stderr, err)
	}
	res, err := analysis.Shared(doc, positional)
	if err != nil {
		return fail(stderr, levelHint(doc, *graphPath, err))
	}
	if err := emitJSON(stdout, res); err != nil {
		return fail(stderr, err)
	}
	return 0
}

// cmdDiff는 두 저장 문서의 구조 차이를 보고한다.
// 수확 없이 파일만 비교하므로 수확 플래그를 받지 않는다.
// --strict는 호환성 위험 신호(Breaking — exported 정점 제거,
// 공개 시그니처의 타입 참조 제거)가 있을 때 1을 돌려준다.
func cmdDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "output format: text|json")
	strict := fs.Bool("strict", false, "exit 1 on breaking changes")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 2 {
		fmt.Fprintln(stderr, "usage: gartograph diff <old.json> <new.json> [--strict]")
		return 2
	}
	oldDoc, err := export.LoadFile(positional[0])
	if err != nil {
		return fail(stderr, err)
	}
	newDoc, err := export.LoadFile(positional[1])
	if err != nil {
		return fail(stderr, err)
	}
	diff := analysis.DiffDocuments(oldDoc, newDoc)
	switch *format {
	case "json":
		if err := emitJSON(stdout, diff); err != nil {
			return fail(stderr, err)
		}
	case "text":
		if err := printDiffText(diff, stdout); err != nil {
			return fail(stderr, err)
		}
	default:
		fmt.Fprintf(stderr, "unknown format %q\n", *format)
		return 2
	}
	if *strict && len(diff.Breaking) > 0 {
		return 1
	}
	return 0
}

// printDiffText는 diff를 유니파이드 스타일의 한 줄 레코드로 출력한다.
func printDiffText(d *analysis.Diff, w io.Writer) error {
	for _, v := range d.AddedVertices {
		fmt.Fprintf(w, "+ vertex %s\n", v)
	}
	for _, v := range d.RemovedVertices {
		fmt.Fprintf(w, "- vertex %s\n", v)
	}
	for _, e := range d.AddedEdges {
		fmt.Fprintf(w, "+ edge %s -> %s (%s)\n", e.From, e.To, e.Kind)
	}
	for _, e := range d.RemovedEdges {
		fmt.Fprintf(w, "- edge %s -> %s (%s)\n", e.From, e.To, e.Kind)
	}
	for _, c := range d.ChangedVertices {
		fmt.Fprintf(w, "~ vertex %s: %s %s -> %s\n", c.ID, c.Field, c.From, c.To)
	}
	for _, s := range d.SignatureChanges {
		fmt.Fprintf(w, "~ signature %s: +%v -%v\n", s.ID, s.Added, s.Removed)
	}
	for _, b := range d.Breaking {
		fmt.Fprintf(w, "breaking: %s\n", b)
	}
	for _, n := range d.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	for _, l := range d.OldLimitations {
		fmt.Fprintf(w, "limitation(old): %s\n", l)
	}
	for _, l := range d.NewLimitations {
		fmt.Fprintf(w, "limitation(new): %s\n", l)
	}
	_, err := fmt.Fprintf(w,
		"diff: +%d/-%d vertices, +%d/-%d edges, %d signature changes, %d breaking\n",
		len(d.AddedVertices), len(d.RemovedVertices),
		len(d.AddedEdges), len(d.RemovedEdges),
		len(d.SignatureChanges), len(d.Breaking))
	return err
}

// emitJSON은 분석 결과를 JSON으로 쓴다. 출력 실패는 분석 성공과 다른
// 사실이므로 에러를 돌려준다 — 잘린 리포트가 성공(0)으로 끝나면 안 된다.
func emitJSON(w io.Writer, v any) error {
	out, err := marshalReport(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(out))
	return err
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

// cmdMetrics는 컴포넌트(설정 없으면 패키지) 단위의 결합도를 보고한다.
// --strict는 없다 — Ca/Ce/A/D는 판정이 아니라 사실이기 때문이다.
// 타입 레벨로 수확해야 abstractness의 분모(인터페이스 비율)가 있다 —
// 저장 문서(--graph)가 패키지 레벨이면 A/D는 빠지고 limitation으로 남는다.
func cmdMetrics(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("metrics", stderr)
	configPath := fs.String("config", "", "rules file (default: .gartograph.yml in --dir)")
	format := fs.String("format", "text", "output format: text|json")
	if fs.Parse(args) != nil {
		return 2
	}
	opts.Level = graph.LevelType
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	if doc.Level.Rank() < graph.LevelPackage.Rank() {
		fmt.Fprintf(stderr,
			"error: metrics needs package-level data; document is %s level\n", doc.Level)
		return 2
	}
	cfg, limitations, err := optionalConfig(opts.Dir, *configPath)
	if err != nil {
		return fail(stderr, err)
	}
	rep := analysis.Metrics(doc, cfg)
	limitations = append(limitations, doc.Limitations...)
	if doc.Level.Rank() < graph.LevelType.Rank() {
		limitations = append(limitations,
			"abstractness/distance omitted: document has no type vertices (harvest with --level type or finer)")
	}
	sort.Strings(limitations)
	switch *format {
	case "json":
		type metricsJSON struct {
			*analysis.MetricsReport
			Limitations []string `json:"limitations,omitempty"`
		}
		if err := emitJSON(stdout, metricsJSON{rep, limitations}); err != nil {
			return fail(stderr, err)
		}
	case "text":
		for _, m := range rep.Components {
			inst := "n/a"
			if m.Instability != nil {
				inst = fmt.Sprintf("%.3g", *m.Instability)
			}
			abs, dist := "n/a", "n/a"
			if m.Abstractness != nil {
				abs = fmt.Sprintf("%.3g", *m.Abstractness)
			}
			if m.Distance != nil {
				dist = fmt.Sprintf("%.3g", *m.Distance)
			}
			fmt.Fprintf(stdout, "%s: %d pkgs, Ca=%d Ce=%d I=%s A=%s D=%s\n",
				m.Name, len(m.Packages), m.Afferent, m.Efferent, inst, abs, dist)
		}
		for _, o := range rep.Orphans {
			fmt.Fprintf(stdout, "orphan: %s\n", o)
		}
		for _, u := range rep.Unmapped {
			fmt.Fprintf(stdout, "unmapped: %s\n", u)
		}
		for _, l := range limitations {
			fmt.Fprintf(stdout, "limitation: %s\n", l)
		}
	default:
		fmt.Fprintf(stderr, "unknown format %q\n", *format)
		return 2
	}
	return 0
}

// optionalConfig는 규칙 파일을 있으면 읽고 없으면 nil을 돌려준다.
// metrics처럼 설정이 선택인 명령 전용 — rules는 설정이 없으면 오류다.
// 파일이 존재하는데 깨졌으면 에러다 — 고장난 설정을 무시하고 패키지 단위로
// 조용히 내려가면 소비자가 컴포넌트 메트릭인 줄 알고 잘못 읽는다.
func optionalConfig(dir, explicit string) (*config.File, []string, error) {
	path := explicit
	if path == "" {
		found, ok := config.Find(dir)
		if !ok {
			return nil, []string{
				"no .gartograph.yml found; metrics computed per package"}, nil
		}
		path = found
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	return cfg, nil, nil
}

// cmdMapping은 규칙 파일이 각 패키지를 어느 컴포넌트로 해석하는지 보여준다.
// "규칙이 왜 이 의존을 못 잡지"의 첫 진단 도구다.
func cmdMapping(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("mapping", stderr)
	configPath := fs.String("config", "", "rules file (default: .gartograph.yml in --dir)")
	format := fs.String("format", "text", "output format: text|json")
	if fs.Parse(args) != nil {
		return 2
	}
	cfgPath := *configPath
	if cfgPath == "" {
		found, ok := config.Find(opts.Dir)
		if !ok {
			fmt.Fprintf(stderr, "error: no .gartograph.yml found in %s\n", opts.Dir)
			return 2
		}
		cfgPath = found
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fail(stderr, err)
	}
	opts.Level = graph.LevelPackage
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	if doc.Level.Rank() < graph.LevelPackage.Rank() {
		fmt.Fprintf(stderr,
			"error: mapping needs package-level data; document is %s level\n", doc.Level)
		return 2
	}
	m := analysis.MapComponents(doc, cfg)
	switch *format {
	case "json":
		type mappingJSON struct {
			*analysis.Mapping
			Limitations []string `json:"limitations,omitempty"`
		}
		if err := emitJSON(stdout, mappingJSON{m, doc.Limitations}); err != nil {
			return fail(stderr, err)
		}
	case "text":
		names := make([]string, 0, len(m.Components))
		for n := range m.Components {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(stdout, "%s:\n", n)
			for _, p := range m.Components[n] {
				fmt.Fprintf(stdout, "  %s\n", p)
			}
		}
		for _, u := range m.Unmapped {
			fmt.Fprintf(stdout, "unmapped: %s\n", u)
		}
		for _, u := range m.UnmappedExternal {
			fmt.Fprintf(stdout, "unmapped external: %s\n", u)
		}
		for _, u := range m.UnmatchedComponents {
			fmt.Fprintf(stdout, "unmatched component: %s\n", u)
		}
		for _, l := range doc.Limitations {
			fmt.Fprintf(stdout, "limitation: %s\n", l)
		}
	default:
		fmt.Fprintf(stderr, "unknown format %q\n", *format)
		return 2
	}
	return 0
}

// cmdInit은 관찰된 import를 그대로 허용 목록으로 삼는 초기 규칙 파일을 만든다.
// 첫 검사부터 통과하는 설정이어야 기존 레포 도입이 성립한다 — 조이는 것은
// 사용자가 점진적으로 한다. 이미 파일이 있으면 덮어쓰지 않는다.
func cmdInit(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("init", stderr)
	if fs.Parse(args) != nil {
		return 2
	}
	if _, ok := config.Find(opts.Dir); ok {
		fmt.Fprintf(stderr,
			"error: .gartograph.yml already exists in %s — remove it to regenerate\n",
			opts.Dir)
		return 2
	}
	opts.Level = graph.LevelPackage
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	cfg := analysis.ScaffoldConfig(doc)
	if len(cfg.Components) == 0 {
		fmt.Fprintf(stderr,
			"error: no internal packages found in %s — nothing to scaffold\n", opts.Dir)
		return 2
	}
	data, err := config.Render(cfg)
	if err != nil {
		return fail(stderr, err)
	}
	path := filepath.Join(opts.Dir, ".gartograph.yml")
	// O_EXCL로 만든다 — 존재 확인과 쓰기 사이에 다른 프로세스가 파일을
	// 만들어도 덮어쓰지 않는다.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fail(stderr, fmt.Errorf("writing %s: %w", path, err))
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fail(stderr, fmt.Errorf("writing %s: %w", path, err))
	}
	if err := f.Close(); err != nil {
		return fail(stderr, fmt.Errorf("closing %s: %w", path, err))
	}
	fmt.Fprintf(stdout, "wrote %s (%d components)\n", path, len(cfg.Components))
	return 0
}

// cmdUnusedDeps는 go.mod의 require 중 어느 패키지도 import하지 않는
// 모듈을 보고한다 — `go mod tidy`가 지울 대상을 읽기 전용으로 미리 본다.
// --graph는 없다 — require 목록은 그래프가 아니라 go.mod에서 온다.
// --strict는 미사용 require가 있을 때 1을 돌려준다.
func cmdUnusedDeps(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("unused-deps", stderr)
	format := fs.String("format", "text", "output format: text|json")
	strict := fs.Bool("strict", false, "exit 1 when unused requires exist")
	if fs.Parse(args) != nil {
		return 2
	}
	if *graphPath != "" {
		fmt.Fprintln(stderr,
			"--graph is meaningless for unused-deps; requires live in go.mod, not the graph")
		return 2
	}
	rep, err := source.UnusedRequires(*opts)
	if err != nil {
		return fail(stderr, err)
	}
	switch *format {
	case "json":
		if err := emitJSON(stdout, rep); err != nil {
			return fail(stderr, err)
		}
	case "text":
		for _, m := range rep.Unused {
			fmt.Fprintf(stdout, "unused require: %s\n", m)
		}
		for _, m := range rep.UnusedIndirect {
			fmt.Fprintf(stdout, "unused indirect require: %s\n", m)
		}
		fmt.Fprintf(stdout, "%d unused requires (%d indirect, %d used)\n",
			len(rep.Unused), len(rep.UnusedIndirect), rep.UsedRequires)
	default:
		fmt.Fprintf(stderr, "unknown format %q\n", *format)
		return 2
	}
	if *strict && len(rep.Unused) > 0 {
		return 1
	}
	return 0
}

// cmdBridges는 isthmus bridge-facts v1 문서를 낸다.
// go 문서는 v1에서 사실을 담지 않는다 — cgo 관측은 unscanned-ffi-interop
// limitation으로만 신고하는 게 계약이다(isthmus docs/GRAPH-EXCHANGE.md).
// 수확 파이프라인을 타지 않고 소스 파일을 직접 스캔하므로 수확 플래그는
// 받지 않는다.
func cmdBridges(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bridges", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", ".", "module root to scan")
	out := fs.String("out", "", "write the document to FILE instead of stdout")
	if fs.Parse(args) != nil {
		return 2
	}
	doc, err := source.BridgeFacts(*dir, Version)
	if err != nil {
		return fail(stderr, err)
	}
	data, err := marshalReport(doc)
	if err != nil {
		return fail(stderr, err)
	}
	if *out != "" {
		if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
			return fail(stderr, fmt.Errorf("writing %s: %w", *out, err))
		}
		return 0
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// cmdSchema는 isthmus persistence 도메인의 go 문서를 낸다 —
// 코드가 SQL 관계를 이름으로 참조하는 경계를 relation-use 사실로 수확한다.
func cmdSchema(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", ".", "module root to scan")
	out := fs.String("out", "", "write the document to FILE instead of stdout")
	if fs.Parse(args) != nil {
		return 2
	}
	doc, err := source.SchemaFacts(*dir, Version)
	if err != nil {
		return fail(stderr, err)
	}
	data, err := marshalReport(doc)
	if err != nil {
		return fail(stderr, err)
	}
	if *out != "" {
		if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
			return fail(stderr, fmt.Errorf("writing %s: %w", *out, err))
		}
		return 0
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}
