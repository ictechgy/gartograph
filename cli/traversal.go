// reach·impact --format language-traversal — isthmus trace가 읽는 다중 root 순회 문서.
//
// 계약은 ../isthmus의 docs/LANGUAGE-TRAVERSAL.md가 정본이다. 순회의 의미론은
// analysis.Traverse에 있고, 여기서는 입력 검증·문서 조립·종료 코드만 다룬다.
// 종료 코드: 0 정상, 2 수확·그래프 문서(--graph) 오류, 64 사용법 오류(표준 출력 비움) 또는
// root-not-found(문서를 쓰고 64) — 계열(tsograph·pythograph·cartograph)과 같은 규칙이다.
// --roots-from을 읽지 못하거나 형식이 틀린 경우도 root 인자의 오류라 64다(pythograph와 같다).
package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ictechgy/gartograph/analysis"
	"github.com/ictechgy/gartograph/export"
	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/source"
)

// exitUsage는 순회 명령의 사용법 오류 종료 코드다(sysexits EX_USAGE, 계열 공통).
const exitUsage = 64

// formatTraversal은 순회 문서 형식 이름이다.
const formatTraversal = "language-traversal"

// maxRootsInput은 --roots-from 입력의 최대 바이트 수다.
const maxRootsInput = 16 << 20

// maxRevisionLength는 --revision 최대 길이다.
const maxRevisionLength = 256

// stdinReader는 `--roots-from -`의 입력이다 — 테스트가 바꿔 끼운다.
var stdinReader io.Reader = os.Stdin

// forbiddenChars는 id·revision에 올 수 없는 문자다(제어 문자, U+2028/2029) — isthmus
// 검증과 같은 집합이다. 줄바꿈이 섞인 id는 로그·문서에서 다른 id로 읽힐 수 있다.
var forbiddenChars = regexp.MustCompile("[\u0000-\u001f\u007f-\u009f  ]")

// objectIDPattern은 git 커밋 id 형식(SHA-1 40자, SHA-256 64자)이다.
var objectIDPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// usageError는 사용법 오류다 — 표준 출력을 비우고 64로 끝낸다.
type usageError struct{ msg string }

// Error는 error 계약이다.
func (e *usageError) Error() string { return e.msg }

// traversalFlags는 순회 문서 전용 플래그다.
type traversalFlags struct {
	rootsFrom   string
	revision    string
	generatedAt string
	revisionSet bool
}

// registerTraversalFlags는 순회 문서 전용 플래그를 등록한다.
func registerTraversalFlags(fs *flag.FlagSet) *traversalFlags {
	tf := &traversalFlags{}
	fs.StringVar(&tf.rootsFrom, "roots-from", "",
		"more root ids from a JSON string array or a bridge-facts document (- is stdin)")
	fs.Func("revision", "source revision to record (default: git HEAD when the work tree is clean)",
		func(v string) error {
			tf.revision, tf.revisionSet = v, true
			return nil
		})
	fs.StringVar(&tf.generatedAt, "generated-at", "", "fixed generatedAt timestamp (RFC 3339, UTC)")
	return tf
}

// traversalRun은 한 번의 순회 명령 입력이다.
type traversalRun struct {
	direction  string
	positional []string
	opts       *source.Options
	graphPath  string
	depth      int
	maxReached int
	flags      *traversalFlags
}

// cmdReach는 root가 기대는 심볼(dependencies)을 language-traversal 문서로 낸다 —
// trace의 forward 분석(핸들러에서 relation-use를 감싼 심볼까지)이다.
func cmdReach(args []string, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("reach", stderr)
	depth := fs.Int("depth", 0, "max depth 1-128 (0 = 128)")
	maxN := fs.Int("max", 0, "max reached symbols 1-100000 (0 = 100000)")
	format := fs.String("format", formatTraversal, "output format: language-traversal")
	tf := registerTraversalFlags(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if *format != formatTraversal {
		fmt.Fprintln(stderr, "error: reach writes only --format language-traversal")
		return exitUsage
	}
	return runTraversal(traversalRun{direction: analysis.DirectionDependencies, positional: positional,
		opts: opts, graphPath: *graphPath, depth: *depth, maxReached: *maxN, flags: tf}, stdout, stderr)
}

// runTraversal은 입력을 검증하고 순회 문서를 쓴다.
func runTraversal(in traversalRun, stdout, stderr io.Writer) int {
	req, err := traversalRequest(in)
	if err != nil {
		return reportTraversalError(stderr, err)
	}
	generatedAt, err := parseGeneratedAt(in.flags.generatedAt)
	if err != nil {
		return reportTraversalError(stderr, err)
	}
	in.opts.Level = graph.LevelSymbol
	doc, err := loadDoc(in.opts, in.graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	if err := requireLevel(doc, graph.LevelSymbol); err != nil {
		return fail(stderr, err)
	}
	out, missing, err := buildTraversalDocument(in, doc, req, generatedAt)
	if err != nil {
		return fail(stderr, err)
	}
	data, err := marshalReport(out)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, string(data))
	if missing > 0 {
		fmt.Fprintf(stderr, "error: %d root id(s) are not graph vertices (root-not-found); "+
			"the document lists them without symbol\n", missing)
		return exitUsage
	}
	return 0
}

// reportTraversalError는 사용법 오류를 64로, 나머지를 2로 알린다.
func reportTraversalError(stderr io.Writer, err error) int {
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintln(stderr, "error:", ue.msg)
		return exitUsage
	}
	return fail(stderr, err)
}

// traversalRequest는 root·깊이·상한·revision을 검증해 순회 요청을 만든다.
func traversalRequest(in traversalRun) (analysis.TraversalRequest, error) {
	roots, err := collectRoots(in.positional, in.flags.rootsFrom)
	if err != nil {
		return analysis.TraversalRequest{}, err
	}
	depth, err := boundedFlag("--depth", in.depth, analysis.MaxTraversalDepth)
	if err != nil {
		return analysis.TraversalRequest{}, err
	}
	maxReached, err := boundedFlag("--max", in.maxReached, analysis.MaxTraversalReached)
	if err != nil {
		return analysis.TraversalRequest{}, err
	}
	if in.flags.revisionSet && (in.flags.revision == "" || len(in.flags.revision) > maxRevisionLength ||
		!utf8.ValidString(in.flags.revision) || forbiddenChars.MatchString(in.flags.revision)) {
		return analysis.TraversalRequest{}, &usageError{fmt.Sprintf(
			"--revision must be 1-%d bytes of UTF-8 without control characters", maxRevisionLength)}
	}
	return analysis.TraversalRequest{Roots: roots, Direction: in.direction,
		MaxDepth: depth, MaxReached: maxReached}, nil
}

// boundedFlag는 0(기본=상한) 또는 1~limit 정수를 받는다.
func boundedFlag(name string, value, limit int) (int, error) {
	if value == 0 {
		return limit, nil
	}
	if value < 0 || value > limit {
		return 0, &usageError{fmt.Sprintf("%s takes an integer from 1 to %d (0 means %d)", name, limit, limit)}
	}
	return value, nil
}

// collectRoots는 위치 인자 다음에 --roots-from의 id를 잇고, 처음 나온 순서로 중복을
// 지운다 — 그 순서가 reached[].roots 인덱스의 뜻이다.
func collectRoots(positional []string, rootsFrom string) ([]string, error) {
	ids := append([]string(nil), positional...)
	if rootsFrom != "" {
		more, err := readRootsFrom(rootsFrom)
		if err != nil {
			return nil, err
		}
		ids = append(ids, more...)
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if id == "" || !utf8.ValidString(id) || forbiddenChars.MatchString(id) {
			return nil, &usageError{"root ids must be non-empty UTF-8 without control characters"}
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, &usageError{"give at least one vertex id as an argument or through --roots-from"}
	}
	if len(out) > analysis.MaxTraversalRoots {
		return nil, &usageError{fmt.Sprintf(
			"at most %d roots are allowed per run; split the roots into several runs", analysis.MaxTraversalRoots)}
	}
	return out, nil
}

// readRootsFrom은 JSON 문자열 배열 또는 bridge-facts 문서(facts[].symbol.usr)를 읽는다 —
// isthmus capture의 roots-from 파일과 schema 문서를 그대로 받는다.
func readRootsFrom(path string) ([]string, error) {
	var r io.Reader = stdinReader
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, &usageError{fmt.Sprintf("--roots-from could not be read: %v; pass a readable file or - for stdin", err)}
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxRootsInput+1))
	if err != nil {
		return nil, &usageError{fmt.Sprintf("--roots-from could not be read: %v", err)}
	}
	if len(data) > maxRootsInput {
		return nil, &usageError{"--roots-from is larger than 16 MiB; split the roots into several runs"}
	}
	return parseRootsJSON(data)
}

// parseRootsJSON은 roots-from 내용을 해석한다. JSON null은 배열이 아니다 — 받으면 빈
// 입력 파일이 "root 없음"으로 가려진다.
func parseRootsJSON(data []byte) ([]string, error) {
	bad := &usageError{"--roots-from must be a JSON array of strings or a bridge-facts document"}
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		if list == nil {
			return nil, bad
		}
		return list, nil
	}
	var doc struct {
		Format string `json:"format"`
		Facts  []struct {
			Symbol *struct {
				Usr *string `json:"usr"`
			} `json:"symbol"`
		} `json:"facts"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Format != "bridge-facts" || doc.Facts == nil {
		return nil, bad
	}
	var out []string
	for _, f := range doc.Facts {
		if f.Symbol != nil && f.Symbol.Usr != nil {
			out = append(out, *f.Symbol.Usr)
		}
	}
	return out, nil
}

// parseGeneratedAt은 --generated-at을 계약의 UTC 밀리초 형식으로 정규화한다(없으면 지금).
func parseGeneratedAt(value string) (string, error) {
	if value == "" {
		return time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", &usageError{"--generated-at must be an RFC 3339 timestamp such as 2026-09-30T00:00:00.000Z"}
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z"), nil
}

// requestsTraversalFormat은 인자에 --format language-traversal(또는 = 형식)이 있는지 본다 —
// 플래그 파싱이 실패해 값을 읽지 못했을 때도 종료 코드를 정하기 위해서다.
func requestsTraversalFormat(args []string) bool {
	for i, a := range args {
		if a == "--" {
			return false
		}
		if a == "--format="+formatTraversal || a == "-format="+formatTraversal {
			return true
		}
		if (a == "--format" || a == "-format") && i+1 < len(args) && args[i+1] == formatTraversal {
			return true
		}
	}
	return false
}

// traversalDocument는 isthmus language-traversal v1 문서다. 키 순서는 계약 문서의
// 나열 순서다.
type traversalDocument struct {
	Format            string                 `json:"format"`
	Version           int                    `json:"version"`
	Tool              source.BridgeFactsTool `json:"tool"`
	GeneratedAt       string                 `json:"generatedAt"`
	Platform          string                 `json:"platform"`
	Project           string                 `json:"project"`
	Revision          string                 `json:"revision,omitempty"`
	GraphRevision     string                 `json:"graphRevision"`
	Direction         string                 `json:"direction"`
	Roots             []traversalRoot        `json:"roots"`
	Reached           []traversalEntry       `json:"reached"`
	RootsTruncated    bool                   `json:"rootsTruncated,omitempty"`
	Truncated         bool                   `json:"truncated"`
	TruncationReasons []string               `json:"truncationReasons,omitempty"`
	Limitations       []string               `json:"limitations"`
}

// traversalRoot는 root 하나다 — 정점이 아닌 id는 symbol 없이 원문만 싣는다.
type traversalRoot struct {
	ID     string           `json:"id"`
	Symbol *traversalSymbol `json:"symbol,omitempty"`
}

// traversalSymbol은 순회 문서의 심볼이다. usr는 정점 ID — schema 사실의 usr와 같은 문자열이다.
type traversalSymbol struct {
	Usr           string             `json:"usr"`
	QualifiedName string             `json:"qualifiedName"`
	Kind          string             `json:"kind,omitempty"`
	Location      *traversalLocation `json:"location,omitempty"`
}

// traversalLocation은 project 상대 1 기반 위치다(열은 알 때만).
type traversalLocation struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column,omitempty"`
}

// traversalEntry는 도달 정점 하나다.
type traversalEntry struct {
	Symbol        traversalSymbol `json:"symbol"`
	Via           string          `json:"via"`
	Depth         int             `json:"depth"`
	Roots         []int           `json:"roots"`
	Relationships []string        `json:"relationships,omitempty"`
	Evidence      string          `json:"evidence,omitempty"`
}

// buildTraversalDocument는 순회를 돌려 문서를 만든다. 두 번째 값은 정점이 아닌 root 수다.
func buildTraversalDocument(in traversalRun, doc *graph.Document, req analysis.TraversalRequest,
	generatedAt string) (*traversalDocument, int, error) {
	project, err := traversalProject(in, doc)
	if err != nil {
		return nil, 0, err
	}
	graphRevision, err := documentRevision(doc)
	if err != nil {
		return nil, 0, err
	}
	res := analysis.Traverse(doc, req)
	sym := newSymbolizer(doc, project)
	out := &traversalDocument{
		Format: formatTraversal, Version: 1, Tool: source.BridgeFactsTool{Name: "gartograph", Version: Version},
		GeneratedAt: generatedAt, Platform: "go", Project: project, Revision: traversalRevision(in, project),
		GraphRevision: graphRevision, Direction: req.Direction, RootsTruncated: res.RootsTruncated,
		TruncationReasons: res.TruncationReasons, Reached: []traversalEntry{},
	}
	missing := 0
	for _, id := range req.Roots {
		root := traversalRoot{ID: id, Symbol: sym.symbol(id)}
		if root.Symbol == nil {
			missing++
		}
		out.Roots = append(out.Roots, root)
	}
	for _, r := range res.Reached {
		out.Reached = append(out.Reached, traversalEntry{Symbol: *sym.symbol(r.ID), Via: r.Via, Depth: r.Depth,
			Roots: r.Roots, Relationships: kindStrings(r.Relationships), Evidence: r.Evidence})
	}
	if missing > 0 {
		out.TruncationReasons = append(out.TruncationReasons, "root-not-found")
	}
	out.Truncated = len(out.TruncationReasons) > 0
	sort.Strings(out.TruncationReasons)
	out.Limitations = traversalLimitations(doc, res, missing)
	return out, missing, nil
}

// traversalProject는 문서의 project다 — 수확이면 --dir의 realpath(schema와 같은 함수),
// 저장 문서면 그 문서의 root를 realpath로 푼 값이다(풀 수 없으면 적힌 그대로).
func traversalProject(in traversalRun, doc *graph.Document) (string, error) {
	if in.graphPath == "" {
		return source.ProjectPath(in.opts.Dir)
	}
	if p, err := source.ProjectPath(doc.Root); err == nil {
		return p, nil
	}
	return doc.Root, nil
}

// traversalRevision은 --revision, 없으면 수확한 작업 트리가 깨끗할 때의 git HEAD다.
// 저장 문서(--graph)는 어느 revision에서 만들었는지 모르므로 --revision 없이는 싣지 않는다 —
// isthmus trace가 analysis-revision-unknown으로 드러낸다.
func traversalRevision(in traversalRun, project string) string {
	if in.flags.revisionSet {
		return in.flags.revision
	}
	if in.graphPath != "" {
		return ""
	}
	return gitRevision(project)
}

// gitRevision은 작업 트리가 깨끗하면 HEAD 커밋 id를 돌려준다. 커밋하지 않은 변경·추적
// 안 하는 파일이 있으면 HEAD는 분석한 소스가 아니므로 빈 문자열이다. 저장소 index를
// 고치지 않도록 선택 잠금·fsmonitor를 끈다(isthmus capture와 같은 설정).
func gitRevision(dir string) string {
	status, ok := runGit(dir, "status", "--porcelain=v1", "--untracked-files=normal", "--ignore-submodules=none")
	if !ok || strings.TrimSpace(status) != "" {
		return ""
	}
	head, ok := runGit(dir, "rev-parse", "--verify", "HEAD")
	head = strings.TrimSpace(head)
	if !ok || !objectIDPattern.MatchString(head) {
		return ""
	}
	return head
}

// runGit은 git 명령 하나를 실행한다. git이 없거나 실패하면 false다 — revision이 없다는
// 것 자체가 소비자에게 가는 신호라 오류로 올리지 않는다.
func runGit(dir string, args ...string) (string, bool) {
	full := append([]string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// documentRevision은 순회에 쓴 그래프 문서의 신원이다 — 결정적 JSON 바이트의 소문자
// hex SHA-256(schemagraph graphRevision과 같은 규칙).
func documentRevision(doc *graph.Document) (string, error) {
	data, err := export.JSON(doc)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// symbolizer는 정점 ID를 순회 문서의 심볼로 바꾼다.
type symbolizer struct {
	vertices map[string]graph.Vertex
	bases    []string // 위치를 project 상대로 바꿀 기준 디렉터리 후보
}

// newSymbolizer는 정점 색인과 위치 기준을 만든다. 정점 위치는 수확 때의 절대 경로라
// realpath project와 심링크 차이(/tmp ↔ /private/tmp)가 날 수 있어 문서 root도 기준으로 둔다.
func newSymbolizer(doc *graph.Document, project string) symbolizer {
	s := symbolizer{vertices: make(map[string]graph.Vertex, len(doc.Vertices)), bases: []string{project}}
	for _, v := range doc.Vertices {
		s.vertices[v.ID] = v
	}
	if doc.Root != "" && doc.Root != project {
		s.bases = append(s.bases, doc.Root)
	}
	return s
}

// symbol은 정점의 심볼이다. 정점이 아니면 nil이다.
func (s symbolizer) symbol(id string) *traversalSymbol {
	v, ok := s.vertices[id]
	if !ok {
		return nil
	}
	return &traversalSymbol{Usr: id, QualifiedName: graph.ShortName(id), Kind: string(v.Kind),
		Location: s.location(v.Position)}
}

// location은 project 안의 위치만 상대 경로로 싣는다 — 밖(모듈 캐시 등)은 생략한다.
func (s symbolizer) location(pos *graph.Position) *traversalLocation {
	if pos == nil || pos.File == "" || pos.Line < 1 {
		return nil
	}
	files := []string{pos.File}
	if resolved, err := filepath.EvalSymlinks(pos.File); err == nil && resolved != pos.File {
		files = append(files, resolved)
	}
	for _, base := range s.bases {
		for _, file := range files {
			rel, err := filepath.Rel(base, file)
			if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
				continue
			}
			return &traversalLocation{Path: filepath.ToSlash(rel), Line: pos.Line, Column: max(pos.Column, 0)}
		}
	}
	return nil
}

// kindStrings는 간선 종류를 문자열로 바꾼다(이미 정렬돼 있다).
func kindStrings(kinds []graph.EdgeKind) []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	return out
}

// traversalLimitations는 그래프가 센 한계에 순회가 센 공백을 더한다.
func traversalLimitations(doc *graph.Document, res *analysis.TraversalResult, missing int) []string {
	out := append([]string{}, doc.Limitations...)
	if missing > 0 {
		out = append(out, fmt.Sprintf(
			"root-not-found: %d root id(s) are not vertices of this graph; they are listed in roots without symbol",
			missing))
	}
	if !res.EvidenceClassified && len(res.Reached) > 0 {
		out = append(out, "evidence-unassessed: the graph document predates dispatchEvidence; "+
			"re-harvest to tell interface-dispatch candidate edges from compiler-resolved ones")
	}
	if res.EvidenceApproximated {
		out = append(out, "evidence-approximated: too many roots for exact per-root evidence; "+
			"symbols downstream of any reached candidate edge are reported as candidate")
	}
	return out
}
