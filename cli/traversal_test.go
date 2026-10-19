package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/export"
	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// traversalFixture는 인터페이스 디스패치와 SQL 사실이 섞인 핸들러 모듈이다.
func traversalFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "database/sql"

type Store interface{ List() }

type sqlStore struct{ db *sql.DB }

func (s sqlStore) List() { s.db.Query("SELECT * FROM users") }

type memStore struct{}

func (memStore) List() {}

// 인터페이스로만 부른다 — 구현 메서드는 팬아웃 후보다.
func handleUsers(s Store) { s.List() }

// 구체 타입으로 부른다 — 확정 호출이다.
func handleDirect(s sqlStore) { s.List() }

func main() { handleUsers(sqlStore{}); handleDirect(sqlStore{}) }
`,
	})
}

// traversalOut은 테스트가 읽는 순회 문서 필드다.
type traversalOut struct {
	Format            string   `json:"format"`
	Platform          string   `json:"platform"`
	Project           string   `json:"project"`
	GeneratedAt       string   `json:"generatedAt"`
	Revision          *string  `json:"revision"`
	GraphRevision     string   `json:"graphRevision"`
	Direction         string   `json:"direction"`
	Truncated         bool     `json:"truncated"`
	TruncationReasons []string `json:"truncationReasons"`
	Limitations       []string `json:"limitations"`
	Roots             []struct {
		ID     string          `json:"id"`
		Symbol *map[string]any `json:"symbol"`
	} `json:"roots"`
	Reached []struct {
		Symbol struct {
			Usr      string `json:"usr"`
			Kind     string `json:"kind"`
			Location *struct {
				Path string `json:"path"`
				Line int    `json:"line"`
			} `json:"location"`
		} `json:"symbol"`
		Via           string   `json:"via"`
		Depth         int      `json:"depth"`
		Roots         []int    `json:"roots"`
		Relationships []string `json:"relationships"`
		Evidence      *string  `json:"evidence"`
	} `json:"reached"`
}

// parseTraversal은 표준 출력을 순회 문서로 읽는다.
func parseTraversal(t *testing.T, out string) traversalOut {
	t.Helper()
	var doc traversalOut
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not a traversal document: %v\n%s", err, out)
	}
	return doc
}

// evidenceByUsr는 도달 정점 usr → 근거 등급 표다(없으면 빈 문자열).
func evidenceByUsr(doc traversalOut) map[string]string {
	out := map[string]string{}
	for _, r := range doc.Reached {
		ev := ""
		if r.Evidence != nil {
			ev = *r.Evidence
		}
		out[r.Symbol.Usr] = ev
	}
	return out
}

const fx = "example.com/fixture"

// TestReachEvidence는 reach 문서의 도달·root별 하한 등급·위치를 확인한다.
func TestReachEvidence(t *testing.T) {
	dir := traversalFixture(t)
	code, out, errb := run(t, "reach", "--dir", dir, "--generated-at", "2026-09-30T09:00:00+09:00",
		fx+".handleUsers", fx+".handleDirect")
	if code != 0 {
		t.Fatalf("reach: %d %s", code, errb)
	}
	doc := parseTraversal(t, out)
	if doc.Format != "language-traversal" || doc.Platform != "go" || doc.Direction != "dependencies" ||
		doc.GeneratedAt != "2026-09-30T00:00:00.000Z" || len(doc.GraphRevision) != 64 || doc.Truncated {
		t.Fatalf("header wrong: %+v", doc)
	}
	ev := evidenceByUsr(doc)
	// handleUsers에서는 팬아웃으로만 닿으므로 root별 하한은 candidate다.
	want := map[string]string{
		fx + ".(Store).List":    "direct",
		fx + ".(sqlStore).List": "candidate",
		fx + ".(memStore).List": "candidate",
		fx + ".Store":           "direct",
		// handleDirect는 서명으로 확정해 닿지만 handleUsers는 팬아웃 메서드를 거쳐서만 닿는다.
		fx + ".sqlStore": "candidate",
	}
	for usr, tier := range want {
		if ev[usr] != tier {
			t.Errorf("%s evidence = %q, want %q (all: %v)", usr, ev[usr], tier, ev)
		}
	}
	for _, r := range doc.Reached {
		if r.Symbol.Usr == fx+".(sqlStore).List" {
			// 두 root 모두 depth 1이라 가장 가까운 root는 작은 인덱스(handleUsers)다.
			if !slices.Equal(r.Roots, []int{0, 1}) || r.Via != fx+".handleUsers" || r.Depth != 1 ||
				r.Symbol.Location == nil || r.Symbol.Location.Path != "main.go" {
				t.Fatalf("sqlStore.List row wrong: %+v", r)
			}
		}
	}
	// 확정 호출 root 하나만이면 같은 정점이 direct다.
	_, out, _ = run(t, "reach", "--dir", dir, fx+".handleDirect")
	if got := evidenceByUsr(parseTraversal(t, out))[fx+".(sqlStore).List"]; got != "direct" {
		t.Fatalf("direct-only root must yield direct, got %q", got)
	}
}

// TestReachJoinsSchemaUsr는 schema 사실의 usr가 핸들러 순회의 도달 정점 usr와 같은
// 문자열인지 확인한다 — isthmus trace 조인의 전제다.
func TestReachJoinsSchemaUsr(t *testing.T) {
	dir := traversalFixture(t)
	code, out, errb := run(t, "schema", "--dir", dir)
	if code != 0 {
		t.Fatalf("schema: %d %s", code, errb)
	}
	var facts struct {
		Project string `json:"project"`
		Facts   []struct {
			Channel string `json:"channel"`
			Symbol  *struct {
				Usr string `json:"usr"`
			} `json:"symbol"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &facts); err != nil {
		t.Fatal(err)
	}
	if len(facts.Facts) != 1 || facts.Facts[0].Symbol == nil {
		t.Fatalf("expected one users fact with usr: %s", out)
	}
	usr := facts.Facts[0].Symbol.Usr
	_, out, _ = run(t, "reach", "--dir", dir, fx+".handleDirect")
	doc := parseTraversal(t, out)
	if _, ok := evidenceByUsr(doc)[usr]; !ok {
		t.Fatalf("schema usr %q not in reach set %v", usr, evidenceByUsr(doc))
	}
	if doc.Project != facts.Project {
		t.Fatalf("project mismatch: traversal %q, schema %q", doc.Project, facts.Project)
	}
	// schema 문서를 그대로 --roots-from으로 받아 역방향 순회 root로 쓴다.
	schemaFile := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(schemaFile, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	_, schemaOut, _ := run(t, "schema", "--dir", dir)
	if err := os.WriteFile(schemaFile, []byte(schemaOut), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb = run(t, "impact", "--format", "language-traversal", "--dir", dir, "--roots-from", schemaFile)
	if code != 0 {
		t.Fatalf("impact --roots-from schema: %d %s", code, errb)
	}
	rev := parseTraversal(t, out)
	if rev.Direction != "dependents" || len(rev.Roots) != 1 || rev.Roots[0].ID != usr {
		t.Fatalf("roots from schema wrong: %+v", rev.Roots)
	}
	got := evidenceByUsr(rev)
	if got[fx+".handleDirect"] != "direct" || got[fx+".handleUsers"] != "candidate" {
		t.Fatalf("dependents evidence wrong: %v", got)
	}
}

// TestTraversalRootsFrom는 JSON 배열 파일·표준 입력과 위치 인자의 순서·중복 규칙을 본다.
func TestTraversalRootsFrom(t *testing.T) {
	dir := traversalFixture(t)
	file := filepath.Join(t.TempDir(), "roots.json")
	if err := os.WriteFile(file, []byte(`["`+fx+`.handleDirect","`+fx+`.handleUsers"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ := run(t, "reach", "--dir", dir, fx+".handleUsers", "--roots-from", file)
	doc := parseTraversal(t, out)
	var ids []string
	for _, r := range doc.Roots {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []string{fx + ".handleUsers", fx + ".handleDirect"}) {
		t.Fatalf("roots order/dedupe wrong: %v", ids)
	}
	old := stdinReader
	stdinReader = strings.NewReader(`["` + fx + `.main"]`)
	defer func() { stdinReader = old }()
	code, out, errb := run(t, "reach", "--dir", dir, "--roots-from", "-")
	if code != 0 || parseTraversal(t, out).Roots[0].ID != fx+".main" {
		t.Fatalf("stdin roots: %d %s %s", code, errb, out)
	}
}

// TestTraversalUsageErrors는 사용법 오류가 64이고 표준 출력이 비는지 확인한다.
func TestTraversalUsageErrors(t *testing.T) {
	dir := traversalFixture(t)
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"format":"other"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"root 없음":             {"reach", "--dir", dir},
		"제어 문자 id":            {"reach", "--dir", dir, "a\nb"},
		"깊이 초과":               {"reach", "--dir", dir, "--depth", "129", fx + ".main"},
		"음수 상한":               {"reach", "--dir", dir, "--max", "-1", fx + ".main"},
		"revision 제어 문자":      {"reach", "--dir", dir, "--revision", "a\tb", fx + ".main"},
		"빈 revision":          {"reach", "--dir", dir, "--revision", "", fx + ".main"},
		"generated-at 형식":     {"reach", "--dir", dir, "--generated-at", "yesterday", fx + ".main"},
		"reach 다른 형식":         {"reach", "--dir", dir, "--format", "json", fx + ".main"},
		"모르는 플래그":             {"reach", "--dir", dir, "--bogus", fx + ".main"},
		"roots-from 없는 파일":    {"reach", "--dir", dir, "--roots-from", "/no/such/roots.json"},
		"roots-from 형식":       {"reach", "--dir", dir, "--roots-from", bad},
		"impact 파일 모드와 함께":    {"impact", "--format", "language-traversal", "--dir", dir, "--files", "main.go"},
		"impact package 레벨":   {"impact", "--format", "language-traversal", "--dir", dir, "--level", "package", fx},
		"impact root 없음":      {"impact", "--format", "language-traversal", "--dir", dir, "--roots-from", bad},
		"roots-from 빈 bridge": {"reach", "--dir", dir, "--roots-from", writeTemp(t, `{"format":"bridge-facts","facts":[]}`)},
		"roots-from null":     {"reach", "--dir", dir, "--roots-from", writeTemp(t, "null"), fx + ".main"},
		"impact 순회 형식 플래그 오류": {"impact", "--format", "language-traversal", "--dir", dir, "--depth=abc", fx + ".main"},
		"impact 순회 형식 = 표기":   {"impact", "--format=language-traversal", "--dir", dir, "--bogus", fx + ".main"},
		"type-edges 값":        {"reach", "--dir", dir, "--type-edges", "fields", fx + ".main"},
	}
	for name, args := range cases {
		code, out, _ := run(t, args...)
		if code != exitUsage || out != "" {
			t.Errorf("%s: code %d stdout %q, want 64 and empty stdout", name, code, out)
		}
	}
	if code, _, _ := run(t, "impact", "--format", "bogus", "--dir", dir, fx+".main"); code != 2 {
		t.Fatalf("unknown impact format must stay a usage error 2, got %d", code)
	}
	if code, _, _ := run(t, "impact", "--type-edges", "all", "--dir", dir, fx+".main"); code != 2 {
		t.Fatalf("--type-edges with the json impact format must be a usage error 2, got %d", code)
	}
}

// typeEdgesFixture는 공유 Handler struct 데모다: health는 리시버만, users는 users 필드와 저장소를 쓴다.
func typeEdgesFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{"main.go": `package main

import "database/sql"

type User struct {
	Email string ` + "`db:\"email\"`" + `
}

type Store struct {
	db   *sql.DB
	rows []User
}

func (s *Store) List() { s.db.Query("SELECT email FROM users") }

type Handler struct {
	store *Store
	name  string
}

func (h *Handler) health() {}

func (h *Handler) users() { h.store.List() }

func main() { h := &Handler{}; h.health(); h.users() }
`})
}

// TestReachTypeEdges는 기본(members)이 health 핸들러를 Store·User에서 떼고 users 핸들러의 도달은
// 지키며, --type-edges all이 이전 과대 근사를 돌려주는지 본다. 좁힌 곳은 셈과 함께 한계로 싣는다.
func TestReachTypeEdges(t *testing.T) {
	dir := typeEdgesFixture(t)
	reached := func(args ...string) (map[string]bool, traversalOut) {
		code, out, errb := run(t, append([]string{"reach", "--dir", dir}, args...)...)
		if code != 0 {
			t.Fatalf("reach %v: %d %s", args, code, errb)
		}
		doc := parseTraversal(t, out)
		set := map[string]bool{}
		for _, r := range doc.Reached {
			set[r.Symbol.Usr] = true
		}
		return set, doc
	}
	health, doc := reached(fx + ".(Handler).health")
	if health[fx+".Store"] || health[fx+".User"] || !health[fx+".Handler"] {
		t.Errorf("members: health reached %v", health)
	}
	if !strings.Contains(strings.Join(doc.Limitations, "\n"), "type-edges-members: 2 symbol(s)") {
		t.Errorf("limitations = %v", doc.Limitations)
	}
	users, _ := reached(fx + ".(Handler).users")
	for _, want := range []string{fx + ".(Handler).store", fx + ".Store", fx + ".(Store).List"} {
		if !users[want] {
			t.Errorf("members lost %s from users: %v", want, users)
		}
	}
	if all, _ := reached("--type-edges", "all", fx+".(Handler).health"); !all[fx+".Store"] || !all[fx+".User"] {
		t.Errorf("--type-edges all must keep the previous reach: %v", all)
	}
}

// writeTemp는 내용을 임시 파일로 쓰고 경로를 돌려준다.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTraversalRootNotFound는 정점이 아닌 root를 symbol 없이 싣고 64로 끝나는지 본다.
func TestTraversalRootNotFound(t *testing.T) {
	dir := traversalFixture(t)
	code, out, _ := run(t, "reach", "--dir", dir, "nope", fx+".handleDirect")
	if code != exitUsage {
		t.Fatalf("root-not-found must exit 64, got %d", code)
	}
	doc := parseTraversal(t, out)
	if doc.Roots[0].ID != "nope" || doc.Roots[0].Symbol != nil || doc.Roots[1].Symbol == nil {
		t.Fatalf("roots wrong: %+v", doc.Roots)
	}
	if !doc.Truncated || !slices.Contains(doc.TruncationReasons, "root-not-found") {
		t.Fatalf("truncation wrong: %+v", doc)
	}
	var found bool
	for _, lim := range doc.Limitations {
		found = found || strings.HasPrefix(lim, "root-not-found: 1 ")
	}
	if !found {
		t.Fatalf("missing root-not-found limitation: %v", doc.Limitations)
	}
	// 정점인 root의 도달은 root 인덱스 1로 그대로 실린다.
	for _, r := range doc.Reached {
		if !slices.Equal(r.Roots, []int{1}) {
			t.Fatalf("reached roots must point at index 1: %+v", r)
		}
	}
}

// TestTraversalLocationResolvesEachFileOnce는 저장 문서의 위치가 심링크 root(/tmp ↔ /private/tmp
// 같은) 아래에 있을 때 project 상대 위치를 그대로 싣고, 심링크 해석은 위치 파일마다 한 번만 하는지
// 본다. 한 파일에 심볼이 수십~수백 개라 행마다 풀면 경로 구성요소마다 lstat가 반복된다. 풀 수 없는
// 파일(지운 파일)도 한 번만 시도하고, 적힌 경로가 문서 root 아래면 그 상대 위치를 싣는다.
func TestTraversalLocationResolvesEachFileOnce(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.go", "sub/b.go"} {
		if err := os.WriteFile(filepath.Join(real, name), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	const pkg = "example.com/linked"
	at := func(name string, line int) *graph.Position {
		return &graph.Position{File: filepath.Join(link, name), Line: line}
	}
	doc := &graph.Document{Version: graph.Version, Tool: graph.Tool, Level: graph.LevelSymbol, Root: link,
		Vertices: []graph.Vertex{
			{ID: pkg + ".Root", Kind: graph.KindFunc, Position: at("a.go", 1)},
			{ID: pkg + ".A1", Kind: graph.KindFunc, Position: at("a.go", 2)},
			{ID: pkg + ".A2", Kind: graph.KindFunc, Position: at("a.go", 3)},
			{ID: pkg + ".B1", Kind: graph.KindFunc, Position: at("sub/b.go", 1)},
			{ID: pkg + ".B2", Kind: graph.KindFunc, Position: at("sub/b.go", 2)},
			{ID: pkg + ".Gone1", Kind: graph.KindFunc, Position: at("gone.go", 1)},
			{ID: pkg + ".Gone2", Kind: graph.KindFunc, Position: at("gone.go", 2)},
			{ID: pkg + ".Outside", Kind: graph.KindFunc,
				Position: &graph.Position{File: filepath.Join(base, "other", "x.go"), Line: 1}},
		}}
	for _, v := range doc.Vertices[1:] {
		doc.Edges = append(doc.Edges, graph.Edge{From: pkg + ".Root", To: v.ID, Kind: graph.EdgeCall})
	}
	file := filepath.Join(t.TempDir(), "g.json")
	if err := export.SaveFile(doc, file); err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	resolve := evalSymlinks
	evalSymlinks = func(path string) (string, error) {
		calls[path]++
		return resolve(path)
	}
	defer func() { evalSymlinks = resolve }()
	code, out, errb := run(t, "reach", "--graph", file, pkg+".Root")
	if code != 0 {
		t.Fatalf("reach: %d %s", code, errb)
	}
	got := map[string]string{}
	for _, r := range parseTraversal(t, out).Reached {
		got[r.Symbol.Usr] = ""
		if loc := r.Symbol.Location; loc != nil {
			got[r.Symbol.Usr] = fmt.Sprintf("%s:%d", loc.Path, loc.Line)
		}
	}
	want := map[string]string{pkg + ".A1": "a.go:2", pkg + ".A2": "a.go:3", pkg + ".B1": "sub/b.go:1",
		pkg + ".B2": "sub/b.go:2", pkg + ".Gone1": "gone.go:1", pkg + ".Gone2": "gone.go:2", pkg + ".Outside": ""}
	if !maps.Equal(got, want) {
		t.Fatalf("locations = %v, want %v", got, want)
	}
	if len(calls) != 4 {
		t.Errorf("resolved %d distinct files %v, want a.go, sub/b.go, gone.go and the outside file", len(calls), calls)
	}
	for path, n := range calls {
		if n != 1 {
			t.Errorf("%s resolved %d times, want once per file", path, n)
		}
	}
}

// TestTraversalDeterministic은 generatedAt을 고정하면 같은 바이트인지 본다.
func TestTraversalDeterministic(t *testing.T) {
	dir := traversalFixture(t)
	args := []string{"impact", "--format", "language-traversal", "--dir", dir, "--generated-at",
		"2026-09-30T00:00:00Z", "--revision", "rev-1", fx + ".(sqlStore).List", fx + ".(memStore).List"}
	_, a, _ := run(t, args...)
	_, b, _ := run(t, args...)
	if a != b {
		t.Fatalf("output differs between runs:\n%s\n%s", a, b)
	}
	if doc := parseTraversal(t, a); doc.Revision == nil || *doc.Revision != "rev-1" {
		t.Fatalf("--revision must be recorded: %s", a)
	}
}

// TestTraversalGitRevision은 깨끗한 작업 트리에서만 HEAD를 revision으로 싣는지 본다.
func TestTraversalGitRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := traversalFixture(t)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "-A")
	git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	head := git("rev-parse", "HEAD")
	_, out, _ := run(t, "reach", "--dir", dir, fx+".main")
	if doc := parseTraversal(t, out); doc.Revision == nil || *doc.Revision != head {
		t.Fatalf("clean tree must record HEAD %s: %v", head, doc.Revision)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ = run(t, "reach", "--dir", dir, fx+".main")
	if doc := parseTraversal(t, out); doc.Revision != nil {
		t.Fatalf("dirty tree must omit revision, got %q", *doc.Revision)
	}
}

// TestParseFilterDriverNames는 `git config -z --get-regexp` 출력에서 드라이버 이름을 뽑는지 본다 — 이름은
// `filter.`과 마지막 `.키` 사이 전체(점 포함, subsection이라 대소문자 그대로)이고, 빈 이름(`[filter ""]`)도
// git이 쓰므로 버리지 않는다. 값 없는 키는 개행 없이 끝난다. 결과는 정렬·중복 제거돼 -c 순서가 결정적이다.
func TestParseFilterDriverNames(t *testing.T) {
	out := "filter.p.process\ncmd\x00filter.A.b.clean\nsh -c 'x\ny'\x00filter..clean\nc\x00" +
		"filter.A.b.smudge\nc\x00filter.v.clean\x00"
	got, err := parseFilterDriverNames([]byte(out))
	if err != nil || !slices.Equal(got, []string{"", "A.b", "p", "v"}) {
		t.Fatalf("parseFilterDriverNames = %q, %v", got, err)
	}
	if got, err := parseFilterDriverNames(nil); err != nil || len(got) != 0 {
		t.Fatalf("no output must mean no drivers: %q, %v", got, err)
	}
	for _, bad := range []string{"core.editor\nvi\x00", "filter\x00"} {
		if _, err := parseFilterDriverNames([]byte(bad)); err == nil {
			t.Errorf("a key outside filter.<name>.<key> must be refused (fail-closed): %q", bad)
		}
	}
}

// TestFilterNeutralizingArgs는 드라이버 이름마다 clean·smudge·process를 빈 명령으로, required를 false로
// 덮는 -c 인자를 만드는지 본다 — 빈 명령은 git이 실행하지 않고, required=false라야 "filter 실패"로 죽지 않는다.
func TestFilterNeutralizingArgs(t *testing.T) {
	got := filterNeutralizingArgs([]string{"", "a.b"})
	want := []string{
		"-c", "filter..clean=", "-c", "filter..smudge=", "-c", "filter..process=", "-c", "filter..required=false",
		"-c", "filter.a.b.clean=", "-c", "filter.a.b.smudge=", "-c", "filter.a.b.process=", "-c", "filter.a.b.required=false",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("filterNeutralizingArgs = %q", got)
	}
}

// TestNeutralizableDriverName은 -c로 덮을 수 없는 이름을 가려내는지 본다. git은 -c 값을 첫 '='에서 키와
// 값으로 나누고, 제어 문자·깨진 UTF-8은 인자 변환(windows UTF-16)에서 다른 이름이 될 수 있다.
func TestNeutralizableDriverName(t *testing.T) {
	for _, name := range []string{"", "lfs", "a.b", "A B", "ünï", "x-y_z"} {
		if !neutralizableDriverName(name) {
			t.Errorf("%q must be neutralizable", name)
		}
	}
	for _, name := range []string{"a=b", "=", "a\tb", "a\x1bb", "a\x7fb", "a\u0085b", "a\u2028b", "a\xffb"} {
		if neutralizableDriverName(name) {
			t.Errorf("%q must be refused", name)
		}
	}
}

// fakeGitLog는 PATH 맨 앞에 가짜 git을 두고, 호출마다 인자를 한 줄(인자 사이 \x1f)로 남길 로그 경로를
// 돌려준다. answers는 하위 명령 이름 → 셸 동작이다(첫 일치 인자로 고른다). 없는 하위 명령은 종료 3이다.
func fakeGitLog(t *testing.T, answers map[string]string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a POSIX shell script")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls.log")
	var cases strings.Builder
	for sub, action := range answers {
		fmt.Fprintf(&cases, "    %s) %s ;;\n", sub, action)
	}
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\037' \"$a\"; done >> '" + log + "'\n" +
		"printf '\\n' >> '" + log + "'\n" +
		"for a in \"$@\"; do\n  case \"$a\" in\n" + cases.String() + "  esac\ndone\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return log
}

// readGitCalls는 fakeGitLog가 남긴 호출들의 인자 목록이다.
func readGitCalls(t *testing.T, log string) [][]string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		calls = append(calls, strings.Split(strings.TrimSuffix(line, "\x1f"), "\x1f"))
	}
	return calls
}

// TestGitCallsCarryIsolation은 분석용 git 호출마다 바로 앞에서 filter 드라이버를 다시 조회하고, 모은 이름을
// 빈 명령으로 덮으며, 공통 설정(fsmonitor·untracked cache·서명 표시 끄기)을 붙이는지 본다. diff에는 외부
// diff·textconv를 끄고 서브모듈 안으로 들어가지 않으며 index를 갱신하지 않는 인자를, status에는 서브모듈을
// 보지 않는 인자를 붙인다. 인자 하나하나가 분석 대상 설정의 명령 실행을 막는 계약이라 전부 고정한다.
func TestGitCallsCarryIsolation(t *testing.T) {
	const oid = "0123456789abcdef0123456789abcdef01234567"
	log := fakeGitLog(t, map[string]string{
		"config":    `printf 'filter.a.b.clean\nx\000filter.Z.smudge\ny\000filter.a.b.process\nz\000'; exit 0`,
		"diff":      `printf 'x.go\000sub dir/y.go\000'; exit 0`,
		"ls-files":  `printf '100644 ` + oid + ` 0\tx.go\000'; exit 0`,
		"status":    `exit 0`,
		"rev-parse": `echo ` + oid + `; exit 0`,
	})
	dir := t.TempDir()
	files, err := changedFilesSince(dir, "origin/main...HEAD")
	if err != nil || !slices.Equal(files, []string{"x.go", "sub dir/y.go"}) {
		t.Fatalf("changedFilesSince = %q, %v", files, err)
	}
	if rev, note := gitRevision(dir); rev != oid || note != "" {
		t.Fatalf("gitRevision = %q, %q", rev, note)
	}
	base := []string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false",
		"-c", "log.showSignature=false"}
	query := append(slices.Clone(base), "config", "-z", "--includes", "--get-regexp", `^filter\..*\.(clean|smudge|process)$`)
	isolated := append(slices.Clone(base), "-c", "filter.Z.clean=", "-c", "filter.Z.smudge=", "-c", "filter.Z.process=",
		"-c", "filter.Z.required=false", "-c", "filter.a.b.clean=", "-c", "filter.a.b.smudge=",
		"-c", "filter.a.b.process=", "-c", "filter.a.b.required=false")
	want := [][]string{
		query,
		append(slices.Clone(isolated), "-c", "diff.autoRefreshIndex=false", "diff", "--name-only", "--relative",
			"--no-renames", "-z", "--no-ext-diff", "--no-textconv", "--ignore-submodules=dirty",
			"--end-of-options", "origin/main...HEAD", "--"),
		query,
		append(slices.Clone(isolated), "ls-files", "--stage", "-z"),
		query,
		append(slices.Clone(isolated), "status", "--porcelain=v1", "--untracked-files=normal", "--ignore-submodules=all"),
		query,
		append(slices.Clone(isolated), "rev-parse", "--verify", "HEAD"),
	}
	got := readGitCalls(t, log)
	if len(got) != len(want) {
		t.Fatalf("git ran %d times, want %d:\n%q", len(got), len(want), got)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("git call %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}

// TestGitRefusesWithoutIsolation은 filter 드라이버를 모을 수 없거나 덮을 수 없으면 분석용 git을 하나도
// 실행하지 않는지 본다(fail-closed, DECISIONS 원칙 1). impact --since는 원인과 --files를 담은 오류다.
// revision은 생략하고, 덮을 수 없는 이름 때문이면 limitation 문구로 이유를 알린다. 설정을 읽지 못한 것은
// git 자체가 그 저장소에서 동작하지 않는 경우라 지금처럼 revision만 생략한다(이름 원문은 싣지 않는다).
func TestGitRefusesWithoutIsolation(t *testing.T) {
	cases := []struct {
		name, config, sinceSays string
		limitation              bool
	}{
		{"config fails", `echo 'fatal: bad config line 3 in file .git/config' >&2; exit 128`,
			"git config: fatal: bad config line 3 in file .git/config", false},
		{"no match with output", `printf 'filter.x.clean\nc\000'; exit 1`, "could not list the git filter drivers", false},
		{"equals sign", `printf 'filter.a=b.clean\nc\000'; exit 0`, "'='", true},
		{"control character", `printf 'filter.a\tb.clean\nc\000'; exit 0`, "control character", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := fakeGitLog(t, map[string]string{"config": c.config, "diff": "exit 0", "ls-files": "exit 0",
				"status": "exit 0", "rev-parse": "echo 0123456789abcdef0123456789abcdef01234567; exit 0"})
			dir := t.TempDir()
			_, err := changedFilesSince(dir, "HEAD")
			if err == nil || !strings.Contains(err.Error(), c.sinceSays) || !strings.Contains(err.Error(), "--files") ||
				!strings.Contains(err.Error(), "did not run git diff") {
				t.Fatalf("changedFilesSince error = %v", err)
			}
			rev, note := gitRevision(dir)
			if rev != "" || (note != "") != c.limitation ||
				(c.limitation && (!strings.HasPrefix(note, "revision-unverified:") || !strings.Contains(note, "--revision"))) {
				t.Fatalf("gitRevision = %q, %q", rev, note)
			}
			for i, call := range readGitCalls(t, log) {
				if !slices.Contains(call, "config") {
					t.Errorf("git call %d ran without isolation: %q", i, call)
				}
			}
		})
	}
}

// TestTraversalSavedGraph는 저장 문서를 쓰면 revision을 싣지 않고, dispatchEvidence
// 표시 없는 옛 문서는 등급 없이 한계를 싣는지 본다.
func TestTraversalSavedGraph(t *testing.T) {
	dir := traversalFixture(t)
	file := filepath.Join(t.TempDir(), "g.json")
	if code, _, errb := run(t, "graph", "--level", "symbol", "--dir", dir, "--out", file); code != 0 {
		t.Fatalf("graph: %s", errb)
	}
	_, out, _ := run(t, "reach", "--graph", file, fx+".handleUsers")
	doc := parseTraversal(t, out)
	if doc.Revision != nil || evidenceByUsr(doc)[fx+".(sqlStore).List"] != "candidate" {
		t.Fatalf("saved graph traversal wrong: %s", out)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["dispatchEvidence"] != true {
		t.Fatal("graph document must carry the dispatchEvidence marker")
	}
	delete(raw, "dispatchEvidence")
	stripped, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	legacy := string(stripped)
	if err := os.WriteFile(file, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ = run(t, "reach", "--graph", file, fx+".handleUsers")
	doc = parseTraversal(t, out)
	for usr, ev := range evidenceByUsr(doc) {
		if ev != "" {
			t.Fatalf("legacy document must not classify evidence: %s = %s", usr, ev)
		}
	}
	var unassessed bool
	for _, lim := range doc.Limitations {
		unassessed = unassessed || strings.HasPrefix(lim, "evidence-unassessed:")
	}
	if !unassessed {
		t.Fatalf("missing evidence-unassessed limitation: %v", doc.Limitations)
	}
	if code, _, _ := run(t, "graph", "--level", "package", "--dir", dir, "--out", file); code != 0 {
		t.Fatal("package graph")
	}
	if code, _, _ := run(t, "reach", "--graph", file, fx); code != 2 {
		t.Fatal("package-level saved graph must be rejected with 2")
	}
}
