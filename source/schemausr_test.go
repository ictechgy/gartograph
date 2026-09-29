// schema 사실 symbol.usr 귀속 테스트 — usr가 impact와 같은 심볼 그래프의 정점 ID이고,
// 수확이 간선 출발점으로 쓰는 선언에 귀속되는지 fixture 모듈로 확인한다.
package source

import (
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// usrFixture는 귀속 규칙의 경우를 모두 담은 모듈이다.
func usrFixture(t *testing.T) string {
	t.Helper()
	return testutil.WriteModule(t, map[string]string{
		"store/store.go": `package store

import "database/sql"

var db *sql.DB

type Repo struct{}

// 포인터 리시버 메서드 — ID는 리시버 포인터를 벗긴 (Repo)다.
func (r *Repo) List() { db.Query("SELECT * FROM repo_t") }

// 클로저 안의 사실은 감싸는 함수에 귀속한다.
func Each() {
	f := func() { db.Exec("DELETE FROM closure_t") }
	f()
}

// 패키지 변수 초기화식 — 그 변수 정점이다.
var countQuery = "SELECT count(*) FROM var_t"

// 이름과 값이 짝지어진 다중 선언 — 값마다 그 이름이다.
var qa, qb = "SELECT * FROM qa_t", "SELECT * FROM qb_t"

// 모듈 심볼을 쓰는 빈 선언 — 패키지 초기화 루트(pkg._)다.
var _ = register("SELECT * FROM blank_t")

func register(q string) string { return q }

// 태그는 타입 정점, TableName 리터럴은 그 메서드 정점이다.
type User struct {
	Email string ` + "`db:\"email\"`" + `
}

func (User) TableName() string { return "users" }

// 제네릭 리시버 — ID는 타입 파라미터 없는 (Box)다.
type Box[T any] struct{}

func (Box[T]) Get() { db.Query("SELECT * FROM box_t") }

func init() { db.Exec("CREATE TABLE IF NOT EXISTS init_t (id int)") }

// 빈 함수는 정점이 없다 — usr 없이 센다.
func _() { db.Query("SELECT * FROM blankfn_t") }

// 패키지 경로 store.v와 겹치는 심볼 — 충돌 접미사가 붙은 ID다.
func v() { db.Query("SELECT * FROM coll_t") }

func use() { _ = countQuery; _ = qa; _ = qb; v() }
`,
		// 심볼 ID example.com/fixture/store.v와 겹치는 패키지 경로.
		"store.v/p.go": "package storev\n\nfunc P() {}\n",
		// 모듈 심볼을 쓰지 않는 빈 선언만 있는 패키지 — pkg._ 정점이 없다.
		"other/other.go": `package other

import "strings"

var _ = strings.ToUpper("select * from nomod_t")
`,
		// exclude로 뺀 패키지 — 정점이 없어 usr를 싣지 않는다.
		"excl/excl.go": `package excl

import "database/sql"

func Run(db *sql.DB) { db.Query("SELECT * FROM excl_t") }
`,
	})
}

// channelUsrs는 비동적 사실의 channel → usr(없으면 빈 문자열) 표다.
func channelUsrs(t *testing.T, doc *BridgeFactsDocument) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range doc.Facts {
		fact := f.(RelationFact)
		key := fact.Channel
		if fact.Method != "" {
			key += "|" + fact.Method
		}
		usr := ""
		if fact.Symbol != nil {
			usr = fact.Symbol.Usr
			if fact.Symbol.QualifiedName == "" {
				t.Fatalf("symbol without qualifiedName: %+v", fact)
			}
		}
		if prev, ok := out[key]; ok && prev != usr {
			t.Fatalf("channel %q has two owners %q and %q", key, prev, usr)
		}
		out[key] = usr
	}
	return out
}

// TestSchemaUsrAttribution은 선언 종류별 귀속과 빠진 신원의 계수를 확인한다.
func TestSchemaUsrAttribution(t *testing.T) {
	dir := usrFixture(t)
	doc, err := SchemaFacts(Options{Dir: dir, Exclude: []string{"excl"}}, "test")
	if err != nil {
		t.Fatalf("SchemaFacts: %v", err)
	}
	const s = "example.com/fixture/store"
	want := map[string]string{
		"repo_t":      s + ".(Repo).List",
		"closure_t":   s + ".Each",
		"var_t":       s + ".countQuery",
		"qa_t":        s + ".qa",
		"qb_t":        s + ".qb",
		"blank_t":     s + "._",
		"users|email": s + ".User",
		"users":       s + ".(User).TableName",
		"box_t":       s + ".(Box).Get",
		"init_t":      s + ".init",
		"coll_t":      s + ".v" + graph.CollisionSuffix,
		"blankfn_t":   "",
		"nomod_t":     "",
		"excl_t":      "",
	}
	got := channelUsrs(t, doc)
	for channel, usr := range want {
		if g, ok := got[channel]; !ok || g != usr {
			t.Errorf("channel %q: usr = %q (present %v), want %q", channel, g, ok, usr)
		}
	}
	if got := missingUsrCount(doc); got != 3 {
		t.Fatalf("missing-relation-usrs = %d, want 3: %v", got, doc.Limitations)
	}
}

// TestSchemaUsrsAreGraphVertices는 모든 usr가 같은 옵션으로 수확한 impact 그래프의
// 심볼 정점인지 확인한다 — trace가 문자열 일치로 잇는 계약의 핵심이다.
func TestSchemaUsrsAreGraphVertices(t *testing.T) {
	dir := usrFixture(t)
	for _, exclude := range [][]string{nil, {"excl"}} {
		doc, err := SchemaFacts(Options{Dir: dir, Exclude: exclude}, "test")
		if err != nil {
			t.Fatalf("SchemaFacts: %v", err)
		}
		g, err := Load(Options{Dir: dir, Level: graph.LevelSymbol, Exclude: exclude})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		kinds := map[string]graph.VertexKind{}
		for _, v := range g.Vertices {
			kinds[v.ID] = v.Kind
		}
		withUsr := 0
		for _, f := range doc.Facts {
			fact := f.(RelationFact)
			if fact.Symbol == nil {
				continue
			}
			withUsr++
			kind, ok := kinds[fact.Symbol.Usr]
			if !ok || kind == graph.KindPackage || kind == graph.KindModule {
				t.Errorf("usr %q is not a symbol vertex of the impact graph (kind %q)", fact.Symbol.Usr, kind)
			}
		}
		if withUsr == 0 {
			t.Fatal("fixture must yield facts with usr")
		}
		// exclude가 없으면 excl 패키지 사실도 신원을 가진다.
		if exclude == nil && channelUsrs(t, doc)["excl_t"] != "example.com/fixture/excl.Run" {
			t.Fatalf("excl_t without exclude must carry excl.Run: %v", doc.Facts)
		}
	}
}

// missingUsrCount는 missing-relation-usrs limitation의 수를 읽는다(없으면 0).
func missingUsrCount(doc *BridgeFactsDocument) int {
	for _, lim := range doc.Limitations {
		if rest, ok := strings.CutPrefix(lim, "missing-relation-usrs: "); ok {
			n := 0
			for _, c := range rest {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int(c-'0')
			}
			return n
		}
	}
	return 0
}
