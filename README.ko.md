# gartograph

<img src="icon.png" alt="gartograph의 새 마스코트" width="112" height="112" align="right">

Go 의존성 그래프 도구 — Go 모듈을 읽어 의존성 그래프를 만들고, 그 위에서
순환·도달성·심볼 이웃·레이어 규칙을 질의합니다.

설계는 한 문장입니다. **그래프가 산출물이고, 나머지는 전부 그 위의 질의입니다.**

> 이 파일은 [README.md](README.md)의 한국어 참조입니다. 정본은 영어판입니다.

자매 프로젝트: [cartograph](https://github.com/ictechgy/cartograph)(Swift) ·
kartograph(Kotlin/Android) · dartograph(Dart/Flutter) ·
[schemagraph](https://github.com/ictechgy/schemagraph)(DB) ·
isthmus(언어 경계 조인).

## 왜

Go에는 이미 `deadcode`, `goda`, `go-arch-lint`, `go-callvis`가 있지만 각자
한 종류의 질문에 제각각의 출력으로 답합니다. gartograph는 대신 네 레벨
(모듈/패키지/타입/심볼)의 **버전ed 그래프 하나**를 만들고 모든 분석을
그 위의 질의로 표현합니다 — 코딩 에이전트를 위한 결정적 JSON 계약으로,
삭제 판정 없이 사실과 근거만 담습니다.

## 설치

```bash
brew install ictechgy/tap/gartograph
# 또는
go install github.com/ictechgy/gartograph/cmd/gartograph@latest
```

## 사용

```bash
gartograph graph                              # 패키지 그래프(JSON, 결정적)
gartograph graph --level symbol               # call/implements/embeds/references
gartograph graph --level module               # 모듈(go.work 워크스페이스)
gartograph graph --level type --format mermaid    # Graphviz는 --format dot
gartograph graph --level symbol --out .gartograph/graph.json

gartograph cycles --level symbol --strict     # 순환 검사 — 패키지 순환은 Go가 금지하므로
                                              # 실전 검사는 type/symbol 레벨
gartograph dead                               # main·init에서 도달 불가 심볼 보고
                                              # 심볼 레벨은 struct 필드까지 —
                                              # 참조 안 된 멤버는 kind "field"로 보고
gartograph dead --retain-public               # 라이브러리: 공개 API 보존
gartograph dead --root my/pkg.Setup           # 추가 보존 루트
gartograph dead --explain my/pkg.F            # 왜 살아 있나 — 도달 경로 출력
gartograph dead --algo rta                    # RTA 정밀도 — 소스 필요, --graph와 불가
# 소스 레벨 보존: 선언(struct 필드 포함) 위의
# //deadcode:keep 또는 //gartograph:keep이 보존 루트가 됩니다.

# isthmus bridge-facts 문서 생성(platform "go")
# Go는 cgo를 unscanned-ffi-interop limitation으로만 신고 — 채널 사실 없음.
gartograph bridges --out go-facts.json

# isthmus persistence 사실 생성 — 타입 확인된 database/sql·sqlx·gorm
# 호출, SQL 리터럴, TableName() 바인딩, db/sql/gorm 컬럼 태그에서
# SQL 관계 참조를 수확한다(platform "go", target "persistence").
gartograph schema --out go-schema-facts.json

# isthmus http route 선언 생성 — net/http ServeMux·chi·gin·echo 라우트를
# (method, 정규 경로 템플릿)과 핸들러 정점 ID로(platform "go", target "http", roles ["server"])
gartograph routes --role server --out go-routes.json

# isthmus http route 호출 생성 — net/http·resty v2 요청과 isthmus http-wrappers v1로
# 선언한 래퍼 호출을 (method, 정규 경로 템플릿)과 감싸는 선언으로(roles ["client"])
gartograph routes --role client --wrappers http-wrappers.json --out go-calls.json

# isthmus trace용 language-traversal 문서 — root가 기대는 쪽(reach)과
# root에 기대는 쪽(impact)을 여러 root 한 번에. struct 필드 타입은 그 필드를 쓰는 선언에서만
# 따라간다(--type-edges members, 기본). --type-edges all은 이전의 넓은 도달이다.
gartograph reach --roots-from go-routes.json
gartograph impact --format language-traversal --roots-from go-schema-facts.json
gartograph impact --format language-traversal --roots-from go-calls.json
gartograph rules --strict                     # .gartograph.yml 레이어 규칙 검사

# query·impact·path·shared는 기본으로 symbol 레벨을 수확해 패키지·타입·
# 함수·메서드 ID를 모두 찾는다. --level package는 큰 저장소에서 빠르지만
# 그 문서에는 패키지 ID만 있다.
gartograph query <정점ID> --depth 2            # 이웃 되묻기(에이전트용 JSON)
gartograph query 'github.com/ictechgy/gartograph/graph.(Document).Sort'
gartograph impact <정점ID> --depth 2           # 역방향 전이 — 바꾸면 뭐가 깨지나
gartograph impact --since origin/main...HEAD   # 바뀐 파일 기준 영향 분석
                                              # (--files cli/cli.go도 가능)
gartograph path <from-id> <to-id>              # 최단 의존 경로 — 왜 도달하나
gartograph shared <id1> <id2>                  # 공통 도달 집합 + 루트별 고유분
gartograph unused-deps --strict                # 어느 패키지도 안 쓰는 go.mod require
gartograph diff old.json new.json --strict     # 문서 비교 — breaking 신호에 1
# (breaking: 공개 심볼 제거·비공개화·kind 변경·시그니처 참조 소실·
#  인터페이스 메서드 추가·서명 변경(직접·임베드 경유 — 두 문서에 메서드 집합이 있으면
#  io.Reader 같은 모듈 밖 임베드도)·struct 필드 계약 파괴·공개 상수 값 변경)
gartograph metrics                             # Ca/Ce/불안정성 + 추상성(A)·주 계열
                                               # 거리(D=|A+I-1|) + orphan 패키지
                                               # (A/D는 타입 레벨 수확이 필요)
gartograph mapping                             # 패키지→컴포넌트 매핑 보기
gartograph init                                # .gartograph.yml 스캐폴딩
gartograph mcp --level symbol                  # MCP stdio로 에이전트에 서빙
gartograph dead --graph .gartograph/graph.json # 저장 문서로 분석
```

공통 플래그: `--dir`(모듈 루트), `--pattern`(반복 가능), `--tests`(테스트
진입점이 보존 루트가 됨), `--deps`, `--tags`(빌드 태그; 제약으로 빠진 파일은
limitations에 셈), `--goos`/`--goarch`(다른 타깃 플랫폼으로 수확 — 선택 사실이
limitations에 남음), `--graph`(저장 문서 읽기).
종료 코드: `0` 정상 · `1` strict 위반 · `2` 사용법/분석 오류.

## 규칙 설정

`gartograph rules`는 모듈 루트의 `.gartograph.yml`을 읽습니다. `components`는
모듈 상대 패키지 경로를 매핑하고, `deps`는 **허용 목록**입니다 — 항목이 없는
컴포넌트는 자기 자신 외에 아무것도 의존할 수 없습니다. 어느 컴포넌트에도
속하지 않은 패키지는 `unmapped`로 보고됩니다 — 매핑 구멍은 "규칙 무관"이
아니라 "규칙이 모르는 영역"입니다.

```yaml
components:
  cli:      ["cli"]
  analysis: ["analysis"]
  core:     ["graph"]
deps:
  cli:      ["analysis", "core"]
  analysis: ["core"]
  core:     []
```

선택 섹션 둘이 더 좁힙니다:

```yaml
deny:                     # 무조건 금지 — deps보다 우선
  analysis: ["cli"]
  web:                    # 항목에 사유를 달 수 있습니다 — 위반 보고에 실림
    - {to: db, reason: "internal/store를 거쳐라"}
signature:                # 공개 API 타입 누출 (심볼 레벨 필요)
  api:      ["core"]      # api의 공개 시그니처는 core 타입만 가리킬 수 있음
```

- `deny`는 `deps`를 이깁니다 — "보통 허용, 이 조합은 금지".
- `signature`는 **exported** 심볼의 `signature` 간선을 검사합니다 — 본문 의존은
  허용하면서 공개 API의 타입 누출만 막을 때 씁니다. 위반은 `rule` 필드로
  구분됩니다(`allow`/`deny`/`signature`/`visibleTo`/`forbidden`/`independence`).

선택 섹션이 계약 어휘를 더 넓힙니다:

```yaml
common: ["core"]          # 모든 컴포넌트가 deps에 적지 않아도 의존 가능
visibleTo:                # 공급자 측 규칙 — 누가 나를 의존할 수 있나
  db: ["store"]           # store만 db를 import 가능
forbidden:                # 전이 금지 — 직접이든 경유든 도달 자체가 위반
  - {from: api, to: db}   # api는 다른 컴포넌트를 경유해서도 db에 닿으면 안 됨
independent: [web, cli]   # web과 cli는 어느 방향으로도 서로 도달 불가
```

- `common`은 공통 부품을 매 deps에 반복 적는 boilerplate를 없앱니다.
- `visibleTo`는 `deps`의 거울입니다 — deps는 "내가 무엇을 쓸 수 있나",
  visibleTo는 "누가 나를 쓸 수 있나". 둘 다 허용 목록이라 visibleTo는
  좁힐 뿐 풀지 않습니다.
- `forbidden`은 직접 간선이 아니라 도달성을 봅니다 — deps는 직접 import만
  봅니다. 위반에는 목격 경로 `path`가 실립니다.
- `independent`는 목록 안 모든 쌍에 대한 양방향 `forbidden`입니다 —
  "둘은 독립"이라는 의도가 이름으로 남습니다. 위반은 `rule: "independence"`로
  보고됩니다.
- `stability: true`는 안정성 방향 계약입니다 — dependency-cruiser의
  `moreUnstable`입니다. 컴포넌트는 자기보다 불안정한(I = Ce/(Ca+Ce),
  `metrics`가 보고하는 같은 수치) 컴포넌트에 의존할 수 없습니다.
  위반은 `rule: "stability"`와 사유에 두 불안정도 값을 싣습니다.
- `limits`는 컴포넌트 덩치 상한입니다 — dependency-cruiser의
  `max-dependencies` 계약입니다. `maxOut`은 의존 가능한 다른 컴포넌트
  수(Ce), `maxIn`은 나를 의존할 수 있는 수(Ca)를 제한합니다. 상한 초과는
  간선 하나의 위반이 아니라 컴포넌트 수준의 사실입니다 — 위반은
  `rule: "limit"`, `name: "maxIn"|"maxOut"`, 실제 수치를 싣습니다.
- `exclude`는 규칙이 아니라 수확 필터입니다 — 패턴에 맞는 패키지
  (컴포넌트와 같은 글롭 의미론 — 주 모듈은 상대 경로, 외부는 전체 경로)는
  정점이 되지 않고, 그쪽으로의 import는 `limitations`에 셉니다.
  생성 코드나 vendored 트리에 씁니다:
- `fileRules`는 import가 일어나는 **파일**로 금지 범위를 좁힙니다 —
  dependency-cruiser의 `not-to-dev-dep` 계약입니다. `from`은 `/`가 없으면
  파일명에, 있으면 모듈 상대 경로에 맞는 글롭이고, `!` 접두사는 반전입니다.
  위반은 `rule: "fileScope"`, 규칙 `name`, 해당 지점을 싣습니다.
  지점이 없는 간선은 검사할 수 없어 `fileScopeUnchecked`로 셉니다.
- 규칙이 참조하는 모든 이름은 정의된 컴포넌트여야 합니다 — `Load`가
  죽은 참조를 거부해 오타가 규칙인 척하지 못하게 합니다.

```yaml
fileRules:
  - name: no-testdeps-in-prod       # 프로덕션 파일의 테스트 헬퍼 import 금지
    from: "!*_test.go"              # 글롭에 안 맞는 파일이 위반
    to: testhelpers                 # 컴포넌트
    reason: "테스트 헬퍼는 프로덕션에 새면 안 됨"

stability: true                     # 의존은 더 안정된 컴포넌트 방향으로만
limits:                             # 방향이 아니라 덩치 상한
  - {component: web, maxOut: 4}     # web은 최대 4개 컴포넌트까지만 의존
  - {component: db, maxIn: 2}       # db는 최대 2개 컴포넌트가 의존 가능
exclude: [gen/**, testdata/**]      # 이 패키지들은 수확하지 않음
```

**외부/vendor 규칙.** 컴포넌트 패턴은 `--deps`로 수확된 외부 패키지의 전체
import 경로에도 매칭됩니다 — `deps`/`deny`가 서드파티 모듈을 통제합니다:

```yaml
components:
  store: ["internal/store/**"]
  aws:   ["github.com/aws/**"]     # --deps 수확 시 외부 정점에 매칭
deps:
  store: ["core", "aws"]           # store 계층만 AWS를 import 가능
```

외부 정점이 있으려면 `rules --deps`로 검사하세요. 어느 패키지에도 매칭되지
않은 컴포넌트는 `unmatchedComponents`로 보고됩니다 — 외부 패턴을 쓰고
`--deps`를 빼먹었다는(또는 패턴이 stale하다는) 신호입니다. 컴포넌트에 매칭
되지 않은 외부 패키지는 `unmappedExternal`로 따로 보고됩니다.

**Baseline.** 기존 레포에 규칙을 도입할 때: 오늘의 위반을 한 번 기록하고,
이후에는 새 위반만 `--strict`에서 실패합니다. 같은 플래그가 `cycles`와
`dead`에도 동작합니다 — 종류마다 다른 baseline 파일 kind를 쓰므로
파일이 조용히 엇갈려 적용되지 않습니다.

```bash
gartograph rules  --write-baseline .gartograph-baseline.json
gartograph rules  --baseline .gartograph-baseline.json --strict
gartograph cycles --level symbol --write-baseline .cycles-baseline.json
gartograph dead   --baseline .dead-baseline.json --strict
```

기록된 위반은 `baselined`로 보고되고, 더 이상 발생하지 않는 항목은
`staleBaseline`으로 돌아옵니다 — 쌓이면 재생성하세요. SARIF와 `--strict`는
새 위반만 봅니다.

패턴: 정확 일치, `x/**` 재귀 접두사, `*` 세그먼트 글롭.
`rules`·`cycles`·`dead` 모두 `--format sarif`를 받습니다 — 순환은
`dependency-cycle` error, unreachable은 `unreachable-symbol` warning
(삭제 판정이 아니라 사실)으로 보고됩니다.

## 출력 계약(에이전트용)

- 결정적 JSON — 같은 입력, 같은 바이트.
- `query`는 `depth`·`truncated`와 이웃 간선 종류 전부를 싣습니다.
- `dead`는 finding마다 `state`+`reason`+`exported`를, 보고에 사용한
  `roots`를 항상 싣습니다. `exported`가 triage 축입니다 — 비공개
  unreachable은 저장소 안에 닫혀 있고, 공개 unreachable은 외부 호출자·
  reflection·플러그인이 쓸 수 있습니다. 도구는 확신도를 매기지 않고
  사실을 싣습니다.
- `dead`는 struct 필드(`kind: "field"`)도 보고합니다 — 이름 있는 접근
  (`x.F`, `T{F: v}`, 위치 리터럴, 승격 경로, `==`)이 없는 필드는
  unreachable입니다. reflection·직렬화·통째 복사는 그래프에 안 보이므로
  필드 보고가 나오면 해당 limitation이 함께 실립니다.
- `//deadcode:keep`·`//gartograph:keep`을 선언(함수·메서드·var·const·
  타입·struct 필드)에 붙이면 문서의 `roots`에 보존 루트로 남습니다 —
  보존 의도가 CLI 플래그가 아니라 선언 옆에 삽니다. 표지는 주석 줄의
  첫 토큰이어야 합니다(`//deadcode:keep` 또는 `// deadcode:keep <사유>`).
  선언 위 doc 주석이나 struct 필드 줄에 두세요 — `func` 몸체 뒤의
  trailing 주석은 그 선언에 붙지 않습니다.
- `limitations`는 매 실행에서 실제로 세어 만듭니다(생략한 외부 참조 수,
  `reflect` 사용, `//go:linkname`) — 없으면 키가 빠집니다.
- 삭제 판정은 없습니다. `unreachable`은 그래프 사실이지 "지워도 됨"이 아닙니다.
- 모듈 밖에서 선언된 인터페이스(`error`, `fmt.Stringer`, `flag.Value`,
  `encoding.TextUnmarshaler` 등)를 구현하는 메서드는 심볼 그래프에
  `satisfies`·`receiver`를 싣습니다. 호출자가 표준 라이브러리·의존 안에
  있으므로 `dead`는 리시버 타입이 도달하면 그 메서드도 도달한 것으로 셉니다.
  보고된 메서드는 `satisfies` 목록을 triage 사실로 유지하고, `dead --explain`은
  리시버→메서드 걸음에 그래프 간선이 없으므로 `(external dispatch: …)`로 표시합니다.
  이 규칙은 `dead` 전용이며 `shared`·`path`·`impact`는 의존 간선만 따릅니다.
  의존 소스의 이름 없는 인터페이스(`errors.Is/As/Unwrap`의 `interface{ Unwrap() error }`,
  함수 안 인터페이스 타입, 인자 타입 리터럴, 패키지 스코프 별칭)도 파라미터
  이름 없는 메서드 집합 이름(`interface{Unwrap() error}`)으로 셉니다. 이렇게
  수확한 문서는 `anonymousDispatch: true`를 싣고, 표시 없는 옛 저장 문서에는
  재수확 권고가 붙습니다. reflection·제네릭 인터페이스 경유 디스패치는 여전히
  안 보이고 보고가 그 사실을 밝힙니다.

## 그래프 문서

정점 ID: 패키지는 `pkg/path`, 패키지 수준 심볼은 `pkg/path.Name`,
메서드는 `pkg/path.(Recv).Name`. 패키지마다 초기화 루트 정점 `pkg/path._`가 최대
하나 있습니다(`init`과 같은 방식, kind `var`). 기여하는 선언이 모듈 심볼을 참조할 때만
만들어지고(열거형의 `const ( _ = iota )`는 만들지 않음), 위치는 손으로 쓴 기여 선언이
있으면 그쪽이라 기여가 전부 생성 파일일 때만 `generated`입니다. 문서는
`initializerRoots: true`를 싣고, 표시 없는 옛 저장 문서에 `dead`(CLI·MCP)는 재수확을
권합니다. 초기화 루트는 빈 식별자 `var _`·`const _` 선언이 쓰는 것
(`var _ I = (*T)(nil)`은 `T`·`I`를 씀)과 호출을 실행하는 이름 있는 변수 초기화식이
쓰는 것(`var registered = register()`는 `registered`를 아무도 읽지 않아도 초기화 때
`register`를 실행)을 참조합니다. 호출이 없는 초기화식(함수 값 표·형 변환·builtin·함수
리터럴 본문)은 변수를 거쳐서만 닿고, 호출을 실행하는 초기화 식은 그 식 전체(같은 식 안의
함수 값·리터럴 본문 포함)가 붙습니다 — "살아 있다" 쪽 과대 근사입니다. `dead --algo rta`도
같은 이유로 로드한 모든 패키지의 합성 초기화 함수를 루트로 삼습니다.
점이 든 패키지 경로(`example.com/m/x.y`, `--tests`의 테스트 main 패키지 `x.test`)는
심볼 ID(`example.com/m/x`의 `y`·`test`)와 같아질 수 있습니다 — 그렇게 겹치는 심볼
ID에만 `#symbol` 접미사가 붙습니다(`example.com/m/x.y#symbol`). `#`는 import 경로에
쓸 수 없어 패키지는 경로 그대로이고 다른 ID는 바뀌지 않습니다. 접미사는 수확된 패키지
집합(형제 `x.y/` 디렉터리, `--tests`·`--deps`·`exclude`)에 따라 붙으므로 `diff`와
baseline은 `ID`와 `ID#symbol`을 같은 심볼로 짝짓습니다.
`dead --algo rta --explain`은 합성 패키지 초기화 함수 `pkgpath#init`(그래프 정점 아님)을
거칠 수 있습니다. `// Code generated ... DO NOT EDIT.`
마커 파일 출신 정점은 `generated: true`를 답니다 — 숨기지 않고 표시합니다.
type 정점은 `interface: true` 또는 `fields`(선언 순서의 `"name:Type"` 목록)를
답니다 — `diff`가 인터페이스 메서드 추가와 struct 필드 계약 파괴를
breaking으로 분류하는 재료입니다.
모듈 밖 인터페이스를 구현하는 method 정점은 `satisfies`(정렬된 인터페이스 이름)와
`receiver`(리시버 타입 정점 ID)를 답니다. 모듈 코드가 이름으로 쓸 수 없는 명명
인터페이스(비공개 `context.stringer`, `internal/` 경로 등)는 다른 인터페이스(공개 명명·
`error`·이름 없는 표기)가 그 메서드를 설명하지 못할 때만 싣습니다 — 목록이 비지
않으므로 도달성은 같습니다. 문서는 이름 없는 인터페이스까지 수확했으면
`anonymousDispatch: true`를 싣습니다(심볼 레벨 수확, 옛 문서엔 없음). 인터페이스 타입
정점은 `methods`(임베드 포함 전체 메서드 집합, 정렬된 `Name(params) results`)를 담고,
문서는 `interfaceMethodSets: true`를 싣습니다 — 이 표시가 없으면 목록 부재는 "몰랐다",
있으면 "메서드 없음"입니다. `fields`·`methods`의 타입은 정규 표기입니다(별칭을 풀고
`byte`→`uint8`, 타입 파라미터는 선언 위치 `P0`…) — 표기만 바뀐 리팩터가 거짓 breaking이 되지
않게. 제약 인터페이스는 `typeSet`(실효 타입 원소의 교집합 — 임베드한 제약(비공개 포함)과
유니언의 인터페이스 항을 펼치고, 항을 정렬하고, `comparable`은 항목으로 남김 — 표시
`interfaceTypeSets: true`)도 담고, `diff`는 공개 제약의 타입 집합이 바뀌면 breaking으로
봅니다(좁히면 인스턴스화가, 넓히면 허용 연산에 기댄 제네릭 코드가 깨질 수 있음).
간선 종류: `import`/`contains`/`embeds`/`implements`/`references`/`call`/
`signature`(선언 시그니처 안의 타입 참조 — `contains`와 달리 의존 관계).
인터페이스 호출은 CHA 팬아웃으로 인터페이스 메서드와 모든 구현 메서드에
간선을 긋습니다 — 과대 근사는 "살아 있다" 쪽으로만 기울어 `dead`가
도달 가능 코드를 오판하지 않습니다.

문서 버전은 `2`입니다. 간선은 그 관계가 성립하는 모든 사용 지점을
`positions`에 담습니다(`import`는 import 선언, `call`은 호출 식).
`contains`·`implements`·모듈 간선처럼 단일 지점이 없는 관계는 생략합니다.
`dead --algo rta`는 수확한 CHA 간선 대신 SSA 기반 RTA를 씁니다 —
더 좁지만 소스가 필요하고 과소 근사이므로 그 사실이 `limitations`에 실립니다.

## isthmus 교환 — `schema` usr와 순회 문서

`schema`의 relation-use 사실은 `symbol: {qualifiedName, usr}`를 싣습니다. `usr`는 사실을
감싸는 선언의 정점 ID로, `query`·`impact`·`reach`와 같은 ID입니다(`pkg.Func`,
포인터·타입 파라미터를 뗀 `pkg.(Type).Method`, `pkg.init`, `pkg.var`, 초기화 때 실행되는
빈 패키지 선언은 `pkg._`, 패키지 경로와 겹치면 `#symbol` 접미사). 귀속은 심볼 수확이 그
자리의 간선을 긋는 정점과 같습니다 — 함수·메서드(클로저 포함), 타입 선언(struct 컬럼
태그는 타입에 귀속 — 핸들러가 행 타입에 닿으면 그 컬럼들에 기댄다고 봅니다), 패키지
변수(`var a, b = x, y`의 i번째 값은 i번째 이름). `qualifiedName`은 import 경로 마지막
요소부터의 ID입니다. usr는 같은 `.gartograph.yml` exclude로 `impact`가 수확할 그래프의
심볼 정점일 때만 싣고, 없으면(빈 함수, 모듈 심볼을 쓰지 않는 빈 선언, exclude 패키지, 타입
정보 없는 패키지) `symbol`을 빼고 체인 전용 `missing-relation-usrs:`로 셉니다.

`reach`(`dependencies`)와 `impact --format language-traversal`(`dependents`)은
`impact`와 같은 의존 간선 위에서 isthmus
[`language-traversal` v1](https://github.com/ictechgy/isthmus/blob/main/docs/LANGUAGE-TRAVERSAL.md)
문서를 냅니다.

- root는 위치 인자 + `--roots-from FILE|-`(JSON 문자열 배열 또는 bridge-facts 문서의
  `facts[].symbol.usr`)이고 처음 나온 순서로 중복을 지웁니다 — 그 순서가
  `reached[].roots`의 뜻입니다.
- 다중 root 한 번 순회: 자기 아닌 root에서 닿은 모든 심볼, 닿는 root 인덱스(오름차순
  최대 64개, 넘으면 `rootsTruncated`), 가장 가까운 root의 `depth`와 최단 경로 목격
  `via`, via 간선 종류. 무작위 그래프에서 root별 BFS 오라클과 대조합니다.
- 모든 도달 심볼의 `evidence`는 root별 하한입니다 — 닿는 root 모두가 컴파일러가 확정한
  간선만으로 닿으면 `direct`, 어느 root가 인터페이스 디스패치 팬아웃 간선을 거쳐야 하면
  `candidate`. 함수 값·리플렉션 호출은 세지 않아 `dispatch`·`unresolvedCalls`는 싣지
  않습니다(완전성 주장 없음).
- `--depth`(1–128, 기본 128)·`--max`(도달 1–100000, 기본 100000)로 자르면
  `truncationReasons`에 `depth`·`max-reached`가 실립니다.
- `project`는 `schema`와 같은 realpath, `revision`은 `--revision` 또는 작업 트리가
  깨끗할 때의 git `HEAD`(`--graph`면 생략), `graphRevision`은 그래프 JSON의 SHA-256,
  `--generated-at`은 시각을 고정합니다.
- 정점이 아닌 id는 `symbol` 없이 `roots`에 싣고 `root-not-found:` 한계·잘림 이유와 함께
  문서를 쓴 뒤 64로 끝납니다. 사용법 오류(root 없음, 제어 문자, 범위 밖 숫자, 잘못된
  `--revision`·`--generated-at`, 순회 형식과 `--since`·`--files`·symbol 아닌 `--level`
  조합)는 표준 출력을 비우고 64입니다.

그래프 문서의 `dispatchEvidence: true`(symbol 레벨 수확)는 간선의 `candidate: true`
(모든 지점이 CHA 팬아웃인 관계)를 수확했다는 표시입니다. 표시 없는 옛 저장 문서로 순회하면
등급을 싣지 않고 `evidence-unassessed:` 한계를 답니다.

`--type-edges members|all`(기본 `members`)은 struct 필드 타입을 따라가는 방식입니다. 심볼 수확은
struct의 필드 타입 참조를 타입 정점에서 긋기 때문에, `all`에서는 공유 `Handler` struct의 모든 메서드가
리시버를 거쳐 모든 필드 뒤의 행 타입에 닿았고(`/api/health` → `users` 컬럼), 역방향에서는 필드 타입이
컨테이너의 모든 메서드로 퍼졌습니다. `members`는 필드 선언에서 나온 간선 `T → X`(타입 파라미터 머리가 아닌 references·signature)를 타입의 필드
목록(`fields`, 정규 타입)에서 `X`를 담는 필드들(`a, b *X`, `m map[K]X`, `Box[X]`, `struct{ x X }`)의 정점으로
옮깁니다(`T.f → X`). `T`에 닿아도 `X`에는 닿지 않고, `f`를 읽는 코드는 여전히 닿습니다. 닿지 않은 struct의
필드를 읽는 코드는 이제 그 필드 타입에 닿습니다(`all`은 놓쳤습니다). 두 방향 모두 평범한 그래프 하나라 계약의 `depth = via depth + 1`과
roots 포함 규칙이 그대로 성립합니다. `members`가 포기하는 것: 필드 이름 없이 struct 값 전체를
리플렉션에 넘기는 경로(`json.Marshal(h)`, ORM `Save(&u)`)는 임베드가 아닌 필드 타입에 닿지 않습니다
(임베드는 `embeds` 간선이라 그대로이고, 타입 자신과 그 태그에는 닿습니다). 실제로 잘라낸 곳이 있으면
같은 root·깊이에서 `all`이 더 닿는 심볼 수와 함께 `type-edges-members:` 한계를 싣습니다 — 이전 과대 근사가 필요하면 `--type-edges all`로
다시 돌립니다. 필드 목록이 없는 옛 그래프 문서는 옮길 간선이 없어 `all`과 같습니다. `impact`의 json
형식은 바뀌지 않습니다(거기에 `--type-edges`를 주면 사용법 오류 2).

`isthmus trace`는 route 선언(`routes`)을 핸들러 도달에, 핸들러 도달을 relation-use에 usr 정확 일치로
잇습니다 — route → 핸들러 → relation-use → 테이블, 그리고 그 역방향.

## isthmus 교환 — 서버 라우트(`routes`)

`gartograph routes --role server`는 isthmus bridge-facts http 문서(`platform: "go"`, `target: "http"`,
`roles: ["server"]`, `dispatch: "specificity"`, `sourceSets.tests: "excluded"`)를 냅니다. 등록이 받는
(method, 정규 템플릿)마다 `route-decl` 하나입니다. 호출은 이름이 아니라 타입(패키지 경로, 리시버 타입,
이름)으로 알아봅니다.

| 라우터 | 등록 | 접두사 합성 |
|---|---|---|
| net/http `ServeMux`, `http.Handle`·`HandleFunc`(DefaultServeMux) | `Handle`, `HandleFunc` | `mux.Handle("/api/", http.StripPrefix("/api", inner))`는 `inner`를 `/api` 아래에 붙입니다 |
| chi v5(`Mux`, `Router`) | `Get`…`Trace`, `Handle`·`HandleFunc`(`"POST /x"` 포함), `Method`·`MethodFunc` | `Route`, `Mount`(chi 라우터; 불투명 핸들러는 `P`·`P/`·`P/*`), `Group`, `With` |
| gin v1(`RouterGroup`, `IRoutes`, `IRouter`) | `GET`…`OPTIONS`, `Handle`, `Any`, `Match`, `Static*` | `Group`(`joinPaths`: 상대 경로의 끝 슬래시를 보존하는 `path.Join`) |
| echo v4(`Echo`, `Group`) | `GET`…`CONNECT`, `Add`, `Any`, `Match`, `Static*`, `File*` | `Group`(문자열 연결), `Host`(`narrowed`) |

라우터 값은 변수·struct 필드·함수 파라미터·결과·chi `Route`·`Group` 콜백을 흐름에 둔감하게 따라갑니다.
생성 호출까지 추적하지 못한 라우터의 등록(모듈 밖에서만 불리는 `Register(g *gin.RouterGroup)`)은
`pathAnchor: "base"`와 `templateSuffixes` 스코프의 `unresolved-route-prefix:`로 냅니다. 상수가 아닌
경로는 `dynamic` 사실과 `route-coverage:`, 상수가 아닌 동사(chi `Method`, gin·echo `Match`)는 사실 없이
그 템플릿 스코프의 `route-coverage:`입니다. 계약 밖 동사(`CONNECT`·`PROPFIND`)는 아무것도 내지 않습니다
— 모델링된 호출이 보낼 수 없습니다.

패턴 의미론은 공식 소스(Go 1.27.1 `net/http` `pattern.go`·`routing_tree.go`, chi v5.2.5 `tree.go`·
`mux.go`, gin v1.10.1 `tree.go`·`routergroup.go`·`utils.go`, echo v4.16.0 `router.go`·`group.go`)로 읽고
실제 라우터로 확인했습니다(아래 오라클).

- ServeMux(Go 1.22+): `[METHOD ][HOST]/PATH`. 리터럴은 `url.PathUnescape` 뒤에 비교합니다. `{x}`는
  비어 있지 않은 세그먼트 하나(끝 슬래시와는 맞지 않음), `{x...}`와 끝 `/`는 빈 나머지까지 받으므로
  `/a/` → `/a/{**}`와 `/a/`, 루트 `/` → `/{**}`와 `catchAllPrefix` `/`, `{$}`는 끝 슬래시만입니다.
  패닉하거나 절대 맞지 않는(method 있는 정리되지 않은 경로) 패턴은 내지 않습니다. host 패턴은
  `narrowed`입니다. go.mod의 `go`가 1.22 미만이거나 go.mod `godebug httpmuxgo121=1`·
  `//go:debug httpmuxgo121=1`이면 옛 리터럴·하위 트리 패턴입니다.
- chi: `{name}`·`{name:regexp}`는 다음 tail 바이트까지라 `/{name}.json`은 부분 세그먼트입니다. 정규식은
  고정(`^…$`)되고 `paramConstraints`가 됩니다(`[0-9]+`·`\d+`는 `int`, 그 밖은 패턴을 담은 `regex`).
  `*`는 패턴 끝에만 오고 빈 나머지도 받습니다. 뒤에 리터럴이 붙은 부분 파라미터는 빈 값을 받아
  `/f/{name}.json`은 `/f/.json`도 냅니다(계약의 빈 값 변형). 한 세그먼트의 파라미터 둘은 정규 템플릿이
  아닙니다(`route-coverage:`).
- gin: `:name`은 `/`까지입니다(`/:file.json`은 이름이 `file.json`인 세그먼트 전체 파라미터). 리터럴
  접두사 뒤에 올 수 있습니다(`/avatar_:n` → `/avatar_{}`). `*name`은 `/` 뒤 경로 끝에만 오고 `/x/`도
  받습니다.
- echo: `:name`은 `/`까지(`\:`는 리터럴 콜론), `*`는 빈 값까지 나머지 전부입니다. 자식 없는 파라미터
  노드는 `/`를 넘어 나머지 전부를 받습니다(`Find`의 `isLeaf`) — 그런 라우트는 `trailingSlash:
  "optional"`과 템플릿·동사 스코프의 `route-coverage:`를 답니다. `e.Static("/static", …)`은
  `/static*`를 등록합니다: `/static`·`/static/`·`/static/{**}`와 `/staticX` 경로를 위한
  `route-coverage:`(부모 접두사 스코프).
- 경로 중간의 세그먼트 전체 파라미터의 빈 값 변형(chi·gin은 `/users//posts`를 받음)은 내지 않습니다 —
  정리되지 않은 경로에서만 생기고 라우트마다 `route-decl-without-call` 경고를 늘립니다. 같은 라우터에
  같은 템플릿의 명시적 선언이 있으면 변형은 뺍니다.

`trailingSlash`: ServeMux는 `strict`(`/`로 끝나는 템플릿은 슬래시 없는 요청을 301로 보내 생략), chi·echo는
`strict`(모듈이 `middleware.StripSlashes`·`RedirectSlashes`나 echo `Add/RemoveTrailingSlash`를 쓰면 생략),
gin은 `optional`(`RedirectTrailingSlash` 기본 true가 다른 형태에 301/307로 답함), 엔진이 상수 `false`로
두면 `strict`. `{**}` 템플릿은 생략합니다.

디스패치는 넷 모두 `specificity`입니다. ServeMux는 어느 쪽도 더 구체적이지 않은 두 패턴을 등록 때
거부하므로, 실행되는 프로그램에서는 트리 순서(리터럴 → 단일 와일드카드 → 다중 와일드카드, 왼쪽부터,
역추적)가 곧 가장 구체적인 매치입니다. chi·gin·echo 기수 트리도 왼쪽부터 정적 → 파라미터 → catch-all
순서로 역추적합니다. 소비자와의 알려진 차이(최악이 거짓 match, 거짓 error는 없음): chi는 정규식을 부분
세그먼트보다 먼저 봅니다. chi·gin·echo의 GET 라우트는 HEAD에 답하지 않지만(405/404, 요청으로 확인)
소비자는 HEAD 호출을 GET 선언에 잇습니다. OPTIONS는 echo만 아는 경로에 204로 답합니다.

`symbol.usr`는 핸들러 정점 ID입니다: 메서드 값(`s.handleX`)·함수, 핸들러 인자가 없는 핸들러 생성 함수
호출(`handleX(db)` — 반환 클로저의 간선이 그 함수에서 나감), 핸들러 인자가 하나인 래퍼의 안쪽
핸들러(`auth(h)`, `http.TimeoutHandler`), `http.HandlerFunc(f)`, 모듈 타입의 `ServeHTTP`. 함수 리터럴(이나
그것을 담은 지역 변수)은 감싸는 선언으로 귀속하고 `anonymous-route-handlers:`로 셉니다. 모듈 밖
핸들러(정적 파일 서버, `promhttp.Handler()`)와 exclude 패키지는 symbol 없이 `missing-route-usrs:`로
셉니다. 수확하지 않는 라우터 import(gorilla/mux, httprouter, fiber, chi v1–v4 경로, echo v3/v5,
grpc-gateway 등)는 스코프 없는 `route-coverage:`를 더해, 사실 0건 문서가 "스캔했으나 없음"으로 읽히지
않게 합니다. 문서는 쓰기 전에 계약 자기 검증(템플릿·동사·catch-all 접두사 원본)을 거칩니다.
`conformance/`에 isthmus `http-template`·`http-dispatch`·`url-compose` 벡터를 벤더링했고(`conformance.lock`)
생산자 사례 114건(route 선언 51, route 호출 63)을 모두 통과합니다.

`experiments/routes-oracle`(별도 모듈, `run.sh`가 proxy.golang.org에서 chi·gin·echo를 받음)은 합성
ServeMux·chi·gin·echo 서버를 만들어 문서를 각 라우터 자신의 표(`chi.Walk`, `Engine.Routes()`, echo
`Routes()`·`Routers()`, reflect로 읽은 ServeMux 등록 색인)와 표본 요청 실행으로 대조합니다: 네 라우터
모두 정밀도·재현율 100%입니다(사실/항목 19/16, 28/34, 20/24, 19/23).

서버가 여럿인 모듈(`main` 패키지가 여럿)은 `--pattern ./cmd/api/...`와 `--service NAME`으로 서버마다
문서를 따로 내야 합니다. 아니면 라우트가 한 scope를 공유해 `route-decl-conflict`로 부딪힙니다.

## isthmus 교환 — 클라이언트 route 호출(`routes --role client`)

`gartograph routes --role client [--wrappers http-wrappers.json]`는 isthmus bridge-facts http 문서
(`platform: "go"`, `target: "http"`, `roles: ["client"]`, `sourceSets.tests: "excluded"`)를 쓰고, 호출 지점이
만드는 요청마다 `route-call` 하나를 냅니다. 호출은 이름이 아니라 타입으로 확인합니다:

| 라이브러리 | 호출 | base URL |
|---|---|---|
| net/http | `http.Get`·`Head`·`Post`·`PostForm`, `(*http.Client)`의 같은 메서드, `http.NewRequest(WithContext)` — `client.Do`가 보내는 URL은 만든 요청의 URL이라 사실은 요청을 만든 자리입니다 | 없음: 쓴 URL 문자열 그대로 |
| resty v2 | `R()`·`NewRequest()` 체인의 `Get`…`Patch`·`Execute`. 클라이언트는 변수·필드·파라미터·결과로 따라갑니다 | `SetBaseURL`·`SetHostURL`(끝 `/`를 뗌) 또는 `BaseURL`·`HostURL` 직접 대입(떼지 않음) |
| 선언한 래퍼 | `"language": "go"`인 `http-wrappers` v1 항목 | 선언의 `pathAnchor` |

URL 식은 문자열 상수(패키지를 건너 접힌 값), `+`, `fmt.Sprintf`(`%s`·`%v`는 인자를 펼치고 상수 `%d`는
값을 씀), 한 번만 대입된 지역 변수와 비공개 패키지 변수, 모든 대입이 비었거나 `?`로 시작하는 지역
변수(query 꼬리), `url.URL{Scheme, Host, Path}`(`Path`는 디코드된 경로라 `EscapedPath`처럼 인코딩하고,
상수가 아닌 `Host`는 `String()`이 `/`를 이스케이프해 경로를 담을 수 없으므로 경로는 `authority` 없는
root), `url.Parse`·`ParseRequestURI`, `(*url.URL).String`·`JoinPath`·`ResolveReference`·`Parse`,
`url.JoinPath`, `path.Join`, `strings.TrimSuffix`·`TrimRight(x, "/")`를 풉니다. 그 밖(필드, 파라미터,
함수 결과, 다른 모듈이 바꿀 수 있는 공개 패키지 변수, 주소를 꺼낸 변수)은 값입니다. 세그먼트 전체를
채우는 값은 `{}`, 세그먼트 안의 값은 호출을 dynamic과 `channelPrefix`로 만듭니다.

base 결합은 isthmus `http-wrappers`의 결합 방식
([HTTP-WRAPPERS "Go, Rust, Python 클라이언트"](https://github.com/ictechgy/isthmus/blob/main/docs/HTTP-WRAPPERS.md))을
따르고, Go 1.27.1 `net/url`(`resolvePath`·`joinPath`)·`path`와 resty v2.17.2 `client.go`·`middleware.go`
(`parseRequestURL`)에서 읽었습니다. 테스트가 원문 입력마다 실제 `url.JoinPath`·`ResolveReference`·
`path.Join` 결과와 대조합니다:

| 결합 방식 | API | 미상 base 뒤 `/x` | 미상 base 뒤 `x` | 리터럴 base |
|---|---|---|---|---|
| `rfc3986` | `ResolveReference`, `(*URL).Parse` | `root`(authority 없음) | `base`. base로 오르는 `..`나 빈 참조는 dynamic + `ambiguous-base-join:` | RFC 3986 병합과 점 세그먼트 제거. `//host/p`는 그 host |
| `go-join-path` | `url.JoinPath`, `(*URL).JoinPath` | `base` | `base`. base로 오르는 `..`는 dynamic + `ambiguous-base-join:` | `path.Join` 정리(`//` 축약, `..`는 base 경로 밖으로도 나감), 마지막 원소의 끝 `/` 보존 |
| `resty-base-url` | resty base URL | `base` | `base` | base(`SetBaseURL`이 뗀 값) + `/`로 시작하게 한 경로. `//`·점 세그먼트를 그대로 보냄. 절대 URL은 base를 쓰지 않음 |

문자열 연결(`+`, `fmt.Sprintf`)은 결합이 아니라 쓴 그대로의 URL 조립입니다(`//`·점 세그먼트를 그대로
보냄). 값 뒤에 `/`로 시작하는 리터럴이 오면 `base` 꼬리, 그 밖이면 dynamic + `ambiguous-base-join:`입니다
(벡터 없는 결합에 대한 계약 규칙). `path.Join`도 같은 방식으로 인자를 정리하고, 앞이 값이면 `base`
꼬리입니다. host가 동적인 문자열 URL(`"https://" + host + "/v1"`)은 그 값이 경로를 담을 수 있어 `base`이고,
net/http의 경로만 있는 URL도 `base`지만 base 식을 잇지 않았으므로 unresolved base로 세지 않습니다. resty
path param은 수신 클라이언트마다 따로 풉니다: 그 클라이언트나 그 요청의 `SetPathParam(s)`가 설정한
`{name}`은 `{}`(resty가 값을 path escape해 한 세그먼트. `parseRequestURL`처럼 escape 키가 raw 키를 이김),
raw param(`SetRawPathParam(s)`)은 `/`를 담을 수 있어 dynamic, path param이 하나도 없는 클라이언트는
`{name}`을 그대로 보내고(`%7Bname%7D`), 추적하지 못한 클라이언트는 `{}`로 둡니다(원문으로 두면 거짓
`route-call-without-decl` error가 될 수 있음). 한 클라이언트의 base URL이 여럿이면 base마다 사실 하나입니다.
동사는 API마다 고정이고, `NewRequest`·`Execute`는 계약 동사로 풀리는 문자열이어야 합니다(`""`은
`http.NewRequest`처럼 GET). 아니면 `methodDynamic`입니다. 메서드 식(`(*http.Client).Get(c, u)`)은 리시버를
인자 목록에서 뺍니다.

Go 래퍼 선언: 패키지 함수는 `owner` = import 경로, 메서드는 `owner` = `import경로.타입`(선언한 타입,
인터페이스 포함), `kind: "constructor"`는 struct 리터럴 `T{…}`·`&T{…}`이고 `owner` = `import경로.T`,
`name` = `T`입니다. 두 해석이 다 되는 `owner`(마지막 경로 원소에 점이 있는 `example.com/api.v2`)는 패키지
함수가 있으면 패키지 함수입니다. `index`는 호출 인자 위치(리시버 제외), `label`은 선언의 파라미터(생성자는
필드) 이름이고, `methodEnum`의 키는 상수 이름입니다(`api.Get` → `Get`). 선언한 래퍼 본문의 dynamic 요청은
내지 않고, `pathArg`를 바인딩하지 못한 호출은 `http-wrapper-unresolved:`로 셉니다. 모르는 필드나 잘못된
항목, 항목의 `service`와 다른 `--service`는 종료 코드 2입니다.

`symbol.usr`는 감싸는 선언의 정점 ID(`schema` relation-use와 같은 귀속)라 `impact --format
language-traversal --roots-from go-calls.json`이 호출 지점에서 호출자로 걸어갑니다. `location`은
호출식이 시작하는 자리입니다. dynamic 사실은 `channel: null`입니다(원문 식에 자격 증명이 있을 수
있음). userinfo·query·fragment는 떼고 고엔트로피·웹훅 세그먼트는 가립니다(`maskedSegments`).
`baseRef`는 풀지 못한 base를 담은 필드·패키지 변수의 정점 ID입니다(workspace `match.baseRefs`용).
호출 측 한계는 세어서만 냅니다: `route-call-coverage:`(로드 오류, 모델링하지 않은 클라이언트 import —
resty v1/v3, fasthttp, req, retryablehttp 등 — 와 URL 인자 없이 만든 요청: `http.Request` 리터럴, resty
`Send`), `unresolved-base-url:`, `ambiguous-base-join:`, `url-rewrite-interceptors:`(만든 요청의
`URL`·`Method` 대입), `http-wrapper-unresolved:`, `http-wrapper-undeclared:`(파라미터를 URL 머리나
동사로 넘기는 함수), `missing-route-usrs:`. `limitationScopes`는 내지 않습니다.

공유 `url-compose` 벡터(isthmus 3a45450)에서 이 생산자 대상 63건(그중 `producer:gartograph` Go 결합 22건)을
모두 통과합니다(`dio-concat`은 단순 연결로 실행 — 그 사례들은 dio의 `//`·점 세그먼트 처리를 쓰지 않음.
Spring·Rust·Python 사례는 다른 생산자 전용). `experiments/client-oracle`
(별도 모듈, `run.sh`가 proxy.golang.org에서 resty v2.17.2를 받음)은 합성 호출 32개를 127.0.0.1의
`httptest` 서버(`HTTP_PROXY`)로 보내 요청마다 동사·host·경로를 기록하고 사실과 대조합니다: 일치 30,
dynamic 2, 불일치 0. `source/clientoracle_test.go`가 그 기록을 resty 스텁으로 오프라인에서 다시 대조합니다.

isthmus는 3a45450(#133)부터 go `route-call`을 받습니다. 그 빌드로 `check`가 오라클 사실 32건을 모두
받아들이고, workspace `trace`가 `GET /users/{}`를 Go 서버 핸들러에서 Go 클라이언트 호출 지점과 그 호출자까지
잇습니다.

## MCP 서버

`gartograph mcp`는 수확한 문서를 MCP stdio(개행 구분 JSON-RPC)로 서빙합니다:
`gartograph_summary`, `gartograph_query`, `gartograph_impact`,
`gartograph_path`, `gartograph_cycles`, `gartograph_dead`,
`gartograph_rules`, `gartograph_metrics`, `gartograph_mapping`.
문서는 기동 시 한 번 수확해 모든 호출이 같은 스냅샷 위에서 답합니다.

```json
{"mcpServers": {"gartograph": {
  "command": "gartograph",
  "args": ["mcp", "--dir", "/path/to/repo", "--level", "symbol"]}}}
```

## 개발

```bash
go test ./...                    # 테스트
Scripts/coverage.sh              # 테스트 + 커버리지 게이트 90%
Scripts/verify-cli-contract.sh   # fixture 모듈에서 종료 코드 계약 검증
```

## 라이선스

MIT — [LICENSE](LICENSE).
