// isthmus `http-wrappers` v1 선언 — 사용자가 선언한 Go HTTP 래퍼의 호출을 route-call로 낸다.
//
// 스키마·해석 규칙의 정본은 ../isthmus의 docs/HTTP-WRAPPERS.md다. Go 규칙(`"language": "go"` 항목만):
//   - kind "function": 패키지 함수는 owner = import 경로(`example.com/app/api`), 메서드는 owner =
//     `import경로.타입이름`(포인터·타입 파라미터 없이, 메서드를 선언한 타입 — 임베드로 승격된 메서드는
//     임베드된 타입). 인터페이스 메서드도 된다. name = 함수·메서드 이름.
//   - kind "constructor": struct 리터럴(`T{…}`·`&T{…}`). owner = `import경로.타입이름`, name = 타입 이름.
//   - 인자: index는 호출 인자 위치(리시버 제외, 0부터), label은 선언의 파라미터 이름(생성자는 필드
//     이름)이다. Go 호출에는 이름 붙은 인자가 없으므로 위치 인자에 그 자리의 파라미터 이름을 레이블로
//     단 뒤 계약의 바인딩 규칙(레이블 먼저, 없으면 위치)을 그대로 쓴다.
//   - methodEnum의 case는 이름 있는 상수의 이름이다(`api.Get`의 `Get`). 매핑에 없으면 문자열 상수 값이
//     계약 동사와 정확히 같을 때만 동사다(http.MethodGet은 "GET").
package source

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"strings"

	"golang.org/x/tools/go/packages"
)

// WrapperFile은 http-wrappers v1 선언 파일이다.
type WrapperFile struct {
	Format   string        `json:"format"`
	Version  int           `json:"version"`
	Wrappers []WrapperDecl `json:"wrappers"`
}

// WrapperDecl은 래퍼 선언 하나다.
type WrapperDecl struct {
	Language      string            `json:"language"`
	Kind          string            `json:"kind"`
	Owner         string            `json:"owner"`
	Name          string            `json:"name"`
	MethodArg     *WrapperArgRef    `json:"methodArg,omitempty"`
	PathArg       *WrapperArgRef    `json:"pathArg"`
	DefaultMethod string            `json:"defaultMethod,omitempty"`
	MethodEnum    map[string]string `json:"methodEnum,omitempty"`
	PathAnchor    string            `json:"pathAnchor"`
	Service       string            `json:"service,omitempty"`
}

// WrapperArgRef는 인자 지정이다(index·label 중 하나 이상).
type WrapperArgRef struct {
	Index *int   `json:"index,omitempty"`
	Label string `json:"label,omitempty"`
}

// wrapperLanguages는 계약이 받는 호출 측 생산자 언어다. go 외의 항목은 검증만 하고 적용하지 않는다.
var wrapperLanguages = map[string]bool{"swift": true, "kotlin": true, "dart": true, "js": true, "go": true}

// LoadWrapperFile은 선언 파일을 읽고 검증한다. 모르는 필드와 잘못된 선언은 오류다 — 조용히 무시하면
// 낡은 선언이 호출 0건을 내어 "호출 없음"으로 읽힌다.
func LoadWrapperFile(path string) (*WrapperFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading --wrappers %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file WrapperFile
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("--wrappers %s is not a valid http-wrappers v1 document: %w", path, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("--wrappers %s has trailing content after the JSON document", path)
	}
	if err := file.validate(); err != nil {
		return nil, fmt.Errorf("--wrappers %s: %w", path, err)
	}
	return &file, nil
}

// validate는 문서와 선언 전체를 검증한다.
func (f *WrapperFile) validate() error {
	if f.Format != "http-wrappers" || f.Version != 1 {
		return fmt.Errorf(`format must be "http-wrappers" and version 1`)
	}
	if f.Wrappers == nil {
		return fmt.Errorf("wrappers must be an array")
	}
	for i, w := range f.Wrappers {
		if err := w.validate(); err != nil {
			return fmt.Errorf("wrappers[%d]: %w", i, err)
		}
	}
	return nil
}

// validate는 선언 하나를 검증한다.
func (w WrapperDecl) validate() error {
	switch {
	case !wrapperLanguages[w.Language]:
		return fmt.Errorf("language %q is not one of swift, kotlin, dart, js, go", w.Language)
	case w.Kind != "constructor" && w.Kind != "function":
		return fmt.Errorf(`kind must be "constructor" or "function"`)
	case strings.TrimSpace(w.Owner) == "" || strings.TrimSpace(w.Name) == "":
		return fmt.Errorf("owner and name must be non-empty")
	case w.PathArg == nil || !w.PathArg.valid():
		return fmt.Errorf("pathArg needs a non-negative index or a label")
	case w.MethodArg != nil && !w.MethodArg.valid():
		return fmt.Errorf("methodArg needs a non-negative index or a label")
	case w.MethodArg == nil && w.DefaultMethod == "":
		return fmt.Errorf("a wrapper without methodArg needs defaultMethod")
	case w.DefaultMethod != "" && !routeMethods[w.DefaultMethod]:
		return fmt.Errorf("defaultMethod %q is not an HTTP method of the contract", w.DefaultMethod)
	case w.PathAnchor != "root" && w.PathAnchor != "base":
		return fmt.Errorf(`pathAnchor must be "root" or "base"`)
	}
	for k, v := range w.MethodEnum {
		if !routeMethods[v] {
			return fmt.Errorf("methodEnum[%q] = %q is not an HTTP method of the contract", k, v)
		}
	}
	return nil
}

// valid는 인자 지정이 비어 있지 않은지 본다.
func (r *WrapperArgRef) valid() bool {
	return (r.Index != nil && *r.Index >= 0) || r.Label != ""
}

// wrapperArg는 바인딩 규칙이 보는 호출 인자 하나다. 벡터(conformance)와 Go 호출이 같은 모양을 쓴다.
type wrapperArg struct {
	label string
	// enumCase는 이름 있는 상수의 이름(벡터의 enumCase)이다.
	enumCase string
	// literal은 문자열 상수 값이다(없으면 nil).
	literal *string
	expr    ast.Expr
}

// bindWrapperArg는 계약의 바인딩 규칙(wrapper.method)으로 인자를 찾는다: label이 있으면 같은 레이블의
// 인자를 먼저, 없으면 index 위치의 인자를 쓰되 그 인자가 선언과 다른 레이블을 달고 있으면 쓰지 않는다.
func bindWrapperArg(ref *WrapperArgRef, args []wrapperArg) (wrapperArg, bool) {
	if ref == nil {
		return wrapperArg{}, false
	}
	if ref.Label != "" {
		for _, a := range args {
			if a.label == ref.Label {
				return a, true
			}
		}
	}
	if ref.Index == nil || *ref.Index >= len(args) {
		return wrapperArg{}, false
	}
	a := args[*ref.Index]
	if ref.Label != "" && a.label != "" && a.label != ref.Label {
		return wrapperArg{}, false
	}
	return a, true
}

// wrapperMethod는 선언과 호출 인자로 동사를 정한다. 동사를 증명하지 못하면 빈 문자열이다(methodDynamic).
//   - 인자가 없으면 defaultMethod
//   - enum case가 methodEnum에 있으면 그 동사
//   - 문자열 상수가 계약 동사와 정확히 같으면 그 동사(소문자 "get"은 동사가 아니다)
func wrapperMethod(decl WrapperDecl, args []wrapperArg) string {
	if decl.MethodArg == nil {
		return decl.DefaultMethod
	}
	arg, ok := bindWrapperArg(decl.MethodArg, args)
	if !ok {
		return decl.DefaultMethod
	}
	if m, ok := decl.MethodEnum[arg.enumCase]; ok && arg.enumCase != "" {
		return m
	}
	if arg.literal != nil && routeMethods[*arg.literal] {
		return *arg.literal
	}
	return ""
}

// goWrapper는 적용할 Go 선언 하나와 조회 키다.
type goWrapper struct {
	index int
	decl  WrapperDecl
	// keys는 함수 선언이면 funcKey 후보("pkg..Name"·"pkg.Type.Name"), 생성자면 typeKey("pkg.Type")다.
	keys  map[string]bool
	found bool
	calls int
	// bodies는 선언된 함수의 정점 ID다 — 래퍼 본문의 dynamic 요청은 내지 않는다.
	bodies map[string]bool
}

// goWrappers는 선언 파일에서 Go 항목만 고른다.
func goWrappers(file *WrapperFile) []*goWrapper {
	if file == nil {
		return nil
	}
	var out []*goWrapper
	for i, d := range file.Wrappers {
		if d.Language != "go" {
			continue
		}
		w := &goWrapper{index: i, decl: d, keys: map[string]bool{}, bodies: map[string]bool{}}
		if d.Kind == "constructor" {
			w.keys[d.Owner] = true
		} else {
			w.keys[d.Owner+".."+d.Name] = true
			if i := strings.LastIndex(d.Owner, "."); i > 0 && isGoIdent(d.Owner[i+1:]) {
				w.keys[d.Owner[:i]+"."+d.Owner[i+1:]+"."+d.Name] = true
			}
		}
		out = append(out, w)
	}
	return out
}

// isGoIdent는 Go 식별자 모양인지 본다(ASCII만 — 선언 owner의 타입 이름 판정용).
func isGoIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		letter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// resolveDeclarations는 선언이 실제 심볼과 맞는지 로드한 패키지(의존 포함)에서 확인하고, 함수 선언의
// 정점 ID를 모은다. 생성자 선언은 owner가 struct 타입이고 name이 그 타입 이름이어야 한다.
func resolveDeclarations(wrappers []*goWrapper, pkgs []*packages.Package, ids symbolIDs) {
	byPath := map[string]*types.Package{}
	for _, p := range walkImports(pkgs) {
		if p.Types != nil {
			byPath[p.PkgPath] = p.Types
		}
	}
	for _, w := range wrappers {
		if w.decl.Kind == "constructor" {
			w.found = structTypeNamed(byPath, w.decl.Owner, w.decl.Name)
			continue
		}
		for _, fn := range declaredFuncs(byPath, w.decl.Owner, w.decl.Name) {
			w.found = true
			w.bodies[ids.objectVertexID(fn)] = true
		}
	}
}

// structTypeNamed는 owner가 name이라는 struct 타입인지 본다.
func structTypeNamed(byPath map[string]*types.Package, owner, name string) bool {
	i := strings.LastIndex(owner, ".")
	if i <= 0 || owner[i+1:] != name {
		return false
	}
	pkg := byPath[owner[:i]]
	if pkg == nil {
		return false
	}
	tn, ok := pkg.Scope().Lookup(name).(*types.TypeName)
	if !ok {
		return false
	}
	_, isStruct := tn.Type().Underlying().(*types.Struct)
	return isStruct
}

// declaredFuncs는 owner·name이 가리키는 함수(패키지 함수, 타입에 선언된 메서드, 인터페이스 메서드)다.
func declaredFuncs(byPath map[string]*types.Package, owner, name string) []*types.Func {
	var out []*types.Func
	if pkg := byPath[owner]; pkg != nil {
		if fn, ok := pkg.Scope().Lookup(name).(*types.Func); ok {
			out = append(out, fn)
		}
	}
	i := strings.LastIndex(owner, ".")
	if i <= 0 {
		return out
	}
	pkg := byPath[owner[:i]]
	if pkg == nil {
		return out
	}
	tn, ok := pkg.Scope().Lookup(owner[i+1:]).(*types.TypeName)
	if !ok {
		return out
	}
	return append(out, typeMethodsNamed(tn, name)...)
}

// typeMethodsNamed는 타입에 직접 선언된(인터페이스면 명시한) 이름 같은 메서드다.
func typeMethodsNamed(tn *types.TypeName, name string) []*types.Func {
	var out []*types.Func
	if iface, ok := tn.Type().Underlying().(*types.Interface); ok {
		for m := range iface.ExplicitMethods() {
			if m.Name() == name {
				out = append(out, m)
			}
		}
		return out
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return out
	}
	for m := range named.Methods() {
		if m.Name() == name {
			out = append(out, m)
		}
	}
	return out
}

// callArgs는 Go 호출 인자를 바인딩 모양으로 바꾼다. 레이블은 그 자리의 파라미터 이름이다(가변 인자는
// 마지막 파라미터 이름).
func callArgs(info *types.Info, sig *types.Signature, args []ast.Expr) []wrapperArg {
	params := sig.Params()
	out := make([]wrapperArg, len(args))
	for i, a := range args {
		at := i
		if at >= params.Len() {
			at = params.Len() - 1
		}
		label := ""
		if at >= 0 {
			label = params.At(at).Name()
		}
		out[i] = goArg(info, label, a)
	}
	return out
}

// literalArgs는 struct 리터럴 원소를 바인딩 모양으로 바꾼다. 레이블은 필드 이름이다.
func literalArgs(info *types.Info, st *types.Struct, lit *ast.CompositeLit) []wrapperArg {
	var out []wrapperArg
	for i, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok {
				out = append(out, goArg(info, key.Name, kv.Value))
			}
			continue
		}
		label := ""
		if i < st.NumFields() {
			label = st.Field(i).Name()
		}
		out = append(out, goArg(info, label, elt))
	}
	return out
}

// goArg는 인자 식 하나의 enum case(이름 있는 상수의 이름)와 문자열 상수 값을 읽는다.
func goArg(info *types.Info, label string, expr ast.Expr) wrapperArg {
	arg := wrapperArg{label: label, expr: expr}
	var id *ast.Ident
	switch e := unparen(expr).(type) {
	case *ast.Ident:
		id = e
	case *ast.SelectorExpr:
		id = e.Sel
	}
	if id != nil {
		if c, ok := info.Uses[id].(*types.Const); ok {
			arg.enumCase = c.Name()
		}
	}
	if s, ok := constantString(info, expr); ok {
		arg.literal = &s
	}
	return arg
}
