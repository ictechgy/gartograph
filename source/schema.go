// isthmus persistence 생산자 — Go 코드가 SQL 스키마 관계를 이름으로
// 참조하는 경계를 relation-use 사실로 낸다.
//
// 계약은 ../isthmus의 docs/GRAPH-EXCHANGE.md가 정본이다. 이 파일은
// 수확만 한다 — 이름 해석(한정·비한정·모호성)과 진단은 isthmus의 몫이다.
//
// 수확 범위는 의도적으로 좁다:
//   - database/sql·sqlx·gorm 수신자로 타입이 확인된 호출의 SQL 인자
//   - SQL 리터럴(SELECT/INSERT/UPDATE/DELETE 동사 + FROM/JOIN/INTO/
//     UPDATE/TABLE 뒤의 식별자) — 변수에 담겨 호출로 이어지는 쿼리도
//     문자열 자체가 관계 참조이므로 위치와 함께 낸다
//   - `TableName() string` 리터럴 반환과 그 구조체의 db/sql/gorm 태그
//   - gorm Table("name")·Model(&T{}) 인자
//
// 리터럴이 아닌 SQL 인자는 버리지 않고 원문 표현식을 실은 dynamic 사실로
// 보존한다 — 조인하지 못하는 이유를 소비자가 셀 수 있어야 한다.
package source

import (
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ictechgy/gartograph/graph"
	"golang.org/x/tools/go/packages"
)

// RelationFact는 isthmus bridge-facts v1의 relation-use 사실이다.
// 키 순서는 계약 문서의 나열 순서를 따라 diff 가능하게 유지한다.
type RelationFact struct {
	Kind     string          `json:"kind"`
	Channel  string          `json:"channel"`
	Method   string          `json:"method,omitempty"`
	Dynamic  bool            `json:"dynamic"`
	Location *BridgeLocation `json:"location"`
}

// BridgeLocation은 계약의 1 기반 소스 위치다.
type BridgeLocation struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// SchemaFacts는 dir 아래 Go 소스를 스캔해 persistence target의
// bridge-facts v1 문서를 만든다. go 문서가 사실을 담는 유일한 target이다.
func SchemaFacts(dir, toolVersion string) (*BridgeFactsDocument, error) {
	root, err := realPath(dir)
	if err != nil {
		return nil, err
	}
	pkgs, err := load(Options{Dir: root, Level: graph.LevelSymbol})
	if err != nil {
		return nil, err
	}
	scan := &schemaScan{root: root}
	for _, p := range pkgs {
		if !keep(p, false) {
			continue
		}
		scan.unparsed += len(p.Errors)
		scanPackage(scan, p)
	}
	facts := scan.facts()
	// 계약: target은 사실이 있을 때만 설정된다 — 빈 문서는 target null이다.
	var target any
	if len(facts) > 0 {
		target = "persistence"
	}
	doc := &BridgeFactsDocument{
		Format:      "bridge-facts",
		Version:     1,
		Tool:        BridgeFactsTool{Name: "gartograph", Version: toolVersion},
		GeneratedAt: bridgeTimestamp(time.Now()),
		Platform:    "go",
		Target:      target,
		Project:     root,
		Facts:       facts,
	}
	if !scan.latest.IsZero() {
		doc.SourceModifiedAt = bridgeTimestamp(scan.latest)
	}
	doc.Limitations = scan.limitations()
	return doc, nil
}

// schemaScan은 persistence 수확의 중간 상태다.
type schemaScan struct {
	root         string
	list         []RelationFact
	unparsed     int       // 타입 로더·파서가 실패한 패키지의 오류 수
	unattributed int       // 관계를 알 수 없는 컬럼 태그 수
	unresolved   int       // TableName을 찾지 못한 모델 인자 수
	dynamic      int       // 리터럴로 읽히지 않아 조인 불가한 SQL 인자 수
	latest       time.Time // 읽은 소스의 최신 mtime
	seen         map[string]bool
}

// scanPackage는 패키지의 모든 파일을 훑어 사실을 모은다.
// 관계 바인딩(TableName)과 모델 인자는 같은 패키지의 다른 파일에 있을 수
// 있으므로 파일 스캔이 끝난 뒤 패키지 단위로 귀속한다.
func scanPackage(scan *schemaScan, p *packages.Package) {
	tableNames := map[string]string{} // 구조체 타입명 → 테이블 이름
	var tags []columnTag              // 테이블 귀속을 기다리는 태그
	var models []modelSite            // 테이블 귀속을 기다리는 모델 인자
	for _, f := range p.Syntax {
		scanFile(scan, p, f, tableNames, &tags, &models)
	}
	for _, tag := range tags {
		table, ok := tableNames[tag.typeName]
		if !ok || table == "" {
			scan.unattributed++
			continue
		}
		scan.push(RelationFact{
			Kind:     "relation-use",
			Channel:  table,
			Method:   tag.column,
			Dynamic:  false,
			Location: scan.locate(p.Fset, tag.pos),
		})
	}
	for _, site := range models {
		table, ok := tableNames[site.typeName]
		if !ok || table == "" {
			scan.unresolved++
			continue
		}
		scan.push(RelationFact{
			Kind:     "relation-use",
			Channel:  table,
			Dynamic:  false,
			Location: scan.locate(p.Fset, site.pos),
		})
	}
}

// columnTag는 구조체 필드 태그에서 읽은 컬럼 이름과 위치다.
type columnTag struct {
	typeName string
	column   string
	pos      token.Pos
}

// modelSite는 TableName 귀속을 기다리는 모델 인자다.
type modelSite struct {
	typeName string
	pos      token.Pos
}

// scanFile은 파일 하나의 선언·호출·리터럴을 관측한다.
func scanFile(scan *schemaScan, p *packages.Package, f *ast.File,
	tableNames map[string]string, tags *[]columnTag, models *[]modelSite) {
	scan.mtime(p, f)
	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			scanTableName(scan, p, node, tableNames)
		case *ast.GenDecl:
			*tags = append(*tags, structTags(node)...)
		case *ast.CallExpr:
			scanCall(scan, p, node, models)
		case *ast.BasicLit:
			scanLiteral(scan, p, node)
		}
		return true
	})
}

// mtime은 읽은 파일의 mtime을 문서의 sourceModifiedAt 근거로 모은다.
func (s *schemaScan) mtime(p *packages.Package, f *ast.File) {
	if p.Fset == nil {
		return
	}
	filename := p.Fset.Position(f.Pos()).Filename
	if info, err := os.Stat(filename); err == nil && info.ModTime().After(s.latest) {
		s.latest = info.ModTime()
	}
}

// scanTableName은 `func (T) TableName() string { return "name" }` 꼴의
// 리터럴 반환을 읽어 구조체 타입을 테이블 이름에 묶는다. 묶인 이름 자체가
// 관계 참조이므로 사실로도 낸다.
func scanTableName(scan *schemaScan, p *packages.Package, decl *ast.FuncDecl,
	tableNames map[string]string) {
	if decl.Name.Name != "TableName" || decl.Recv == nil || decl.Body == nil {
		return
	}
	typeName := receiverTypeName(decl.Recv.List[0].Type)
	if typeName == "" {
		return
	}
	lit, ok := singleStringReturn(decl.Body)
	if !ok {
		return
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil || name == "" {
		return
	}
	tableNames[typeName] = name
	scan.push(RelationFact{
		Kind:     "relation-use",
		Channel:  name,
		Dynamic:  false,
		Location: scan.locate(p.Fset, lit.Pos()),
	})
}

// receiverTypeName은 `*T`·`T` 수신자의 타입 이름을 돌려준다.
func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// singleStringReturn은 몸체가 문자열 리터럴 하나를 반환하는지 본다.
func singleStringReturn(body *ast.BlockStmt) (*ast.BasicLit, bool) {
	if len(body.List) != 1 {
		return nil, false
	}
	ret, ok := body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil, false
	}
	lit, ok := ret.Results[0].(*ast.BasicLit)
	return lit, ok && lit.Kind == token.STRING
}

// structTags는 GenDecl 안의 struct 필드 태그에서 컬럼 이름을 읽는다.
// `db:"name"`·`sql:"name"`은 첫 쉼표 앞이 이름이고, `gorm:"column:name"`은
// column 키다 — 어떤 형태도 관계 이름은 담지 않으므로 귀속은 나중이다.
func structTags(decl *ast.GenDecl) []columnTag {
	var out []columnTag
	for _, spec := range decl.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		st, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			continue
		}
		for _, field := range st.Fields.List {
			if field.Tag == nil {
				continue
			}
			column := tagColumn(field.Tag.Value)
			if column == "" || column == "-" {
				continue
			}
			out = append(out, columnTag{
				typeName: typeSpec.Name.Name,
				column:   column,
				pos:      field.Tag.Pos(),
			})
		}
	}
	return out
}

// tagColumn은 struct 태그 원문에서 컬럼 이름 하나를 읽는다.
// db·sql 태그는 첫 쉼표 앞이 이름, gorm 태그는 `column:` 키다.
func tagColumn(raw string) string {
	tag, err := strconv.Unquote(raw)
	if err != nil {
		return ""
	}
	for _, key := range []string{"db", "sql"} {
		if value := structTagGet(tag, key); value != "" {
			return strings.SplitN(value, ",", 2)[0]
		}
	}
	if value := structTagGet(tag, "gorm"); value != "" {
		for _, part := range strings.Split(value, ";") {
			if name, ok := strings.CutPrefix(strings.ToUpper(part), "COLUMN:"); ok {
				return name
			}
		}
	}
	return ""
}

// structTagGet은 reflect.StructTag.Get과 같은 규칙으로 태그 값을 읽는다.
// reflect를 쓰지 않는 이유는 태그 원문 위치를 유지하기 위해서다.
func structTagGet(tag, key string) string {
	for tag != "" {
		tag = strings.TrimLeft(tag, " ")
		i := strings.Index(tag, ":")
		if i <= 0 {
			break
		}
		name, rest := tag[:i], tag[i+1:]
		if !strings.HasPrefix(rest, `"`) {
			break
		}
		consumed := quotedLen(rest)
		if consumed == 0 {
			break
		}
		if name == key {
			value, err := strconv.Unquote(rest[:consumed])
			if err != nil {
				return ""
			}
			return value
		}
		tag = rest[consumed:]
	}
	return ""
}

// quotedLen은 여는 따옴표부터 짝이 닫히는 위치까지의 바이트 길이다.
// 짝이 없으면 0을 돌려준다 — 손상된 태그를 건너뛰기 위해서다.
func quotedLen(s string) int {
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			return i + 1
		}
	}
	return 0
}

// argKind는 SQL 인자가 어떤 내용을 담는지 나눈다.
type argKind int

const (
	argSQL          argKind = iota // SQL 텍스트 — 리터럴은 스캔, 비리터럴은 dynamic
	argRelationName                // 인자 자체가 관계 이름이다
	argModel                       // 인자가 모델 타입 값이다 (&T{})
)

// sqlMethodRule은 DB 메서드의 SQL 관련 인자 위치와 해석 규칙이다.
type sqlMethodRule struct {
	arg  int
	kind argKind
}

// dbMethods는 "패키지 경로.수신자 타입.메서드" → 인자 규칙이다.
// 수신자 타입은 types.Info로 확인하므로 이름만 같은 메서드는 걸리지 않는다.
var dbMethods = map[string]sqlMethodRule{
	// database/sql — 쿼리 문자열이 첫 인자, Context 변형은 ctx 다음이다.
	"database/sql.DB.Query":             {0, argSQL},
	"database/sql.DB.QueryContext":      {1, argSQL},
	"database/sql.DB.QueryRow":          {0, argSQL},
	"database/sql.DB.QueryRowContext":   {1, argSQL},
	"database/sql.DB.Exec":              {0, argSQL},
	"database/sql.DB.ExecContext":       {1, argSQL},
	"database/sql.DB.Prepare":           {0, argSQL},
	"database/sql.DB.PrepareContext":    {1, argSQL},
	"database/sql.Tx.Query":             {0, argSQL},
	"database/sql.Tx.QueryContext":      {1, argSQL},
	"database/sql.Tx.QueryRow":          {0, argSQL},
	"database/sql.Tx.QueryRowContext":   {1, argSQL},
	"database/sql.Tx.Exec":              {0, argSQL},
	"database/sql.Tx.ExecContext":       {1, argSQL},
	"database/sql.Tx.Prepare":           {0, argSQL},
	"database/sql.Tx.PrepareContext":    {1, argSQL},
	"database/sql.Conn.QueryContext":    {1, argSQL},
	"database/sql.Conn.QueryRowContext": {1, argSQL},
	"database/sql.Conn.ExecContext":     {1, argSQL},
	"database/sql.Conn.PrepareContext":  {1, argSQL},
	"database/sql.Stmt.Query":           {0, argSQL},
	"database/sql.Stmt.QueryContext":    {1, argSQL},
	"database/sql.Stmt.QueryRow":        {0, argSQL},
	"database/sql.Stmt.QueryRowContext": {1, argSQL},
	"database/sql.Stmt.Exec":            {0, argSQL},
	"database/sql.Stmt.ExecContext":     {1, argSQL},
	// jmoiron/sqlx — Select/Get은 첫 인자가 결과 dest라 쿼리는 그 다음이다.
	"github.com/jmoiron/sqlx.DB.Select":           {1, argSQL},
	"github.com/jmoiron/sqlx.DB.SelectContext":    {2, argSQL},
	"github.com/jmoiron/sqlx.DB.Get":              {1, argSQL},
	"github.com/jmoiron/sqlx.DB.GetContext":       {2, argSQL},
	"github.com/jmoiron/sqlx.DB.Queryx":           {0, argSQL},
	"github.com/jmoiron/sqlx.DB.QueryxContext":    {1, argSQL},
	"github.com/jmoiron/sqlx.DB.QueryRowx":        {0, argSQL},
	"github.com/jmoiron/sqlx.DB.QueryRowxContext": {1, argSQL},
	"github.com/jmoiron/sqlx.DB.MustExec":         {0, argSQL},
	"github.com/jmoiron/sqlx.DB.MustExecContext":  {1, argSQL},
	"github.com/jmoiron/sqlx.DB.NamedExec":        {0, argSQL},
	"github.com/jmoiron/sqlx.DB.NamedExecContext": {1, argSQL},
	"github.com/jmoiron/sqlx.DB.PrepareNamed":     {0, argSQL},
	"github.com/jmoiron/sqlx.Tx.Select":           {1, argSQL},
	"github.com/jmoiron/sqlx.Tx.SelectContext":    {2, argSQL},
	"github.com/jmoiron/sqlx.Tx.Get":              {1, argSQL},
	"github.com/jmoiron/sqlx.Tx.GetContext":       {2, argSQL},
	"github.com/jmoiron/sqlx.Tx.Queryx":           {0, argSQL},
	"github.com/jmoiron/sqlx.Tx.QueryxContext":    {1, argSQL},
	"github.com/jmoiron/sqlx.Tx.Exec":             {0, argSQL},
	"github.com/jmoiron/sqlx.Tx.ExecContext":      {1, argSQL},
	"github.com/jmoiron/sqlx.Tx.NamedExec":        {0, argSQL},
	"github.com/jmoiron/sqlx.Stmt.Select":         {1, argSQL},
	"github.com/jmoiron/sqlx.Stmt.Get":            {1, argSQL},
	"github.com/jmoiron/sqlx.Stmt.Queryx":         {0, argSQL},
	"github.com/jmoiron/sqlx.Stmt.Exec":           {0, argSQL},
	"github.com/jmoiron/sqlx.NamedStmt.Queryx":    {0, argSQL},
	"github.com/jmoiron/sqlx.NamedStmt.Exec":      {0, argSQL},
	// gorm — Table의 인자는 SQL이 아니라 관계 이름 그대로다.
	"gorm.io/gorm.DB.Table": {0, argRelationName},
	"gorm.io/gorm.DB.Raw":   {0, argSQL},
	"gorm.io/gorm.DB.Exec":  {0, argSQL},
	"gorm.io/gorm.DB.Model": {0, argModel},
	// AutoMigrate는 스키마를 만드는 호출이라 모델 인자가 관계를 가리킨다.
	"gorm.io/gorm.DB.AutoMigrate": {0, argModel},
}

// scanCall은 타입이 확인된 DB 수신자 호출을 읽어 SQL 인자를 관측한다.
// 리터럴 SQL 인자는 BasicLit 스캔이 따로 잡으므로 여기서는 비리터럴 인자를
// dynamic 사실로 보존한다 — gorm Table처럼 이름 자체가 인자인 메서드와
// Model 같은 타입 인자만 여기서 직접 읽는다.
func scanCall(scan *schemaScan, p *packages.Package, call *ast.CallExpr, models *[]modelSite) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	rule, ok := sqlArgRule(p.TypesInfo, sel, sel.Sel.Name)
	if !ok {
		return
	}
	if len(call.Args) <= rule.arg {
		return
	}
	arg := call.Args[rule.arg]
	switch rule.kind {
	case argRelationName:
		// Table("name") — 인자가 곧 관계 이름이다.
		if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if name, err := strconv.Unquote(lit.Value); err == nil && name != "" {
				scan.push(RelationFact{
					Kind:     "relation-use",
					Channel:  name,
					Dynamic:  false,
					Location: scan.locate(p.Fset, lit.Pos()),
				})
				return
			}
		}
		scan.pushDynamic(p, arg)
	case argModel:
		// Model(&T{})·AutoMigrate(&T{}) — 타입 인자를 TableName에 묶는다.
		if typeName := modelTypeName(arg); typeName != "" {
			*models = append(*models, modelSite{typeName: typeName, pos: arg.Pos()})
		} else {
			scan.unresolved++
		}
	case argSQL:
		// SQL 텍스트 인자 — 리터럴은 BasicLit 스캔이 잡고, 여기서는
		// 비리터럴만 동적 사실로 보존한다.
		if lit, ok := arg.(*ast.BasicLit); !ok || lit.Kind != token.STRING {
			scan.pushDynamic(p, arg)
		}
	}
}

// sqlArgRule은 셀렉터 호출이 알려진 DB 메서드인지 타입으로 확인한다.
func sqlArgRule(info *types.Info, sel *ast.SelectorExpr, method string) (sqlMethodRule, bool) {
	selection, ok := info.Selections[sel]
	if !ok {
		return sqlMethodRule{}, false
	}
	recv := selection.Recv()
	if ptr, ok := recv.(*types.Pointer); ok {
		recv = ptr.Elem()
	}
	named, ok := recv.(*types.Named)
	if !ok {
		return sqlMethodRule{}, false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return sqlMethodRule{}, false
	}
	key := obj.Pkg().Path() + "." + obj.Name() + "." + method
	rule, ok := dbMethods[key]
	return rule, ok
}

// modelTypeName은 `&T{}`·`[]T{}`·`new(T)` 인자의 요소 타입 이름을 읽는다.
// 자기 호출 대신 루프로 포장을 벗긴다 — 재귀는 symbol 레벨 순환으로 보인다.
func modelTypeName(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.UnaryExpr:
			if e.Op != token.AND {
				return ""
			}
			expr = e.X
		case *ast.CompositeLit:
			return typeExprName(e.Type)
		case *ast.CallExpr:
			// new(T) 꼴이다.
			if id, ok := e.Fun.(*ast.Ident); ok && id.Name == "new" && len(e.Args) == 1 {
				return typeExprName(e.Args[0])
			}
			return ""
		default:
			return ""
		}
	}
}

// typeExprName은 `T`·`*T`·`[]T` 타입 표현의 이름을 읽는다.
func typeExprName(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e.Name
		case *ast.StarExpr:
			expr = e.X
		case *ast.ArrayType:
			expr = e.Elt
		default:
			return ""
		}
	}
}

// sqlVerbPattern은 문자열을 SQL로 판정하는 동사 표이다 — 산문 속
// "from" 같은 오탐을 막기 위해 동사가 없는 리터럴은 스캔하지 않는다.
var sqlVerbPattern = regexp.MustCompile(
	`(?i)\b(?:select|insert|update|delete|create|alter|drop|truncate|replace|merge)\b`)

// scanLiteral은 SQL로 보이는 문자열 리터럴에서 관계 이름을 읽는다.
// 변수에 담긴 쿼리도 문자열 자체가 참조이므로 모든 리터럴을 본다.
func scanLiteral(scan *schemaScan, p *packages.Package, lit *ast.BasicLit) {
	if lit.Kind != token.STRING {
		return
	}
	text, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}
	if !sqlVerbPattern.MatchString(text) {
		return
	}
	for _, name := range sqlRelations(text) {
		scan.push(RelationFact{
			Kind:     "relation-use",
			Channel:  name,
			Dynamic:  false,
			Location: scan.locate(p.Fset, lit.Pos()),
		})
	}
}

// sqlToken은 SQL 텍스트의 어휘 하나다 — 인용된 식별자는 키워드가 아니다.
type sqlToken struct {
	text   string
	quoted bool
}

// relationKeywords는 뒤따르는 식별자가 관계 이름인 키워드다.
var relationKeywords = map[string]bool{
	"from": true, "join": true, "into": true, "update": true,
	"table": true, "truncate": true,
}

// sqlRelations는 SQL 텍스트에서 관계 이름을 읽는다.
// 한정 이름(`schema.table`)은 그대로 두고, 이름 자체에 점이 있는 인용
// 식별자("a.b")는 한 세그먼트로 읽는다 — escape는 사실 기록 시에 한다.
func sqlRelations(text string) []string {
	tokens := lexSQL(text)
	var out []string
	for i, tok := range tokens {
		if tok.quoted || !relationKeywords[strings.ToLower(tok.text)] {
			continue
		}
		j := i + 1
		// ONLY·IF NOT EXISTS 같은 수식어는 건너뛴다.
		for j < len(tokens) && !tokens[j].quoted &&
			(strings.EqualFold(tokens[j].text, "only") ||
				strings.EqualFold(tokens[j].text, "if") ||
				strings.EqualFold(tokens[j].text, "not") ||
				strings.EqualFold(tokens[j].text, "exists")) {
			j++
		}
		// 쉼표로 이어지는 목록(`FROM a, b`)을 읽는다.
		for j < len(tokens) {
			name, next := readQualifiedName(tokens, j)
			if name == "" {
				break
			}
			out = append(out, name)
			if next < len(tokens) && tokens[next].text == "," {
				j = next + 1
				continue
			}
			break
		}
	}
	return out
}

// lexSQL은 SQL 텍스트를 어휘로 나눈다 — 인용 식별자는 내용을 보존하고
// 그 외엔 식별자 문자열과 단일 기호 토큰만 만든다.
func lexSQL(text string) []sqlToken {
	var tokens []sqlToken
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '"' || c == '`' || c == '[':
			end := byte('"')
			if c == '[' {
				end = ']'
			} else {
				end = c
			}
			j := i + 1
			for j < len(text) && text[j] != end {
				j++
			}
			tokens = append(tokens, sqlToken{text: text[i+1 : j], quoted: true})
			i = j + 1
		case isIdentStart(c):
			j := i + 1
			for j < len(text) && isIdentPart(text[j]) {
				j++
			}
			tokens = append(tokens, sqlToken{text: text[i:j]})
			i = j
		case c == '-' && i+1 < len(text) && text[i+1] == '-':
			// 행 주석은 줄 끝까지 건너뛴다.
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			// 블록 주석은 닫힘까지 건너뛴다.
			i += 2
			for i+1 < len(text) && !(text[i] == '*' && text[i+1] == '/') {
				i++
			}
			i += 2
		case c == '\'':
			// 문자열 리터럴은 이름이 아니다.
			i++
			for i < len(text) && text[i] != '\'' {
				if text[i] == '\\' || (i+1 < len(text) && text[i+1] == '\'') {
					i++
				}
				i++
			}
			i++
		default:
			if c == '.' || c == ',' || c == '(' || c == ')' {
				tokens = append(tokens, sqlToken{text: string(c)})
			}
			i++
		}
	}
	return tokens
}

// readQualifiedName은 `ident(.ident)*` 한정 이름을 읽어 다음 위치를 돌려준다.
func readQualifiedName(tokens []sqlToken, start int) (string, int) {
	if start >= len(tokens) {
		return "", start
	}
	first := tokens[start]
	if first.quoted {
		// 인용 세그먼트는 점을 담을 수 있다 — 세그먼트로 보존한다.
		if first.text == "" {
			return "", start
		}
	} else if !isNameToken(first) {
		return "", start
	}
	var b strings.Builder
	b.WriteString(escapeSegment(first))
	i := start + 1
	for i+1 < len(tokens) && tokens[i].text == "." && !tokens[i].quoted {
		next := tokens[i+1]
		if !next.quoted && !isNameToken(next) {
			break
		}
		b.WriteByte('.')
		b.WriteString(escapeSegment(next))
		i += 2
	}
	return b.String(), i
}

// escapeSegment는 인용 세그먼트 안의 점을 `%2E`로 escape한다 —
// `"a.b"` 같은 한 식별자가 한정자로 오독되지 않게 한다.
// 비인용 세그먼트는 점을 담을 수 없어 그대로다.
func escapeSegment(tok sqlToken) string {
	if !tok.quoted {
		return tok.text
	}
	return strings.ReplaceAll(tok.text, ".", "%2E")
}

// isNameToken은 비인용 토큰이 식별자인지 본다 — 기호·빈 문자열은 아니다.
func isNameToken(tok sqlToken) bool {
	if tok.quoted || tok.text == "" {
		return false
	}
	return isIdentStart(tok.text[0])
}

// isIdentStart는 SQL 식별자 시작 문자인지 본다.
func isIdentStart(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

// isIdentPart는 SQL 식별자의 이어지는 문자인지 본다.
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// pushDynamic은 리터럴로 읽히지 않는 SQL 인자를 동적 사실로 보존한다.
// channel에는 잘린 원문 표현식을 실어 어느 위치의 호출인지 남긴다.
func (s *schemaScan) pushDynamic(p *packages.Package, expr ast.Expr) {
	var buf strings.Builder
	if err := printer.Fprint(&buf, p.Fset, expr); err != nil {
		return
	}
	text := buf.String()
	if len(text) > 120 {
		text = text[:117] + "..."
	}
	s.push(RelationFact{
		Kind:     "relation-use",
		Channel:  text,
		Dynamic:  true,
		Location: s.locate(p.Fset, expr.Pos()),
	})
}

// push는 위치가 있는 사실만 담고 같은 사실을 한 번만 남긴다.
func (s *schemaScan) push(fact RelationFact) {
	if fact.Location == nil {
		return
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	key := fmt.Sprintf("%s|%s|%s|%v|%s:%d:%d",
		fact.Kind, fact.Channel, fact.Method, fact.Dynamic,
		fact.Location.Path, fact.Location.Line, fact.Location.Column)
	if s.seen[key] {
		return
	}
	s.seen[key] = true
	if fact.Dynamic {
		s.dynamic++
	}
	s.list = append(s.list, fact)
}

// locate은 토큰 위치를 프로젝트 상대의 계약 위치로 바꾼다.
// 프로젝트 밖 파일(모듈 캐시 등)은 상대 경로가 없어 사실로 만들지 않는다.
func (s *schemaScan) locate(fset *token.FileSet, pos token.Pos) *BridgeLocation {
	if fset == nil {
		return nil
	}
	p := fset.Position(pos)
	if !p.IsValid() {
		return nil
	}
	rel, err := filepath.Rel(s.root, p.Filename)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	return &BridgeLocation{
		Path:   filepath.ToSlash(rel),
		Line:   p.Line,
		Column: p.Column,
	}
}

// facts는 결정적 순서의 사실 목록이다 — 위치·종류·이름 순이다.
func (s *schemaScan) facts() []any {
	sort.Slice(s.list, func(i, j int) bool {
		a, b := s.list[i], s.list[j]
		if a.Location.Path != b.Location.Path {
			return a.Location.Path < b.Location.Path
		}
		if a.Location.Line != b.Location.Line {
			return a.Location.Line < b.Location.Line
		}
		if a.Location.Column != b.Location.Column {
			return a.Location.Column < b.Location.Column
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Channel != b.Channel {
			return a.Channel < b.Channel
		}
		return a.Method < b.Method
	})
	out := make([]any, len(s.list))
	for i, fact := range s.list {
		out[i] = fact
	}
	return out
}

// limitations는 수확에서 실제로 센 공백만 문장으로 낸다.
func (s *schemaScan) limitations() []string {
	var out []string
	if s.unparsed > 0 {
		out = append(out, fmt.Sprintf(
			"unparsed-sources: %d package load or parse error(s); relation uses there are uncounted",
			s.unparsed))
	}
	if s.unattributed > 0 {
		out = append(out, fmt.Sprintf(
			"unattributed-column-tags: %d struct tag(s) named a column without a TableName binding",
			s.unattributed))
	}
	if s.unresolved > 0 {
		out = append(out, fmt.Sprintf(
			"unresolved-model-types: %d gorm model argument(s) had no TableName binding",
			s.unresolved))
	}
	if s.dynamic > 0 {
		// isthmus가 미사용 진단을 unverified로 내리는 근거다 — 접두사를
		// 바꾸면 조인기의 severity 계산이 새로 인식하지 못한다.
		out = append(out, fmt.Sprintf(
			"unjoined-dynamic-relations: %d SQL argument(s) were not literals; their relations are uncounted",
			s.dynamic))
	}
	return out
}
