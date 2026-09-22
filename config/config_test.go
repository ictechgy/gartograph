package config

import (
	"os"
	"path/filepath"
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
