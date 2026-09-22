// Package graph는 gartograph의 순수 도메인이다.
//
// 이 프로젝트의 설계는 한 문장이다. 그래프가 산출물이고, 나머지는 전부 그 위의
// 질의다. 이 패키지는 go/packages·SSA 같은 수확 기술을 모른다 — 직렬화 가능한
// 사실과 결정적 정렬만 담으므로 외부 의존 없이 전 계층을 테스트할 수 있다.
package graph

// Version은 Document 와이어 형식의 버전이다.
// 형식이 바뀌면 올리고, isthmus의 GRAPH-EXCHANGE 계약과 함께 갱신한다.
const Version = 1

// Tool은 Document를 만든 생산자 식별자다.
const Tool = "gartograph"

// Level은 그래프 정점의 분해 단계다.
// Go 컴파일러가 패키지 순환 import를 막으므로, 모듈/패키지 레벨 검사만으로
// "순환 없음"을 주장하지 않고 타입·심볼 레벨까지 내려가는 것이 실전이다.
type Level string

const (
	LevelModule  Level = "module"
	LevelPackage Level = "package"
	LevelType    Level = "type"
	LevelSymbol  Level = "symbol"
)

// ParseLevel은 문자열을 Level로 해석한다.
// CLI 입력 경계에서만 쓰고, 알 수 없는 값은 에러로 돌린다.
func ParseLevel(s string) (Level, error) {
	switch l := Level(s); l {
	case LevelModule, LevelPackage, LevelType, LevelSymbol:
		return l, nil
	default:
		return "", &UnknownLevelError{Value: s}
	}
}

// UnknownLevelError는 지원하지 않는 레벨 문자열이다.
type UnknownLevelError struct{ Value string }

// Error는 소비자가 곧바로 고칠 수 있는 형태로 알린다.
func (e *UnknownLevelError) Error() string {
	return "unknown level " + quote(e.Value) + ": want module|package|type|symbol"
}

// VertexKind는 정점의 종류다.
// kind를 보면 같은 이름의 다른 존재(패키지와 심볼)를 구분할 수 있다.
type VertexKind string

const (
	KindModule  VertexKind = "module"
	KindPackage VertexKind = "package"
	KindType    VertexKind = "type"
	KindFunc    VertexKind = "func"
	KindMethod  VertexKind = "method"
	KindVar     VertexKind = "var"
	KindConst   VertexKind = "const"
)

// EdgeKind는 간선의 관계 종류다.
// contains는 소유(담기) 방향이고 나머지는 의존 방향이다 — 둘을 섞으면
// 패키지의 dependsOn이 비어 "아무것도 의존하지 않는다"로 오독된다.
type EdgeKind string

const (
	EdgeImport     EdgeKind = "import"
	EdgeCall       EdgeKind = "call"
	EdgeImplements EdgeKind = "implements"
	EdgeEmbeds     EdgeKind = "embeds"
	EdgeReferences EdgeKind = "references"
	// EdgeSignature는 선언의 타입 표현식(파라미터·결과·타입 정의)이
	// 참조하는 타입을 가리킨다. references의 부분집합이 아니라 별도 관계다 —
	// "본문 의존은 허용, 공개 API 타입 누출은 금지" 규칙을 세우려면
	// 시그니처만 따로 질의할 수 있어야 한다. 의존 관계이므로
	// contains와 달리 전이에 포함된다.
	EdgeSignature EdgeKind = "signature"
	EdgeContains  EdgeKind = "contains"
)

// Position은 소스 위치다.
// 패키지·모듈 정점처럼 한 지점이 없는 정점은 생략한다.
type Position struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column,omitempty"`
}

// Vertex는 그래프의 정점이다.
// ID는 안정 식별자로, 패키지 경로 또는 "pkgpath.Name"·"pkgpath.(Recv).Name"
// 형태를 쓴다 — 이름 충돌이 나면 reader가 분리해 부여한다.
// Exported는 도달성 루트 확장(retain_public)의 기준이다 —
// 수확 시점의 types.Object.Exported()를 그대로 옮긴다.
type Vertex struct {
	ID       string     `json:"id"`
	Kind     VertexKind `json:"kind"`
	Name     string     `json:"name"`
	Package  string     `json:"package,omitempty"`
	Position *Position  `json:"position,omitempty"`
	Exported bool       `json:"exported,omitempty"`
	// Generated는 `// Code generated ... DO NOT EDIT.` 마커 파일 출신이다.
	// 생성 코드를 그래프에서 숨기면 사실이 사라진다 — 표시만 하고
	// 제외 여부는 소비자가 정한다.
	Generated bool `json:"generated,omitempty"`
}

// Edge는 방향 있는 관계다.
// 의존 간선은 From이 To를 필요로 한다(From → To).
type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
}

// Document는 버전ed 그래프 산출물이다.
// 리포트 diff와 캐시가 성립하려면 같은 입력이 같은 바이트가 되어야 하므로,
// 내보내기 전에 반드시 Sort로 정규화한다.
type Document struct {
	Version int    `json:"version"`
	Tool    string `json:"tool"`
	Level   Level  `json:"level"`
	Root    string `json:"root"`
	// Module은 주 모듈 경로다 — 규칙의 컴포넌트 패턴이 파일시스템이 아니라
	// 모듈 상대 경로로 매칭되도록 한다.
	Module string `json:"module,omitempty"`
	// Roots는 수확 시점의 보존 루트(main·init)다 — 루트는 입력 사실이라 문서에 남긴다.
	Roots       []string `json:"roots,omitempty"`
	Vertices    []Vertex `json:"vertices"`
	Edges       []Edge   `json:"edges"`
	Limitations []string `json:"limitations,omitempty"`
}

// Limitation은 분석이 보지 못한 것을 그 입력에서 실제로 세어 적는다.
// 상투적 경고는 읽히지 않으므로, 알릴 것이 없으면 비워 둔다.
func (d *Document) Limitation(msg string) {
	d.Limitations = append(d.Limitations, msg)
}
