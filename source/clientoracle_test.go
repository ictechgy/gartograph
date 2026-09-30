// route-call 모의 서버 오라클의 오프라인 대조 — experiments/client-oracle이 실제 resty v2.17.2와 net/http로
// 기록한 요청(recorded/report.json)을, 같은 fixture 소스를 resty 스텁으로 해석한 지금의 사실과 대조한다.
// 네트워크 없이 돈다. 기록을 다시 만들려면 experiments/client-oracle/run.sh를 실행한다.
package source

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// oracleDir는 오라클 실험 디렉터리다(저장소 루트 기준).
func oracleDir() string { return filepath.Join("..", "experiments", "client-oracle") }

// oracleRow는 기록된 시나리오 하나다.
type oracleRow struct {
	Name    string `json:"name"`
	Request struct {
		Method string `json:"method"`
		Host   string `json:"host"`
		Path   string `json:"path"`
	} `json:"request"`
	Facts  []string `json:"facts"`
	Result string   `json:"result"`
}

// TestClientOracleRecording은 기록된 요청과 지금의 사실이 같고, 요청이 사실과 맞는지 본다.
func TestClientOracleRecording(t *testing.T) {
	var recorded struct {
		Summary   map[string]int `json:"summary"`
		Scenarios []oracleRow    `json:"scenarios"`
	}
	readJSON(t, filepath.Join(oracleDir(), "recorded", "report.json"), &recorded)
	if recorded.Summary["mismatched"] != 0 || recorded.Summary["scenarios"] != len(recorded.Scenarios) ||
		len(recorded.Scenarios) < 30 {
		t.Fatalf("recording summary = %v", recorded.Summary)
	}
	src, err := os.ReadFile(filepath.Join(oracleDir(), "fixtures", "clientapp", "clientapp.go"))
	if err != nil {
		t.Fatal(err)
	}
	wrappers, err := LoadWrapperFile(filepath.Join(oracleDir(), "wrappers.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := clientModule(t, "example.com/clientoracle", map[string]string{"fixtures/clientapp/clientapp.go": string(src)}, "resty")
	doc, err := ClientRouteFacts(ClientRouteOptions{Harvest: Options{Dir: dir, Patterns: []string{"./fixtures/..."}},
		GeneratedAt: fixedTime, Wrappers: wrappers}, "test")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string][]RouteCallFact{}
	const prefix = "example.com/clientoracle/fixtures/clientapp."
	for _, f := range doc.Facts {
		if f.Symbol != nil && strings.HasPrefix(f.Symbol.Usr, prefix) {
			name := strings.TrimPrefix(f.Symbol.Usr, prefix)
			byName[name] = append(byName[name], f)
		}
	}
	for _, row := range recorded.Scenarios {
		var got []string
		for _, f := range byName[row.Name] {
			got = append(got, oracleCompact(f))
		}
		sort.Strings(got)
		want := append([]string(nil), row.Facts...)
		sort.Strings(want)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: facts %q, recorded %q", row.Name, got, want)
		}
		if result := oracleJudge(row, byName[row.Name]); result != row.Result {
			t.Errorf("%s: request %+v judged %s, recorded %s", row.Name, row.Request, result, row.Result)
		}
	}
}

// readJSON은 파일을 JSON으로 푼다.
func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// oracleCompact는 오라클(cmd/oracle compact)과 같은 한 줄 표기다.
func oracleCompact(f RouteCallFact) string {
	method := f.Method
	if f.MethodDynamic {
		method = "?"
	}
	channel := "<dynamic>"
	if f.Channel != nil {
		channel = *f.Channel
	}
	s := method + " " + channel + " " + f.PathAnchor
	if f.ChannelPrefix != "" {
		s += " prefix=" + f.ChannelPrefix
	}
	if f.Authority != "" {
		s += " host=" + f.Authority
	}
	return s
}

// oracleJudge는 오라클(cmd/oracle judge)과 같은 판정이다: 정적 사실은 동사·authority·템플릿(root는 전체,
// base는 세그먼트 경계 꼬리)이 요청과 맞아야 하고, dynamic만 있으면 dynamic이다.
func oracleJudge(row oracleRow, facts []RouteCallFact) string {
	if len(facts) == 0 {
		return "mismatched"
	}
	path := composePath([]urlPart{literalPart(row.Request.Path)}).template
	dynamic := 0
	for _, f := range facts {
		if f.Dynamic {
			dynamic++
			continue
		}
		if (!f.MethodDynamic && f.Method != row.Request.Method) || (f.Authority != "" && f.Authority != row.Request.Host) ||
			!oracleTemplateMatches(*f.Channel, f.PathAnchor, path) {
			return "mismatched"
		}
	}
	if dynamic == len(facts) {
		return "dynamic"
	}
	return "matched"
}

// oracleTemplateMatches는 템플릿이 경로와 세그먼트 단위로 맞는지 본다(`{}`는 비어 있지 않은 세그먼트).
func oracleTemplateMatches(template, anchor, path string) bool {
	ts := strings.Split(template[1:], "/")
	ps := strings.Split(path[1:], "/")
	if anchor == "base" {
		if len(ps) < len(ts) {
			return false
		}
		ps = ps[len(ps)-len(ts):]
	}
	if len(ts) != len(ps) {
		return false
	}
	for i := range ts {
		if (ts[i] == "{}" && ps[i] == "") || (ts[i] != "{}" && ts[i] != ps[i]) {
			return false
		}
	}
	return true
}
