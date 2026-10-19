package config

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// writeRules는 규칙 파일을 임시 디렉터리에 쓰고 경로를 돌려준다.
func writeRules(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".gartograph.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoad는 정상 파일과 비정상 파일을 구분해 읽는지 확인한다.
func TestLoad(t *testing.T) {
	f, err := Load(writeRules(t, `components:
  web: ["web/**"]
  db: ["db"]
deps:
  web: ["db"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Components) != 2 || f.Deps["web"][0] != "db" {
		t.Fatalf("unexpected config: %+v", f)
	}

	if _, err := Load(writeRules(t, "deps: {}\n")); err == nil {
		t.Fatal("empty components must be an error")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yml")); err == nil {
		t.Fatal("missing file must be an error")
	}
}

// TestComponentOf는 패턴 우선순위를 확인한다 — 더 긴 패턴이 구체적 규칙이다.
func TestComponentOf(t *testing.T) {
	f := &File{Components: map[string][]string{
		"app": {"app/**"},
		"kit": {"app/kit/**", "kit"},
	}}
	cases := map[string]string{
		"app/web":   "app",
		"app/kit/x": "kit", // app/**보다 app/kit/**가 길어 이긴다
		"kit":       "kit",
		"app/kit":   "kit",
		"other":     "",
		"apple":     "", // app/**는 app 접두사가 아니라 app/ 접두사다
	}
	for path, want := range cases {
		got, ok := f.ComponentOf(path)
		if want == "" {
			if ok {
				t.Fatalf("%s: expected unmapped, got %s", path, got)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("%s: expected %s, got %s", path, want, got)
		}
	}
}

// TestComponentOfTie는 같은 길이 패턴이 맞을 때 결정적 우승자가 있는지
// 확인한다 — 맵 순회에 맡기면 같은 설정이 실행마다 다른 컴포넌트를 고른다.
func TestComponentOfTie(t *testing.T) {
	f := &File{Components: map[string][]string{
		"beta":  {"x/**"},
		"alpha": {"x/**"},
	}}
	for i := 0; i < 20; i++ {
		if got, ok := f.ComponentOf("x/y"); !ok || got != "alpha" {
			t.Fatalf("tie must resolve deterministically to alpha, got %s", got)
		}
	}
}

// TestComponentOfGlob은 `*` 세그먼트 글롭이 `/`를 넘지 않는지 확인한다.
// `app/*`가 `app/web/x`까지 먹으면 재귀 접두사 `**`와 구분이 없어진다.
func TestComponentOfGlob(t *testing.T) {
	f := &File{Components: map[string][]string{
		"flat": {"app/*"},
		"deep": {"*"},
	}}
	if got, ok := f.ComponentOf("app/web"); !ok || got != "flat" {
		t.Fatalf("app/web: expected flat, got %s %v", got, ok)
	}
	if _, ok := f.ComponentOf("app/web/x"); ok {
		t.Fatal("app/* must not cross a segment boundary")
	}
	if got, ok := f.ComponentOf("solo"); !ok || got != "deep" {
		t.Fatalf("solo: expected deep, got %s %v", got, ok)
	}
	// 중간에 `*`가 있는 세그먼트 — 접두사와 접미사가 동시에 맞아야 한다.
	f.Components["gen"] = []string{"gen/*-x"}
	if got, ok := f.ComponentOf("gen/a-x"); !ok || got != "gen" {
		t.Fatalf("mid-glob must match: %s %v", got, ok)
	}
	if _, ok := f.ComponentOf("gen/ax"); ok {
		t.Fatal("mid-glob must not match a shorter name")
	}
}

// linearComponentOf는 색인 전의 ComponentOf다 — 모든 컴포넌트의 모든 패턴을 graph.MatchPath로
// 맞추고 더 긴 패턴, 길이가 같으면 더 작은 이름을 고른다. 느려도 대조 기준으로 그대로 둔다.
func linearComponentOf(components map[string][]string, relPath string) (string, bool) {
	best, bestLen := "", -1
	for name, pats := range components {
		for _, pat := range pats {
			if !graph.MatchPath(pat, relPath) {
				continue
			}
			if len(pat) > bestLen || (len(pat) == bestLen && name < best) {
				best, bestLen = name, len(pat)
			}
		}
	}
	return best, best != ""
}

// tiedNames는 경로에 맞는 가장 긴 패턴을 가진 서로 다른 컴포넌트 수다 — 2 이상이면
// 이름 순 tie-break가 답을 정한다.
func tiedNames(components map[string][]string, relPath string) int {
	bestLen, names := -1, map[string]bool{}
	for name, pats := range components {
		for _, pat := range pats {
			switch {
			case !graph.MatchPath(pat, relPath) || len(pat) < bestLen:
			case len(pat) > bestLen:
				bestLen, names = len(pat), map[string]bool{name: true}
			default:
				names[name] = true
			}
		}
	}
	return len(names)
}

// TestComponentOfMatchesLinearScan은 색인한 해석이 모든 패턴을 훑던 선형 해석과 같은 컴포넌트를
// 고르는지 무작위 설정으로 확인한다 — 정확 일치·`x/**`·글롭이 한 경로에 함께 맞고, 같은 길이
// 패턴이 여러 이름에 걸리는 tie-break(빈 이름 포함)까지 같아야 한다. Load를 거치지 않은 File은
// 검증 전 패턴(`?`, 중간 `**`, `/**`, 빈 세그먼트)도 담을 수 있어 그것도 섞는다.
func TestComponentOfMatchesLinearScan(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	segments := []string{"a", "b", "ab", "*", "a*", "*b", "**", "?", "", "."}
	randomPath := func(maxSegments int) string {
		parts := make([]string, 1+rng.Intn(maxSegments))
		for i := range parts {
			parts[i] = segments[rng.Intn(len(segments))]
		}
		return strings.Join(parts, "/")
	}
	// pathNear는 패턴에서 경로를 만든다 — `*`를 채우고 `/**` 아래로 내려가야 무작위 경로보다
	// 훨씬 자주 맞고, 여러 패턴이 한 경로에 겹친다.
	pathNear := func(pattern string) string {
		path, recursive := strings.CutSuffix(pattern, "/**")
		path = strings.ReplaceAll(path, "*", []string{"", "a", "ab"}[rng.Intn(3)])
		if recursive && rng.Intn(2) == 0 {
			path += "/" + randomPath(2)
		}
		return path
	}
	names := []string{"", "a", "b", "c", "ab"}
	var matched, unmatched, ties int
	for round := 0; round < 400; round++ {
		components := map[string][]string{}
		var patterns []string
		for n := rng.Intn(6); n >= 0; n-- {
			name := names[rng.Intn(len(names))]
			for k := rng.Intn(3); k >= 0; k-- {
				pattern := randomPath(3)
				if rng.Intn(3) == 0 {
					pattern += "/**"
				}
				// 다른 이름이 같은 패턴을 갖게 해 같은 길이 후보(tie)를 자주 만든다.
				if len(patterns) > 0 && rng.Intn(4) == 0 {
					pattern = patterns[rng.Intn(len(patterns))]
				}
				components[name] = append(components[name], pattern)
				patterns = append(patterns, pattern)
			}
		}
		literal := &File{Components: components}
		for q := 0; q < 50; q++ {
			path := randomPath(4)
			if q%2 == 0 {
				path = pathNear(patterns[rng.Intn(len(patterns))])
			}
			wantName, wantOK := linearComponentOf(components, path)
			if gotName, gotOK := literal.ComponentOf(path); gotName != wantName || gotOK != wantOK {
				t.Fatalf("components %q, path %q: ComponentOf = (%q, %v), linear scan = (%q, %v)",
					components, path, gotName, gotOK, wantName, wantOK)
			}
			if wantOK {
				matched++
			} else {
				unmatched++
			}
			if tiedNames(components, path) > 1 {
				ties++
			}
		}
	}
	t.Logf("matched %d, unmatched %d, ties %d", matched, unmatched, ties)
	if matched < 5000 || unmatched < 5000 || ties < 1000 {
		t.Fatalf("random configs must exercise matches, misses and ties: %d matched, %d unmatched, %d ties",
			matched, unmatched, ties)
	}
}

// TestLoadIndexesComponentPatterns는 Load가 정확 일치·`x/**` 패턴을 경로로 바로 찾는 색인에
// 담고 글롭만 하나씩 맞추게 남기는지 확인한다 — init이 만든 "패키지당 `rel/**`" 설정에서 경로
// 하나를 해석할 때 모든 패턴을 훑으면 비용이 컴포넌트 수에 비례해, 2만 패키지 문서의
// rules·metrics·mapping이 각각 10~16초 걸렸다. 답은 선형 해석과 같아야 한다.
func TestLoadIndexesComponentPatterns(t *testing.T) {
	var b strings.Builder
	b.WriteString("components:\n  root: [\".\"]\n  gen: [\"gen/*\"]\n  p007x: [\"p007/xyz\"]\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "  p%03d: [\"p%03d/**\"]\n", i, i)
	}
	f, err := Load(writeRules(t, b.String()))
	if err != nil {
		t.Fatal(err)
	}
	idx := f.components
	if idx == nil {
		t.Fatal("Load must build the component index")
	}
	if len(idx.exact) != 2 || len(idx.recursive) != 300 || len(idx.globs) != 1 {
		t.Fatalf("Load must index exact and \"dir/**\" patterns and leave only the glob to scan, "+
			"got %d exact, %d recursive, %d globs", len(idx.exact), len(idx.recursive), len(idx.globs))
	}
	// 더 긴 패턴이 이긴다 — "p007/xyz"(8자)는 "p007/**"(7자)를 이기지만 그 아래 경로는 못 맞춘다.
	for path, want := range map[string]string{
		".": "root", "gen/x": "gen", "p007": "p007", "p007/xyz": "p007x", "p007/xyz/q": "p007",
		"p123/a/b": "p123", "p299": "p299", "p300": "", "p1234/x": "", "gen/x/y": "",
	} {
		got, ok := f.ComponentOf(path)
		if got != want || ok != (want != "") {
			t.Errorf("ComponentOf(%q) = (%q, %v), want %q", path, got, ok, want)
		}
		if linear, linearOK := linearComponentOf(f.Components, path); got != linear || ok != linearOK {
			t.Errorf("ComponentOf(%q) = (%q, %v), linear scan = (%q, %v)", path, got, ok, linear, linearOK)
		}
	}
}

// BenchmarkComponentOfScaffold는 init이 만든 모양(패키지당 `rel/**` 하나)의 2만 컴포넌트 설정을
// 읽고 2만 경로를 모두 해석한다 — 경로마다 모든 패턴을 훑으면 한 번에 4억 번 비교다.
func BenchmarkComponentOfScaffold(b *testing.B) {
	const n = 20000
	components := make(map[string][]string, n)
	paths := make([]string, n)
	for i := range paths {
		paths[i] = fmt.Sprintf("p%05d", i)
		components[paths[i]] = []string{paths[i] + "/**"}
	}
	data, err := Render(&File{Components: components})
	if err != nil {
		b.Fatal(err)
	}
	rules := filepath.Join(b.TempDir(), ".gartograph.yml")
	if err := os.WriteFile(rules, data, 0o644); err != nil {
		b.Fatal(err)
	}
	f, err := Load(rules)
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		for _, p := range paths {
			if name, ok := f.ComponentOf(p + "/sub"); !ok || name != p {
				b.Fatalf("%s/sub resolved to %q", p, name)
			}
		}
	}
}

// TestAllowed는 허용 목록 의미론을 확인한다 — 자기 의존은 항상 허용이다.
func TestAllowed(t *testing.T) {
	f := &File{Deps: map[string][]string{"web": {"db"}}}
	if !f.Allowed("web", "db") {
		t.Fatal("listed dep must be allowed")
	}
	if f.Allowed("db", "web") {
		t.Fatal("unlisted dep must be denied")
	}
	if !f.Allowed("web", "web") {
		t.Fatal("self dep must always be allowed")
	}
	if f.Allowed("ghost", "db") {
		t.Fatal("component without deps entry may depend on nothing")
	}
	// deny가 자기 자신을 가리켜도 자기 의존은 금지가 성립하지 않는다 —
	// 컴포넌트 내부 import는 규칙 밖이다.
	f.Deny = map[string][]DenyEntry{"web": {{To: "web"}}}
	if _, denied := f.Denied("web", "web"); denied {
		t.Fatal("self deny must not apply")
	}
	if _, denied := f.Denied("web", "db"); denied {
		t.Fatal("deny key exists but target must match")
	}
}

// TestDenyEntryForms는 deny 항목의 스칼라/맵 두 형태를 확인한다.
func TestDenyEntryForms(t *testing.T) {
	f, err := Load(writeRules(t, `components:
  web: ["web"]
  db: ["db"]
deny:
  web:
    - db
    - {to: other, reason: "use db instead"}
`))
	// other가 정의되지 않았으므로 참조 검사에서 걸려야 한다 — 먼저 추가한다.
	if err == nil {
		t.Fatal("undefined deny target must fail")
	}
	f, err = Load(writeRules(t, `components:
  web: ["web"]
  db: ["db"]
  other: ["other"]
deny:
  web:
    - db
    - {to: other, reason: "use db instead"}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Deny["web"]) != 2 {
		t.Fatalf("deny entries: %+v", f.Deny)
	}
	if f.Deny["web"][0].To != "db" || f.Deny["web"][0].Reason != "" {
		t.Fatalf("scalar deny entry: %+v", f.Deny["web"][0])
	}
	if f.Deny["web"][1].To != "other" || f.Deny["web"][1].Reason != "use db instead" {
		t.Fatalf("map deny entry: %+v", f.Deny["web"][1])
	}
	if reason, denied := f.Denied("web", "other"); !denied || reason != "use db instead" {
		t.Fatalf("Denied must carry the reason: %q %v", reason, denied)
	}
}

// TestCheckRefs는 정의되지 않은 컴포넌트 참조가 Load 오류인지 확인한다.
// 죽은 규칙을 허용하면 "규칙이 있다"는 착각을 만든다.
func TestCheckRefs(t *testing.T) {
	cases := []string{
		"components: {a: [a]}\ndeps: {ghost: []}\n",
		"components: {a: [a]}\ndeps: {a: [ghost]}\n",
		"components: {a: [a]}\nvisibleTo: {a: [ghost]}\n",
		"components: {a: [a]}\ncommon: [ghost]\n",
		"components: {a: [a]}\nforbidden: [{from: a, to: ghost}]\n",
		"components: {a: [a]}\nforbidden: [{from: a, to: a}]\n", // 자기 자신 금지는 무의미
		// 빈 이름도 참조다 — reason만 있는 deny는 조용히 아무것도
		// 금지하지 않으므로 설정 오류로 드러내야 한다.
		"components: {a: [a]}\ndeps: {a: []}\ndeny: {a: [{reason: nope}]}\n",
		"components: {a: [a]}\nforbidden: [{to: a}]\n",
		"components: {a: [a]}\ndeps: {\"\": [a]}\n",
		"components: {a: [a]}\nindependent: [a, ghost]\n",
		"components: {a: [a]}\nindependent: [a, \"\"]\n",
	}
	for _, c := range cases {
		if _, err := Load(writeRules(t, c)); err == nil {
			t.Fatalf("must reject dead reference:\n%s", c)
		}
	}
}

// TestVisibleTo는 공급자 측 허용 목록 의미론을 확인한다.
func TestVisibleTo(t *testing.T) {
	f := &File{VisibleTo: map[string][]string{"db": {"api"}}}
	if !f.Visible("api", "db") || f.Visible("web", "db") {
		t.Fatal("visibleTo must restrict consumers to the list")
	}
	if !f.Visible("db", "db") {
		t.Fatal("self visibility is always allowed")
	}
	if !f.Visible("web", "core") {
		t.Fatal("no visibleTo entry means unrestricted")
	}
	// 빈 목록은 아무도 못 본다 — deps의 빈 허용 목록과 같은 의미론이다.
	f.VisibleTo["sealed"] = []string{}
	if f.Visible("web", "sealed") {
		t.Fatal("empty visibleTo list seals the component")
	}
}

// TestSignatureAllowed는 시그니처 규칙의 의미론을 확인한다 —
// 키가 없으면 검사하지 않고, 있으면 목록만 통과한다.
func TestSignatureAllowed(t *testing.T) {
	f := &File{Signature: map[string][]string{"api": {"core"}}}
	if !f.SignatureAllowed("web", "db") {
		t.Fatal("no signature entry means not checked")
	}
	if !f.SignatureAllowed("api", "core") || f.SignatureAllowed("api", "db") {
		t.Fatal("signature list must be an allowlist")
	}
	if !f.SignatureAllowed("api", "api") {
		t.Fatal("self reference is always allowed")
	}
}

// TestRender는 init 출력이 결정적이고 다시 읽히는지 확인한다.
func TestRender(t *testing.T) {
	f := &File{
		Components: map[string][]string{"b": {"b/**"}, "a": {"a/**"}},
		Deps:       map[string][]string{"a": {"b"}, "b": {}},
	}
	data, err := Render(f)
	if err != nil {
		t.Fatal(err)
	}
	// 맵 키는 정렬되어 나와야 한다 — 결정적 출력 계약.
	s := string(data)
	ia, ib := strings.Index(s, "  a:"), strings.Index(s, "  b:")
	if ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("components must be sorted:\n%s", s)
	}
	// 렌더된 파일이 Load를 통과하는지 — 생성→검사 라운드트립.
	path := filepath.Join(t.TempDir(), ".gartograph.yml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatalf("rendered file must load: %v", err)
	}
	if len(back.Components) != 2 || len(back.Deps["a"]) != 1 {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
}

// TestFind는 두 확장자 후보와 없음을 확인한다.
func TestFind(t *testing.T) {
	dir := t.TempDir()
	if _, ok := Find(dir); ok {
		t.Fatal("no rules file must report not found")
	}
	path := filepath.Join(dir, ".gartograph.yaml")
	if err := os.WriteFile(path, []byte("components: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := Find(dir); !ok || got != path {
		t.Fatalf("expected to find %s, got %s", path, got)
	}
}

// TestMatchFile은 파일 규칙의 패턴 의미론을 확인한다 —
// `/` 없는 패턴은 파일명에, 있는 패턴은 상대 경로에, `!`는 반전이다.
func TestMatchFile(t *testing.T) {
	cases := []struct {
		pattern, rel string
		want         bool
	}{
		{"*_test.go", "web/web_test.go", true},
		{"*_test.go", "web/web.go", false},
		{"!*_test.go", "web/web.go", true},
		{"!*_test.go", "web/web_test.go", false},
		{"web/**", "web/sub/x.go", true},
		{"web/**", "db/x.go", false},
		{"!internal/**", "api/x.go", true},
	}
	for _, c := range cases {
		if got := MatchFile(c.pattern, c.rel); got != c.want {
			t.Fatalf("MatchFile(%q, %q) = %v, want %v",
				c.pattern, c.rel, got, c.want)
		}
	}
}

// TestFileRulesValidation은 fileRules의 참조·이름 검증을 확인한다.
// 이름이 없거나 겹치면 baseline 동일성과 보고 식별이 무너진다.
func TestFileRulesValidation(t *testing.T) {
	bad := []string{
		// 빈 규칙 이름
		`components: {a: ["a"], b: ["b"]}
fileRules: [{name: "", from: "!*_test.go", to: "b"}]`,
		// 중복 이름
		`components: {a: ["a"], b: ["b"]}
fileRules:
  - {name: r, from: "!*_test.go", to: "b"}
  - {name: r, from: "gen/**", to: "b"}`,
		// 미정의 to 컴포넌트
		`components: {a: ["a"]}
fileRules: [{name: r, from: "!*_test.go", to: "ghost"}]`,
		// 빈 from 패턴
		`components: {a: ["a"], b: ["b"]}
fileRules: [{name: r, from: "", to: "b"}]`,
	}
	for _, c := range bad {
		if _, err := Load(writeRules(t, c)); err == nil {
			t.Fatalf("must reject: %s", c)
		}
	}
	// 정상 설정은 통과한다.
	if _, err := Load(writeRules(t, `components: {a: ["a"], b: ["b"]}
fileRules: [{name: no-testdeps, from: "!*_test.go", to: "b", reason: "test only"}]`)); err != nil {
		t.Fatalf("valid fileRules rejected: %v", err)
	}
}

// TestExcluded는 exclude 패턴의 경로 의미론과 로드 시점 글롭 검증을
// 확인한다 — 문법이 깨진 패턴은 영원히 아무것도 매칭하지 않는 죽은
// 설정이라 Load가 거부해야 한다.
func TestExcluded(t *testing.T) {
	cfg := &File{Exclude: []string{"gen/**", "vendor", "internal/*/gen"}}
	if !cfg.Excluded("gen/foo/bar") || !cfg.Excluded("vendor") ||
		!cfg.Excluded("internal/x/gen") {
		t.Fatal("exclude patterns must match their paths")
	}
	if cfg.Excluded("pkg/util") {
		t.Fatal("unrelated path must not be excluded")
	}
	if _, err := Load(writeRules(t,
		"components: {a: [a]}\nexclude: ['[']\n")); err == nil ||
		!strings.Contains(err.Error(), "exclude") {
		t.Fatal("invalid exclude glob must be rejected at Load")
	}
	// 빈 패턴도 같은 이유로 거부다.
	if _, err := Load(writeRules(t,
		"components: {a: [a]}\nexclude: ['']\n")); err == nil {
		t.Fatal("empty exclude pattern must be rejected at Load")
	}
}

// TestLoadRejectsUnsupportedPatterns는 components·exclude·fileRules.from의 패턴을 로드 시점에
// MatchPath 문법으로 검증하는지 확인한다. path.Match만 보던 exclude는 `?`·`[...]`를, 검증이
// 없던 components·fileRules는 무엇이든 받아서 영원히 맞지 않는 규칙이 rules --strict를 조용히
// 통과시켰다. 오류는 섹션·항목·패턴과 지원 문법을 담아야 고칠 곳이 보인다.
func TestLoadRejectsUnsupportedPatterns(t *testing.T) {
	sections := []struct {
		name   string
		config func(pattern string) string
		where  string // 오류가 가리켜야 할 섹션과 항목
	}{
		{"components", func(p string) string {
			return "components: {a: [\"" + p + "\"], b: [b]}\n"
		}, `components "a": pattern`},
		{"exclude", func(p string) string {
			return "components: {a: [a]}\nexclude: [gen, \"" + p + "\"]\n"
		}, "exclude: entry 1"},
		{"fileRules", func(p string) string {
			return "components: {a: [a], b: [b]}\nfileRules: [{name: r, from: \"" + p + "\", to: b}]\n"
		}, `fileRules "r": from pattern`},
	}
	for _, s := range sections {
		for _, p := range []string{"app/**/*.go", "app/su?", "a/[ab]", "**", "a/*_*"} {
			_, err := Load(writeRules(t, s.config(p)))
			if err == nil {
				t.Errorf("%s: Load accepted %q, which MatchPath can never match as written", s.name, p)
				continue
			}
			if msg := err.Error(); !strings.Contains(msg, s.where) || !strings.Contains(msg, `"`+p+`"`) ||
				!strings.Contains(msg, "supported patterns") {
				t.Errorf("%s %q: error must name the entry, the pattern, and the supported syntax: %v",
					s.name, p, err)
			}
		}
		for _, p := range []string{"app/**", "*_test.go", "internal/*/api", "cli", "github.com/aws/**"} {
			if _, err := Load(writeRules(t, s.config(p))); err != nil {
				t.Errorf("%s: supported pattern %q rejected: %v", s.name, p, err)
			}
		}
	}
	// fileRules의 앞 `!` 하나는 반전 표시라 떼고 검증한다 — 이 저장소의 "!*_test.go"가 그대로
	// 읽혀야 하고, 반전이 지원하지 않는 패턴을 가려서도 안 된다.
	negated := func(from string) string {
		return "components: {a: [a], b: [b]}\nfileRules: [{name: r, from: \"" + from + "\", to: b}]\n"
	}
	if _, err := Load(writeRules(t, negated("!*_test.go"))); err != nil {
		t.Errorf("negated file pattern rejected: %v", err)
	}
	if _, err := Load(writeRules(t, negated("!app/**/*.go"))); err == nil {
		t.Error("negation must not hide an unsupported pattern")
	}
}

// TestLimitsValidation은 limits 항목의 검증을 확인한다 —
// 정의되지 않은 컴포넌트·빈 상한·음수는 죽은 설정이라 로드 시점에 거부한다.
func TestLimitsValidation(t *testing.T) {
	cases := []string{
		"components: {a: [a]}\nlimits: [{component: ghost, maxOut: 1}]\n",
		"components: {a: [a]}\nlimits: [{component: a}]\n",            // 상한 없음
		"components: {a: [a]}\nlimits: [{component: a, maxIn: -1}]\n", // 음수
		"components: {a: [a]}\nlimits: [{maxOut: 2}]\n",               // 컴포넌트 없음
		// 같은 상한의 중복 — baseline 키가 충돌해 baseline이 구분 못 한다.
		"components: {a: [a]}\nlimits: [{component: a, maxOut: 1}, {component: a, maxOut: 2}]\n",
		"components: {a: [a]}\nlimits: [{component: a, maxIn: 0, maxIn: 0}]\n",
	}
	for _, c := range cases {
		if _, err := Load(writeRules(t, c)); err == nil {
			t.Fatalf("must reject bad limit:\n%s", c)
		}
	}
	// 0은 유효한 상한이다 — "아무것도 의존하지 말라"를 표현할 수 있어야 한다.
	f, err := Load(writeRules(t,
		"components: {a: [a]}\nlimits: [{component: a, maxIn: 0, maxOut: 0}]\n"))
	if err != nil {
		t.Fatalf("zero limits must load: %v", err)
	}
	if len(f.Limits) != 1 || *f.Limits[0].MaxIn != 0 || *f.Limits[0].MaxOut != 0 {
		t.Fatalf("limits must round-trip: %+v", f.Limits)
	}
}

// TestLoadRejectsNonRegular는 규칙 파일이 정규 파일(또는 그것을 가리키는 링크)이 아니면
// 읽지 않고 원인을 담은 오류를 내는지 확인한다. .gartograph.yml은 분석 대상 저장소가 고르는
// 경로라 /dev/zero 링크(끝없는 읽기)나 FIFO(open에서 무한 대기)일 수 있다 — 장치는
// /dev/null로 대신 검증한다. 회귀하면 /dev/zero는 테스트 프로세스의 메모리를 다 쓴다.
func TestLoadRejectsNonRegular(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.yml")
	testutil.MakeFIFO(t, fifo)
	device := filepath.Join(dir, "device.yml")
	if err := os.Symlink(os.DevNull, device); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{device, t.TempDir(), fifo} {
		var err error
		testutil.FinishWithin(t, testutil.BlockDeadline, func() { _, err = Load(path) })
		if err == nil || !strings.Contains(err.Error(), "is not a regular file") ||
			!strings.Contains(err.Error(), path) {
			t.Fatalf("%s: want a not-a-regular-file error naming the path, got %v", path, err)
		}
	}
	// 링크를 따라간 대상이 정규 파일이면 지금처럼 읽는다 — 공유 설정을 링크로 두는 저장소가 있다.
	link := filepath.Join(dir, ".gartograph.yml")
	if err := os.Symlink(writeRules(t, "components: {a: [a]}\n"), link); err != nil {
		t.Fatal(err)
	}
	if f, err := Load(link); err != nil || len(f.Components) != 1 {
		t.Fatalf("a link to a regular rules file must load: %+v %v", f, err)
	}
}

// TestLoadSizeLimit은 규칙 파일을 상한까지만 읽는지 확인한다. 상한은 --roots-from과 같은
// 16 MiB다 — init이 큰 저장소에서 만든 설정은 받고, 그보다 큰 파일은 읽다가 메모리를 다
// 쓰기 전에 거부한다.
func TestLoadSizeLimit(t *testing.T) {
	head := "components: {a: [a]}\n"
	atLimit := writeRules(t, head+strings.Repeat("\n", maxRulesFileSize-len(head)))
	if _, err := Load(atLimit); err != nil {
		t.Fatalf("a rules file of exactly %d bytes must load: %v", maxRulesFileSize, err)
	}
	over := filepath.Join(t.TempDir(), ".gartograph.yml")
	if err := os.WriteFile(over, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(over, maxRulesFileSize+1); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(over); err == nil || !strings.Contains(err.Error(), "larger than 16 MiB") {
		t.Fatalf("an oversized rules file must be rejected with its limit, got %v", err)
	}
}
