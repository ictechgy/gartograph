package source

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestRTARootsEverySyntheticInit는 RTA가 초기화 루트(pkg._) 없이도 모든 패키지의
// 합성 init을 루트로 삼는지 확인한다 — 문서에서 pkg._를 걷어내 CHA 쪽 장치와 떼어
// 본다. 합성 init은 패키지 변수 초기화식을 실행하므로 rreg가 도달해야 하고, explain
// 인접 맵에는 순회용 ID(pkg#init)로 실린다.
func TestRTARootsEverySyntheticInit(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport _ \"example.com/fixture/r\"\n\nfunc main() {}\n",
		"r/r.go":  "package r\n\nvar X = rreg()\n\nfunc rreg() int { return 1 }\n",
	})
	doc, session, err := LoadSession(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	const blank, target = "example.com/fixture/r._", "example.com/fixture/r.rreg"
	doc.Vertices = slices.DeleteFunc(doc.Vertices, func(v graph.Vertex) bool { return v.ID == blank })
	roots := map[string]bool{}
	for _, r := range doc.Roots {
		if r != blank {
			roots[r] = true
		}
	}
	reach, err := session.RTAReachable(doc, roots)
	if err != nil {
		t.Fatal(err)
	}
	if !reach[target] {
		t.Fatal("rta must root every synthetic init, not only packages with an init-root vertex")
	}
	adj, _, err := session.RTAAdjacency(doc, roots)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(adj["example.com/fixture/r"+PackageInitSuffix], target) {
		t.Fatalf("synthetic init without an init root must appear as pkg#init, adj=%v", adj)
	}
}

// TestLoadSessionDocumentIsTheSymbolHarvest는 LoadSession의 문서가 같은 디렉터리의 symbol Load와 바이트가 같은지
// 본다 — dead --algo rta가 RTA에 쓸 적재로 문서도 만들지만, 그 문서(정점 ID·루트·limitation)는 CHA dead가 보는
// 것과 같아야 한다. opts.Level은 쓰지 않는다: package를 줘도 RTA가 답하는 함수·메서드 정점이 있는 symbol 문서다.
func TestLoadSessionDocumentIsTheSymbolHarvest(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nimport \"example.com/fixture/r\"\n\nfunc init() { r.Touch() }\n\nfunc main() { _ = r.Map(1, r.Double) }\n",
		"r/r.go": "package r\n\nimport \"fmt\"\n\nvar _ = register()\n\nfunc register() int { return 1 }\n\n" +
			"func Touch() {}\n\nfunc Double(x int) int { return 2 * x }\n\n" +
			"func Map[T any](x T, f func(T) T) T { return f(x) }\n\n" +
			"type Name string\n\n//gartograph:keep\nfunc (n Name) String() string { return fmt.Sprint(string(n)) }\n",
	})
	want, err := json.Marshal(loadSymbol(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := LoadSession(Options{Dir: dir, Level: graph.LevelPackage})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the session document must be the symbol harvest\n got: %s\nwant: %s", got, want)
	}
}

// TestRTAWithoutWellTypedPackages는 RTA가 출발할 루트가 없을 때 패닉 대신 원인과 해결 방향을 담은
// 오류를 돌려주는지 확인한다. 유일한 패키지에 타입 오류가 있으면 SSA가 그 함수를 만들지 않고,
// 패턴이 아무것도 고르지 않으면 패키지가 없다 — 어느 쪽이든 루트가 비어 rta.Analyze가 nil을 돌려주고,
// 그 결과를 역참조하면 dead --algo rta가 스택만 남기고 죽는다.
func TestRTAWithoutWellTypedPackages(t *testing.T) {
	dir := testutil.WriteModule(t, map[string]string{
		"main.go": "package main\n\nfunc helper() int { return undefinedName }\n\nfunc main() { _ = helper() }\n",
	})
	doc := loadSymbol(t, dir)
	roots := map[string]bool{}
	for _, r := range doc.Roots {
		roots[r] = true
	}
	for name, opts := range map[string]Options{
		"ill-typed":       {Dir: dir, Level: graph.LevelSymbol},
		"pattern-nothing": {Dir: dir, Level: graph.LevelSymbol, Patterns: []string{"./nothing/..."}},
	} {
		_, session, err := LoadSession(opts)
		if err != nil {
			t.Fatalf("%s: the load itself must succeed: %v", name, err)
		}
		reachErr := callWithoutPanic(t, name+" RTAReachable", func() error {
			_, err := session.RTAReachable(doc, roots)
			return err
		})
		adjErr := callWithoutPanic(t, name+" RTAAdjacency", func() error {
			_, _, err := session.RTAAdjacency(doc, roots)
			return err
		})
		for _, err := range []error{reachErr, adjErr} {
			if err == nil || !strings.Contains(err.Error(), "rta: no well-typed packages to analyze") ||
				!strings.Contains(err.Error(), "without --algo rta") {
				t.Errorf("%s: want the cause and the way out, got %v", name, err)
			}
		}
	}
}

// callWithoutPanic은 fn을 부르고 패닉을 테스트 실패로 바꾼다 — 패닉이 테스트 바이너리를 죽이면
// 같은 실행의 다른 결과까지 잃는다.
func callWithoutPanic(t *testing.T, what string, fn func() error) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked instead of returning an error: %v", what, r)
		}
	}()
	return fn()
}
