// isthmus bridge-facts 생산자 — Go는 언어 경계의 한쪽 증거를 낸다.
//
// 계약은 ../isthmus의 docs/GRAPH-EXCHANGE.md가 정본이다. Go의 브리지는
// cgo(`import "C"`·`//export`)·gomobile 같은 심볼 이름 interop이라
// 채널·이름 리터럴 fact로 귀속할 수 없다 — 그래서 v1에서 go 문서는
// facts를 비우고 관측한 interop 파일 수를 unscanned-ffi-interop
// limitation으로만 신고한다. gomobile bind 경계는 소스 표식이 없어
// 관측되지 않는다 — 추측해 신고하지 않는다.
package source

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BridgeFactsDocument는 isthmus bridge-facts v1 문서다.
// 키 순서는 계약 문서의 나열 순서를 따라 diff 가능하게 유지한다.
type BridgeFactsDocument struct {
	Format           string          `json:"format"`
	Version          int             `json:"version"`
	Tool             BridgeFactsTool `json:"tool"`
	GeneratedAt      string          `json:"generatedAt"`
	SourceModifiedAt string          `json:"sourceModifiedAt,omitempty"`
	Platform         string          `json:"platform"`
	Target           any             `json:"target"`
	Project          string          `json:"project"`
	Facts            []any           `json:"facts"`
	// 계약상 항상 배열이다 — 비어 있어도 생략하면 isthmus 파서가 거부한다.
	Limitations []string `json:"limitations"`
}

// BridgeFactsTool은 문서를 생산한 도구 식별자다.
type BridgeFactsTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// BridgeFacts는 dir 아래 Go 소스를 스캔해 bridge-facts v1 문서를 만든다.
// go 플랫폼 문서는 사실을 담지 않는다 — cgo 사용 파일 수를
// unscanned-ffi-interop limitation으로 신고하는 것이 v1의 계약이다.
func BridgeFacts(dir, toolVersion string) (*BridgeFactsDocument, error) {
	root, err := realPath(dir)
	if err != nil {
		return nil, err
	}
	scan, err := scanCgo(root)
	if err != nil {
		return nil, err
	}
	doc := &BridgeFactsDocument{
		Format:      "bridge-facts",
		Version:     1,
		Tool:        BridgeFactsTool{Name: "gartograph", Version: toolVersion},
		GeneratedAt: bridgeTimestamp(time.Now()),
		Platform:    "go",
		Target:      nil,
		Project:     root,
		Facts:       []any{},
		Limitations: []string{},
	}
	if !scan.latest.IsZero() {
		doc.SourceModifiedAt = bridgeTimestamp(scan.latest)
	}
	if scan.files > 0 {
		doc.Limitations = append(doc.Limitations, fmt.Sprintf(
			"unscanned-ffi-interop: %d Go source files use cgo interop (%d //export symbols)",
			scan.files, scan.exports))
	}
	if scan.unparsed > 0 {
		doc.Limitations = append(doc.Limitations, fmt.Sprintf(
			"unparsed-sources: %d Go files could not be parsed; cgo use there is uncounted",
			scan.unparsed))
	}
	return doc, nil
}

// cgoScan은 스캔의 관측 결과다.
type cgoScan struct {
	files    int       // import "C"를 쓰는 파일 수
	exports  int       // 그 파일 안의 //export 지시자 수
	unparsed int       // 파싱 실패한 파일 수
	latest   time.Time // 읽은 소스의 최신 mtime
}

// scanCgo는 root 아래의 .go 파일을 걸어 cgo 사용 파일을 센다.
// import "C"가 있는 파일만 //export 지시자를 추가로 센다 — //export는
// cgo 선두부 밖에서 의미가 없으므로 두 번 훑을 필요가 없다.
func scanCgo(root string) (cgoScan, error) {
	var scan cgoScan
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "vendor" || strings.HasPrefix(name, ".") ||
				strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		return scanGoFile(path, &scan)
	})
	if err != nil {
		return scan, fmt.Errorf("scanning %s: %w", root, err)
	}
	return scan, nil
}

// scanGoFile은 파일 하나의 import "C"와 //export 지시자를 관측한다.
func scanGoFile(path string, scan *cgoScan) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.ModTime().After(scan.latest) {
		scan.latest = info.ModTime()
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		scan.unparsed++
		return nil
	}
	if !importsC(f) {
		return nil
	}
	scan.files++
	scan.exports += countExports(fset, path)
	return nil
}

// importsC는 파일이 C pseudo-package를 import하는지 본다.
func importsC(f *ast.File) bool {
	for _, imp := range f.Imports {
		if imp.Path.Value == `"C"` {
			return true
		}
	}
	return false
}

// countExports는 파일 안의 //export 지시자 수를 센다.
// ImportsOnly 파싱은 선두부 이후를 버리므로 주석까지 다시 읽는다.
func countExports(fset *token.FileSet, path string) int {
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return 0
	}
	n := 0
	for _, group := range f.Comments {
		for _, c := range group.List {
			if strings.HasPrefix(c.Text, "//export ") {
				n++
			}
		}
	}
	return n
}

// realPath는 계약이 요구하는 POSIX realpath 정규화를 한다.
// 심볼릭 링크·`..`·중복 슬래시가 접힌 절대 경로를 돌려준다 —
// 다른 생산자와 같은 디렉터리를 같은 문자열로 맞추는 것이 조인의 전제다.
func realPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks in %s: %w", dir, err)
	}
	return filepath.Clean(resolved), nil
}

// bridgeTimestamp는 계약의 UTC 밀리초 형식으로 정규화한다.
func bridgeTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
