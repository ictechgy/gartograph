// Package mark는 합성 fixture 핸들러가 자기 이름을 응답 헤더에 싣게 한다.
//
// 오라클은 요청이 어느 핸들러에 닿았는지 이 헤더로 안다. 이름은 runtime.Caller로 얻은 호출
// 함수 이름이라 fixture가 이름을 손으로 적지 않는다(적으면 오라클과 생산자가 같은 실수를
// 할 수 있다).
package mark

import (
	"net/http"
	"runtime"
)

// Header는 핸들러 이름을 싣는 응답 헤더다.
const Header = "X-Handler"

// Name은 skip 단계 위 호출자의 함수 이름이다.
func Name(skip int) string {
	pc, _, _, ok := runtime.Caller(skip + 1)
	if !ok {
		return "unknown"
	}
	return runtime.FuncForPC(pc).Name()
}

// HTTP는 net/http 핸들러가 부른다.
func HTTP(w http.ResponseWriter) {
	w.Header().Set(Header, Name(1))
	w.WriteHeader(http.StatusOK)
}
