// 이름 없는 외부 인터페이스 디스패치 수확.
//
// errors.Is/As/Unwrap은 `interface{ Unwrap() error }` 같은 이름 없는
// 인터페이스로 타입 단언해 메서드를 부른다. 이런 표기는 의존 패키지의 함수
// 본문·인자 타입에만 있어 export data(패키지 스코프)로는 보이지 않는다.
// 그래서 의존 소스를 직접 훑어 표기를 모으고, 이미 로드된 types.Package로
// 해석해 명명 인터페이스와 같은 Satisfies 사실의 재료로 쓴다.
package source

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// anonIfacePattern은 파싱 전 값싼 사전 필터다 — 메서드가 있을 수 있는
// 인터페이스 표기("interface{" 뒤에 '}'가 아닌 글자)가 없는 파일은 읽기만
// 하고 파싱하지 않는다. 주석 안 표기도 걸리지만 파싱이 걸러 준다(상위집합).
var anonIfacePattern = regexp.MustCompile(`interface\s*\{\s*[^}\s]`)

// synthPackage는 리터럴 해석용 합성 패키지 이름이다. 의존 패키지 이름과
// 겹치지 않을 이름이어야 dot import한 이름과 충돌하지 않는다.
const synthPackage = "gartographanon"

// anonScan은 의존 소스 스캔의 누적 결과다.
// unresolved·unreadable은 limitation 재료다 — 해석 못 한 표기를 조용히 버리면
// 그 인터페이스로만 불리는 메서드가 unreachable로 보이는 이유를 알 수 없다.
type anonScan struct {
	ifaces     []externalIface
	seen       map[string]bool
	unresolved int // 타입 파라미터·비공개 로컬 타입 등으로 해석 못 한 표기 수
	unreadable int // 읽거나 파싱하지 못한 의존 소스 파일 수
}

// scanAnonymousInterfaces는 모듈 밖 패키지 소스의 이름 없는 인터페이스 표기를
// 모아 해석한다. 결과는 이름 순이다 — 순회 순서가 사실 순서를 흔들지 않게.
func scanAnonymousInterfaces(internal []*packages.Package) *anonScan {
	scan := &anonScan{seen: map[string]bool{}}
	for _, p := range dependencyPackages(internal) {
		for _, path := range p.GoFiles {
			scan.scanFile(p, path)
		}
	}
	sort.Slice(scan.ifaces, func(i, j int) bool { return scan.ifaces[i].name < scan.ifaces[j].name })
	return scan
}

// limitations는 스캔이 실제로 세어 둔 못 본 영역을 문장으로 돌려준다.
// "unreachable로 보일 수 있다" 같은 판정 결과가 아니라 문서 사실(사실이 빠졌다)로
// 쓴다 — RTA는 의존 코드를 SSA로 직접 봐서 같은 메서드를 살리므로, 결과를
// 말하는 문구는 알고리즘에 따라 거짓이 된다.
func (s *anonScan) limitations() []string {
	var out []string
	if s.unresolved > 0 {
		out = append(out, fmt.Sprintf(
			"%d interface literals in dependencies could not be resolved (type parameters or unexported local types); "+
				"methods satisfying only them carry no satisfies fact", s.unresolved))
	}
	if s.unreadable > 0 {
		out = append(out, fmt.Sprintf(
			"%d dependency source files could not be read or parsed; their anonymous interfaces produce no satisfies facts",
			s.unreadable))
	}
	return out
}

// dependencyPackages는 모듈 패키지에서 import로 닿는 모듈 밖 패키지를
// 경로 순으로 모은다. 타입 정보가 없는 패키지는 해석할 재료가 없어 뺀다.
func dependencyPackages(internal []*packages.Package) []*packages.Package {
	inModule := make(map[string]bool, len(internal))
	for _, p := range internal {
		inModule[p.PkgPath] = true
	}
	seen := map[string]*packages.Package{}
	queue := append([]*packages.Package(nil), internal...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, imp := range cur.Imports {
			if _, ok := seen[imp.PkgPath]; ok || inModule[imp.PkgPath] {
				continue
			}
			seen[imp.PkgPath] = imp
			queue = append(queue, imp)
		}
	}
	return sortedWithTypes(seen)
}

// sortedWithTypes는 타입 정보가 있는 패키지만 경로 순으로 돌려준다.
func sortedWithTypes(pkgs map[string]*packages.Package) []*packages.Package {
	out := make([]*packages.Package, 0, len(pkgs))
	for _, p := range pkgs {
		if p.Types != nil {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PkgPath < out[j].PkgPath })
	return out
}

// scanFile은 의존 소스 파일 하나의 표기를 해석해 누적한다.
func (s *anonScan) scanFile(p *packages.Package, path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		s.unreadable++
		return
	}
	if !anonIfacePattern.Match(src) {
		return
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		s.unreadable++
		return
	}
	lits := interfaceLiterals(file)
	if len(lits) == 0 {
		return
	}
	s.addResolved(resolveLiterals(p, file, literalTexts(fset, src, lits)))
}

// addResolved는 해석 결과를 이름으로 중복 제거해 누적한다.
// 비공개 메서드가 있는 인터페이스는 선언 패키지 밖 타입이 구현할 수 없어 뺀다.
func (s *anonScan) addResolved(ifaces []*types.Interface, unresolved int) {
	s.unresolved += unresolved
	for _, iface := range ifaces {
		if iface.NumMethods() == 0 || hasUnexportedMethod(iface) {
			continue
		}
		name := types.TypeString(iface, nil)
		if s.seen[name] {
			continue
		}
		s.seen[name] = true
		s.ifaces = append(s.ifaces, newExternalIface(name, iface))
	}
}

// hasUnexportedMethod는 인터페이스 메서드 집합에 비공개 메서드가 있는지 본다.
func hasUnexportedMethod(iface *types.Interface) bool {
	for i := 0; i < iface.NumMethods(); i++ {
		if !iface.Method(i).Exported() {
			return true
		}
	}
	return false
}

// interfaceLiterals는 파일에서 메서드·임베드 요소가 있고 비공개 메서드를 직접
// 선언하지 않은 인터페이스 표기를 모은다.
// 패키지 스코프 type 선언의 인터페이스는 뺀다 — export data로 이미 명명
// 인터페이스로 모았다(externalInterfaces). 함수 안 type 선언은 패키지 스코프에
// 없어 export data에 안 보이므로 여기서 잡는다.
func interfaceLiterals(file *ast.File) []*ast.InterfaceType {
	topLevel := packageScopeInterfaces(file)
	var out []*ast.InterfaceType
	ast.Inspect(file, func(n ast.Node) bool {
		it, ok := n.(*ast.InterfaceType)
		if ok && !topLevel[it] && it.Methods != nil && len(it.Methods.List) > 0 &&
			!declaresUnexportedMethod(it) {
			out = append(out, it)
		}
		return true
	})
	return out
}

// declaresUnexportedMethod는 표기가 비공개 메서드를 직접 선언하는지 구문으로 본다.
// 그런 인터페이스는 선언 패키지 밖 타입이 구현할 수 없다 — 해석 전에 걸러야
// 해석 실패(비공개 로컬 타입 등)가 unresolved 수치를 부풀리지 않는다.
func declaresUnexportedMethod(it *ast.InterfaceType) bool {
	for _, field := range it.Methods.List {
		for _, name := range field.Names {
			if !name.IsExported() {
				return true
			}
		}
	}
	return false
}

// packageScopeInterfaces는 파일의 패키지 스코프 type 선언이 직접 가진 인터페이스다.
func packageScopeInterfaces(file *ast.File) map[*ast.InterfaceType]bool {
	out := map[*ast.InterfaceType]bool{}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if it, ok := spec.(*ast.TypeSpec).Type.(*ast.InterfaceType); ok {
				out[it] = true
			}
		}
	}
	return out
}

// literalTexts는 표기들의 원문을 돌려준다 — 합성 파일에 그대로 옮긴다.
func literalTexts(fset *token.FileSet, src []byte, lits []*ast.InterfaceType) []string {
	out := make([]string, len(lits))
	for i, lit := range lits {
		start := fset.Position(lit.Pos()).Offset
		end := fset.Position(lit.End()).Offset
		out[i] = string(src[start:end])
	}
	return out
}

// resolveLiterals는 표기 원문을 합성 파일로 타입체크해 인터페이스 타입을 얻는다.
// 합성 파일은 원 파일의 import를 같은 이름으로 옮기고 원 패키지를 dot import한다
// — 한정 이름(time.Time)과 원 패키지의 공개 이름(Token)이 같은 types 객체로
// 풀려야 모듈 메서드와 Implements로 비교할 수 있다. 원 패키지의 비공개 이름·
// 타입 파라미터를 쓰는 표기는 풀리지 않아 unresolved로 센다.
func resolveLiterals(p *packages.Package, file *ast.File, texts []string) ([]*types.Interface, int) {
	src, spans := synthSource(p, file, texts)
	fset := token.NewFileSet()
	synth, err := parser.ParseFile(fset, "synth.go", src, parser.SkipObjectResolution)
	if err != nil {
		// 원문 조각이 합성 문맥에서 구문 오류가 되는 경우 — 전부 못 푼 것으로 센다.
		return nil, len(texts)
	}
	info, errorOffsets := checkSynth(fset, synth, p)
	return collectLiteralTypes(synth, info, spans, errorOffsets)
}

// literalSpan은 합성 파일 안에서 표기 하나가 차지하는 바이트 범위다.
type literalSpan struct{ start, end int }

// synthSource는 합성 파일 원문과 각 표기의 위치를 만든다.
func synthSource(p *packages.Package, file *ast.File, texts []string) (string, []literalSpan) {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\nimport (\n\t. %q\n", synthPackage, p.PkgPath)
	for _, spec := range file.Imports {
		writeImport(&b, p, spec)
	}
	b.WriteString(")\n\n")
	spans := make([]literalSpan, len(texts))
	for i, text := range texts {
		b.WriteString("var _ ")
		spans[i].start = b.Len()
		b.WriteString(text)
		spans[i].end = b.Len()
		b.WriteString("\n")
	}
	return b.String(), spans
}

// writeImport는 원 파일의 import 하나를 명시 이름으로 옮긴다.
// 이름 없는 import는 대상 패키지의 실제 이름을 쓴다 — 경로 끝 조각과 패키지
// 이름이 다른 경우(gopkg.in/yaml.v3 → yaml)가 있다. 빈 식별자·cgo는 표기가
// 참조할 수 없어 옮기지 않는다.
func writeImport(b *strings.Builder, p *packages.Package, spec *ast.ImportSpec) {
	path, err := strconv.Unquote(spec.Path.Value)
	if err != nil || path == "C" {
		return
	}
	imp := p.Imports[path]
	if imp == nil || imp.Types == nil {
		return
	}
	name := imp.Types.Name()
	if spec.Name != nil {
		name = spec.Name.Name
	}
	if name == "_" {
		return
	}
	fmt.Fprintf(b, "\t%s %q\n", name, path)
}

// checkSynth는 합성 파일을 타입체크해 타입 정보와 오류가 난 바이트 위치를 돌려준다.
// "imported and not used" 같은 import 줄 오류는 표기 범위 밖이라 무관하다 —
// 그래서 첫 오류에서 멈추지 않고 전부 모은다.
func checkSynth(fset *token.FileSet, synth *ast.File, p *packages.Package) (*types.Info, []int) {
	var offsets []int
	conf := types.Config{
		Importer: synthImporter(p),
		Error: func(err error) {
			if te, ok := err.(types.Error); ok {
				offsets = append(offsets, fset.Position(te.Pos).Offset)
			}
		},
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	// 반환 오류는 Error 콜백으로 이미 모았다 — 위치별 판정은 collectLiteralTypes가 한다.
	_, _ = conf.Check(synthPackage, fset, []*ast.File{synth}, info)
	return info, offsets
}

// synthImporter는 합성 파일의 import를 이미 로드된 types.Package로 푼다.
// 새로 로드하면 같은 경로라도 다른 객체가 되어 Implements가 항상 거짓이 된다.
func synthImporter(p *packages.Package) types.Importer {
	return importerFunc(func(path string) (*types.Package, error) {
		if path == p.PkgPath {
			return p.Types, nil
		}
		if path == "unsafe" {
			return types.Unsafe, nil
		}
		if imp := p.Imports[path]; imp != nil && imp.Types != nil {
			return imp.Types, nil
		}
		return nil, fmt.Errorf("package %s is not loaded; cannot resolve its names", path)
	})
}

// importerFunc는 함수를 types.Importer로 쓰게 하는 어댑터다.
type importerFunc func(path string) (*types.Package, error)

// Import는 types.Importer 계약이다.
func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

// collectLiteralTypes는 오류가 범위 안에 없는 표기의 인터페이스 타입을 모은다.
func collectLiteralTypes(synth *ast.File, info *types.Info, spans []literalSpan,
	errorOffsets []int) ([]*types.Interface, int) {
	var out []*types.Interface
	unresolved := 0
	for i, expr := range synthVarTypes(synth) {
		iface, ok := info.Types[expr].Type.(*types.Interface)
		if !ok || spanHasError(spans[i], errorOffsets) {
			unresolved++
			continue
		}
		out = append(out, iface)
	}
	return out, unresolved
}

// synthVarTypes는 합성 파일의 `var _ T` 선언들의 타입 식을 선언 순으로 돌려준다.
func synthVarTypes(synth *ast.File) []ast.Expr {
	var out []ast.Expr
	for _, decl := range synth.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			out = append(out, spec.(*ast.ValueSpec).Type)
		}
	}
	return out
}

// spanHasError는 오류 위치 중 하나가 표기 범위 안에 있는지 본다.
func spanHasError(span literalSpan, offsets []int) bool {
	for _, off := range offsets {
		if off >= span.start && off < span.end {
			return true
		}
	}
	return false
}
