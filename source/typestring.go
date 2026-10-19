// 사실 전용 정규 타입 표기.
//
// types.TypeString은 별칭 이름(any, type U = T)·byte/uint8 표기·타입 파라미터 이름을
// 그대로 옮긴다. 문서 사실(인터페이스 methods, struct fields)은 다른 수확끼리 문자열로
// 비교되므로, 같은 타입이 표기만 달라도 diff가 거짓 breaking을 낸다. 여기서는 별칭을
// 풀고, 기본 타입은 종류로(byte → uint8, rune → int32), 타입 파라미터는 선언 위치로
// (P0, P1…) 적는다. 재귀 대신 명시적 스택이다 — 자기 호출은 심볼 그래프 자기 순환으로
// 새어 cycles 자기 분석을 오염시킨다.
//
// 별칭은 이름에서 멈추지 않고 펼쳐지므로 앞 별칭을 두 번 쓰는 사슬(`type A1 = struct{ a, b A0 }`,
// `type F1 = func(F0, F0)`)은 깊이마다 표기가 두 배가 된다 — 깊이 30이면 표기 하나가 10GB를 넘는다.
// 그래서 표기는 수확 하나가 공유하는 변환기(typeSpeller)가 만든다. 타입마다 표기 길이를 메모해
// 서로 다른 타입 수에 비례하는 시간에 재고, maxSpellingBytes를 넘는 표기는 앞부분과 구조 해시로
// 축약한다.
package source

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"go/types"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ictechgy/gartograph/graph"
)

// maxSpellingBytes는 정규 표기 하나(타입 하나, 또는 메서드 서명 하나)의 바이트 상한이다 — 넘으면
// 축약한다. 2026-10 실측(type·symbol 레벨, --tests 포함 문서의 fields·methods·typeSet·satisfies)에서
// 가장 긴 표기는 이 저장소 362바이트(테스트 파일의 익명 struct 슬라이스), sigs.k8s.io/structured-merge-diff
// 287바이트, k8s.io/kube-openapi 281바이트, golang.org/x/tools v0.50.0 174바이트, pgx v5.11.0
// 131바이트였다. 상한은 그보다 180배 넘게 넉넉해 손으로 쓴 코드와 생성 코드의 큰 익명 struct는
// 그대로 적힌다. 앞 별칭을 두 번 쓰는 별칭 struct 사슬은 깊이 12에서 넘는다.
const maxSpellingBytes = 64 << 10

// abbreviationPrefixBytes는 축약 표기가 남기는 정규 표기 앞부분의 바이트 수다. 모양을 알아볼 만큼만
// 남긴다 — 축약 하나가 330바이트를 넘지 않아 축약이 다시 출력을 키우지 않는다.
const abbreviationPrefixBytes = 256

// typeSpeller는 수확 하나가 공유하는 정규 표기 변환기다. fields·methods·typeSet·satisfies 사실이
// 모두 이것을 지나야 같은 타입이 같은 표기(축약이면 같은 축약)가 된다. 메모의 열쇠는 타입 객체다 —
// 별칭 사슬에서 공유된 하위 타입은 쓰는 곳마다 같은 객체라 한 번만 잰다.
type typeSpeller struct {
	sizes   map[types.Type]int               // 정규 표기 길이(maxSpellingBytes+1에서 포화)
	digests map[types.Type][sha256.Size]byte // 구조 해시 — 축약할 때만 구한다
	open    map[types.Type]bool              // walk가 계산 중인 타입(닫지 않은 조상) — 순환 가드
	// parts는 타입 하나를 표기 조각으로 편다(typeParts). go/types 공개 API로는 순환 타입을 만들 수
	// 없어 순환 가드 테스트만 바꿔 끼운다.
	parts func(types.Type) []any
}

// newTypeSpeller는 메모가 빈 변환기를 만든다.
func newTypeSpeller() *typeSpeller {
	return &typeSpeller{
		sizes:   map[types.Type]int{},
		digests: map[types.Type][sha256.Size]byte{},
		open:    map[types.Type]bool{},
		parts:   typeParts,
	}
}

// canonicalType은 타입의 정규 표기와 그것이 축약인지를 돌려준다. 명명 타입은 경로로 한정한
// 이름에서 멈춘다(밑 타입으로 내려가지 않는다).
func (s *typeSpeller) canonicalType(t types.Type) (string, bool) {
	return s.spell([]any{t})
}

// canonicalSignature는 서명을 파라미터 이름 없이 "(params) results"로 적는다. 상한은 서명
// 전체에 건다 — 메서드 항목에서 이름 뒤가 표기 하나다.
func (s *typeSpeller) canonicalSignature(sig *types.Signature) (string, bool) {
	return s.spell(signatureParts(sig))
}

// spell은 조각 목록(문자열과 타입)을 표기 하나로 편다. 길이를 먼저 재고(메모) 상한 이하일
// 때만 끝까지 편다 — 펴는 일은 표기 길이에, 재는 일은 서로 다른 타입 수에 비례한다.
func (s *typeSpeller) spell(parts []any) (string, bool) {
	if s.size(parts) <= maxSpellingBytes {
		return s.render(parts, -1), false
	}
	return s.abbreviate(parts), true
}

// size는 조각 목록의 정규 표기 길이다(maxSpellingBytes+1에서 포화). 하위 타입 길이부터 메모를
// 채운다.
func (s *typeSpeller) size(parts []any) int {
	for _, p := range parts {
		if t, ok := p.(types.Type); ok {
			s.walk(t, s.hasSize, s.finishSize)
		}
	}
	return s.partsSize(parts)
}

// hasSize는 타입 길이를 이미 쟀는지 본다.
func (s *typeSpeller) hasSize(t types.Type) bool {
	_, ok := s.sizes[t]
	return ok
}

// finishSize는 하위 타입을 다 잰 타입의 길이를 메모한다.
func (s *typeSpeller) finishSize(t types.Type, parts []any) {
	s.sizes[t] = s.partsSize(parts)
}

// partsSize는 하위 타입 길이가 메모된 조각 목록의 길이다(포화). 메모에 없는 하위 타입은 계산
// 중인 조상, 곧 순환이라 끝없는 표기로 센다.
func (s *typeSpeller) partsSize(parts []any) int {
	n := 0
	for _, p := range parts {
		if t, ok := p.(types.Type); ok {
			size, done := s.sizes[t]
			if !done {
				size = maxSpellingBytes + 1
			}
			n += size
		} else {
			n += len(p.(string))
		}
		if n > maxSpellingBytes {
			return maxSpellingBytes + 1
		}
	}
	return n
}

// walk는 root 아래에서 known이 거짓인 타입을 자식 먼저(후위) finish에 넘긴다. 재귀 대신 명시적
// 스택이고, 자식을 다 본 뒤 부모를 닫는 표지(closing)를 둔다. 값이 이미 있는 타입으로는 내려가지
// 않는다 — 별칭 사슬처럼 공유된 하위 타입을 한 번만 계산해 선형이 된다. 순환 가드: 계산 중인
// 타입(닫지 않은 조상)을 다시 만나면 내려가지 않고, 부모의 finish는 그 자식을 값 없이 받는다.
func (s *typeSpeller) walk(root types.Type, known func(types.Type) bool, finish func(types.Type, []any)) {
	type frame struct {
		t       types.Type
		parts   []any
		closing bool
	}
	stack := []frame{{t: root}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.closing {
			delete(s.open, f.t)
			finish(f.t, f.parts)
			continue
		}
		if s.open[f.t] || known(f.t) {
			continue
		}
		s.open[f.t] = true
		parts := s.parts(f.t)
		stack = append(stack, frame{t: f.t, parts: parts, closing: true})
		for i := len(parts) - 1; i >= 0; i-- {
			if child, ok := parts[i].(types.Type); ok {
				stack = append(stack, frame{t: child})
			}
		}
	}
}

// hasDigest는 타입의 구조 해시를 이미 구했는지 본다.
func (s *typeSpeller) hasDigest(t types.Type) bool {
	_, ok := s.digests[t]
	return ok
}

// finishDigest는 하위 타입 해시를 다 구한 타입의 구조 해시를 메모한다. 별칭은 실체 타입의 해시를
// 그대로 쓴다 — 정규 표기가 별칭을 풀어 같으므로 축약도 같아야 한다(별칭을 정의로 바꾸는 리팩터가
// diff에서 변경이 되면 안 된다).
func (s *typeSpeller) finishDigest(t types.Type, parts []any) {
	if _, isAlias := t.(*types.Alias); isAlias && len(parts) == 1 {
		if actual, ok := parts[0].(types.Type); ok && s.hasDigest(actual) {
			s.digests[t] = s.digests[actual]
			return
		}
	}
	s.digests[t] = s.partsDigest(parts)
}

// partsDigest는 하위 타입 해시가 메모된 조각 목록의 구조 해시(SHA-256)다. 표기를 이루는 조각과
// 같은 재료라 표기가 같은 타입은 해시도 같다.
func (s *typeSpeller) partsDigest(parts []any) [sha256.Size]byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(s.digestInput(p)) // hash.Hash의 Write는 오류를 내지 않는다(문서화된 계약)
	}
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	return sum
}

// digestInput은 조각 하나의 구조 해시 입력이다 — 문자열은 's'+길이+바이트, 타입은 't'+그 해시,
// 해시가 없는 타입(계산 중인 조상, 곧 순환)은 'c' 하나다. 길이를 앞에 둬 이웃한 조각의 경계가
// 섞이지 않는다.
func (s *typeSpeller) digestInput(p any) []byte {
	if t, ok := p.(types.Type); ok {
		d, done := s.digests[t]
		if !done {
			return []byte{'c'}
		}
		return append([]byte{'t'}, d[:]...)
	}
	str := p.(string)
	return append(binary.AppendUvarint([]byte{'s'}, uint64(len(str))), str...)
}

// abbreviate는 상한을 넘는 조각 목록을 "앞부분…sha256:구조 해시"(graph.SpellingDigestMarker)로
// 줄인다. 앞부분은 정규 표기의 처음 abbreviationPrefixBytes바이트(UTF-8 경계에서 자름)이고, 해시는
// 표기 구조 전체를 덮는다. 같은 타입은 같은 축약이고, 구조가 다른 타입은 앞부분이 같아도 다른
// 축약이라 문자열 비교(diff)가 그대로 맞다.
func (s *typeSpeller) abbreviate(parts []any) string {
	for _, p := range parts {
		if t, ok := p.(types.Type); ok {
			s.walk(t, s.hasDigest, s.finishDigest)
		}
	}
	sum := s.partsDigest(parts)
	return s.render(parts, abbreviationPrefixBytes) + graph.SpellingDigestMarker + hex.EncodeToString(sum[:])
}

// render는 조각 목록을 정규 표기로 편다. limit이 0 이상이면 표기가 그 바이트에 이르는 대로 멈추고
// UTF-8 경계에서 자른다(축약 앞부분). 별칭은 실체 타입 하나로 펴지고 그 밖의 타입은 펴자마자 비지
// 않은 문자열을 내므로, 순환이 있어도 limit 바이트 안에서 끝난다. limit 없이 펴는 것은 길이를 잰
// 표기(상한 이하)뿐이다.
func (s *typeSpeller) render(parts []any, limit int) string {
	var b strings.Builder
	stack := slices.Clone(parts)
	slices.Reverse(stack)
	for len(stack) > 0 && (limit < 0 || b.Len() < limit) {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if str, ok := top.(string); ok {
			b.WriteString(str)
			continue
		}
		next := s.parts(top.(types.Type))
		for i := len(next) - 1; i >= 0; i-- {
			stack = append(stack, next[i])
		}
	}
	return truncateUTF8(b.String(), limit)
}

// truncateUTF8은 str이 limit 바이트를 넘으면 그 안의 마지막 UTF-8 경계에서 자른다(limit < 0이면
// 그대로) — 잘린 멀티바이트 문자는 JSON에서 U+FFFD로 바뀌어 표기가 달라진다.
func truncateUTF8(str string, limit int) string {
	if limit < 0 || len(str) <= limit {
		return str
	}
	for limit > 0 && !utf8.RuneStart(str[limit]) {
		limit--
	}
	return str[:limit]
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

// methodEntryParts는 메서드 하나를 "Name(params) results"로 편다.
func methodEntryParts(m *types.Func) []any {
	return append([]any{methodName(m)}, signatureParts(m.Signature())...)
}

// methodName은 메서드 항목의 이름 부분이다 — 비공개 메서드는 패키지 경로로 한정한다(다른
// 패키지의 같은 이름은 다른 메서드다).
func methodName(m *types.Func) string {
	if !m.Exported() && m.Pkg() != nil {
		return m.Pkg().Path() + "." + m.Name()
	}
	return m.Name()
}
