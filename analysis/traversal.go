package analysis

import (
	"slices"
	"sort"
	"unicode/utf8"

	"github.com/ictechgy/gartograph/graph"
)

// isthmus language-traversal v1의 상한이다 — 넘는 문서는 소비자가 거부한다.
const (
	// MaxTraversalDepth는 depth 상한이다.
	MaxTraversalDepth = 128
	// MaxTraversalReached는 도달 정점 수 상한이다.
	MaxTraversalReached = 100_000
	// MaxTraversalRoots는 한 문서의 root 수 상한이다.
	MaxTraversalRoots = 10_000
	// MaxRootsPerReached는 정점 하나가 싣는 root 인덱스 상한이다(넘으면 rootsTruncated).
	MaxRootsPerReached = 64
	// dominatingRoots는 전파를 멈추게 하는 더 작은 root 수다 — 정점이 root라 자기
	// 인덱스를 빼도 64개가 남도록 하나 더 둔다.
	dominatingRoots = MaxRootsPerReached + 1
	// evidenceMemoryBytes는 정확한 등급 비교(root당 비트 하나)가 쓸 메모리 상한이다.
	evidenceMemoryBytes = 64 << 20
)

// 순회 방향이다. dependencies는 root가 기대는 쪽(호출·참조 대상), dependents는 root에
// 기대는 쪽(호출자·참조자)이다.
const (
	DirectionDependencies = "dependencies"
	DirectionDependents   = "dependents"
)

// 근거 등급이다. 등급의 간선 집합은 포개진다(direct ⊂ candidate).
const (
	EvidenceDirect    = "direct"
	EvidenceCandidate = "candidate"
)

// TraversalRequest는 다중 root 순회 요청이다. Roots의 순서가 결과 root 인덱스의
// 뜻이다 — 정점이 아닌 id도 자리를 지키며(아무것에도 닿지 않는다), 호출자가
// root-not-found로 신고한다.
type TraversalRequest struct {
	Roots      []string
	Direction  string
	MaxDepth   int
	MaxReached int
	// evidenceMemory는 등급 비교 메모리 상한이다(0이면 기본값) — 근사 경로 테스트용.
	evidenceMemory int
}

// TraversalReached는 도달 정점 하나다.
type TraversalReached struct {
	ID            string
	Via           string
	Depth         int
	Roots         []int
	Relationships []graph.EdgeKind
	// Evidence는 root별 하한 근거 등급이다. 문서가 팬아웃 간선을 표시하지 않았으면 비어 있다.
	Evidence string
}

// TraversalResult는 순회 결과다.
type TraversalResult struct {
	Reached           []TraversalReached
	TruncationReasons []string
	RootsTruncated    bool
	// EvidenceClassified는 근거 등급을 매겼는지다(문서의 DispatchEvidence).
	EvidenceClassified bool
	// EvidenceApproximated는 메모리 상한 때문에 등급을 보수적으로(약하게) 근사했는지다.
	EvidenceApproximated bool
}

// Traverse는 모든 root를 한 번에 출발시키는 단계 동기 너비 우선 순회다(isthmus
// language-traversal v1 의미, pythograph·tsograph와 같은 알고리즘).
//   - reached는 자기 자신이 아닌 root에서 간선 1개 이상으로(깊이 상한 안) 닿은 정점이다.
//     다른 root에서 닿은 root도 싣되 roots에는 그 다른 root만 넣는다.
//   - depth는 그 root 중 가장 가까운 것까지의 거리, via는 가장 가까운 root(같으면 작은
//     인덱스)에서 depth-1 거리인 선행 정점 중 UTF-16 순서로 가장 작은 것이다.
//   - 정점이 자기 아닌 더 작은 인덱스 root를 65개 이상 가졌으면 더 큰 root는 그 정점에서
//     전파를 멈춘다 — 출력(작은 인덱스 64개·depth·via)을 바꿀 수 없다.
//
// 간선은 impact와 같은 의존 간선 전부(contains 제외)다.
func Traverse(d *graph.Document, req TraversalRequest) *TraversalResult {
	adj := buildTraversalAdjacency(d, req.Direction, false)
	prop := propagate(adj, req.Roots, req.MaxDepth)
	rows := reachedRows(adj, prop.levels, req.Roots)
	res := &TraversalResult{EvidenceClassified: d.DispatchEvidence}
	if prop.depthCut {
		res.TruncationReasons = append(res.TruncationReasons, "depth")
	}
	if len(rows) > req.MaxReached {
		rows = rows[:req.MaxReached]
		res.TruncationReasons = append(res.TruncationReasons, "max-reached")
	}
	res.RootsTruncated = prop.pruned
	var evidenceOf func(string) string
	if res.EvidenceClassified {
		evidenceOf, res.EvidenceApproximated = evidenceTiers(d, req, adj, prop.levels)
	}
	for i := range rows {
		if len(rows[i].Roots) > MaxRootsPerReached {
			rows[i].Roots = rows[i].Roots[:MaxRootsPerReached]
			res.RootsTruncated = true
		}
		if evidenceOf != nil {
			rows[i].Evidence = evidenceOf(rows[i].ID)
		}
	}
	res.Reached = rows
	return res
}

// pairKey는 순회 방향 기준의 (출발, 도착) 쌍이다.
type pairKey struct{ from, to string }

// traversalAdjacency는 방향에 맞춘 이웃·선행 목록과 쌍별 간선 종류다.
type traversalAdjacency struct {
	next  map[string][]string
	prev  map[string][]string // UTF-16 순
	kinds map[pairKey][]graph.EdgeKind
	// weak는 모든 관계가 팬아웃(Candidate)인 쌍이다 — 근거 등급 비교의 재료.
	weak []pairKey
}

// buildTraversalAdjacency는 의존 간선(contains 제외)을 순회 방향 쌍으로 모은다.
// directOnly면 확정 관계가 하나라도 있는 쌍만 남긴다(direct 등급 그래프).
func buildTraversalAdjacency(d *graph.Document, direction string, directOnly bool) *traversalAdjacency {
	adj := &traversalAdjacency{
		next: map[string][]string{}, prev: map[string][]string{}, kinds: map[pairKey][]graph.EdgeKind{},
	}
	direct := map[pairKey]bool{}
	var order []pairKey
	for _, e := range d.Edges {
		if e.Kind == graph.EdgeContains {
			continue
		}
		key := pairKey{e.From, e.To}
		if direction == DirectionDependents {
			key = pairKey{e.To, e.From}
		}
		if _, seen := adj.kinds[key]; !seen {
			order = append(order, key)
		}
		adj.kinds[key] = mergeKinds(adj.kinds[key], []graph.EdgeKind{e.Kind})
		direct[key] = direct[key] || !e.Candidate
	}
	for _, key := range order {
		if !direct[key] {
			adj.weak = append(adj.weak, key)
			if directOnly {
				continue
			}
		}
		adj.next[key.from] = append(adj.next[key.from], key.to)
		adj.prev[key.to] = append(adj.prev[key.to], key.from)
	}
	for _, preds := range adj.prev {
		sort.Slice(preds, func(i, j int) bool { return utf16Less(preds[i], preds[j]) })
	}
	return adj
}

// propagation은 전파 결과다.
type propagation struct {
	levels   map[string]map[int]int // 정점 → (root 인덱스 → 처음 닿은 단계)
	depthCut bool
	pruned   bool
}

// propagate는 모든 root에서 단계 동기로 전파한다. 경계는 정점 ID 순으로 훑어 결과가
// 맵 순회 순서에 흔들리지 않게 한다.
func propagate(adj *traversalAdjacency, roots []string, maxDepth int) propagation {
	p := propagation{levels: map[string]map[int]int{}}
	owners := rootOwners(roots)
	frontier := map[string][]int{}
	for i, r := range roots {
		p.levels[r] = map[int]int{i: 0}
		frontier[r] = []int{i}
	}
	for level := 1; level <= maxDepth && len(frontier) > 0; level++ {
		following := map[string][]int{}
		for _, node := range sortedKeys(frontier) {
			for _, nb := range adj.next[node] {
				owner, isRoot := owners[nb]
				if !isRoot {
					owner = -1
				}
				p.pruned = spread(p.levels, owner, nb, frontier[node], level, following) || p.pruned
			}
		}
		frontier = following
	}
	p.depthCut = hasDepthCut(adj, p.levels, owners, frontier)
	return p
}

// rootOwners는 root id → 인덱스다(root id는 유일하다고 호출자가 보장한다).
func rootOwners(roots []string) map[string]int {
	owners := make(map[string]int, len(roots))
	for i, r := range roots {
		owners[r] = i
	}
	return owners
}

// spread는 한 간선으로 root 인덱스들을 이웃에 넘긴다. 전파를 멈춘 쌍이 있으면 참이다.
func spread(levels map[string]map[int]int, owner int, nb string, roots []int, level int,
	following map[string][]int) bool {
	held := levels[nb]
	if held == nil {
		held = map[int]int{}
		levels[nb] = held
	}
	pruned := false
	for _, r := range roots {
		if _, ok := held[r]; ok {
			continue
		}
		if dominated(held, owner, r) {
			pruned = true
			continue
		}
		held[r] = level
		following[nb] = append(following[nb], r)
	}
	return pruned
}

// dominated는 정점이 자기 아닌 root 중 r보다 작은 인덱스를 이미 65개 이상 가졌는지 본다.
func dominated(held map[int]int, owner, r int) bool {
	count := len(held)
	if _, ok := held[owner]; ok {
		count--
	}
	if count < dominatingRoots {
		return false
	}
	smaller := 0
	for idx := range held {
		if idx != owner && idx < r {
			smaller++
			if smaller >= dominatingRoots {
				return true
			}
		}
	}
	return false
}

// hasDepthCut은 마지막 단계 경계에서 아직 그 root를 갖지 않은 이웃이 있으면 참이다 —
// 깊이 상한이 도달 집합을 잘랐다는 뜻이다. 이웃이 그 root를 이미 전파 중단한
// (dominated) 경우는 뺀다 — 한 걸음 더 가도 그 root는 실리지 않으므로 잘린 것이 아니다.
func hasDepthCut(adj *traversalAdjacency, levels map[string]map[int]int, owners map[string]int,
	frontier map[string][]int) bool {
	for node, roots := range frontier {
		for _, nb := range adj.next[node] {
			owner, isRoot := owners[nb]
			if !isRoot {
				owner = -1
			}
			for _, r := range roots {
				if _, ok := levels[nb][r]; !ok && !dominated(levels[nb], owner, r) {
					return true
				}
			}
		}
	}
	return false
}

// reachedRows는 단계 기록에서 도달 행을 만든다. (depth, UTF-16 id) 순이다.
func reachedRows(adj *traversalAdjacency, levels map[string]map[int]int, roots []string) []TraversalReached {
	owners := rootOwners(roots)
	var rows []TraversalReached
	for node, held := range levels {
		row, ok := reachedRow(adj, levels, node, held, owners)
		if ok {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Depth != rows[j].Depth {
			return rows[i].Depth < rows[j].Depth
		}
		return utf16Less(rows[i].ID, rows[j].ID)
	})
	return rows
}

// reachedRow는 정점 하나의 행이다. 자기 아닌 root가 없으면 false다.
func reachedRow(adj *traversalAdjacency, levels map[string]map[int]int, node string,
	held map[int]int, owners map[string]int) (TraversalReached, bool) {
	owner, isRoot := owners[node]
	var idx []int
	for r := range held {
		if !isRoot || r != owner {
			idx = append(idx, r)
		}
	}
	if len(idx) == 0 {
		return TraversalReached{}, false
	}
	sort.Ints(idx)
	depth, nearest := held[idx[0]], idx[0]
	for _, r := range idx[1:] {
		if held[r] < depth {
			depth, nearest = held[r], r
		}
	}
	via := ""
	for _, pred := range adj.prev[node] {
		if lv, ok := levels[pred][nearest]; ok && lv == depth-1 {
			via = pred
			break
		}
	}
	return TraversalReached{ID: node, Via: via, Depth: depth, Roots: idx,
		Relationships: adj.kinds[pairKey{via, node}]}, true
}

// evidenceTiers는 정점의 root별 하한 근거 등급 함수를 만든다: 깊이 상한 안에서 정점에
// 닿는 root 각각의 가장 강한 등급 중 가장 약한 것. 팬아웃 간선의 출발점에 닿지 못하는
// root는 두 등급에서 같게 닿으므로 비교에서 뺀다. 비트 집합이 메모리 상한을 넘으면
// 약하게 적을 수는 있어도 부풀리지 않는 근사로 바꾼다.
func evidenceTiers(d *graph.Document, req TraversalRequest, full *traversalAdjacency,
	levels map[string]map[int]int) (func(string) string, bool) {
	sources := map[string]bool{}
	for _, key := range full.weak {
		sources[key.from] = true
	}
	compared := weakTouchingRoots(full, req.Roots, sources, req.MaxDepth)
	if len(compared) == 0 {
		return func(string) string { return EvidenceDirect }, false
	}
	limit := req.evidenceMemory
	if limit == 0 {
		limit = evidenceMemoryBytes
	}
	words := (len(compared) + 63) / 64
	if estimate := len(d.Vertices) * (words*8 + 48) * 3; estimate > limit {
		return approximateEvidence(full, levels), true
	}
	fullBits := reachBits(full, compared, req.MaxDepth)
	directBits := reachBits(buildTraversalAdjacency(d, req.Direction, true), compared, req.MaxDepth)
	return func(node string) string {
		if slices.Equal(fullBits[node], directBits[node]) {
			return EvidenceDirect
		}
		return EvidenceCandidate
	}, false
}

// weakTouchingRoots는 깊이 상한 안에서 팬아웃 간선의 출발점에 닿을 수 있는 root다
// (입력 순서). 출발점에서 선행 정점을 따라 maxDepth-1 걸음 거슬러 올라간다.
func weakTouchingRoots(full *traversalAdjacency, roots []string, sources map[string]bool, maxDepth int) []string {
	seen := map[string]bool{}
	var frontier []string
	for s := range sources {
		seen[s] = true
		frontier = append(frontier, s)
	}
	for step := 1; step < maxDepth && len(frontier) > 0; step++ {
		var following []string
		for _, node := range frontier {
			for _, pred := range full.prev[node] {
				if !seen[pred] {
					seen[pred] = true
					following = append(following, pred)
				}
			}
		}
		frontier = following
	}
	var out []string
	for _, r := range roots {
		if seen[r] {
			out = append(out, r)
		}
	}
	return out
}

// reachBits는 root마다 비트 하나를 두고 단계 동기로 전파해, 깊이 상한 안에서 각 정점에
// 닿는 root 비트 집합을 구한다. 비트 위치는 roots 순번이다.
func reachBits(adj *traversalAdjacency, roots []string, maxDepth int) map[string][]uint64 {
	words := (len(roots) + 63) / 64
	seen := map[string][]uint64{}
	frontier := map[string][]uint64{}
	for i, r := range roots {
		orBit(seen, r, words, i)
		orBit(frontier, r, words, i)
	}
	for step := 0; step < maxDepth && len(frontier) > 0; step++ {
		following := map[string][]uint64{}
		for node, bits := range frontier {
			for _, nb := range adj.next[node] {
				have := ensureBits(seen, nb, words)
				var fresh []uint64
				for w := range bits {
					if add := bits[w] &^ have[w]; add != 0 {
						if fresh == nil {
							fresh = ensureBits(following, nb, words)
						}
						have[w] |= add
						fresh[w] |= add
					}
				}
			}
		}
		frontier = following
	}
	return seen
}

// orBit은 정점의 비트 집합에 비트 하나를 켠다.
func orBit(m map[string][]uint64, node string, words, bit int) {
	ensureBits(m, node, words)[bit/64] |= 1 << (bit % 64)
}

// ensureBits는 정점의 비트 집합을 (없으면 만들어) 돌려준다.
func ensureBits(m map[string][]uint64, node string, words int) []uint64 {
	bits, ok := m[node]
	if !ok {
		bits = make([]uint64, words)
		m[node] = bits
	}
	return bits
}

// approximateEvidence는 메모리 상한을 넘을 때의 보수적 등급이다. root에서 닿은 출발점을
// 가진 팬아웃 간선의 도착점에서 깊이 제한 없이 닿는 정점을 candidate로 표시한다. 어느
// 표시에도 들지 않는 정점은 모든 root에서 팬아웃 간선 없이 닿으므로 정확히 direct다.
func approximateEvidence(full *traversalAdjacency, levels map[string]map[int]int) func(string) string {
	marked := map[string]bool{}
	var stack []string
	for _, key := range full.weak {
		if _, reached := levels[key.from]; reached && !marked[key.to] {
			marked[key.to] = true
			stack = append(stack, key.to)
		}
	}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, nb := range full.next[node] {
			if !marked[nb] {
				marked[nb] = true
				stack = append(stack, nb)
			}
		}
	}
	return func(node string) string {
		if marked[node] {
			return EvidenceCandidate
		}
		return EvidenceDirect
	}
}

// utf16Less는 UTF-16 코드 단위 순서의 비교다(isthmus 계약의 정렬, locale 무관).
// Go 문자열 비교는 UTF-8 바이트 순서라 보조 평면 문자(서로게이트 쌍)와 U+E000~U+FFFF의
// 순서가 UTF-16과 반대가 된다.
func utf16Less(a, b string) bool {
	for a != "" && b != "" {
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if ra != rb {
			return utf16Unit(ra) < utf16Unit(rb) ||
				(utf16Unit(ra) == utf16Unit(rb) && ra < rb)
		}
		a, b = a[na:], b[nb:]
	}
	return a == "" && b != ""
}

// utf16Unit은 룬의 첫 UTF-16 코드 단위다(보조 평면은 상위 서로게이트).
func utf16Unit(r rune) rune {
	if r >= 0x10000 {
		return 0xD800 + ((r - 0x10000) >> 10)
	}
	return r
}
