// schema 수확의 단위 테스트 — 순수 함수(SQL 렉서·태그 읽기·타입 이름)는
// 표로 검증하고, 패키지 귀속은 fixture 모듈로 SchemaFacts까지 검증한다.
package source

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

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
			got, _, _ := sqlRelations(tc.sql)
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
	// `;`는 문장 경계 기호이고, 자리를 여는 자리표시자 기호는 관계 자리에서 미해석으로 세도록 표시된다.
	// 닫는 `}`는 기호일 뿐이다. `$` 뒤에 숫자가 오면 위치 매개변수(자리표시자)이고, 그 밖의 `$`로 시작하는
	// 이름은 지금처럼 식별자다.
	t.Run("문장 경계·자리표시자", func(t *testing.T) {
		tokens := lexSQL("a; %s {} ? :n @v $12 $x # *")
		var texts, placeholders []string
		for _, tok := range tokens {
			texts = append(texts, tok.text)
			if tok.placeholder {
				placeholders = append(placeholders, tok.text)
			}
		}
		want := []string{"a", ";", "%", "s", "{", "}", "?", ":", "n", "@", "v", "$12", "$x"}
		if strings.Join(texts, "|") != strings.Join(want, "|") {
			t.Fatalf("tokens = %v, want %v", texts, want)
		}
		wantPlaceholders := []string{"%", "{", "?", ":", "@", "$12"}
		if strings.Join(placeholders, "|") != strings.Join(wantPlaceholders, "|") {
			t.Fatalf("placeholders = %v, want %v", placeholders, wantPlaceholders)
		}
		if isNameToken(tokens[11]) || !isNameToken(tokens[12]) {
			t.Fatalf("$12 must not be a name, $x must stay one: %+v %+v", tokens[11], tokens[12])
		}
	})
	// `:`·`@`·`%`는 이름·동사가 바로 붙어야 자리다 — `::` 캐스트, 산문 콜론, 나머지 연산자, `%%`는
	// 목록을 끝내는 기호일 뿐 미해석이 아니다.
	t.Run("떨어진 기호는 자리표시자가 아니다", func(t *testing.T) {
		tokens := lexSQL("a::int b: c 5 % 2 @ x 100%% y:")
		var texts []string
		for _, tok := range tokens {
			texts = append(texts, tok.text)
			if tok.placeholder {
				t.Fatalf("%q must not open a placeholder in %v", tok.text, tokens)
			}
		}
		want := []string{"a", ":", ":", "int", "b", ":", "c", "%", "@", "x", "%", "%", "y", ":"}
		if strings.Join(texts, "|") != strings.Join(want, "|") {
			t.Fatalf("tokens = %v, want %v", texts, want)
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
	t.Run("절 키워드는 관계 이름이 아니다", func(t *testing.T) {
		// `DO UPDATE SET`·`FROM {} WHERE`의 SET·WHERE가 관계 이름으로 새지 않는다.
		for _, word := range []string{"SET", "where", "Lateral", "the"} {
			if name, _ := readQualifiedName([]sqlToken{{text: word}}, 0); name != "" {
				t.Fatalf("clause word %q must not start a name, got %q", word, name)
			}
		}
		// `table`은 UPDATE table SET의 진짜 관계 이름일 수 있어 절 키워드가 아니다.
		if name, _ := readQualifiedName([]sqlToken{{text: "table"}}, 0); name != "table" {
			t.Fatalf("table must stay a name, got %q", name)
		}
	})
	t.Run("점 뒤의 예약어는 이름의 일부다", func(t *testing.T) {
		// 한정 이름의 점 뒤는 예약어여도 식별자다(PostgreSQL·MySQL) — 절 키워드 거르기는 첫 세그먼트만 본다.
		name, next := readQualifiedName(
			[]sqlToken{{text: "shop"}, {text: "."}, {text: "order"}}, 0)
		if name != "shop.order" || next != 3 {
			t.Fatalf("name=%q next=%d, want shop.order/3", name, next)
		}
	})
	t.Run("자리표시자는 이름이 아니다", func(t *testing.T) {
		if name, _ := readQualifiedName([]sqlToken{{text: "$1", placeholder: true}}, 0); name != "" {
			t.Fatalf("placeholder must not start a name, got %q", name)
		}
	})
}

// sqlRelationsSharedCases는 계열 공통 SQL 관계 사례(DECISIONS D2, design/sql-relations-cases.json)다.
// 7개 생산자의 추출 테스트와 isthmus conformance/sql-relations.json 벡터가 같은 SQL 문자열과 같은 기대 집합을
// 쓴다 — 벡터를 벤더링하기 전까지는 각 생산자가 같은 사례를 테스트로 고정하므로 id·SQL·기대값을 고치지 않는다.
// 규칙은 R1 함수 인자 안의 FROM, R2 문장 단위 CTE 이름, R3 IS [NOT] DISTINCT FROM이다.
var sqlRelationsSharedCases = []struct {
	id        string
	sql       string
	relations []string
}{
	{"cte/simple", "WITH recent AS (SELECT * FROM orders) SELECT * FROM recent", []string{"orders"}},
	{"cte/recursive-with-columns", "WITH RECURSIVE tree(id, parent_id) AS (SELECT id, parent_id FROM nodes UNION ALL SELECT n.id, n.parent_id FROM nodes n JOIN tree t ON n.parent_id = t.id) SELECT * FROM tree", []string{"nodes"}},
	{"cte/multiple-materialized", "WITH a AS (SELECT 1), b AS MATERIALIZED (SELECT * FROM orders) SELECT * FROM a JOIN b ON true", []string{"orders"}},
	{"cte/not-materialized", "WITH c AS NOT MATERIALIZED (SELECT * FROM orders) SELECT * FROM c", []string{"orders"}},
	{"cte/insert-select", "WITH staged AS (SELECT * FROM imports) INSERT INTO orders SELECT * FROM staged", []string{"imports", "orders"}},
	{"cte/scope-is-one-statement", "WITH recent AS (SELECT 1) SELECT * FROM recent; SELECT * FROM recent", []string{"recent"}},
	{"function-from/extract", "SELECT EXTRACT(YEAR FROM created_at) FROM orders", []string{"orders"}},
	{"function-from/trim", "SELECT TRIM(BOTH ' ' FROM name) FROM users", []string{"users"}},
	{"function-from/substring", "SELECT SUBSTRING(name FROM 2 FOR 3) FROM users", []string{"users"}},
	{"subquery/derived-table-still-fires", "SELECT * FROM (SELECT * FROM orders) AS sub", []string{"orders"}},
	{"subquery/scalar-still-fires", "SELECT (SELECT max(id) FROM users) FROM orders", []string{"users", "orders"}},
	{"subquery/in-select-still-fires", "SELECT * FROM orders WHERE id IN (SELECT order_id FROM payments)", []string{"orders", "payments"}},
	{"distinct-from/is", "SELECT * FROM events WHERE a IS DISTINCT FROM b", []string{"events"}},
	{"distinct-from/is-not", "SELECT * FROM events WHERE a IS NOT DISTINCT FROM b", []string{"events"}},
}

// sortedNames는 관계 목록을 순서와 무관하게 비교하려고 정렬한 사본을 `|`로 잇는다. 중복은 남겨 둔다 —
// 이 추출기는 한 텍스트에서 같은 이름을 한 번만 내므로 중복이 있으면 그 자체로 실패여야 한다.
func sortedNames(names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return strings.Join(sorted, "|")
}

// TestSQLRelationsSharedCases는 계열 공통 사례를 순서와 무관한 집합으로 비교한다 — CTE 이름, 함수 인자 안의
// FROM(EXTRACT·TRIM·SUBSTRING), IS [NOT] DISTINCT FROM의 피연산자는 관계가 아니고, 하위 질의 안의 FROM은
// 그대로 관계다. isthmus는 관계로 나온 이름을 선언과 잇지 못하면 relation-use-without-decl error를 낸다.
func TestSQLRelationsSharedCases(t *testing.T) {
	for _, tc := range sqlRelationsSharedCases {
		t.Run(tc.id, func(t *testing.T) {
			got, _, _ := sqlRelations(tc.sql)
			if sortedNames(got) != sortedNames(tc.relations) {
				t.Fatalf("sqlRelations(%q) = %v, want the set %v", tc.sql, got, tc.relations)
			}
		})
	}
}

// TestSQLRelationsPortedRules는 공통 사례 밖에서 이 추출기가 지키는 규칙을 검증한다 — tsograph에서 옮긴 절
// 키워드 게이트·자리표시자 계수·GRANT/REVOKE 문장 판정, 세 규칙의 경계(대소문자 접기, 한정 이름, 문장 범위,
// 함수 인자 안의 하위 질의), 그리고 바뀌면 안 되는 기존 판독(UPDATE table, DELETE … RETURNING CTE 본문,
// 테이블 값 함수 미해석). functions는 테이블 값 함수 피연산자 수, placeholders는 관계 자리의 자리표시자 수다.
func TestSQLRelationsPortedRules(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sql          string
		want         []string
		functions    int
		placeholders int
	}{
		// 자리표시자는 관계 이름이 아니라 실행 때 채워지는 자리다 — 미해석으로 센다.
		{"fmt 동사", "DELETE FROM %s WHERE id = $1", nil, 0, 1},
		{"중괄호", "SELECT * FROM {} WHERE id = 1", nil, 0, 1},
		{"물음표", "SELECT * FROM ? WHERE id = 1", nil, 0, 1},
		{"위치 매개변수", "SELECT * FROM $1", nil, 0, 1},
		{"이름 매개변수", "SELECT * FROM :tbl JOIN @other ON true", nil, 0, 2},
		{"템플릿", "SELECT * FROM {{.Table}} WHERE id = $1", nil, 0, 1},
		// 템플릿 안의 Table은 관계 키워드로 읽혀도 닫는 `}`에서 멈춘다 — 구멍을 두 번 세지 않고 별칭 t를
		// 관계로 읽지 않는다.
		{"템플릿 뒤 별칭", "SELECT * FROM {{.Table}} t JOIN {table} u ON true", nil, 0, 2},
		{"자리표시자 한정자", "SELECT * FROM %s.users", nil, 0, 1},
		{"자리표시자 세그먼트", "SELECT * FROM public.%s", nil, 0, 1},
		{"쉼표 뒤 자리표시자", "SELECT * FROM a, %s", []string{"a"}, 0, 1},
		// 절 키워드는 관계 이름 자리에서 목록을 끝낸다 — 세지 않는다.
		{"upsert의 DO UPDATE SET", "INSERT INTO t (id) VALUES (1) ON CONFLICT (id) DO UPDATE SET x = 1", []string{"t"}, 0, 0},
		{"LATERAL", "SELECT * FROM t, LATERAL f(x) AS l", []string{"t"}, 0, 0},
		{"산문", "update the cache from the database", nil, 0, 0},
		// 산문 콜론은 `:name` 자리가 아니다 — 관계도 미해석도 아니다.
		{"산문 콜론", "internal error: update: pointer already set", nil, 0, 0},
		{"GRANT UPDATE ON", "GRANT UPDATE ON orders TO bob", nil, 0, 0},
		{"점 뒤 예약어", "SELECT * FROM shop.order", []string{"shop.order"}, 0, 0},
		// GRANT·REVOKE 문장의 FROM은 권한 주체 자리다. 판정은 그 문장에만 미친다.
		{"REVOKE", "REVOKE SELECT ON orders FROM bob", nil, 0, 0},
		{"GRANT", "GRANT SELECT ON orders TO bob", nil, 0, 0},
		{"REVOKE 다음 문장", "REVOKE SELECT ON orders FROM bob; SELECT * FROM audit", []string{"audit"}, 0, 0},
		// R3: DISTINCT FROM의 피연산자는 자리표시자여도 관계 자리가 아니다. IS 없는 DISTINCT는 그대로다.
		{"DISTINCT FROM 위치 매개변수", "SELECT * FROM events WHERE kind IS DISTINCT FROM $1", []string{"events"}, 0, 0},
		{"SELECT DISTINCT", "SELECT DISTINCT * FROM t", []string{"t"}, 0, 0},
		// R2: 비인용 이름은 대소문자를 접고 인용 이름은 그대로 비교한다. 한정 이름은 CTE가 아니다.
		{"CTE 대소문자", "WITH Recent AS (SELECT 1) SELECT * FROM RECENT", nil, 0, 0},
		{"인용 CTE와 비인용 피연산자", `WITH "recent" AS (SELECT 1) SELECT * FROM Recent`, nil, 0, 0},
		{"인용 대소문자", `WITH "Recent" AS (SELECT 1) SELECT * FROM recent`, []string{"recent"}, 0, 0},
		{"인용 피연산자", `WITH recent AS (SELECT 1) SELECT * FROM "Recent"`, []string{"Recent"}, 0, 0},
		{"한정 이름은 CTE가 아니다", "WITH recent AS (SELECT 1) SELECT * FROM s.recent", []string{"s.recent"}, 0, 0},
		// 공통 규칙은 문장 단위라, 자기 본문에서 같은 이름의 실제 테이블을 읽는 CTE도 그 이름을 내지 않는다
		// (README에 적은 알려진 손실이다 — 계열 생산자가 같은 판정을 내야 isthmus 조인이 언어와 무관하다).
		{"같은 이름 테이블을 가리는 CTE", "WITH users AS (SELECT * FROM users WHERE active) SELECT * FROM users", nil, 0, 0},
		// R1: 함수 인자 안의 하위 질의는 관계를 낸다. 닫히지 않은 함수 괄호는 다음 문장을 삼키지 않는다.
		{"함수 인자 안 하위 질의", "SELECT COALESCE((SELECT max(id) FROM users), 0) FROM orders", []string{"users", "orders"}, 0, 0},
		{"ARRAY 하위 질의", "SELECT ARRAY(SELECT id FROM users)", []string{"users"}, 0, 0},
		{"OVERLAY", "SELECT OVERLAY(name PLACING 'x' FROM 2 FOR 3) FROM users", []string{"users"}, 0, 0},
		{"닫히지 않은 함수 괄호", "SELECT EXTRACT(YEAR FROM d; SELECT * FROM t", []string{"t"}, 0, 0},
		// 보존: CTE 본문 괄호는 함수 인자가 아니다(AS는 절 키워드, MATERIALIZED는 R2가 본문으로 안다).
		{"DELETE CTE", "WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", []string{"t"}, 0, 0},
		{"MATERIALIZED DELETE CTE", "WITH d AS MATERIALIZED (DELETE FROM t RETURNING *) SELECT * FROM d", []string{"t"}, 0, 0},
		{"NOT MATERIALIZED DELETE CTE", "WITH d AS NOT MATERIALIZED (DELETE FROM t RETURNING *) SELECT * FROM d", []string{"t"}, 0, 0},
		{"테이블 값 함수", "SELECT * FROM pragma_table_info('t') p JOIN users ON true", []string{"users"}, 1, 0},
		{"테이블 값 함수 별칭 뒤 쉼표", "SELECT * FROM pragma_table_info('t') p, users", []string{"users"}, 1, 0},
		{"UPDATE table", "UPDATE table SET x = 1", []string{"table"}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, functions, placeholders := sqlRelations(tc.sql)
			if sortedNames(got) != sortedNames(tc.want) || functions != tc.functions || placeholders != tc.placeholders {
				t.Fatalf("sqlRelations(%q) = %v, functions %d, placeholders %d; want %v, %d, %d",
					tc.sql, got, functions, placeholders, tc.want, tc.functions, tc.placeholders)
			}
		})
	}
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
	doc, err := SchemaFacts(Options{Dir: dir}, "test")
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
	doc, err := SchemaFacts(Options{Dir: dir}, "test")
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
	doc, err := SchemaFacts(Options{Dir: dir}, "test")
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
	if _, err := SchemaFacts(Options{Dir: "/no/such/dir/gartograph-test"}, "test"); err == nil {
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
	doc, err := SchemaFacts(Options{Dir: dir}, "test")
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
	doc, err := SchemaFacts(Options{Dir: dir}, "test")
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

// 테이블 값 함수는 테이블 신원으로 보고하지 않고 뒤의 실제 테이블은 유지한다.
func TestSQLTableValuedFunctions(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want []string
	}{
		{"SELECT * FROM pragma_table_info('t')", nil},
		{"SELECT * FROM users JOIN main.pragma_table_info('t') p ON true", []string{"users"}},
		{"SELECT * FROM pragma_table_info('t') AS p, users", []string{"users"}},
		{"SELECT * FROM pragma_table_info('t') p JOIN users ON true", []string{"users"}},
	} {
		got, unresolved, placeholders := sqlRelations(tc.sql)
		if unresolved != 1 || placeholders != 0 {
			t.Fatalf("unresolved = %d, placeholders = %d, want 1 and 0", unresolved, placeholders)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("got %v, want %v", got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		}
	}
}

// 함수 의존 공백의 생산자 문구는 원인을 설명하는 데 남는다 — isthmus는 이 문구가 아니라
// scanLiteral이 내는 dynamic 사실을 직접 센다. 문구는 비리터럴 인자 수(dynamic)의 문구와 갈린다.
func TestSQLFunctionLimitations(t *testing.T) {
	scan := &schemaScan{sqlFunctions: 1}
	limitations := scan.limitations()
	if len(limitations) != 1 || !strings.HasPrefix(limitations[0],
		"unjoined-dynamic-relations: 1 table-valued SQL function operand(s)") {
		t.Fatalf("missing SQL coverage evidence: %v", limitations)
	}
}

// TestSchemaFactsTableValuedOperandIsDynamic은 관계를 증명하지 못한 테이블 값 함수 피연산자가 든 SQL
// 리터럴이 그 위치의 dynamic relation-use 사실 하나를 내는지 확인한다. isthmus는 생산자 limitation
// 문구를 근거로 쓰지 않고 dynamic 사실을 직접 세어 미사용 진단을 판정 불가로 내린다 — 사실이 없으면
// 함수를 거쳐서만 읽는 테이블이 dead-schema 후보처럼 보인다. 여러 줄 raw 문자열 SQL은 채널에서
// 공백이 접혀야 한다 — isthmus는 제어 문자가 든 채널의 문서를 통째로 거부한다.
func TestSchemaFactsTableValuedOperandIsDynamic(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport \"database/sql\"\n\nvar db *sql.DB\n\nfunc main() {\n" +
			"\tdb.Query(\"SELECT * FROM get_active_orders($1)\")\n" +
			"\tdb.Query(`\n\t\tSELECT o.id\n\t\tFROM pragma_table_info('t') AS p, orders o\n\t`)\n}\n",
	})
	doc, err := SchemaFacts(Options{Dir: dir}, "test")
	if err != nil {
		t.Fatalf("SchemaFacts: %v", err)
	}
	var dynamics []RelationFact
	static := map[string]bool{}
	for _, f := range doc.Facts {
		if fact := f.(RelationFact); fact.Dynamic {
			dynamics = append(dynamics, fact)
		} else {
			static[fact.Channel] = true
		}
	}
	want := []struct {
		channel string
		line    int
	}{
		{"SELECT * FROM get_active_orders($1)", 8},
		{"SELECT o.id FROM pragma_table_info('t') AS p, orders o", 9},
	}
	if len(dynamics) != len(want) {
		t.Fatalf("want one dynamic fact per literal with an unresolved operand: %+v", doc.Facts)
	}
	for i, w := range want {
		got := dynamics[i]
		if got.Channel != w.channel || got.Location.Line != w.line ||
			got.Symbol == nil || got.Symbol.Usr != "example.com/fixture.main" {
			t.Errorf("dynamic fact %d = %+v (symbol %+v), want channel %q at line %d in main",
				i, got, got.Symbol, w.channel, w.line)
		}
	}
	// 뒤의 실제 테이블은 그대로 정적 사실이다.
	if len(static) != 1 || !static["orders"] {
		t.Fatalf("the real relation after the function must stay a static fact: %v", static)
	}
	// 생산자 문구는 남고, 비리터럴 SQL 인자 수(were not literals)에는 섞이지 않는다.
	var functions, nonLiteral bool
	for _, l := range doc.Limitations {
		functions = functions || strings.HasPrefix(l, "unjoined-dynamic-relations: 2 table-valued")
		nonLiteral = nonLiteral || strings.Contains(l, "were not literals")
	}
	if !functions || nonLiteral {
		t.Fatalf("operand count must stay under its own phrase: %v", doc.Limitations)
	}
}

// 자리표시자 공백의 생산자 문구는 테이블 값 함수 문구와 따로 센다 — 함수 피연산자 수에 자리표시자를 섞으면
// "table-valued SQL function operand(s)" 문구가 사실과 다른 수를 말한다. 둘 다 있으면 함수 문구가 먼저다.
func TestSQLPlaceholderLimitations(t *testing.T) {
	limitations := (&schemaScan{placeholders: 2}).limitations()
	if len(limitations) != 1 || !strings.HasPrefix(limitations[0],
		"unjoined-dynamic-relations: 2 SQL placeholder operand(s)") {
		t.Fatalf("missing placeholder coverage evidence: %v", limitations)
	}
	both := (&schemaScan{sqlFunctions: 1, placeholders: 3}).limitations()
	if len(both) != 2 || !strings.HasPrefix(both[0], "unjoined-dynamic-relations: 1 table-valued") ||
		!strings.HasPrefix(both[1], "unjoined-dynamic-relations: 3 SQL placeholder") {
		t.Fatalf("function and placeholder counts must keep their own phrases: %v", both)
	}
}

// TestSchemaFactsPlaceholderOperandIsDynamic은 관계 자리에 자리표시자(`%s`·`?`)가 온 SQL 리터럴이 관계 대신
// 그 위치의 dynamic 사실 하나와 자리표시자 문구를 내는지 확인한다 — `fmt.Sprintf("DELETE FROM %s …")`는 Go의
// 흔한 관용구이고, 예전에는 동사 글자 `s`가 관계 이름으로 나가 isthmus가 거짓 relation-use-without-decl
// error를 냈다. 함수 피연산자와 자리표시자가 함께 든 리터럴도 dynamic 사실은 하나이고, 두 수는 각자의 문구로
// 간다. REVOKE의 FROM(권한 주체)과 IS DISTINCT FROM의 피연산자는 사실이 되지 않는다.
func TestSchemaFactsPlaceholderOperandIsDynamic(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport \"database/sql\"\n\nvar db *sql.DB\n\nfunc main() {\n" +
			"\tdb.Exec(\"DELETE FROM %s WHERE id = $1\")\n" +
			"\tdb.Query(\"SELECT * FROM ? JOIN pragma_table_info('t') p ON true\")\n" +
			"\tdb.Exec(\"REVOKE SELECT ON orders FROM bob\")\n" +
			"\tdb.Query(\"SELECT * FROM events WHERE kind IS DISTINCT FROM $1\")\n}\n",
	})
	doc, err := SchemaFacts(Options{Dir: dir}, "test")
	if err != nil {
		t.Fatalf("SchemaFacts: %v", err)
	}
	dynamics := map[int]string{}
	static := map[string]int{}
	for _, f := range doc.Facts {
		if fact := f.(RelationFact); fact.Dynamic {
			dynamics[fact.Location.Line] = fact.Channel
		} else {
			static[fact.Channel] = fact.Location.Line
		}
	}
	wantDynamic := map[int]string{
		8: "DELETE FROM %s WHERE id = $1",
		9: "SELECT * FROM ? JOIN pragma_table_info('t') p ON true",
	}
	if len(dynamics) != len(wantDynamic) || dynamics[8] != wantDynamic[8] || dynamics[9] != wantDynamic[9] {
		t.Fatalf("want one dynamic fact per literal with a placeholder operand: %v", dynamics)
	}
	if len(static) != 1 || static["events"] != 11 {
		t.Fatalf("only events is a relation (no s, bob or $1): %v", static)
	}
	want := []string{
		"unjoined-dynamic-relations: 1 table-valued SQL function operand(s) have unmodeled relation dependencies",
		"unjoined-dynamic-relations: 2 SQL placeholder operand(s)",
	}
	if len(doc.Limitations) != 2 || doc.Limitations[0] != want[0] || !strings.HasPrefix(doc.Limitations[1], want[1]) {
		t.Fatalf("limitations = %v, want %v…", doc.Limitations, want)
	}
}

// TestSchemaFactsMultilineNonLiteralChannel은 여러 줄에 걸친 비리터럴 SQL 인자의 dynamic 사실 채널이
// isthmus가 받는 한 줄 문자열인지 확인한다 — go/printer는 식의 줄바꿈·들여쓰기 탭을 그대로 찍고, raw
// 문자열 피연산자는 줄바꿈을 담는다. 제어 문자가 든 채널 하나로 isthmus는 문서 전체를 거부한다. 긴 식은
// 바이트가 아니라 UTF-8 문자 경계에서 잘려야 한다(잘린 바이트는 JSON에서 U+FFFD로 바뀐다). 한 줄 식은
// 그대로다.
func TestSchemaFactsMultilineNonLiteralChannel(t *testing.T) {
	long := strings.Repeat("\"가나다라마\" +\n\t\t", 8) + "table"
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport \"database/sql\"\n\nvar db *sql.DB\n\nfunc run(table string) {\n" +
			"\tdb.Query(\"SELECT id FROM \" +\n\t\ttable)\n" +
			"\tdb.Query(`SELECT *\n\tFROM ` + table)\n" +
			"\tdb.Query(table + \" LIMIT 1\")\n" +
			"\tdb.Query(" + long + ")\n}\n\nfunc main() { run(\"t\") }\n",
	})
	doc, err := SchemaFacts(Options{Dir: dir}, "test")
	if err != nil {
		t.Fatalf("SchemaFacts: %v", err)
	}
	byLine := map[int]string{}
	for _, f := range doc.Facts {
		if fact := f.(RelationFact); fact.Dynamic {
			byLine[fact.Location.Line] = fact.Channel
		}
	}
	for line, want := range map[int]string{
		8: `"SELECT id FROM " + table`, 10: "`SELECT * FROM ` + table", 12: `table + " LIMIT 1"`,
	} {
		if byLine[line] != want {
			t.Errorf("line %d channel = %q, want %q (all: %v)", line, byLine[line], want, byLine)
		}
	}
	cut := byLine[13]
	if len(cut) > maxDynamicChannel || !strings.HasSuffix(cut, "...") || !utf8.ValidString(cut) ||
		strings.ContainsAny(cut, "\n\t") {
		t.Fatalf("a long multi-line expression must be cut at a rune boundary into one line: %q", cut)
	}
}

// TestDynamicSQLChannel은 SQL 원문을 isthmus가 받는 채널로 다듬는지 확인한다 — 공백류는 한 칸으로
// 접고, 제어 문자는 지우고, 120바이트를 넘으면 UTF-8 문자 경계에서 잘라 "..."를 붙이고, 남는 것이
// 없으면 <dynamic>이다.
func TestDynamicSQLChannel(t *testing.T) {
	long := "a" + strings.Repeat("가", 50) // 117바이트 경계가 문자 한가운데다
	for _, tc := range []struct{ in, want string }{
		{"SELECT * FROM f($1)", "SELECT * FROM f($1)"},
		{"\n\t\tSELECT a\r\n\t\tFROM f() x\u0085y  ", "SELECT a FROM f() x y"},
		{"a\x00b\x1bc\x7fd\u009fe", "abcde"},
		{"\x00\n\t", "<dynamic>"},
		{long, "a" + strings.Repeat("가", 38) + "..."},
	} {
		got := dynamicSQLChannel(tc.in)
		if got != tc.want || !utf8.ValidString(got) {
			t.Errorf("dynamicSQLChannel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
