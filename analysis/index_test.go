package analysis

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/ictechgy/gartograph/graph"
)

// TestIndexReuseMatchesFresh는 무작위 문서에서 Index 하나를 여러 질의가 나눠 쓴 답이, 질의마다 새 색인을
// 만드는 한 번짜리 함수의 답과 같은지 확인한다. 질의 순서를 섞어 한 질의가 만든 색인을 다른 질의가
// 그대로 쓰게 한다. 외부 디스패치 사실(satisfies·receiver)도 섞는다 — dead의 도달성 맵은 의존 인접 맵에
// 디스패치 걸음을 덧붙이므로, 두 맵을 나눠 쓰면 뒤이은 path·shared에 그 걸음이 새어 들어 답이 갈라진다.
func TestIndexReuseMatchesFresh(t *testing.T) {
	r := rand.New(rand.NewSource(31))
	asked := map[int]int{}
	sawDispatch := false
	for trial := 0; trial < 300; trial++ {
		d := randomDependencyDocument(r)
		for i := range d.Vertices {
			d.Vertices[i].Exported = r.Intn(2) == 0
			if d.Vertices[i].Kind == graph.KindMethod && r.Intn(2) == 0 {
				d.Vertices[i].Satisfies = []string{"error"}
				d.Vertices[i].Receiver = d.Vertices[r.Intn(len(d.Vertices))].ID
			}
		}
		pick := func() string {
			if r.Intn(6) == 0 {
				return "m.ghost"
			}
			return d.Vertices[r.Intn(len(d.Vertices))].ID
		}
		x := NewIndex(d)
		for q := 0; q < 12; q++ {
			depth, limit, kind := r.Intn(4), r.Intn(3), r.Intn(6)
			switch kind {
			case 0:
				id := pick()
				got, gotErr := x.Query(id, depth, limit)
				want, wantErr := Query(d, id, depth, limit)
				sameAnswer(t, fmt.Sprintf("trial %d query %s", trial, id), got, want, gotErr, wantErr)
			case 1:
				id := pick()
				got, gotErr := x.FindImpact(id, depth, limit)
				want, wantErr := FindImpact(d, id, depth, limit)
				sameAnswer(t, fmt.Sprintf("trial %d impact %s", trial, id), got, want, gotErr, wantErr)
			case 2:
				from, to := pick(), pick()
				got, gotErr := x.Path(from, to)
				want, wantErr := Path(d, from, to)
				sameAnswer(t, fmt.Sprintf("trial %d path %s→%s", trial, from, to), got, want, gotErr, wantErr)
			case 3:
				roots := []string{pick(), pick(), pick()}[:2+r.Intn(2)]
				got, gotErr := x.Shared(roots)
				want, wantErr := Shared(d, roots)
				sameAnswer(t, fmt.Sprintf("trial %d shared %v", trial, roots), got, want, gotErr, wantErr)
			case 4:
				extra := []string{pick(), pick()}[:r.Intn(3)]
				public := r.Intn(2) == 0
				gotRoots, gotUnknown := x.RetentionRoots(public, extra)
				wantRoots, wantUnknown := RetentionRoots(d, public, extra)
				sameAnswer(t, fmt.Sprintf("trial %d retention %v", trial, extra),
					[][]string{gotRoots, gotUnknown}, [][]string{wantRoots, wantUnknown}, nil, nil)
			default:
				roots := []string{pick(), pick()}
				want := Reachable(d, roots)
				sameAnswer(t, fmt.Sprintf("trial %d reachable %v", trial, roots), x.Reachable(roots), want, nil, nil)
				sawDispatch = sawDispatch || !reflect.DeepEqual(want, DependencyReachable(d, roots))
			}
			asked[kind]++
		}
	}
	// 생성기가 쉬운 경우만 만들면 대조가 아무것도 증명하지 못한다.
	if len(asked) != 6 || !sawDispatch {
		t.Fatalf("random sessions must ask every query kind (%v) and reach through external dispatch (%v)",
			asked, sawDispatch)
	}
}

// sameAnswer는 색인을 나눠 쓴 답(got)과 한 번짜리 답(want)이 오류 문구까지 같은지 본다.
func sameAnswer(t *testing.T, label string, got, want any, gotErr, wantErr error) {
	t.Helper()
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: reused index answered differently:\ngot  %+v (err %v)\nwant %+v (err %v)",
			label, got, gotErr, want, wantErr)
	}
}
