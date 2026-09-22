// gartograph는 Go 저장소의 의존성 그래프를 만들고 그 위에서 질의하는 CLI다.
// 명령 해석과 종료 코드는 cli 패키지가 담당하고, 여기서는 연결만 한다.
package main

import (
	"os"

	"github.com/ictechgy/gartograph/cli"
)

// main은 cli.Run에 인자와 표준 스트림을 넘기고 종료 코드를 전달한다.
func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
