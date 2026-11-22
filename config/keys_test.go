package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ictechgy/gartograph/internal/testutil"
)

// TestLoadRejectsUnknownKeys는 규칙 파일의 모르는 키가 줄 번호·자리·알려진 키 목록을 담은 Load 오류인지
// 확인한다. yaml.v3는 모르는 키를 조용히 버려, `forbiden:` 오타가 forbidden 규칙을 통째로 끄고도 Load가
// 성공했다(rules --strict가 위반 없이 0). 최상위·항목 안·병합 키(<<)로 들어온 키를 모두 본다.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	const comps = "components: {a: [a], b: [b]}\ndeps: {a: [b], b: []}\n"
	cases := []struct {
		name, body, want string
	}{
		{"top-level typo", comps + "forbiden:\n  - {from: a, to: b}\n",
			`line 3: unknown key "forbiden" (known keys: common, components, deny, deps, exclude, ` +
				`fileRules, forbidden, independent, limits, signature, stability, version, visibleTo)`},
		{"exclude typo", comps + "exclud: [gen/**]\n", `line 3: unknown key "exclud"`},
		{"case matters", comps + "fileRUles: []\n", `unknown key "fileRUles"`},
		{"forbidden entry field", comps + "forbidden:\n  - {from: a, to: b, reason: x}\n",
			`line 4: unknown key "reason" in a forbidden entry (known keys: from, to)`},
		{"fileRules entry field", comps + "fileRules:\n  - name: r\n    form: \"*.go\"\n    to: b\n",
			`line 5: unknown key "form" in a fileRules entry (known keys: from, name, reason, to)`},
		{"limits entry field", comps + "limits:\n  - {component: a, maxOut: 1, maxin: 0}\n",
			`unknown key "maxin" in a limits entry (known keys: component, maxIn, maxOut)`},
		{"deny entry field", comps + "deny:\n  a:\n    - b\n    - {to: b, reasn: x}\n",
			`line 6: unknown key "reasn" in a deny entry (known keys: reason, to)`},
		{"merged into an entry", comps + "fileRules:\n  - &r {name: r, from: \"*.go\", to: b}\n" +
			"deny:\n  a:\n    - {<<: *r}\n", `unknown key "name" in a deny entry`},
		{"merged at top level", comps + "<<: {independant: [a, b]}\n", `unknown key "independant"`},
	}
	for _, c := range cases {
		_, err := Load(writeRules(t, c.body))
		if err == nil || !strings.Contains(err.Error(), c.want) ||
			!strings.Contains(err.Error(), "a misspelled key would leave its setting unread") {
			t.Errorf("%s: want an error containing %q, got %v", c.name, c.want, err)
		}
	}
}

// TestLoadAcceptsEveryDocumentedKey는 키 검사가 문서화된 형태(모든 최상위 섹션, deny의 스칼라·매핑 항목,
// version, 알려진 키만 병합하는 앵커)를 그대로 받는지 확인한다 — 검사가 정상 설정을 거부하면 모든 수확
// 명령이 종료 2가 된다.
func TestLoadAcceptsEveryDocumentedKey(t *testing.T) {
	body := `version: 1
components: {web: [web], db: [db], store: [store], core: [core], th: [testhelpers]}
deps:
  web: [store, core]
  store: [db, core]
  db: [core]
  core: []
  th: []
deny:
  web:
    - db
    - {to: th, reason: "test helpers stay in tests"}
signature: {web: [core]}
common: [core]
visibleTo: {db: [store]}
forbidden:
  - &ban {from: web, to: db}
  - {<<: *ban, to: th}
independent: [web, th]
fileRules:
  - name: no-testhelpers
    from: "!*_test.go"
    to: th
    reason: "production code must not import test helpers"
stability: true
limits:
  - {component: web, maxOut: 4}
  - {component: db, maxIn: 2}
exclude: [gen/**]
`
	f, err := Load(writeRules(t, body))
	if err != nil {
		t.Fatalf("every documented key must load: %v", err)
	}
	if len(f.Forbidden) != 2 || f.Forbidden[1].From != "web" || f.Forbidden[1].To != "th" ||
		len(f.Deny["web"]) != 2 || f.Deny["web"][1].Reason == "" || !f.Stability || len(f.Limits) != 2 {
		t.Fatalf("documented keys must decode: %+v", f)
	}
}

// TestLoadRejectsSecondDocument는 `---` 뒤의 둘째 YAML 문서가 Load 오류인지 확인한다. yaml.Unmarshal은 첫
// 문서만 읽어 둘째 문서의 규칙이 조용히 빠졌다. 내용 없는 끝 문서(`---`·`...`만)는 잃을 규칙이 없어 받는다.
func TestLoadRejectsSecondDocument(t *testing.T) {
	const head = "components: {a: [a], b: [b]}\ndeps: {a: [b], b: []}\n"
	_, err := Load(writeRules(t, head+"---\nforbidden:\n  - {from: a, to: b}\n"))
	if err == nil || !strings.Contains(err.Error(), "line 4: a second YAML document starts here") {
		t.Fatalf("a second document must be rejected with its line, got %v", err)
	}
	for _, tail := range []string{"---\n", "...\n", "---\n# only a comment\n", "--- ~\n"} {
		if _, err := Load(writeRules(t, "---\n"+head+tail)); err != nil {
			t.Errorf("an empty trailing document %q must load: %v", tail, err)
		}
	}
	// 빈 파일·주석뿐인 파일은 지금처럼 "컴포넌트 없음" 오류다(문서 없음이 패닉이나 성공이 되면 안 된다).
	for _, body := range []string{"", "# nothing yet\n"} {
		if _, err := Load(writeRules(t, body)); err == nil || !strings.Contains(err.Error(), "no components defined") {
			t.Errorf("%q: want the no-components error, got %v", body, err)
		}
	}
}

// TestEachPairVisitsEachMergedMappingOnce는 병합 키 펼침이 매핑마다 한 번만 보고 자기 자신을 병합하는 매핑에서도
// 끝나는지 확인한다. Load에서는 디코딩이 그런 앵커를 먼저 거부하지만(`anchor … value contains itself`), 펼침이
// 재귀였다면 겹친 앵커마다 일이 배로 늘고 자기 병합에서 끝나지 않는다.
func TestEachPairVisitsEachMergedMappingOnce(t *testing.T) {
	merge := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!merge", Value: "<<"}
	shared := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "from"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: "a"},
	}}
	self := &yaml.Node{Kind: yaml.MappingNode}
	self.Content = []*yaml.Node{
		merge, {Kind: yaml.AliasNode, Alias: self},
		merge, {Kind: yaml.SequenceNode, Content: []*yaml.Node{
			{Kind: yaml.AliasNode, Alias: shared}, {Kind: yaml.AliasNode, Alias: shared},
		}},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "to"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: "b"},
	}
	var keys []string
	testutil.FinishWithin(t, testutil.BlockDeadline, func() {
		if err := eachPair(self, func(key, _ *yaml.Node) error {
			keys = append(keys, key.Value)
			return nil
		}); err != nil {
			t.Error(err)
		}
	})
	if strings.Join(keys, ",") != "to,from" {
		t.Fatalf("want own keys then each merged mapping once (to,from), got %v", keys)
	}
}

// TestKnownKeysMatchStructTags는 키 검사의 알려진 키 목록이 구조체의 yaml 태그와 같은지 확인한다. 필드를 더하고
// 목록을 빠뜨리면 그 새 키가 "모르는 키"로 거부되고, 필드를 빼고 목록에 남기면 오타가 다시 조용히 버려진다.
func TestKnownKeysMatchStructTags(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		v    any
	}{
		{"File", fileKeys, File{}},
		{"DenyEntry", denyEntryKeys, DenyEntry{}},
		{"ForbiddenRule", entrySections["forbidden"], ForbiddenRule{}},
		{"FileRule", entrySections["fileRules"], FileRule{}},
		{"LimitRule", entrySections["limits"], LimitRule{}},
	}
	for _, c := range cases {
		if got := yamlTagKeys(c.v); !slices.Equal(got, c.keys) {
			t.Errorf("%s: known keys %v, struct yaml tags %v", c.name, c.keys, got)
		}
	}
	if len(entrySections) != 3 {
		t.Errorf("entrySections holds %d sections; add the new one to this test", len(entrySections))
	}
}

// yamlTagKeys는 구조체 값 v의 공개 필드가 받는 yaml 키를 정렬해 돌려준다(태그가 없거나 "-"인 필드는 뺀다).
func yamlTagKeys(v any) []string {
	typ := reflect.TypeOf(v)
	var keys []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if !field.IsExported() || name == "" || name == "-" {
			continue
		}
		keys = append(keys, name)
	}
	slices.Sort(keys)
	return keys
}
