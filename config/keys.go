package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// 규칙 파일의 키 검사. yaml.v3는 구조체에 없는 키를 조용히 버린다 — `forbiden:` 같은 오타 하나가
// forbidden 섹션을 통째로 꺼도 Load가 성공해 `rules --strict`가 위반 없이 0으로 끝났고, `exclud:`면
// 빼려던 패키지가 아무 표시 없이 수확됐다. checkRefs가 정의되지 않은 컴포넌트 이름을 거부하는 것과
// 같은 이유로, 모르는 키도 규칙이 있다는 착각을 만들기 전에 거부한다.

// 알려진 키 목록. 구조체의 yaml 태그와 같아야 한다 — TestKnownKeysMatchStructTags가 태그와 대조해, 필드를 더하고
// 여기를 빠뜨리면 테스트가 실패한다. 리플렉션으로 만들지 않는 것은 제품 코드에 reflect import를 들이지 않기
// 위해서다(자기 분석의 반사 limitation이 이 패키지 몫만큼 늘어난다).
var (
	// fileKeys는 규칙 파일 최상위의 알려진 키다(File).
	fileKeys = []string{"common", "components", "deny", "deps", "exclude", "fileRules", "forbidden",
		"independent", "limits", "signature", "stability", "version", "visibleTo"}
	// denyEntryKeys는 deny 목록의 매핑 항목이 받는 키다(DenyEntry).
	denyEntryKeys = []string{"reason", "to"}
	// entrySections는 항목이 매핑인 최상위 목록 섹션과 그 항목이 받는 키다(ForbiddenRule·FileRule·LimitRule).
	// deny는 컴포넌트마다 목록이 하나라 checkDenyEntries가 따로 본다.
	entrySections = map[string][]string{
		"forbidden": {"from", "to"},
		"fileRules": {"from", "name", "reason", "to"},
		"limits":    {"component", "maxIn", "maxOut"},
	}
)

// parseRules는 규칙 파일을 yaml 노드로 읽는다. 문서가 없으면(빈 파일, 주석뿐인 파일) nil이다.
// 둘째 문서부터는 내용이 있으면 거부한다 — yaml.Unmarshal은 첫 문서만 읽어 `---` 뒤의 규칙이 조용히
// 빠졌다. 끝에 붙은 `---`·`...`처럼 내용이 없는(null) 문서는 잃을 규칙이 없어 받는다. 오류의 줄은 그 문서의
// `---` 줄이다(문서 노드의 줄 — 내용 노드의 줄은 `---` 다음 줄일 수 있다).
func parseRules(data []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var first yaml.Node
	if err := dec.Decode(&first); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	for {
		var next yaml.Node
		err := dec.Decode(&next)
		if errors.Is(err, io.EOF) {
			return &first, nil
		}
		if err != nil {
			return nil, err
		}
		if content := documentContent(&next); content != nil && content.ShortTag() != "!!null" {
			return nil, fmt.Errorf("line %d: a second YAML document starts here; gartograph reads only the "+
				"first document of a rules file, so everything from this line on would be ignored — merge the "+
				"documents into one", next.Line)
		}
	}
}

// documentContent는 문서 노드의 내용 노드다(별칭은 푼다). 내용이 없으면 nil이다.
func documentContent(doc *yaml.Node) *yaml.Node {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	return resolveAlias(doc.Content[0])
}

// resolveAlias는 별칭 노드(*anchor)를 그 대상으로 바꾼다. 별칭이 아니면 그대로다.
func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// checkKnownKeys는 규칙 문서의 매핑 키가 File과 그 항목 형식(deny·forbidden·fileRules·limits 항목)이 아는
// 키인지 문서 순서대로 본다. 첫 모르는 키의 줄과 알려진 키 목록을 오류로 돌려준다. 형식이 틀린 값(매핑이어야
// 할 자리의 스칼라 등)은 여기서 보지 않는다 — Decode가 이미 줄 번호와 함께 거부했다.
func checkKnownKeys(doc *yaml.Node) error {
	root := documentContent(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	return eachPair(root, func(key mapKey, value *yaml.Node) error {
		if err := knownKey(key, fileKeys, ""); err != nil {
			return err
		}
		if key.name == "deny" {
			return checkDenyEntries(value)
		}
		if keys, ok := entrySections[key.name]; ok {
			return checkEntries(value, key.name+" entry", keys)
		}
		return nil
	})
}

// checkDenyEntries는 deny 섹션(컴포넌트 → 목록)의 매핑 항목 키를 본다. 스칼라 항목은 키가 없다.
func checkDenyEntries(section *yaml.Node) error {
	section = resolveAlias(section)
	if section == nil || section.Kind != yaml.MappingNode {
		return nil
	}
	return eachPair(section, func(_ mapKey, list *yaml.Node) error {
		return checkEntries(list, "deny entry", denyEntryKeys)
	})
}

// checkEntries는 목록 노드의 매핑 항목마다 키가 keys 안에 있는지 본다. where는 오류 문구의 자리 이름이다.
func checkEntries(list *yaml.Node, where string, keys []string) error {
	list = resolveAlias(list)
	if list == nil || list.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range list.Content {
		item = resolveAlias(item)
		if item == nil || item.Kind != yaml.MappingNode {
			continue
		}
		if err := eachPair(item, func(key mapKey, _ *yaml.Node) error {
			return knownKey(key, keys, where)
		}); err != nil {
			return err
		}
	}
	return nil
}

// mapKey는 매핑 키 하나의 이름과 그 키를 쓴 줄이다.
type mapKey struct {
	name string
	line int
}

// eachPair는 매핑의 키·값 쌍마다 visit을 부른다. 병합 키(`<<: *anchor`, `<<: [*a, *b]`)는 yaml.v3가
// 디코딩할 때처럼 병합되는 매핑의 쌍으로 펼친다 — 펼치지 않으면 병합한 매핑 속 오타를 놓친다.
// 별칭 키(`{&k from: a}` 뒤의 `{*k : b}`)는 yaml.v3 디코딩처럼 대상의 이름(from)으로 읽고, 줄은 별칭을 쓴
// 자리다. 병합 키 판정은 별칭을 풀기 전의 키로 한다 — yaml.v3도 `<<`를 가리키는 별칭은 병합으로 보지 않는다.
// 재귀 대신 작업 목록으로 펼치고 매핑마다 한 번만 본다: 같은 앵커를 겹겹이 병합해도 일이 매핑 수에 묶이고,
// 자기 자신을 병합하는 앵커가 있어도 끝난다(자기 재귀 함수는 자기 분석 cycles --level symbol의 순환이기도 하다).
// 순서는 매핑 자신의 키(문서 순서) 다음에 병합된 매핑의 키다 — 결정적이라 첫 오류가 실행마다 같다.
func eachPair(mapping *yaml.Node, visit func(key mapKey, value *yaml.Node) error) error {
	pending := []*yaml.Node{mapping}
	seen := map[*yaml.Node]bool{mapping: true}
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		for i := 0; i+1 < len(current.Content); i += 2 {
			key, value := current.Content[i], current.Content[i+1]
			if key.Kind == yaml.ScalarNode && key.ShortTag() == "!!merge" {
				for _, source := range mergeSources(value) {
					if !seen[source] {
						seen[source] = true
						pending = append(pending, source)
					}
				}
				continue
			}
			if err := visit(mapKey{name: resolveAlias(key).Value, line: key.Line}, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergeSources는 병합 키 값이 가리키는 매핑들이다 — 매핑 하나 또는 매핑(별칭)의 목록이다. 매핑이 아닌
// 값은 yaml.v3가 디코딩에서 거부하므로 건너뛴다.
func mergeSources(value *yaml.Node) []*yaml.Node {
	value = resolveAlias(value)
	if value == nil {
		return nil
	}
	if value.Kind == yaml.MappingNode {
		return []*yaml.Node{value}
	}
	var out []*yaml.Node
	if value.Kind == yaml.SequenceNode {
		for _, item := range value.Content {
			if item = resolveAlias(item); item != nil && item.Kind == yaml.MappingNode {
				out = append(out, item)
			}
		}
	}
	return out
}

// knownKey는 키가 keys 중 하나인지 본다. 아니면 줄 번호, 자리(where, 최상위면 비움), 알려진 키 목록을
// 담은 오류다 — 무엇이 틀렸고 무엇을 쓸 수 있는지를 함께 알려 준다. 스칼라가 아닌 키(`? [x]`)는 구조체로
// 디코딩할 수 없어 Decode가 먼저 거부한다.
func knownKey(key mapKey, keys []string, where string) error {
	if slices.Contains(keys, key.name) {
		return nil
	}
	place := ""
	if where != "" {
		place = " in a " + where
	}
	return fmt.Errorf("line %d: unknown key %q%s (known keys: %s); a misspelled key would leave "+
		"its setting unread", key.line, key.name, place, strings.Join(keys, ", "))
}
