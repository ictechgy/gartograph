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
	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/export"
	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/source"
)

// Version은 CLI가 스스로를 보고하는 버전 문자열이다. 릴리스는
// -ldflags "-X .../cli.Version=<태그>"로 이 값을 덮어쓴다.
var Version = "0.2.0"

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
	case "diff":
		return cmdDiff(args[1:], stdout, stderr)
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
  gartograph cycles [--level package|type|symbol] [--strict] [--format text|json] [flags]
  gartograph dead   [--retain-public] [--root ID]... [--explain ID] [--strict] [flags]
  gartograph rules  [--config FILE] [--strict] [--format text|json|sarif] [flags]
  gartograph query  <id> [--depth N] [--max N] [flags]
  gartograph impact <id> [--depth N] [--max N] [flags]
  gartograph impact --since <git-rev>|--files F... [--depth N] [flags]
  gartograph path   <from-id> <to-id> [flags]
  gartograph diff   <old.json> <new.json> [--strict] [--format text|json]
  gartograph mcp    serve the graph over MCP stdio [flags]
  gartograph version

Harvest flags (graph, cycles, dead, rules, query):
  --dir PATH    module root to analyze (default ".")
  --pattern P   package pattern, repeatable (default "./...")
  --tests       include test variant packages
  --deps        include dependencies outside the main module
  --graph FILE  read a saved graph document instead of harvesting`)
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
func loadDoc(opts *source.Options, graphPath string) (*graph.Document, error) {
	if graphPath != "" {
		return export.LoadFile(graphPath)
	}
	return source.Load(*opts)
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
	doc, err := source.Load(*opts)
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

// cmdCycles는 순환 의존성을 찾는다.
// --level은 문서를 어느 레벨로 투영할지 고른다 — 패키지 순환은 컴파일러가
// 막으므로 실전 검사는 type·symbol 레벨이다.
// --strict가 켜지면 순환이 있을 때 1을 돌려준다.
func cmdCycles(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("cycles", stderr)
	strict := fs.Bool("strict", false, "exit 1 when cycles are found")
	format := fs.String("format", "text", "output format: text|json")
	level := fs.String("level", string(graph.LevelPackage), "view level: package|type|symbol")
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
	switch *format {
	case "json":
		out, _ := json.MarshalIndent(cycles, "", "  ")
		fmt.Fprintln(stdout, string(out))
	case "text":
		for _, c := range cycles {
			fmt.Fprintf(stdout, "cycle: %v\n", c.Members)
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
	Roots        []string           `json:"roots"`
	UnknownRoots []string           `json:"unknownRoots,omitempty"`
	Unreachable  []analysis.Finding `json:"unreachable"`
	Limitations  []string           `json:"limitations,omitempty"`
}

// cmdDead는 보존 루트에서 도달 불가능한 심볼을 보고한다.
// 도달성은 그래프 사실이고 삭제 판정은 어디에도 없다.
func cmdDead(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("dead", stderr)
	retainPublic := fs.Bool("retain-public", false,
		"retain all exported symbols — use for libraries without main")
	var extraRoots stringsFlag
	fs.Var(&extraRoots, "root", "additional retention root vertex ID (repeatable)")
	explain := fs.String("explain", "", "show a reachability path for vertex ID")
	format := fs.String("format", "text", "output format: text|json")
	strict := fs.Bool("strict", false, "exit 1 when unreachable symbols exist")
	if fs.Parse(args) != nil {
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
		return explainDead(doc, *explain, roots, stdout, stderr)
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
	if hasMethodFinding(findings) {
		limitations = append(limitations,
			"methods may satisfy interfaces declared outside the module; "+
				"dynamic dispatch from external packages is invisible to this graph")
	}
	sort.Strings(limitations)

	switch *format {
	case "json":
		out, _ := json.MarshalIndent(deadReport{
			Roots: roots, UnknownRoots: unknown,
			Unreachable: findings, Limitations: limitations,
		}, "", "  ")
		fmt.Fprintln(stdout, string(out))
	case "text":
		for _, f := range findings {
			fmt.Fprintf(stdout, "unreachable %s: %s\n", f.Kind, f.ID)
		}
		fmt.Fprintf(stdout, "%d unreachable symbols (%d retention roots)\n",
			len(findings), len(roots))
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

// explainDead는 한 정점이 왜 살아 있는지(또는 왜 못 찾았는지) 보여준다.
func explainDead(doc *graph.Document, id string, roots []string,
	stdout, stderr io.Writer) int {
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
		} else {
			fmt.Fprintf(stdout, "  -> %s\n", p)
		}
	}
	return 0
}

// rulesReport는 rules 명령의 JSON 출력 형식이다.
type rulesReport struct {
	Violations  []analysis.Violation `json:"violations"`
	Unmapped    []string             `json:"unmapped,omitempty"`
	Limitations []string             `json:"limitations,omitempty"`
}

// cmdRules는 .gartograph.yml의 컴포넌트 의존 규칙을 검사한다.
func cmdRules(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("rules", stderr)
	configPath := fs.String("config", "", "rules file (default: .gartograph.yml in --dir)")
	format := fs.String("format", "text", "output format: text|json|sarif")
	strict := fs.Bool("strict", false, "exit 1 when violations exist")
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
	// signature 규칙은 signature 간선이 있어야 검사된다 — 패키지 레벨로
	// 수확하면 규칙이 조용히 통과하므로 수확 레벨을 올린다.
	if len(cfg.Signature) > 0 && *graphPath == "" {
		opts.Level = graph.LevelSymbol
	}
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	violations, unmapped := analysis.CheckRules(doc, cfg)

	limitations := append([]string(nil), doc.Limitations...)
	if len(cfg.Signature) > 0 && doc.Level.Rank() < graph.LevelSymbol.Rank() {
		limitations = append(limitations,
			"signature rules configured but document is below symbol level; signature checks skipped")
	}
	if len(unmapped) > 0 {
		limitations = append(limitations, fmt.Sprintf(
			"%d packages match no component; rules did not check them", len(unmapped)))
	}
	sort.Strings(limitations)

	switch *format {
	case "json":
		out, _ := json.MarshalIndent(rulesReport{
			Violations: violations, Unmapped: unmapped, Limitations: limitations,
		}, "", "  ")
		fmt.Fprintln(stdout, string(out))
	case "sarif":
		out, err := rulesSARIF(violations)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, string(out))
	case "text":
		for _, v := range violations {
			fmt.Fprintf(stdout, "violation[%s]: %s (%s) -> %s (%s)\n",
				v.Rule, v.From, v.FromComponent, v.To, v.ToComponent)
		}
		fmt.Fprintf(stdout, "%d violations (%d packages unmapped)\n",
			len(violations), len(unmapped))
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
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: gartograph query <vertex-id> [--depth N]")
		return 2
	}
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	res, err := analysis.Query(doc, positional[0], *depth, *maxN)
	if err != nil {
		return fail(stderr, err)
	}
	sortNeighborsJSON(res)
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Fprintln(stdout, string(out))
	return 0
}

// cmdImpact는 정점의 역방향 전이 클로저를 본다 — 이걸 바꾸면 무엇이 깨지는가.
// query가 양방향 1~N홉 이웃을 보는 것과 달리 의존자 방향만, 거리를 싣고 모은다.
func cmdImpact(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("impact", stderr)
	depth := fs.Int("depth", 0, "max reverse-dependency depth (0 = full transitive closure)")
	maxN := fs.Int("max", 0, "max dependers to report (0 = unlimited)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: gartograph impact <vertex-id> [--depth N]")
		return 2
	}
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	res, err := analysis.FindImpact(doc, positional[0], *depth, *maxN)
	if err != nil {
		return fail(stderr, err)
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Fprintln(stdout, string(out))
	return 0
}

// cmdPath는 두 정점 사이의 최단 의존 경로를 찾는다.
// "왜 A가 B를 아는가"에 답하는 명령 — 경로가 없으면 found:false가
// 그래프 사실로 돌아가고, 정점이 없으면 사용법이 아닌 분석 오류(2)다.
func cmdPath(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("path", stderr)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 2 {
		fmt.Fprintln(stderr, "usage: gartograph path <from-id> <to-id>")
		return 2
	}
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	res, err := analysis.Path(doc, positional[0], positional[1])
	if err != nil {
		return fail(stderr, err)
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Fprintln(stdout, string(out))
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
		out, _ := json.MarshalIndent(diff, "", "  ")
		fmt.Fprintln(stdout, string(out))
	case "text":
		printDiffText(diff, stdout)
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
func printDiffText(d *analysis.Diff, w io.Writer) {
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
	fmt.Fprintf(w, "diff: +%d/-%d vertices, +%d/-%d edges, %d signature changes, %d breaking\n",
		len(d.AddedVertices), len(d.RemovedVertices),
		len(d.AddedEdges), len(d.RemovedEdges),
		len(d.SignatureChanges), len(d.Breaking))
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
