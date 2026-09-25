package cli

import (
	"encoding/json"
	"reflect"
	"strings"
)

// marshalReport는 CLI의 JSON 산출물을 직렬화한다. omitempty가 없는 목록 필드의 nil
// 슬라이스는 빈 배열로 바꾼다 — 결과가 없을 때 null을 내면 소비자가 null과 "없음"을
// 따로 다뤄야 하고, SARIF는 results가 배열이어야 유효하다. 선택 필드(omitempty)는
// 그대로 키가 빠진다(에이전트 출력 계약).
func marshalReport(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.IsValid() {
		copied := reflect.New(rv.Type())
		copied.Elem().Set(rv)
		fillNilLists(copied)
		v = copied.Interface()
	}
	return json.MarshalIndent(v, "", "  ")
}

// fillNilLists는 값 안의 omitempty 없는 nil 슬라이스 필드를 빈 슬라이스로 채운다.
// 재귀 대신 명시적 스택이다 — 자기 호출은 cycles 자기 분석에 순환으로 잡힌다.
// 맵의 값은 주소를 얻을 수 없어 건너뛴다.
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
