// route-decl의 symbol.usr — 등록 호출의 핸들러 인자를 impact 그래프의 심볼 정점 ID로 푼다.
//
// isthmus trace는 route-decl의 usr와 reach 순회의 root를 문자열 일치로 잇는다. 그래서 usr는
// 핸들러 코드가 간선 출발점으로 쓰이는 정점이어야 한다:
//   - 함수 이름·메서드 값(`s.handleX`)·메서드 식: 그 함수·메서드 정점
//   - 함수 리터럴: 감싸는 선언(심볼 수확이 클로저 본문의 간선을 긋는 정점) + anonymous 표시
//   - 핸들러 생성 함수 호출(`handleX(db)`, 인자에 핸들러가 없음): 그 함수 — 반환하는 클로저의
//     간선이 그 함수 정점에서 나간다
//   - 래퍼 호출(`auth(h)`, `http.TimeoutHandler(h, …)`): 핸들러 타입 인자가 하나면 그 인자
//   - `http.HandlerFunc(f)` 변환: f
//   - ServeHTTP를 가진 모듈 타입의 값: 그 타입의 ServeHTTP 메서드
//
// 풀지 못하면(모듈 밖 핸들러, 함수 값 변수 등) usr를 싣지 않고 missing-route-usrs로 센다.
package source

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/packages"
)

// handlerResolution은 핸들러 인자 해석 결과다.
type handlerResolution struct {
	usr       string // 심볼 정점 ID 후보(검증 전)
	anonymous bool   // 함수 리터럴이라 감싸는 선언으로 귀속했다
}

// resolveHandler는 핸들러 식의 usr 후보를 찾는다. 래퍼를 벗기는 반복은 깊이 8로 막는다 —
// 재귀 대신 반복으로 써서 심볼 순환 자기 분석에 걸리지 않게 한다.
func resolveHandler(ids symbolIDs, p *packages.Package, expr ast.Expr, owner string) handlerResolution {
	info := p.TypesInfo
	for depth := 0; depth < 8 && expr != nil; depth++ {
		expr = unparen(expr)
		switch e := expr.(type) {
		case *ast.FuncLit:
			return handlerResolution{usr: owner, anonymous: true}
		case *ast.CallExpr:
			next, done, res := unwrapHandlerCall(ids, info, e)
			if done {
				return res
			}
			expr = next
			continue
		}
		if fn := referencedFunc(info, expr); fn != nil {
			return handlerResolution{usr: ids.objectVertexID(fn)}
		}
		if isLocalFuncVar(info, expr) {
			return handlerResolution{usr: owner, anonymous: true}
		}
		return handlerResolution{usr: serveHTTPMethod(ids, info.TypeOf(expr))}
	}
	return handlerResolution{}
}

// unwrapHandlerCall은 호출 식 핸들러를 한 겹 푼다. 계속 풀 식이 있으면 그것을, 결론이 나면
// done과 결과를 돌려준다.
func unwrapHandlerCall(ids symbolIDs, info *types.Info, call *ast.CallExpr) (ast.Expr, bool, handlerResolution) {
	if tv, ok := info.Types[call.Fun]; ok && tv.IsType() && len(call.Args) == 1 {
		return call.Args[0], false, handlerResolution{}
	}
	if inner := singleHandlerArg(info, call); inner != nil {
		return inner, false, handlerResolution{}
	}
	fn := calleeFunc(info, call)
	if fn == nil {
		return nil, true, handlerResolution{}
	}
	return nil, true, handlerResolution{usr: ids.objectVertexID(fn)}
}

// singleHandlerArg는 인자 중 핸들러 타입이 정확히 하나면 그 인자다(래퍼 호출의 안쪽 핸들러).
func singleHandlerArg(info *types.Info, call *ast.CallExpr) ast.Expr {
	var found ast.Expr
	for _, arg := range call.Args {
		if !isHandlerType(info.TypeOf(arg)) {
			continue
		}
		if found != nil {
			return nil
		}
		found = arg
	}
	return found
}

// isHandlerType은 네 프레임워크의 핸들러 타입인지 본다: net/http의 func(ResponseWriter, *Request)·
// http.Handler 구현, gin의 func(*gin.Context), echo의 func(echo.Context) error.
func isHandlerType(t types.Type) bool {
	if t == nil {
		return false
	}
	if sig, ok := t.Underlying().(*types.Signature); ok {
		return isHandlerSignature(sig)
	}
	return hasServeHTTP(t)
}

// isHandlerSignature는 함수 시그니처가 핸들러 모양인지 본다.
func isHandlerSignature(sig *types.Signature) bool {
	params := sig.Params()
	switch params.Len() {
	case 2:
		return typeKey(params.At(0).Type()) == "net/http.ResponseWriter" &&
			typeKey(params.At(1).Type()) == "net/http.Request"
	case 1:
		key := typeKey(params.At(0).Type())
		return key == ginPath+".Context" || key == echoPath+".Context"
	}
	return false
}

// hasServeHTTP는 타입(또는 그 포인터)이 ServeHTTP(ResponseWriter, *Request)를 가졌는지 본다.
func hasServeHTTP(t types.Type) bool {
	return serveHTTPFunc(t) != nil
}

// serveHTTPFunc는 타입의 ServeHTTP 메서드 객체다(없으면 nil). 인터페이스는 구현을 모르므로 제외한다.
func serveHTTPFunc(t types.Type) *types.Func {
	if t == nil {
		return nil
	}
	if _, isInterface := t.Underlying().(*types.Interface); isInterface {
		return nil
	}
	obj, _, _ := types.LookupFieldOrMethod(t, true, nil, "ServeHTTP")
	fn, ok := obj.(*types.Func)
	if !ok || !isHandlerSignature(fn.Signature()) {
		return nil
	}
	return fn
}

// serveHTTPMethod는 모듈 타입 값 핸들러의 ServeHTTP 메서드 정점 ID 후보다.
func serveHTTPMethod(ids symbolIDs, t types.Type) string {
	fn := serveHTTPFunc(t)
	if fn == nil {
		return ""
	}
	return ids.objectVertexID(fn.Origin())
}

// isLocalFuncVar는 식이 함수 안에서 선언한 함수 타입 지역 변수인지 본다 — 흔히 바로 위에서
// 함수 리터럴을 담는다(`h := func(w, r) {…}; mux.HandleFunc("/", h)`). 감싸는 선언으로 귀속한다.
func isLocalFuncVar(info *types.Info, expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	v, ok := info.Uses[id].(*types.Var)
	if !ok || v.IsField() || v.Parent() == nil || v.Pkg() == nil || v.Parent() == v.Pkg().Scope() {
		return false
	}
	_, isFunc := v.Type().Underlying().(*types.Signature)
	return isFunc
}
