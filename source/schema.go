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
// 보존한다 — 조인하지 못하는 이유를 소비자가 셀 수 있어야 한다. 관계를 증명하지
// 못한 피연산자(테이블 값 함수, 관계 자리의 `%s`·`?` 같은 자리표시자)가 든 SQL 리터럴도
// 같은 이유로 dynamic 사실 하나를 낸다.
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
	"unicode"
	"unicode/utf8"

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
	// Symbol은 사실을 감싸는 그래프 선언이다. usr는 `impact`·`reach`와 같은 심볼 정점
	// ID라 isthmus trace가 순회 도달 집합과 이 사실을 문자열 일치로 잇는다. 감싸는
	// 심볼 정점이 없으면 생략하고 missing-relation-usrs limitation으로 센다.
	Symbol *FactSymbol `json:"symbol,omitempty"`
	// owner는 수확 중 기록한 감싸는 선언의 후보 ID다 — 그래프 정점인지 확인한 뒤에만
	// Symbol이 된다(직렬화하지 않는다).
	owner string
}

// FactSymbol은 계약의 사실 symbol이다 — qualifiedName은 필수, usr는 생산자 안정 ID다.
type FactSymbol struct {
	QualifiedName string `json:"qualifiedName"`
	Usr           string `json:"usr"`
}

// BridgeLocation은 계약의 1 기반 소스 위치다.
type BridgeLocation struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// SchemaFacts는 opts.Dir 아래 Go 소스를 스캔해 persistence target의
// bridge-facts v1 문서를 만든다. go 문서가 사실을 담는 유일한 target이다.
// opts의 Dir·Exclude만 쓴다 — 레벨은 늘 symbol이다. Exclude는 스캔 범위가 아니라
// usr 확인용 그래프(impact가 .gartograph.yml exclude로 만드는 그래프)에만 적용된다.
func SchemaFacts(opts Options, toolVersion string) (*BridgeFactsDocument, error) {
	root, err := realPath(opts.Dir)
	if err != nil {
		return nil, err
	}
	loadOpts := Options{Dir: root, Level: graph.LevelSymbol, Exclude: opts.Exclude}
	pkgs, err := load(loadOpts)
	if err != nil {
		return nil, err
	}
	// 같은 로드 결과로 impact 그래프와 같은 심볼 정점 집합을 만든다(usrDocument — 간선은 만들지 않는다) — usr는
	// 이 집합의 정점일 때만 싣는다(유령 ID 금지).
	symbolDoc, err := usrDocument(pkgs, loadOpts)
	if err != nil {
		return nil, err
	}
	scan := &schemaScan{root: root, ids: newSymbolIDs(symbolDoc)}
	for _, p := range pkgs {
		if !keep(p, false) {
			continue
		}
		scan.unparsed += len(p.Errors)
		scanPackage(scan, p)
	}
	scan.attachSymbols()
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
	sqlFunctions int       // 의존 관계를 증명하지 못한 테이블 값 함수 피연산자 수
	placeholders int       // 관계 자리에 자리표시자가 와 관계를 읽지 못한 SQL 피연산자 수(함수 수와 따로 센다)
	dynamic      int       // 리터럴로 읽히지 않아 조인 불가한 SQL 인자 수
	missingUsrs  int       // 감싸는 심볼 정점이 없어 usr를 싣지 못한 사실 수
	latest       time.Time // 읽은 소스의 최신 mtime
	seen         map[string]bool
	// ids는 usr 확인용 심볼 그래프의 정점 색인이다.
	ids symbolIDs
	// owner는 지금 훑는 선언의 감싸는 그래프 정점 후보 ID다(없으면 빈 문자열).
	// 호출·리터럴 스캔 함수가 인자 없이 같은 귀속을 쓰도록 스캔 상태에 둔다.
	owner string
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
			owner:    tag.owner,
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
			owner:    site.owner,
		})
	}
}

// columnTag는 구조체 필드 태그에서 읽은 컬럼 이름과 위치다.
type columnTag struct {
	typeName string
	column   string
	pos      token.Pos
	owner    string // 태그가 놓인 타입 선언의 정점 ID
}

// modelSite는 TableName 귀속을 기다리는 모델 인자다.
type modelSite struct {
	typeName string
	pos      token.Pos
	owner    string // 모델 인자를 감싸는 선언의 정점 ID
}

// scanFile은 파일 하나의 선언·호출·리터럴을 관측한다. 선언 단위로 훑는 이유는
// 사실마다 감싸는 그래프 선언(declOwners)을 정하기 위해서다 — 심볼 수확(declEdges)이
// 간선 출발점으로 쓰는 정점과 같은 귀속이다.
func scanFile(scan *schemaScan, p *packages.Package, f *ast.File,
	tableNames map[string]string, tags *[]columnTag, models *[]modelSite) {
	scan.mtime(p, f)
	for _, decl := range f.Decls {
		for _, part := range declParts(scan.ids, p, decl) {
			scan.owner = part.owner
			scanNode(scan, p, part.node, tableNames, tags, models)
		}
	}
	scan.owner = ""
}

// scanNode는 노드 아래의 선언·호출·리터럴을 지금 귀속(scan.owner)으로 관측한다.
func scanNode(scan *schemaScan, p *packages.Package, root ast.Node,
	tableNames map[string]string, tags *[]columnTag, models *[]modelSite) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			scanTableName(scan, p, node, tableNames)
		case *ast.TypeSpec:
			*tags = append(*tags, structTags(node, scan.owner)...)
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
		owner:    scan.owner,
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

// structTags는 타입 선언의 struct 필드 태그에서 컬럼 이름을 읽는다.
// `db:"name"`·`sql:"name"`은 첫 쉼표 앞이 이름이고, `gorm:"column:name"`은
// column 키다 — 어떤 형태도 관계 이름은 담지 않으므로 귀속은 나중이다.
// owner는 태그가 놓인 타입 선언의 정점 ID다 — 심볼 수확도 타입 선언 안의 참조를
// 그 타입 정점에서 긋는다(specEdges). 필드 정점이 아닌 타입으로 두는 이유는
// 태그가 타입의 행 매핑 전체를 선언하기 때문이다 — 핸들러가 타입에 닿으면(Scan 대상,
// 반환 타입) 그 컬럼들에 기댄다고 보는 쪽이 영향 분석에서 안전한 과대 근사다.
func structTags(typeSpec *ast.TypeSpec, owner string) []columnTag {
	st, ok := typeSpec.Type.(*ast.StructType)
	if !ok {
		return nil
	}
	var out []columnTag
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
			owner:    owner,
		})
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
			// 키 비교는 대소문자를 접되 이름은 원문 대소문자를 보존한다.
			if len(part) > len("COLUMN:") && strings.EqualFold(part[:7], "COLUMN:") {
				return part[7:]
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
					owner:    scan.owner,
				})
				return
			}
		}
		scan.pushDynamic(p, arg)
	case argModel:
		// Model(&T{})·AutoMigrate(&T{}) — 타입 인자를 TableName에 묶는다.
		if typeName := modelTypeName(arg); typeName != "" {
			*models = append(*models, modelSite{typeName: typeName, pos: arg.Pos(), owner: scan.owner})
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
	relations, functions, placeholders := sqlRelations(text)
	scan.sqlFunctions += functions
	scan.placeholders += placeholders
	for _, name := range relations {
		scan.push(RelationFact{
			Kind:     "relation-use",
			Channel:  name,
			Dynamic:  false,
			Location: scan.locate(p.Fset, lit.Pos()),
			owner:    scan.owner,
		})
	}
	// 관계를 증명하지 못한 피연산자(테이블 값 함수, 관계 자리의 자리표시자)가 있으면 그 리터럴을 dynamic
	// 사실 하나로도 남긴다 — isthmus는 생산자 limitation 문구가 아니라 dynamic 사실을 직접 세어 미사용
	// 진단을 판정 불가로 내린다(GRAPH-EXCHANGE). 문구만 남기면 함수를 거쳐서만 읽거나 `fmt.Sprintf`로
	// 이름을 채워 읽는 테이블이 dead-schema 후보로 보인다.
	if functions+placeholders > 0 {
		scan.push(RelationFact{
			Kind:     "relation-use",
			Channel:  dynamicSQLChannel(text),
			Dynamic:  true,
			Location: scan.locate(p.Fset, lit.Pos()),
			owner:    scan.owner,
		})
	}
}

// maxDynamicChannel은 dynamic 사실 채널의 최대 바이트 수다(SQL 리터럴과 비리터럴 인자 모두).
const maxDynamicChannel = 120

// dynamicSQLChannel은 SQL 리터럴 원문이나 비리터럴 인자의 원문 식을 dynamic 사실의 채널로 다듬는다.
// 공백류(줄바꿈·탭 포함)는 한 칸으로 접고 C0·DEL·C1 제어 문자는 지운다 — isthmus는 제어 문자가 든
// 채널의 문서를 통째로 거부하는데, 여러 줄 raw 문자열 SQL과 여러 줄에 걸친 인자 식은 Go에서 흔하다.
// 길면 117바이트에 "..."를 붙이되 UTF-8 문자 경계에서 자른다. 남는 것이 없으면 <dynamic>이다(채널은
// 비면 안 된다).
func dynamicSQLChannel(text string) string {
	var b strings.Builder
	pendingSpace := false
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			pendingSpace = b.Len() > 0
		case unicode.IsControl(r):
			// 제어 문자는 채널에 남기지 않는다.
		default:
			if pendingSpace {
				b.WriteByte(' ')
				pendingSpace = false
			}
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > maxDynamicChannel {
		cut := maxDynamicChannel - len("...")
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out = out[:cut] + "..."
	}
	if out == "" {
		return "<dynamic>"
	}
	return out
}

// sqlToken은 SQL 텍스트의 어휘 하나다 — 인용된 식별자는 키워드가 아니다.
type sqlToken struct {
	text   string
	quoted bool
	// placeholder는 실행 때 채워지는 자리를 여는 토큰(`%s`의 `%`, `{}`의 `{`, `?`, `:name`의 `:`,
	// `@name`의 `@`, `$1`)인지다. 관계 자리에 오면 이름을 읽을 수 없으므로 관계 대신 미해석으로 센다.
	placeholder bool
}

// relationKeywords는 뒤따르는 식별자가 관계 이름인 키워드다.
var relationKeywords = map[string]bool{
	"from": true, "join": true, "into": true, "update": true,
	"table": true, "truncate": true,
}

// clauseWords는 관계 이름 자리에 올 수 없는 SQL 절 키워드다(tsograph sql-relations.ts의 목록과 같다).
// 관계 자리에 오면 피연산자 목록이 끝난 것이고 미해석으로 세지 않는다 — `DO UPDATE SET`·`FROM {} WHERE`의
// SET·WHERE가 관계 이름으로 새지 않는다. `table`은 `UPDATE table SET`의 진짜 관계 이름일 수 있어 빼고,
// 산문 관사 the·an은 넣는다(a는 흔한 별칭이라 뺀다).
var clauseWords = map[string]bool{
	"where": true, "set": true, "on": true, "group": true, "order": true, "by": true, "having": true,
	"limit": true, "offset": true, "union": true, "intersect": true, "except": true, "values": true,
	"returning": true, "as": true, "left": true, "right": true, "inner": true, "outer": true, "full": true,
	"cross": true, "natural": true, "lateral": true, "using": true, "and": true, "or": true, "not": true,
	"null": true, "select": true, "insert": true, "delete": true, "from": true, "join": true, "into": true,
	"update": true, "truncate": true, "with": true, "for": true, "in": true, "is": true, "case": true,
	"when": true, "then": true, "else": true, "end": true, "distinct": true, "asc": true, "desc": true,
	"if": true, "exists": true, "only": true, "between": true, "like": true, "to": true, "grant": true,
	"revoke": true, "option": true, "cascade": true, "restrict": true, "privileges": true, "the": true,
	"an": true,
}

// placeholderSymbols는 관계 자리에 오면 미해석으로 세는 한 글자 자리표시자 기호다 — Go fmt 동사(`%s`),
// 템플릿 구멍(`{}`·`{{.Table}}`), 바인드 매개변수(`?`·`:name`·`@name`). `$1`은 lexSQL이 따로 읽는다.
// 닫는 `}`는 자리를 열지 않으므로 기호 토큰일 뿐이다 — 구멍은 `{`에서 이미 셌고, `{{.Table}}`의
// Table이 관계 키워드로 읽혀 `}`를 또 세거나 그 뒤 별칭을 관계로 읽지 않게 목록을 끝내기만 한다.
// `%`·`:`·`@`는 동사나 이름이 바로 붙을 때만 자리를 연다(opensPlaceholder).
const placeholderSymbols = "%{?:@"

// opensPlaceholder는 text[i]의 자리표시자 기호가 실행 때 채워지는 자리를 여는지 본다. `?`·`{`는 늘
// 자리다. `:`·`@`는 이름이나 번호가 바로 붙을 때(`:name`·`:1`·`@name`·`@@var`, `::` 캐스트는 아니다),
// `%`는 fmt 동사가 바로 붙을 때(`%s`·`%[1]v`)만 자리다 — 산문 "update: pointer"의 `:`나 "100% of"의
// `%`는 자리가 아니라 목록을 끝내는 기호일 뿐이다.
func opensPlaceholder(text string, i int) bool {
	c := text[i]
	if c == '?' || c == '{' {
		return true
	}
	if i+1 >= len(text) {
		return false
	}
	next := text[i+1]
	switch c {
	case ':':
		return (i == 0 || text[i-1] != ':') && (isIdentStart(next) || isDigit(next))
	case '@':
		return isIdentStart(next) || next == '@'
	}
	// `%` 뒤의 공백·제어 문자는 동사가 아니고, `%%`는 fmt가 글자 `%` 하나로 찍는다.
	return next > ' ' && next != '%'
}

// sqlRelations는 SQL 텍스트에서 관계 이름을 읽는다.
// 한정 이름(`schema.table`)은 그대로 두고, 이름 자체에 점이 있는 인용
// 식별자("a.b")는 한 세그먼트로 읽는다 — escape는 사실 기록 시에 한다.
// 관계를 증명하지 못한 피연산자는 원인별로 센다: functions는 테이블 값 함수, placeholders는 관계 자리의
// 자리표시자다. 계열 공통 규칙(R1 함수 인자 안의 FROM, R2 같은 문장의 CTE 이름, R3 IS [NOT] DISTINCT
// FROM)과 GRANT·REVOKE 문장의 FROM·JOIN, 관계 자리의 절 키워드는 관계로 내지 않는다.
func sqlRelations(text string) (relations []string, functions, placeholders int) {
	tokens := lexSQL(text)
	r := &relationReader{
		tokens:   tokens,
		shape:    shapeSQL(tokens),
		seen:     map[string]bool{},
		consumed: make([]bool, len(tokens)),
	}
	for i := range tokens {
		r.visit(i)
	}
	return r.out, r.functions, r.placeholders
}

// relationReader는 sqlRelations 한 번의 판독 상태다.
type relationReader struct {
	tokens       []sqlToken
	shape        sqlShape
	out          []string
	seen         map[string]bool // TRUNCATE TABLE처럼 겹치는 키워드 창의 중복을 막는다
	consumed     []bool          // 이름·수식어로 소비된 토큰 — 키워드로 재발화하지 않는다
	functions    int             // 관계를 증명하지 못한 테이블 값 함수 피연산자 수
	placeholders int             // 관계 자리에 온 자리표시자 수
}

// visit은 i번째 토큰이 관계 자리를 여는 키워드면 뒤따르는 피연산자 목록을 읽는다.
func (r *relationReader) visit(i int) {
	tok := r.tokens[i]
	if r.consumed[i] || tok.quoted || !relationKeywords[strings.ToLower(tok.text)] || !r.fires(i) {
		return
	}
	r.readOperands(i, r.skipModifiers(i))
}

// fires는 관계 키워드가 그 자리에서 관계를 여는지 본다. GRANT·REVOKE 문장의 FROM·JOIN은 권한을 주고받는
// 사용자·역할 자리다. FROM은 함수 인자 괄호 바로 안(R1, `EXTRACT(YEAR FROM d)`)이거나 IS [NOT] DISTINCT
// FROM 비교 연산자의 일부(R3)일 때도 관계를 열지 않는다.
func (r *relationReader) fires(i int) bool {
	word := strings.ToLower(r.tokens[i].text)
	switch {
	case word != "from" && word != "join":
		return true
	case r.shape.grant[r.shape.statement[i]]:
		return false
	case word == "join":
		return true
	}
	return !r.shape.inFunctionArguments(r.tokens, i) && !isDistinctFrom(r.tokens, i)
}

// skipModifiers는 ONLY·IF NOT EXISTS 같은 수식어를 건너뛰고 첫 피연산자 위치를 돌려준다. `table`은
// TRUNCATE 뒤의 수식어일 때만 건너뛴다 — UPDATE table 같은 문에서 table이 진짜 관계 이름일 수 있고,
// 억지로 건너뛰면 SET 같은 다음 단어가 관계명으로 읽힌다.
func (r *relationReader) skipModifiers(keyword int) int {
	headIsTruncate := strings.EqualFold(r.tokens[keyword].text, "truncate")
	j := keyword + 1
	for j < len(r.tokens) && !r.tokens[j].quoted && isNameModifier(r.tokens[j].text, headIsTruncate) {
		r.consumed[j] = true
		j++
	}
	return j
}

// isNameModifier는 관계 키워드와 이름 사이에 올 수 있는 수식어인지 본다.
func isNameModifier(word string, afterTruncate bool) bool {
	return strings.EqualFold(word, "only") || strings.EqualFold(word, "if") ||
		strings.EqualFold(word, "not") || strings.EqualFold(word, "exists") ||
		(afterTruncate && strings.EqualFold(word, "table"))
}

// readOperands는 keyword 뒤 j부터 쉼표로 이어지는 피연산자 목록(`FROM a, b`)을 읽는다.
func (r *relationReader) readOperands(keyword, j int) {
	fromOrJoin := strings.EqualFold(r.tokens[keyword].text, "from") ||
		strings.EqualFold(r.tokens[keyword].text, "join")
	for j < len(r.tokens) {
		next, more := r.readOperand(j, fromOrJoin)
		if !more || next >= len(r.tokens) || r.tokens[next].text != "," {
			return
		}
		j = next + 1
	}
}

// readOperand는 j의 피연산자 하나를 읽어 그 뒤 위치와, 목록이 이어질 수 있는지를 돌려준다. 이름이
// 아니거나 이름을 알 수 없는 피연산자면 목록이 끝난 것이다(false).
func (r *relationReader) readOperand(j int, fromOrJoin bool) (int, bool) {
	tokens := r.tokens
	name, next := readQualifiedName(tokens, j)
	switch {
	case name == "":
		// 이름 자리의 절 키워드·괄호·기호는 목록의 끝이다 — 자리표시자만 미해석으로 센다.
		if j < len(tokens) && tokens[j].placeholder {
			r.placeholders++
		}
		return next, false
	case symbolAt(tokens, next, ".") && next+1 < len(tokens) && tokens[next+1].placeholder:
		// `public.%s`처럼 세그먼트 하나가 자리표시자면 관계 이름을 알 수 없다.
		r.placeholders++
		r.consume(j, next)
		return next, false
	case fromOrJoin && symbolAt(tokens, next, "("):
		// FROM/JOIN의 함수 이름은 테이블이 아니다. 괄호 안 SQL은 바깥 스캔이 읽는다.
		r.functions++
		r.consume(j, next)
		return r.skipFunctionAlias(r.shape.closeAfter[next]), true
	}
	r.consume(j, next)
	// R2: 한정자 없는 이름이 같은 문장의 CTE 이름이면 관계가 아니다.
	if !(next == j+1 && r.shape.isCTE(r.shape.statement[j], tokens[j])) && !r.seen[name] {
		r.seen[name] = true
		r.out = append(r.out, name)
	}
	return next, true
}

// skipFunctionAlias는 테이블 값 함수 괄호 뒤(next)의 별칭 — `AS p`, 또는 쉼표 바로 앞의 `p` — 을 건너뛴다.
func (r *relationReader) skipFunctionAlias(next int) int {
	if next+1 < len(r.tokens) && strings.EqualFold(r.tokens[next].text, "as") {
		return next + 2
	}
	if next+1 < len(r.tokens) && r.tokens[next+1].text == "," {
		return next + 1
	}
	return next
}

// consume은 [from, to) 토큰을 이름으로 소비했다고 표시한다 — 키워드로 다시 발화하지 않는다.
func (r *relationReader) consume(from, to int) {
	for k := from; k < to; k++ {
		r.consumed[k] = true
	}
}

// sqlShape는 토큰 목록을 한 번 훑어 미리 계산한 문장·괄호 정보다. 키워드마다 괄호 짝이나 문장 경계를
// 다시 훑으면 닫히지 않은 괄호가 반복되는 입력에서 작업이 토큰 수의 제곱이 된다. 재귀 없이 스택으로 센다.
type sqlShape struct {
	statement  []int             // 토큰 → 문장 번호(비인용 `;`를 지날 때마다 1씩 는다)
	group      []int             // 토큰 → 같은 문장 안에서 그 토큰을 감싼 가장 안쪽 `(` 위치(없으면 -1)
	closeAfter []int             // `(` 위치 → 짝 `)` 다음 위치(닫히지 않으면 토큰 수). 그 밖은 -1
	grant      []bool            // 문장 번호 → 머리 동사가 GRANT·REVOKE인지
	ctes       []map[string]bool // 문장 번호 → 그 문장의 CTE 이름 비교 키(R2)
	cteBodies  map[int]bool      // CTE 본문을 여는 `(` 위치 — 함수 인자 괄호가 아니다
}

// shapeSQL은 토큰 목록의 sqlShape를 계산한다.
func shapeSQL(tokens []sqlToken) sqlShape {
	s := sqlShape{
		statement:  make([]int, len(tokens)),
		group:      make([]int, len(tokens)),
		closeAfter: make([]int, len(tokens)),
		cteBodies:  map[int]bool{},
	}
	statements := s.markParens(tokens)
	s.grant = make([]bool, statements)
	s.ctes = make([]map[string]bool, statements)
	s.markGrants(tokens)
	s.markCTEs(tokens)
	return s
}

// markParens는 토큰마다 문장 번호와 감싼 괄호를, `(`마다 짝 `)`를 기록하고 문장 수를 돌려준다. 짝은 텍스트
// 전체의 스택으로 찾는다 — 테이블 값 함수 괄호를 건너뛰던 깊이 세기와 같은 짝이고, 짝 없는 `)`는 건너뛴다.
// 감싼 괄호(R1)는 문장마다 새로 센다 — 닫히지 않은 함수 괄호가 다음 문장의 FROM까지 삼키지 않게 한다.
func (s *sqlShape) markParens(tokens []sqlToken) int {
	var open, groups []int
	statement := 0
	for i, tok := range tokens {
		s.statement[i], s.group[i], s.closeAfter[i] = statement, -1, -1
		if len(groups) > 0 {
			s.group[i] = groups[len(groups)-1]
		}
		switch {
		case tok.quoted:
			// 인용 식별자는 기호가 아니다(`"("`라는 이름일 수 있다).
		case tok.text == ";":
			statement++
			groups = groups[:0]
		case tok.text == "(":
			s.closeAfter[i] = len(tokens)
			open, groups = append(open, i), append(groups, i)
		case tok.text == ")":
			if len(open) > 0 {
				s.closeAfter[open[len(open)-1]] = i + 1
				open = open[:len(open)-1]
			}
			if len(groups) > 0 {
				groups = groups[:len(groups)-1]
			}
		}
	}
	return statement + 1
}

// markGrants는 머리 동사(문장의 첫 이름 토큰)가 GRANT·REVOKE인 문장을 표시한다.
func (s *sqlShape) markGrants(tokens []sqlToken) {
	headed := -1 // 머리 동사를 이미 본 마지막 문장 번호
	for i, tok := range tokens {
		if s.statement[i] == headed || !isNameToken(tok) {
			continue
		}
		headed = s.statement[i]
		s.grant[headed] = strings.EqualFold(tok.text, "grant") || strings.EqualFold(tok.text, "revoke")
	}
}

// markCTEs는 문장마다 CTE 이름을 모은다(R2) — `WITH [RECURSIVE] name [(cols)] AS [NOT] [MATERIALIZED] (`와
// `, name [(cols)] AS [NOT] [MATERIALIZED] (`의 name이다. 본문을 여는 `(`도 기록한다 — MATERIALIZED 뒤
// 괄호를 함수 인자(R1)로 읽으면 `WITH d AS MATERIALIZED (DELETE FROM t …)`의 t를 잃는다.
func (s *sqlShape) markCTEs(tokens []sqlToken) {
	for i := range tokens {
		name := i + 1
		switch {
		case wordAt(tokens, i, "with"):
			if wordAt(tokens, name, "recursive") {
				name++
			}
		case !symbolAt(tokens, i, ","):
			continue
		}
		body, ok := cteBody(tokens, s.closeAfter, name)
		if !ok {
			continue
		}
		statement := s.statement[i]
		if s.ctes[statement] == nil {
			s.ctes[statement] = map[string]bool{}
		}
		s.ctes[statement][cteKey(tokens[name])] = true
		s.cteBodies[body] = true
	}
}

// cteBody는 name 위치에서 `name [(cols)] AS [NOT] [MATERIALIZED] (`가 시작하면 본문 `(` 위치를 돌려준다.
func cteBody(tokens []sqlToken, closeAfter []int, name int) (int, bool) {
	if name >= len(tokens) || !(isNameToken(tokens[name]) || (tokens[name].quoted && tokens[name].text != "")) {
		return 0, false
	}
	k := name + 1
	if symbolAt(tokens, k, "(") {
		k = closeAfter[k]
	}
	if !wordAt(tokens, k, "as") {
		return 0, false
	}
	k++
	if wordAt(tokens, k, "not") {
		k++
	}
	if wordAt(tokens, k, "materialized") {
		k++
	}
	return k, symbolAt(tokens, k, "(")
}

// isCTE는 한정자 없는 피연산자 tok이 같은 문장의 CTE 이름인지 본다(R2).
func (s *sqlShape) isCTE(statement int, tok sqlToken) bool {
	return s.ctes[statement][cteKey(tok)]
}

// cteKey는 CTE 이름과 피연산자의 비교 키다. 비인용 이름은 소문자로 접고 인용 이름은 그대로 둔다 —
// PostgreSQL의 식별자 규칙이고 isthmus 참조 구현과 같다(계약은 대소문자가 다른 경우를 정하지 않는다).
func cteKey(tok sqlToken) string {
	if tok.quoted {
		return tok.text
	}
	return strings.ToLower(tok.text)
}

// inFunctionArguments는 i번째 토큰이 함수 인자 괄호 바로 안에 있는지 본다(R1). 함수 인자 괄호는 바로 앞이
// 함수 이름이고 첫 토큰이 SELECT·WITH·VALUES(하위 질의)가 아닌 괄호다. 함수 이름은 절 키워드가 아닌 비인용
// 식별자이고, CTE 본문 괄호는 함수 인자가 아니다 — `AS (DELETE FROM t …)`·`MATERIALIZED (…)`의 FROM은
// 관계를 낸다.
func (s *sqlShape) inFunctionArguments(tokens []sqlToken, i int) bool {
	open := s.group[i]
	if open < 1 || s.cteBodies[open] {
		return false
	}
	callee := tokens[open-1]
	if !isNameToken(callee) || clauseWords[strings.ToLower(callee.text)] {
		return false
	}
	return !wordAt(tokens, open+1, "select") && !wordAt(tokens, open+1, "with") &&
		!wordAt(tokens, open+1, "values")
}

// isDistinctFrom은 i번째 FROM이 IS [NOT] DISTINCT FROM 비교 연산자의 일부인지 본다(R3).
func isDistinctFrom(tokens []sqlToken, i int) bool {
	return wordAt(tokens, i-1, "distinct") &&
		(wordAt(tokens, i-2, "is") || (wordAt(tokens, i-2, "not") && wordAt(tokens, i-3, "is")))
}

// wordAt은 k번째 토큰이 인용되지 않은 단어 word인지 대소문자 없이 본다(범위 밖이면 false).
func wordAt(tokens []sqlToken, k int, word string) bool {
	return k >= 0 && k < len(tokens) && !tokens[k].quoted && strings.EqualFold(tokens[k].text, word)
}

// symbolAt은 k번째 토큰이 인용되지 않은 기호 symbol인지 본다(범위 밖이면 false).
func symbolAt(tokens []sqlToken, k int, symbol string) bool {
	return k >= 0 && k < len(tokens) && !tokens[k].quoted && tokens[k].text == symbol
}

// lexSQL은 SQL 텍스트를 어휘로 나눈다 — 인용 식별자는 내용을 보존하고
// 그 외엔 식별자 문자열과 단일 기호 토큰만 만든다. `;`는 문장 경계로,
// 자리표시자 기호와 `$1`은 관계 자리의 미해석 판정용으로 남긴다.
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
		case c == '$' && i+1 < len(text) && isDigit(text[i+1]):
			// `$1` 위치 매개변수는 이름이 아니라 자리표시자다 — `$`는 식별자 시작 문자라 따로 읽지
			// 않으면 `FROM $1`이 관계 이름 `$1`이 된다. `$name`은 지금처럼 식별자다.
			j := i + 1
			for j < len(text) && isDigit(text[j]) {
				j++
			}
			tokens = append(tokens, sqlToken{text: text[i:j], placeholder: true})
			i = j
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
			// 문자열 리터럴은 이름이 아니다 — '' 와 \' 는 escape다.
			i++
			for i < len(text) {
				if text[i] == '\\' {
					i += 2
					continue
				}
				if text[i] == '\'' {
					if i+1 < len(text) && text[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		default:
			switch {
			case c == '.' || c == ',' || c == '(' || c == ')' || c == ';' || c == '}':
				tokens = append(tokens, sqlToken{text: string(c)})
			case strings.IndexByte(placeholderSymbols, c) >= 0:
				tokens = append(tokens, sqlToken{text: string(c), placeholder: opensPlaceholder(text, i)})
			}
			i++
		}
	}
	return tokens
}

// readQualifiedName은 `ident(.ident)*` 한정 이름을 읽어 다음 위치를 돌려준다.
// 첫 세그먼트가 절 키워드면 이름이 아니다(관계 자리가 비었다). 점 뒤 세그먼트는 예약어여도
// 식별자다(PostgreSQL attr_name·MySQL 규칙, `shop.order`) — 그래서 절 키워드를 거르지 않는다.
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
	} else if !isNameToken(first) || clauseWords[strings.ToLower(first.text)] {
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

// isNameToken은 비인용 토큰이 식별자인지 본다 — 기호·자리표시자(`$1`)·빈 문자열은 아니다.
func isNameToken(tok sqlToken) bool {
	if tok.quoted || tok.placeholder || tok.text == "" {
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
	return isIdentStart(c) || isDigit(c)
}

// isDigit은 ASCII 숫자인지 본다.
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// pushDynamic은 리터럴로 읽히지 않는 SQL 인자를 동적 사실로 보존한다.
// channel에는 잘린 원문 표현식을 실어 어느 위치의 호출인지 남긴다. 원문은 dynamicSQLChannel로
// 다듬는다 — 여러 줄 식(`"SELECT … " +` 뒤 줄바꿈)을 go/printer가 줄바꿈·탭째 찍고, raw 문자열
// 피연산자는 제어 문자를 그대로 담는다. isthmus는 그런 채널이 하나라도 있으면 문서를 통째로 거부한다.
func (s *schemaScan) pushDynamic(p *packages.Package, expr ast.Expr) {
	var buf strings.Builder
	if err := printer.Fprint(&buf, p.Fset, expr); err != nil {
		return
	}
	// dynamic 수치는 비리터럴 인자만 센다 — 미해석 피연산자의 dynamic 사실(scanLiteral)은
	// table-valued 문구와 자리표시자 문구가 따로 센다.
	if s.push(RelationFact{
		Kind:     "relation-use",
		Channel:  dynamicSQLChannel(buf.String()),
		Dynamic:  true,
		Location: s.locate(p.Fset, expr.Pos()),
		owner:    s.owner,
	}) {
		s.dynamic++
	}
}

// push는 위치가 있는 사실만 담고 같은 사실을 한 번만 남긴다. 새로 담았는지를 돌려준다 —
// 호출자가 자기 종류의 수치를 담은 사실만큼만 센다.
func (s *schemaScan) push(fact RelationFact) bool {
	if fact.Location == nil {
		return false
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	key := fmt.Sprintf("%s|%s|%s|%v|%s:%d:%d",
		fact.Kind, fact.Channel, fact.Method, fact.Dynamic,
		fact.Location.Path, fact.Location.Line, fact.Location.Column)
	if s.seen[key] {
		return false
	}
	s.seen[key] = true
	s.list = append(s.list, fact)
	return true
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
// 계약상 항상 배열이라 빈 경우에도 nil이 아닌 빈 슬라이스를 돌려준다.
func (s *schemaScan) limitations() []string {
	out := []string{}
	if s.sqlFunctions > 0 {
		// 원인을 설명하는 문구다 — isthmus는 생산자 문구를 근거로 쓰지 않고 dynamic 사실을 직접
		// 센다. 그래서 그런 피연산자가 든 리터럴은 scanLiteral이 dynamic 사실로도 낸다.
		out = append(out, fmt.Sprintf("unjoined-dynamic-relations: %d table-valued SQL function operand(s) have unmodeled relation dependencies", s.sqlFunctions))
	}
	if s.placeholders > 0 {
		// 테이블 값 함수 문구와 같은 이유로 남긴다 — 그런 리터럴도 scanLiteral이 dynamic 사실로 낸다.
		out = append(out, fmt.Sprintf(
			"unjoined-dynamic-relations: %d SQL placeholder operand(s) (%%s, {}, ?, $1, :name, @name) stand where a relation name belongs; the relations filled in at run time are uncounted",
			s.placeholders))
	}
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
	if s.missingUsrs > 0 {
		// isthmus 체인 전용 접두사다 — check 심각도에는 영향이 없고 trace가 그
		// 사실에서 순회로 이어 가지 못한다는 것을 드러낸다.
		out = append(out, fmt.Sprintf(
			"missing-relation-usrs: %d relation-use fact(s) are outside any symbol vertex of the impact graph; they carry no symbol",
			s.missingUsrs))
	}
	if s.dynamic > 0 {
		// 원인을 설명하는 문구다 — isthmus는 이 문구가 아니라 dynamic 사실을 직접 세어(origin
		// consumer) 미사용 진단을 unverified로 내린다. 수치는 비리터럴 SQL 인자(pushDynamic)만 센다.
		out = append(out, fmt.Sprintf(
			"unjoined-dynamic-relations: %d SQL argument(s) were not literals; their relations are uncounted",
			s.dynamic))
	}
	return out
}
