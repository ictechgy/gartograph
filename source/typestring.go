// 사실 전용 정규 타입 표기.
//
// types.TypeString은 별칭 이름(any, type U = T)·byte/uint8 표기·타입 파라미터 이름을
// 그대로 옮긴다. 문서 사실(인터페이스 methods, struct fields)은 다른 수확끼리 문자열로
// 비교되므로, 같은 타입이 표기만 달라도 diff가 거짓 breaking을 낸다. 여기서는 별칭을
// 풀고, 기본 타입은 종류로(byte → uint8, rune → int32), 타입 파라미터는 선언 위치로
// (P0, P1…) 적는다. 재귀 대신 명시적 스택이다 — 자기 호출은 심볼 그래프 자기 순환으로
// 새어 cycles 자기 분석을 오염시킨다.
package source

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"
)

// canonicalType은 타입의 정규 표기를 돌려준다. 명명 타입은 경로로 한정한 이름에서
// 멈춘다(밑 타입으로 내려가지 않는다) — 순환 타입도 유한하게 끝난다.
func canonicalType(t types.Type) string {
	var b strings.Builder
	stack := []any{t}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if s, ok := top.(string); ok {
			b.WriteString(s)
			continue
		}
		parts := typeParts(top.(types.Type))
		for i := len(parts) - 1; i >= 0; i-- {
			stack = append(stack, parts[i])
		}
	}
	return b.String()
}

// typeParts는 타입 하나를 출력 조각(문자열)과 하위 타입의 순서 있는 목록으로 편다.
func typeParts(t types.Type) []any {
	switch t := t.(type) {
	case *types.Alias:
		return []any{types.Unalias(t)}
	case *types.Basic:
		return []any{basicName(t)}
	case *types.Named:
		return namedParts(t)
	case *types.TypeParam:
		return []any{"P" + strconv.Itoa(t.Index())}
	case *types.Pointer:
		return []any{"*", t.Elem()}
	case *types.Slice:
		return []any{"[]", t.Elem()}
	case *types.Array:
		return []any{fmt.Sprintf("[%d]", t.Len()), t.Elem()}
	case *types.Map:
		return []any{"map[", t.Key(), "]", t.Elem()}
	case *types.Chan:
		return []any{chanPrefix(t.Dir()), t.Elem()}
	case *types.Signature:
		return append([]any{"func"}, signatureParts(t)...)
	case *types.Interface:
		return interfaceParts(t)
	case *types.Struct:
		return structParts(t)
	}
	return []any{t.String()}
}

// basicName은 기본 타입을 종류로 적는다 — byte·rune 별칭 표기를 uint8·int32로 맞춘다.
func basicName(t *types.Basic) string {
	if t.Kind() == types.UnsafePointer {
		return "unsafe.Pointer"
	}
	return types.Typ[t.Kind()].Name()
}

// namedParts는 명명 타입을 경로 한정 이름과 타입 인자로 편다(universe 타입은 이름만).
func namedParts(t *types.Named) []any {
	name := t.Obj().Name()
	if pkg := t.Obj().Pkg(); pkg != nil {
		name = pkg.Path() + "." + name
	}
	args := t.TypeArgs()
	if args.Len() == 0 {
		return []any{name}
	}
	out := []any{name, "["}
	for i := 0; i < args.Len(); i++ {
		if i > 0 {
			out = append(out, ", ")
		}
		out = append(out, args.At(i))
	}
	return append(out, "]")
}

// chanPrefix는 채널 방향 표기다.
func chanPrefix(dir types.ChanDir) string {
	switch dir {
	case types.SendOnly:
		return "chan<- "
	case types.RecvOnly:
		return "<-chan "
	}
	return "chan "
}

// signatureParts는 서명을 파라미터 이름 없이 "(params) results"로 편다.
// 파라미터 이름은 계약이 아니다 — 이름만 바뀐 같은 메서드가 다른 항목이 되면 안 된다.
func signatureParts(sig *types.Signature) []any {
	out := append([]any{"("}, tupleParts(sig.Params(), sig.Variadic())...)
	out = append(out, ")")
	res := sig.Results()
	switch res.Len() {
	case 0:
		return out
	case 1:
		return append(out, " ", res.At(0).Type())
	}
	out = append(out, " (")
	out = append(out, tupleParts(res, false)...)
	return append(out, ")")
}

// tupleParts는 튜플의 타입들을 쉼표로 편다. 가변 인자는 마지막 슬라이스를 ...으로 적는다.
func tupleParts(t *types.Tuple, variadic bool) []any {
	var out []any
	for i := 0; i < t.Len(); i++ {
		if i > 0 {
			out = append(out, ", ")
		}
		typ := t.At(i).Type()
		if s, ok := typ.(*types.Slice); ok && variadic && i == t.Len()-1 {
			out = append(out, "...", s.Elem())
			continue
		}
		out = append(out, typ)
	}
	return out
}

// interfaceParts는 인터페이스를 정렬된 메서드 집합으로 편다. 타입 원소(유니언 등)가
// 있는 제약 인터페이스는 메서드 집합으로 다 표현되지 않아 go/types 표기를 덧붙인다.
func interfaceParts(t *types.Interface) []any {
	out := []any{"interface{"}
	for i := 0; i < t.NumMethods(); i++ {
		if i > 0 {
			out = append(out, "; ")
		}
		out = append(out, methodEntryParts(t.Method(i))...)
	}
	if !t.IsMethodSet() {
		out = append(out, "; "+t.String())
	}
	return append(out, "}")
}

// structParts는 struct를 선언 순서의 "name type" 필드로 편다(태그 포함 — 타입 동일성의 일부).
func structParts(t *types.Struct) []any {
	out := []any{"struct{"}
	for i := 0; i < t.NumFields(); i++ {
		if i > 0 {
			out = append(out, "; ")
		}
		f := t.Field(i)
		out = append(out, f.Name()+" ", f.Type())
		if tag := t.Tag(i); tag != "" {
			out = append(out, " "+strconv.Quote(tag))
		}
	}
	return append(out, "}")
}

// methodEntryParts는 메서드 하나를 "Name(params) results"로 편다 — 비공개 메서드는
// 패키지 경로로 한정한다(다른 패키지의 같은 이름은 다른 메서드다).
func methodEntryParts(m *types.Func) []any {
	name := m.Name()
	if !m.Exported() && m.Pkg() != nil {
		name = m.Pkg().Path() + "." + name
	}
	return append([]any{name}, signatureParts(m.Signature())...)
}
