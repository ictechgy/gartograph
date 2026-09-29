package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
