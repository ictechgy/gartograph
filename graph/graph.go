// Package graph는 gartograph의 순수 도메인이다.
//
// 이 프로젝트의 설계는 한 문장이다. 그래프가 산출물이고, 나머지는 전부 그 위의
// 질의다. 이 패키지는 go/packages·SSA 같은 수확 기술을 모른다 — 직렬화 가능한
// 사실과 결정적 정렬만 담으므로 외부 의존 없이 전 계층을 테스트할 수 있다.
package graph

// Version은 Document 와이어 형식의 버전이다.
// 형식이 바뀌면 올리고, isthmus의 GRAPH-EXCHANGE 계약과 함께 갱신한다.
// 2: Edge에 positions — "의존이 어디서 일어나는가"를 남겨 파일 스코프
// 규칙과 위반 위치 보고의 재료가 된다.
const Version = 2

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
	// KindField는 struct 필드다 — 심볼 레벨에서만 수확된다.
	// 메서드와 같은 멤버 단위라 ID는 "pkgpath.(Recv).Name" 형태를 공유한다.
	// 필드는 나가는 의존을 만들지 않으므로(값은 담지만 코드는 아님)
	// 들어오는 references만이 도달성을 결정한다.
	KindField VertexKind = "field"
	KindVar   VertexKind = "var"
	KindConst VertexKind = "const"
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
	// External은 패키지 정점이 주 모듈 밖에 속한다는 표시다 — --deps로
	// 수확된 의존이나 중첩 모듈 패키지. 경로 접두사 추론은 중첩 모듈에서
	// 틀리므로 수확 시점의 모듈 소속 사실을 그대로 옮긴다.
	External bool `json:"external,omitempty"`
	// Generated는 `// Code generated ... DO NOT EDIT.` 마커 파일 출신이다.
	// 생성 코드를 그래프에서 숨기면 사실이 사라진다 — 표시만 하고
	// 제외 여부는 소비자가 정한다.
	Generated bool `json:"generated,omitempty"`
	// Interface는 type 정점이 인터페이스 타입이라는 표시다 —
	// implements 간선이 없어도(모듈 안 구현체가 없어도) 알 수 있어야
	// "인터페이스에 메서드가 추가됐다"는 breaking 신호를 diff가 잡는다.
	Interface bool `json:"interface,omitempty"`
	// Fields는 struct 타입 정점의 필드 목록을 "name:Type" 형태로 선언
	// 순서대로 담는다 — unkeyed composite literal의 컴파일 계약은 필드
	// 목록·순서·타입이기 때문에 diff의 breaking 판정 재료다.
	// struct가 아닌 타입이나 필드를 수확하지 않은 옛 문서는 nil이다.
	Fields []string `json:"fields,omitempty"`
	// Value는 const 정점의 상수 값을 Go 리터럴 형태로 담는다 —
	// 상수는 컴파일 시 소비자 코드에 인라인되므로 값 변경은 재컴파일을
	// 깨지는 않아도 API 계약의 변경이다. diff의 breaking 분류 재료다.
	// const가 아닌 정점이나 값을 수확하지 않은 옛 문서는 비어 있다.
	Value string `json:"value,omitempty"`
	// Satisfies는 method 정점이 모듈 밖에서 선언된 인터페이스의 메서드를
	// 구현한다는 사실이다 — 명명 인터페이스는 "경로.이름"(universe는 "error"),
	// 의존 소스의 이름 없는 표기는 파라미터 이름 없는 메서드 집합
	// ("interface{Unwrap() error}")으로
	// 정렬해 담는다. 모듈 밖 코드(fmt, flag, encoding/json 등)의 호출 지점은
	// 그래프에 없으므로, 이 사실 없이는 error.Error·flag.Value.Set 같은
	// 메서드가 살아 있어도 unreachable로 보인다. 판정은 analysis가 한다.
	// 비공개·internal 경로 명명 인터페이스(context.stringer 등)는 다른 인터페이스
	// (공개 명명·error·이름 없는 표기)가 같은 메서드를 설명하지 못할 때만 싣는다 — 목록이 비지 않는 한 도달성은 같고, 늘 붙는
	// 비공개 이름은 triage만 흐린다.
	Satisfies []string `json:"satisfies,omitempty"`
	// Receiver는 Satisfies가 있는 method 정점의 리시버 타입 정점 ID다 —
	// "리시버 타입이 도달하면 외부 디스패치로 이 메서드도 도달할 수 있다"를
	// ID 문자열 파싱 없이 계산하기 위한 짝 사실이다. Satisfies가 없으면 비어 있다.
	Receiver string `json:"receiver,omitempty"`
}

// Edge는 방향 있는 관계다.
// 의존 간선은 From이 To를 필요로 한다(From → To).
// Positions는 이 관계가 성립하는 사용 지점들이다 — 같은 From→To 호출이
// 파일 여럿에 흩어져 있으면 지점이 여러 개다. 정렬돼 있고, 한 지점이
// 없는 관계(contains·implements·모듈 import)는 비어 있다.
type Edge struct {
	From      string     `json:"from"`
	To        string     `json:"to"`
	Kind      EdgeKind   `json:"kind"`
	Positions []Position `json:"positions,omitempty"`
}

// Document는 버전ed 그래프 산출물이다.
// 리포트 diff와 캐시가 성립하려면 같은 입력이 같은 바이트가 되어야 하므로,
// 내보내기 전에 반드시 Sort로 정규화한다.
type Document struct {
	Version int    `json:"version"`
	Tool    string `json:"tool"`
	Level   Level  `json:"level"`
	// Root는 수확 시점의 --dir을 절대 경로로 적는다 — Position.File이
	// 절대 경로이므로 파일→정점 해석의 기준점도 절대여야 한다.
	Root string `json:"root"`
	// Module은 주 모듈 경로다 — 규칙의 컴포넌트 패턴이 파일시스템이 아니라
	// 모듈 상대 경로로 매칭되도록 한다.
	Module string `json:"module,omitempty"`
	// ModuleDir은 주 모듈의 go.mod가 있는 디렉터리다 — --dir이 모듈의
	// 하위 디렉터리를 가리킬 때 Root와 ModuleDir이 갈라지므로, 파일→패키지
	// 해석은 ModuleDir을 기준으로 해야 한다. 없으면 Root가 그 역할이다.
	ModuleDir string `json:"moduleDir,omitempty"`
	// Roots는 수확 시점의 보존 루트(main·init)다 — 루트는 입력 사실이라 문서에 남긴다.
	Roots       []string `json:"roots,omitempty"`
	Vertices    []Vertex `json:"vertices"`
	Edges       []Edge   `json:"edges"`
	Limitations []string `json:"limitations,omitempty"`
	// AnonymousDispatch는 이 문서의 Satisfies가 의존 소스의 이름 없는 인터페이스
	// 표기까지 담았다는 수확 사실이다. 이 수확 이전 문서(false)에도 명명
	// 인터페이스의 Satisfies는 있어서, Satisfies 존재만으로는 "이름 없는 것도
	// 셌다"를 말할 수 없다 — 소비자가 거짓 한계 문구를 내지 않게 하는 표시다.
	AnonymousDispatch bool `json:"anonymousDispatch,omitempty"`
}

// Limitation은 분석이 보지 못한 것을 그 입력에서 실제로 세어 적는다.
// 상투적 경고는 읽히지 않으므로, 알릴 것이 없으면 비워 둔다.
func (d *Document) Limitation(msg string) {
	d.Limitations = append(d.Limitations, msg)
}
