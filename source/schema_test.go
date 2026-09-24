// schema 수확의 단위 테스트 — 순수 함수(SQL 렉서·태그 읽기·타입 이름)는
// 표로 검증하고, 패키지 귀속은 fixture 모듈로 SchemaFacts까지 검증한다.
package source

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestSQLRelations는 SQL 텍스트에서 관계 이름을 읽는 규칙을 검증한다.
func TestSQLRelations(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{"단순 FROM", "SELECT * FROM users", []string{"users"}},
		{"한정 이름", "SELECT * FROM public.users", []string{"public.users"}},
		{"JOIN", "SELECT * FROM a JOIN b ON a.id = b.id", []string{"a", "b"}},
		{"INSERT INTO", "INSERT INTO logs (id) VALUES (1)", []string{"logs"}},
		{"UPDATE", "UPDATE accounts SET x = 1", []string{"accounts"}},
		{"DELETE FROM", "DELETE FROM sessions WHERE id = 1", []string{"sessions"}},
		{"TABLE 수식", "ALTER TABLE orders ADD COLUMN x int", []string{"orders"}},
		{"TRUNCATE", "TRUNCATE TABLE events", []string{"events"}},
		{"TRUNCATE 단독", "TRUNCATE events", []string{"events"}},
		// `table`을 수식어로 건너뛰는 것은 TRUNCATE 뒤에서만이다 —
		// UPDATE에서는 table이 진짜 관계 이름이다.
		{"UPDATE의 table 관계", "UPDATE table SET x = 1", []string{"table"}},
		{"FROM의 table 관계", "SELECT * FROM table", []string{"table"}},
		{"인용된 table", `SELECT * FROM "table"`, []string{"table"}},
		{"쉼표 목록", "SELECT * FROM a, b, c.d", []string{"a", "b", "c.d"}},
		{"ONLY 수식어", "SELECT * FROM ONLY users", []string{"users"}},
		{"IF NOT EXISTS", "CREATE TABLE IF NOT EXISTS t (id int)", []string{"t"}},
		// 서브쿼리 괄호는 이름이 아니므로 다음 식별자까지 건너뛰지 않는다.
		{"서브쿼리", "SELECT * FROM (SELECT id FROM inner_t) x JOIN outer_t ON true",
			[]string{"inner_t", "outer_t"}},
		// 인용 식별자 안의 점은 한정자가 아니라 이름의 일부다.
		{"점 있는 인용명", `SELECT * FROM "odd.name"`, []string{"odd%2Ename"}},
		{"인용 한정", `SELECT * FROM "my schema"."my table"`,
			[]string{"my schema.my table"}},
		{"백틱 인용", "SELECT * FROM `mysql.users`", []string{"mysql%2Eusers"}},
		{"대괄호 인용", "SELECT * FROM [dbo].[users]", []string{"dbo.users"}},
		{"행 주석 뒤 FROM", "SELECT 1 -- comment\nFROM real_table",
			[]string{"real_table"}},
		{"블록 주석 안 키워드", "SELECT /* FROM fake */ * FROM real_t",
			[]string{"real_t"}},
		{"문자열 속 키워드", "SELECT 'not FROM fake' FROM actual",
			[]string{"actual"}},
		{"대소문자 무관", "select * from MixedCase", []string{"MixedCase"}},
		{"관계 없음", "SELECT 1", nil},
		{"빈 문자열", "", nil},
		{"끝나지 않은 인용", `SELECT * FROM "unterminated`, []string{"unterminated"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sqlRelations(tc.sql)
			if len(got) != len(tc.want) {
				t.Fatalf("sqlRelations(%q) = %v, want %v", tc.sql, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("sqlRelations(%q)[%d] = %q, want %q",
						tc.sql, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestLexSQL은 어휘 분할의 경계 조건을 검증한다.
func TestLexSQL(t *testing.T) {
	t.Run("인용·기호·식별자", func(t *testing.T) {
		tokens := lexSQL(`a.b, "q.q" (x)`)
		var texts []string
		for _, tok := range tokens {
			texts = append(texts, tok.text)
		}
		want := []string{"a", ".", "b", ",", "q.q", "(", "x", ")"}
		if strings.Join(texts, "|") != strings.Join(want, "|") {
			t.Fatalf("tokens = %v, want %v", texts, want)
		}
		if tokens[4].quoted != true {
			t.Fatal("quoted segment must be marked")
		}
	})
	t.Run("닫히지 않은 블록 주석", func(t *testing.T) {
		if got := lexSQL("a /* never ends"); len(got) != 1 {
			t.Fatalf("unterminated comment must swallow the rest: %v", got)
		}
	})
	t.Run("닫히지 않은 행 주석", func(t *testing.T) {
		if got := lexSQL("a -- rest"); len(got) != 1 {
			t.Fatalf("line comment must swallow the rest: %v", got)
		}
	})
	t.Run("escape된 작은따옴표", func(t *testing.T) {
		if got := lexSQL("a 'it''s' b"); len(got) != 2 {
			t.Fatalf("escaped quote must not end the literal: %v", got)
		}
	})
}

// TestReadQualifiedName은 한정 이름 읽기의 종료 조건을 검증한다.
func TestReadQualifiedName(t *testing.T) {
	t.Run("범위 밖", func(t *testing.T) {
		if name, _ := readQualifiedName(nil, 0); name != "" {
			t.Fatalf("empty input must yield empty name, got %q", name)
		}
	})
	t.Run("빈 인용 식별자", func(t *testing.T) {
		if name, _ := readQualifiedName([]sqlToken{{text: "", quoted: true}}, 0); name != "" {
			t.Fatalf("empty quoted name must be rejected, got %q", name)
		}
	})
	t.Run("비식별자 시작", func(t *testing.T) {
		if name, _ := readQualifiedName([]sqlToken{{text: "("}}, 0); name != "" {
			t.Fatalf("symbol must not start a name, got %q", name)
		}
	})
	t.Run("끝의 점은 붙지 않는다", func(t *testing.T) {
		// `a.` 뒤에 이름이 없으면 `a`만 읽는다.
		name, next := readQualifiedName(
			[]sqlToken{{text: "a"}, {text: "."}, {text: "("}}, 0)
		if name != "a" || next != 1 {
			t.Fatalf("name=%q next=%d, want a/1", name, next)
		}
	})
}

// TestTagColumn은 struct 태그 원문에서 컬럼 이름을 읽는 규칙을 검증한다.
func TestTagColumn(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"db 태그", "`db:\"email\"`", "email"},
		{"db 옵션", "`db:\"email,omitempty\"`", "email"},
		{"sql 태그", "`sql:\"user_id\"`", "user_id"},
		{"gorm column", "`gorm:\"column:nick_name;primaryKey\"`", "nick_name"},
		{"gorm column 대소문자", "`gorm:\"COLUMN:Nick\"`", "Nick"},
		{"db 우선", "`gorm:\"column:g\" db:\"d\"`", "d"},
		{"무시 컬럼", "`db:\"-\"`", "-"},
		{"키 없음", "`json:\"name\"`", ""},
		{"gorm에 column 없음", "`gorm:\"primaryKey\"`", ""},
		{"깨진 태그", "`db:\"unclosed`", ""},
		{"따옴표 없는 값", "`db:x`", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tagColumn(tc.raw); got != tc.want {
				t.Fatalf("tagColumn(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestStructTagGet은 태그 키 조회의 경계 조건을 검증한다.
func TestStructTagGet(t *testing.T) {
	cases := []struct {
		name string
		tag  string
		key  string
		want string
	}{
		{"두 번째 키", `a:"1" b:"2"`, "b", "2"},
		{"escape된 따옴표", `a:"x\"y" b:"2"`, "a", `x"y`},
		{"닫히지 않은 값", `a:"x`, "a", ""},
		{"콜론 없음", `abc`, "a", ""},
		{"키가 따옴표로 시작", `:"v"`, "a", ""},
		{"빈 태그", ``, "a", ""},
		{"앞 공백", `   k:"v"`, "k", "v"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := structTagGet(tc.tag, tc.key); got != tc.want {
				t.Fatalf("structTagGet(%q, %q) = %q, want %q",
					tc.tag, tc.key, got, tc.want)
			}
		})
	}
}

// parseExpr는 타입 표현 테스트용으로 Go 식 하나를 파싱한다.
func parseExpr(t *testing.T, src string) ast.Expr {
	t.Helper()
	expr, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("ParseExpr(%q): %v", src, err)
	}
	return expr
}

// TestModelTypeName은 모델 인자 포장 벗기기를 검증한다.
func TestModelTypeName(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"&T{}", "T"},
		{"&[]T{}", "T"},
		{"new(T)", "T"},
		{"[]*T{}", "T"},
		{"&pkg.T{}", ""}, // 다른 패키지 타입은 로컬 TableName과 묶이지 않는다
		{"f(T)", ""},     // new가 아닌 호출
		{"new()", ""},    // 인자 없음
		{"*T{}", ""},     // 단항 포장만 벗긴다 — CompositeLit 안의 *T는 typeExprName이 읽는다
		{"-x", ""},       // &가 아닌 단항
		{"x", ""},        // 값 식별자는 모델이 아니다
	}
	for _, tc := range cases {
		if got := modelTypeName(parseExpr(t, tc.src)); got != tc.want {
			t.Fatalf("modelTypeName(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestReceiverTypeName은 수신자 타입 이름 읽기를 검증한다.
func TestReceiverTypeName(t *testing.T) {
	if got := receiverTypeName(parseExpr(t, "*T")); got != "T" {
		t.Fatalf("star receiver = %q, want T", got)
	}
	if got := receiverTypeName(parseExpr(t, "T")); got != "T" {
		t.Fatalf("value receiver = %q, want T", got)
	}
	if got := receiverTypeName(parseExpr(t, "pkg.T")); got != "" {
		t.Fatalf("qualified receiver = %q, want empty", got)
	}
}

// TestSingleStringReturn은 리터럴 반환 몸체 판정을 검증한다.
func TestSingleStringReturn(t *testing.T) {
	parseBody := func(src string) *ast.BlockStmt {
		t.Helper()
		f, err := parser.ParseFile(token.NewFileSet(), "x.go",
			"package x\nfunc f() string {"+src+"}", 0)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var body *ast.BlockStmt
		ast.Inspect(f, func(n ast.Node) bool {
			if decl, ok := n.(*ast.FuncDecl); ok {
				body = decl.Body
			}
			return body == nil
		})
		return body
	}
	lit, ok := singleStringReturn(parseBody(`return "users"`))
	if !ok || lit.Value != `"users"` {
		t.Fatal("single string return must be detected")
	}
	if _, ok := singleStringReturn(parseBody(`return "a"; return "b"`)); ok {
		t.Fatal("two statements must be rejected")
	}
	if _, ok := singleStringReturn(parseBody(`return 42`)); ok {
		t.Fatal("non-string literal must be rejected")
	}
	if _, ok := singleStringReturn(parseBody(`return name`)); ok {
		t.Fatal("non-literal return must be rejected")
	}
}

// TestSchemaFactsTags는 struct 태그와 TableName 바인딩의 귀속을 검증한다.
func TestSchemaFactsTags(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"model.go": `package fixture

type User struct {
	Email string ` + "`db:\"email\"`" + `
	Name  string ` + "`gorm:\"column:nick;size:64\"`" + `
	Skip  string ` + "`db:\"-\"`" + `
}

func (User) TableName() string { return "users" }

// 태그는 있는데 TableName이 없는 구조체 — 귀속 불가로 센다.
type Orphan struct {
	ID int ` + "`db:\"id\"`" + `
}
`,
	})
	doc, err := SchemaFacts(dir, "test")
	if err != nil {
		t.Fatalf("SchemaFacts: %v", err)
	}
	var found map[string]bool
	found = map[string]bool{}
	for _, f := range doc.Facts {
		fact := f.(RelationFact)
		found[fact.Channel+"|"+fact.Method] = true
	}
	for _, want := range []string{"users|email", "users|nick", "users|"} {
		if !found[want] {
			t.Fatalf("missing fact %q in %v", want, doc.Facts)
		}
	}
	var hasUnattributed bool
	for _, lim := range doc.Limitations {
		if strings.HasPrefix(lim, "unattributed-column-tags: 1") {
			hasUnattributed = true
		}
	}
	if !hasUnattributed {
		t.Fatalf("expected unattributed-column-tags limitation: %v", doc.Limitations)
	}
	if doc.SourceModifiedAt == "" {
		t.Fatal("sourceModifiedAt must reflect scanned files")
	}
}

// TestSchemaFactsDynamic는 비리터럴 SQL 인자와 비DB 호출 구분을 검증한다.
func TestSchemaFactsDynamic(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "database/sql"

var db *sql.DB

type store struct{}

func (store) Query(q string) {} // DB가 아닌 같은 이름의 메서드

func main() {
	db.Query("SELECT * FROM users")
	db.Query("SELECT * FROM " + "orders")
	var s store
	s.Query("SELECT * FROM fake_table")
}
`,
	})
	doc, err := SchemaFacts(dir, "test")
	if err != nil {
		t.Fatalf("SchemaFacts: %v", err)
	}
	relations := map[string]bool{}
	var dynamics []string
	for _, f := range doc.Facts {
		fact := f.(RelationFact)
		if fact.Dynamic {
			dynamics = append(dynamics, fact.Channel)
		} else {
			relations[fact.Channel] = true
		}
	}
	if !relations["users"] {
		t.Fatalf("missing users relation in %v", doc.Facts)
	}
	// 비DB 수신자의 "SELECT" 리터럴도 스캔된다 — 리터럴 자체가 관계 참조다.
	if !relations["fake_table"] {
		t.Fatal("literal scan is receiver-independent")
	}
	// `"..." + "..."` 연결은 리터럴이 아니므로 동적 사실로 보존된다.
	if len(dynamics) != 1 || !strings.Contains(dynamics[0], "+") {
		t.Fatalf("dynamics = %v, want one concatenated expr", dynamics)
	}
	var hasDynamicLimitation bool
	for _, lim := range doc.Limitations {
		if strings.HasPrefix(lim, "unjoined-dynamic-relations: 1") {
			hasDynamicLimitation = true
		}
	}
	if !hasDynamicLimitation {
		t.Fatalf("expected dynamic limitation: %v", doc.Limitations)
	}
}

// TestSchemaFactsEmpty는 관계 참조가 없는 모듈이 target null 문서를 내는지
// 확인한다 — 계약상 빈 facts와 null target은 짝이다.
func TestSchemaFactsEmpty(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nfunc main() { println(\"hi\") }\n",
	})
	doc, err := SchemaFacts(dir, "test")
	if err != nil {
		t.Fatalf("SchemaFacts: %v", err)
	}
	if len(doc.Facts) != 0 {
		t.Fatalf("facts = %v, want empty", doc.Facts)
	}
	if doc.Target != nil {
		t.Fatalf("target = %v, want nil for empty facts", doc.Target)
	}
}

// TestSchemaFactsBadDir는 존재하지 않는 디렉터리가 오류를 돌려주는지 본다.
func TestSchemaFactsBadDir(t *testing.T) {
	if _, err := SchemaFacts("/no/such/dir/gartograph-test", "test"); err == nil {
		t.Fatal("missing dir must return an error")
	}
}

// TestSchemaFactsGorm은 replace로 심은 gorm 스텁으로 타입이 확인된
// Table·Model·Raw·AutoMigrate 호출의 수확을 검증한다.
func TestSchemaFactsGorm(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		// 로컬 스텁 — 시그니처만 흉내 내면 수신자 타입 확인이 그대로 작동한다.
		"go.mod": `module example.com/fixture

go 1.27

require gorm.io/gorm v0.0.0

replace gorm.io/gorm => ./gormstub
`,
		"gormstub/go.mod": "module gorm.io/gorm\n\ngo 1.27\n",
		"gormstub/gorm.go": `package gorm

type DB struct{}

func (d *DB) Table(name string) *DB                    { return d }
func (d *DB) Model(v any) *DB                          { return d }
func (d *DB) Raw(q string, args ...any) *DB            { return d }
func (d *DB) Exec(q string, args ...any) *DB           { return d }
func (d *DB) AutoMigrate(v ...any) error               { return nil }
`,
		"main.go": `package main

import "gorm.io/gorm"

var gdb *gorm.DB

type User struct{ ID int }

func (User) TableName() string { return "users" }

// TableName이 없는 모델 — 귀속 불가로 센다.
type Orphan struct{ ID int }

// gorm과 같은 모양의 메서드를 가진 로컬 타입 — 수신자 타입 확인이
// 이름만 같은 호출을 걸러내는지 보는 음성 대조다.
type fakeDB struct{}

func (fakeDB) Table(name string) {}

func main() {
	var name string
	var fake fakeDB
	gdb.Table("widgets")                 // 인자가 곧 관계 이름
	gdb.Table(name)                      // 비리터럴 — 동적 사실
	gdb.Model(&User{})                   // TableName으로 해석
	gdb.Model(&Orphan{})                 // 바인딩 없음 — unresolved
	gdb.Model(compute())                 // 타입 이름 없음 — unresolved
	gdb.AutoMigrate(&User{})             // 마이그레이션도 관계 참조
	gdb.Raw("SELECT * FROM raw_t")       // SQL 인자
	gdb.Exec("DELETE FROM exec_t")       // SQL 인자
	fake.Table(other)                    // gorm이 아닌 수신자 — 무시
}

func compute() any { return nil }

var other string
`,
	})
	doc, err := SchemaFacts(dir, "test")
	if err != nil {
		t.Fatalf("SchemaFacts: %v", err)
	}
	channels := map[string]bool{}
	var sawDynamic bool
	for _, f := range doc.Facts {
		fact := f.(RelationFact)
		channels[fact.Channel] = true
		if fact.Dynamic && fact.Channel == "name" {
			sawDynamic = true
		}
	}
	for _, want := range []string{"widgets", "users", "raw_t", "exec_t"} {
		if !channels[want] {
			t.Fatalf("missing channel %q in %v", want, doc.Facts)
		}
	}
	if !sawDynamic {
		t.Fatalf("Table(name) must be a dynamic fact: %v", doc.Facts)
	}
	// 음성 대조 — fakeDB.Table의 수신자는 gorm이 아니므로 인자가
	// 동적 사실로도 남으면 안 된다.
	for _, f := range doc.Facts {
		if f.(RelationFact).Channel == "other" {
			t.Fatalf("non-gorm receiver leaked a fact: %v", doc.Facts)
		}
	}
	// users는 TableName 리터럴 + Model + AutoMigrate로 여러 번 관측된다 —
	// 위치가 달라 사실은 별개다.
	var unresolved int
	for _, lim := range doc.Limitations {
		if strings.HasPrefix(lim, "unresolved-model-types: 2") {
			unresolved = 1
		}
	}
	if unresolved == 0 {
		t.Fatalf("expected unresolved-model-types limitation: %v", doc.Limitations)
	}
}

// TestSchemaFactsEdges는 스캔의 보조 경로를 검증한다 — 파싱 실패 집계,
// 긴 동적 인자 절단, 인자 부족·비셀렉터 호출, TableName 변형.
func TestSchemaFactsEdges(t *testing.T) {
	// 120자를 넘는 동적 표현식 — channel 절단 경로를 연다.
	longArg := strings.Repeat("query + ", 20) + "query"
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": `package main

import "database/sql"

var db *sql.DB

// 수신자 없는 TableName은 바인딩이 아니다.
func TableName() string { return "loose" }

// 리터럴이 아닌 반환도 바인딩이 아니다.
type Namer struct{ name string }

func (n Namer) TableName() string { return n.name }

func run(query string) {
	db.Query()            // 인자 부족 — 읽을 SQL이 없다
	db.Exec("DELETE FROM jobs")
	db.Query(` + longArg + `)
	noop("SELECT * FROM via_plain_call") // 비셀렉터 호출
}

func noop(string) {}

func main() { run("SELECT 1") }
`,
		// 별도 패키지에 두어 main의 타입 정보가 깨지지 않게 한다.
		"broken/broken.go": "package broken\n\nfunc broken( {\n",
	})
	doc, err := SchemaFacts(dir, "test")
	if err != nil {
		t.Fatalf("SchemaFacts: %v", err)
	}
	channels := map[string]bool{}
	var sawTruncated bool
	for _, f := range doc.Facts {
		fact := f.(RelationFact)
		channels[fact.Channel] = true
		if fact.Dynamic && strings.HasSuffix(fact.Channel, "...") {
			sawTruncated = true
		}
	}
	for _, want := range []string{"jobs", "via_plain_call"} {
		if !channels[want] {
			t.Fatalf("missing channel %q in %v", want, doc.Facts)
		}
	}
	if !sawTruncated {
		t.Fatalf("expected a truncated dynamic channel in %v", doc.Facts)
	}
	// `loose`·`n.name` 같은 비바인딩 반환은 사실이 되면 안 된다.
	if channels["loose"] || channels["n.name"] {
		t.Fatalf("non-binding TableName leaked: %v", doc.Facts)
	}
	var hasUnparsed bool
	for _, lim := range doc.Limitations {
		if strings.HasPrefix(lim, "unparsed-sources:") {
			hasUnparsed = true
		}
	}
	if !hasUnparsed {
		t.Fatalf("expected unparsed-sources limitation: %v", doc.Limitations)
	}
}
