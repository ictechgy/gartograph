package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// routesFixture는 ServeMux 서버 하나와 .gartograph.yml exclude를 담은 모듈이다.
func routesFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"app/app.go": `package app

import "net/http"

type Server struct{}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {}

func New() http.Handler {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", s.get)
	return mux
}
`,
		"gen/gen.go": `package gen

import "net/http"

func h(w http.ResponseWriter, r *http.Request) {}

func Register() { http.HandleFunc("/generated", h) }
`,
		".gartograph.yml": "components:\n  app: [\"app\"]\ndeps: {}\nexclude: [\"gen\"]\n",
	})
}

// TestRoutes는 routes가 http route-decl 문서를 내고, exclude 패키지의 핸들러는 usr 없이 세는지 본다.
func TestRoutes(t *testing.T) {
	dir := routesFixture(t)
	code, out, errb := run(t, "routes", "--role", "server", "--dir", dir, "--generated-at", "2026-01-01T00:00:00Z",
		"--service", "items-api")
	if code != 0 {
		t.Fatalf("routes failed: %d %s", code, errb)
	}
	var doc struct {
		Format, Platform, Target, Dispatch, Service, GeneratedAt string
		Roles                                                    []string
		Facts                                                    []struct {
			Method, Channel, PathAnchor string
			Symbol                      *struct{ Usr string }
		}
		Limitations []string
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if doc.Format != "bridge-facts" || doc.Platform != "go" || doc.Target != "http" || doc.Dispatch != "specificity" ||
		doc.Service != "items-api" || doc.GeneratedAt != "2026-01-01T00:00:00.000Z" || len(doc.Roles) != 1 {
		t.Fatalf("bad envelope: %s", out)
	}
	if len(doc.Facts) != 2 {
		t.Fatalf("facts = %+v", doc.Facts)
	}
	for _, f := range doc.Facts {
		switch f.Channel {
		case "/items/{}":
			if f.Method != "GET" || f.Symbol == nil || f.Symbol.Usr != "example.com/fixture/app.(Server).get" {
				t.Errorf("items fact = %+v", f)
			}
		case "/generated":
			if f.Symbol != nil {
				t.Errorf("an excluded package is not in the impact graph; usr must be omitted: %+v", f.Symbol)
			}
		default:
			t.Errorf("unexpected fact %+v", f)
		}
	}
	if !strings.Contains(strings.Join(doc.Limitations, "\n"), "missing-route-usrs: 1 ") {
		t.Errorf("limitations = %v", doc.Limitations)
	}
}

// TestRoutesOutAndUsage는 --out 파일 쓰기와 사용법 오류(2)를 본다.
func TestRoutesOutAndUsage(t *testing.T) {
	dir := routesFixture(t)
	path := filepath.Join(t.TempDir(), "routes.json")
	if code, _, errb := run(t, "routes", "--dir", dir, "--out", path); code != 0 {
		t.Fatalf("routes --out failed: %s", errb)
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), `"target": "http"`) {
		t.Fatalf("--out file = %q, %v", data, err)
	}
	for _, args := range [][]string{
		{"routes", "--role", "client", "--dir", dir},
		{"routes", "--generated-at", "yesterday", "--dir", dir},
		{"routes", "--bogus"},
	} {
		if code, _, _ := run(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
