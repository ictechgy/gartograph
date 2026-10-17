//go:build !js && !wasip1

package source

import (
	"os"
	"syscall"
)

// readFlags는 분석 대상이 고른 경로를 종류 확인 전에 여는 플래그다. O_NONBLOCK이 핵심이다 —
// 쓰는 쪽이 없는 FIFO는 보통의 읽기 open에서 영원히 막혀, fstat로 종류를 보기도 전에 수확이
// 멈춘다. 정규 파일 읽기에는 영향이 없고 windows는 이 비트를 무시한다.
// syscall.O_NONBLOCK이 없는 js·wasip1은 readflags_other.go가 맡는다.
const readFlags = os.O_RDONLY | syscall.O_NONBLOCK
