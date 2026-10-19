//go:build js || wasip1

package source

import "os"

// readFlags는 readflags.go와 같은 역할이다. js·wasip1에는 syscall.O_NONBLOCK이 없어 보통의
// 읽기 전용 open을 쓴다 — fstat 종류 확인은 그대로라 장치는 거르지만 FIFO open은 막을 수 없다.
// 이 플랫폼에서도 빌드가 깨지지 않게 두는 것이 목적이다.
const readFlags = os.O_RDONLY
