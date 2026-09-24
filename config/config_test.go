package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
