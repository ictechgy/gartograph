package analysis

import (
	"context"
	"math"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/ictechgy/gartograph/graph"
)

// TestQueryMaxDepth는 이웃을 모두 방문한 뒤 요청 깊이만큼 빈 순회를 하지 않게 한다.
// 회귀가 생겨도 테스트 프로세스가 무한 대기하지 않도록 자식 실행에 기한을 둔다.
func TestQueryMaxDepth(t *testing.T) {
	if os.Getenv("GARTOGRAPH_TEST_MAX_DEPTH") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestQueryMaxDepth$")
		cmd.Env = append(os.Environ(), "GARTOGRAPH_TEST_MAX_DEPTH=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("maximum-depth query did not finish: %v (%v)\n%s", err, ctx.Err(), output)
		}
		return
	}
	isolated := &graph.Document{Vertices: []graph.Vertex{{ID: "only"}}}
	result, err := Query(isolated, "only", math.MaxInt, 0)
	if err != nil || result.Depth != math.MaxInt || result.Truncated ||
		len(result.DependsOn) != 0 || len(result.DependedBy) != 0 {
		t.Fatalf("isolated query: %+v, %v", result, err)
	}
	result, err = Query(doc(), "a", math.MaxInt, 1)
	if err != nil || !result.Truncated || len(result.DependsOn) != 1 || len(result.DependedBy) != 1 {
		t.Fatalf("cyclic truncated query: %+v, %v", result, err)
	}
}
