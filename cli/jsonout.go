package cli

import (
	"encoding/json"
	"reflect"
	"strings"
)

// marshalReport는 CLI의 JSON 산출물을 직렬화한다. omitempty가 없는 목록 필드·최상위 목록·
// 맵 값의 nil 슬라이스는 빈 배열로 바꾼다 — 결과가 없을 때 null을 내면 소비자가 null과
// "없음"을 따로 다뤄야 하고, SARIF는 results가 배열이어야 유효하다. 선택 필드(omitempty)는
// 그대로 키가 빠진다(에이전트 출력 계약).
// 주의: 최상위만 복사하는 얕은 복사다 — 슬라이스 원소·포인터가 가리키는 구조체·맵은 호출자와
// 공유되어 그 안의 nil 목록이 빈 목록으로 바뀐다(보고서 값은 출력 직전에 만들어져 무해하다).
// 인터페이스에 담긴 구조체 값·비공개 임베드 구조체의 필드는 주소를 얻을 수 없어 건너뛴다.
func marshalReport(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.IsValid() {
		copied := reflect.New(rv.Type())
		copied.Elem().Set(rv)
		if copied.Elem().Kind() == reflect.Slice && copied.Elem().IsNil() {
			copied.Elem().Set(reflect.MakeSlice(rv.Type(), 0, 0))
		}
		fillNilLists(copied)
		v = copied.Interface()
	}
	return json.MarshalIndent(v, "", "  ")
}

// fillNilLists는 값 안의 omitempty 없는 nil 슬라이스 필드를 빈 슬라이스로 채운다.
// 재귀 대신 명시적 스택이다 — 자기 호출은 cycles 자기 분석에 순환으로 잡힌다.
// 포인터가 서로를 가리키는 순환 구조는 보고서에 없다고 전제한다(방문 집합이 없다).
func fillNilLists(root reflect.Value) {
	stack := []reflect.Value{root}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch cur.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !cur.IsNil() {
				stack = append(stack, cur.Elem())
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < cur.Len(); i++ {
				stack = append(stack, cur.Index(i))
			}
		case reflect.Struct:
			stack = append(stack, structFieldsToFill(cur)...)
		case reflect.Map:
			fillMapLists(cur)
		}
	}
}

// fillMapLists는 맵 값 중 nil 슬라이스(인터페이스에 담긴 것 포함)를 빈 슬라이스로 바꾼다.
// 맵 값은 주소를 얻을 수 없어 새 값으로 다시 넣는다(mapping의 빈 컴포넌트, summary의
// limitations).
func fillMapLists(m reflect.Value) {
	for _, k := range m.MapKeys() {
		v := m.MapIndex(k)
		if v.Kind() == reflect.Interface && !v.IsNil() {
			v = v.Elem()
		}
		if v.Kind() == reflect.Slice && v.IsNil() {
			m.SetMapIndex(k, reflect.MakeSlice(v.Type(), 0, 0))
		}
	}
}

// structFieldsToFill은 구조체의 nil 목록 필드를 채우고, 더 내려가 볼 필드를 돌려준다.
func structFieldsToFill(s reflect.Value) []reflect.Value {
	var next []reflect.Value
	for i := 0; i < s.NumField(); i++ {
		f, sf := s.Field(i), s.Type().Field(i)
		if !sf.IsExported() || !f.CanSet() {
			continue
		}
		if f.Kind() == reflect.Slice && f.IsNil() && !isOmitEmpty(sf.Tag.Get("json")) {
			f.Set(reflect.MakeSlice(f.Type(), 0, 0))
		}
		next = append(next, f)
	}
	return next
}

// isOmitEmpty는 json 태그에 omitempty가 있는지 본다.
func isOmitEmpty(tag string) bool {
	for _, opt := range strings.Split(tag, ",")[1:] {
		if opt == "omitempty" {
			return true
		}
	}
	return false
}
