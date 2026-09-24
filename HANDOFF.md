# HANDOFF

세션 이어받기용 상태 파일. 지금 어디까지 왔고 다음이 무엇인지만 적는다.

## 현재 상태 (2026-09-24)

**v0.7.0 릴리스·Homebrew tap 배포 완료** — 경쟁 비교 잔여 5종
(dead exported 분류·keep 어노테이션·필드 dead·metrics A/D·limits)이
GLM 리뷰 수정을 포함해 main(origin)에 있다. `brew upgrade`
0.6.0→0.7.0 실측, `gartograph 0.7.0` 보고, `brew test` 통과.
tap은 여전히 수동 갱신(HOMEBREW_TAP_TOKEN 미설정, 체크섬은 릴리스
checksums.txt와 대조 후 `59fbe78`).

feature/dead-confidence-members(머지 `98b2e54`)가 구현한 것:
- `Finding.exported` — dead 보고가 심볼의 공개 여부를 싣는다. 비공개
  unreachable이 공개보다 triage 우선이라는 분류의 사실 재료(확신도
  문자열을 싣지 않는다 — 공개 심볼의 외부 호출 가능성은 해석이지 사실이
  아니므로). 텍스트 출력은 `(exported)` 접미.
- keep 어노테이션 — `//deadcode:keep`·`//gartograph:keep`이 붙은 선언
  (func/method/var/const/type + struct 필드 개별)이 문서 `roots`의
  보존 루트로 수확된다. 주의: `CommentGroup.Text()`는 지시문 형태 주석을
  빼므로 원문 `c.Text`를 훑어야 한다(`keepMarked`).
- 멤버 레벨 dead — struct 필드가 심볼 레벨 정점(kind `field`,
  ID `pkg.(T).F`). `refObject`가 패키지 레벨 필터 전에 `fieldIDs` 맵을
  본다. 참조 수확 경로: 셀렉터(x.F)·키 리터럴(T{F:v})은 go/types가
  해석하고, 위치 리터럴(T{a,b})·승격 경유(Selections.Index의 중간
  임베드 필드)·`==`/`!=` 구조체 비교는 수확기가 직접 전개한다 —
  과대 근사는 "살아 있다" 쪽. 필드는 나가는 간선이 없어서 들어오는
  references만으로 도달이 결정된다. 필드 보고가 나오면 reflection·
  직렬화·통째 복사가 안 보인다는 limitation이 실린다.
- metrics A/D — `abstractness`(컴포넌트 안 인터페이스 타입 비율,
  `Vertex.Interface`)와 `distance`(|A+I−1|)를 ComponentMetric에 추가
  (둘 다 *float64 — 타입 없는 단위·I 미정의 고립은 정의 불가로 키 생략).
  metrics 명령은 이제 type 레벨로 수확해 A의 분모를 확보한다 — 패키지
  레벨 저장 문서는 A/D를 빼고 limitation으로 알린다.
- `limits:` 규칙 — `{component, maxIn?, maxOut?}`(0 유효, 둘 중 하나 필수,
  음수·미정의 컴포넌트는 Load 거부). 상한 초과는 간선이 아니라
  컴포넌트 사실이라 Violation의 From/To는 비우고 `rule:"limit"`+
  `name:"maxIn"|"maxOut"`+실제 수치 사유. baseline 키는 컴포넌트+종류만
  (수치는 들어가지 않는다 — 3→5로 변해도 같은 위반). degree 계산은
  stability와 같은 `componentDegrees`를 공유한다.

자기 분석 실측: `dead`가 `config.(File).Version`(yaml만 씀)·
`cli.(rpcRequest).JSONRPC`(JSON marshal만 씀)를 field finding으로 올바르게
잡았다 — reflection blind spot limitation이 그대로 설명이 된다.
`rules --strict`·`cycles --level symbol --strict`는 여전히 clean.

GLM 리뷰(packet-ask, diff 기준)가 잡아서 반영된 수정:
- 위치 리터럴·== 비교가 중첩 struct·배열 원소의 잎 필드까지
  worklist로 재귀 마킹(자기 호출 재귀는 심볼 그래프 자기 순환으로
  새어 cycles 자기 분석을 오염시켜 worklist로 썼다).
- 제네릭 인스턴스 경유 필드는 `Var.Origin()`으로 origin 정점 해석.
- keep 표지는 주석 줄의 첫 토큰일 때만 — 인용 문장은 루트 불가.
- 임베드 필드의 keep은 AST↔types 인덱스 대응으로 잡는다(Names 부재).
- `Finding.Exported`는 omitempty 없이 항상 출력 — false와 "옛 형식
  문서"를 소비자가 구분할 수 있어야 한다.
- distance는 미반올림 A·I로 계산해 한 번만 반올림.
- 같은 컴포넌트의 중복 상한 limits 항목은 baseline 키 충돌이라 Load 거부.

v0.5.0이 들고 있던 것(이번 릴리스에도 포함):
- `fileRules` — dep-cruiser not-to-dev-dep 계약. `{name, from, to, reason}`.
  `from`은 `/` 없으면 파일명·있으면 모듈 상대 경로 글롭, `!` 접두사 반전
  ("!*_test.go" = 프로덕션 파일의 import 금지). 위반은 `rule:"fileScope"`+
  `name`+실제 지점. 지점 없는 대상 간선은 `fileScopeUnchecked`로 셈.
  이 레포 자체에 `testutil-only-in-tests` 규칙으로 도그푸딩.
- `cycles`/`dead`에 `--format sarif`와 `--baseline`/`--write-baseline`.
  순환은 `dependency-cycle` error, unreachable은 `unreachable-symbol`
  warning(사실이지 판정 아님). baseline 파일은 kind별 구분
  (`cycles-baseline`/`dead-baseline` — 엇갈려 적용 불가).
  `SplitBaseline`이 제네릭이 됐고 `ViolationBaselineKey`·`CycleBaselineKey`·
  `FindingBaselineKey`가 동일성을 정의한다.
- `cycles --format json`이 이제 봉투다 — `{cycles, baselined,
  staleBaseline, limitations}`(기존 `[]Cycle` 대신 — 0.x 계약 변경).
- `diff` 심화 — 공개 심볼 kind 변경·공개→비공개 전환·인터페이스 메서드
  추가·struct 필드 계약 파괴를 breaking으로. 정점에 `interface`/`fields`
  (선언 순서 "name:Type" 목록) 추가 — omitempty라 기존 문서 호환,
  Version은 2 유지. 인터페이스 메서드 추가는 양쪽 문서에 있는 인터페이스에만
  breaking(새 인터페이스는 신규 API). 필드 규칙은 apidiff 판정:
  공개 필드 제거·재형 = 항상 breaking, 전원 공개 struct의 추가·순서 =
  unkeyed literal 파괴로 breaking, 비공개 섞인 struct의 추가 = 호환.
- baseline 버그 수정 — independence 위반을 forbidden처럼 컴포넌트 쌍으로
  식별(목격 경로 끝점이 From/To라 경로가 바뀌면 새 위반으로 오인됐다).

feature/backlog-round에서 잔여 격차를 전부 구현했다:
- `shared <a> <b>` — 두 루트(이상)의 공통 도달 집합 교집합 + 루트별
  고유분(`only`). 루트 자신도 only에 포함 — 도달성 의미론 그대로.
  MCP 도구 `gartograph_shared`도 추가.
- `--goos`/`--goarch` — 로더 환경을 덮어 다른 타깃의 조건부 파일을 수확.
  덮어쓰면 limitations에 선택 사실이 남는다.
- `exclude:` 설정 — 수확 시점 필터. 주 모듈은 상대 경로, 외부는 전체
  경로로 매칭(graph.MatchPath 공용 의미론). 제외 패키지는 정점이 되지
  않고 그쪽 import는 limitations에 셈. 잘못된 글롭·빈 패턴은 Load 거부.
- `stability: true` — dep-cruiser moreUnstable 계약. I=Ce/(Ca+Ce)가
  target > source인 import 간선을 `rule:"stability"`로 보고 — 사유에 두
  불안정도 수치. metrics와 동일한 in/out 차수 의미론.
- `unused-deps` — go.mod의 require 중 로드된 패키지(테스트 포함, tidy와
  같은 기준)가 어느 모듈에도 속하지 않는 것을 보고. direct/indirect 분리,
  strict는 direct 미사용에만 1. --graph는 거부(사실은 go.mod에서 온다).
- RTA `--explain` — `--algo rta`와 함께면 RTA 콜그래프 인접 맵 위에서
  경로를 설명한다("no path ... (rta call graph)"). `analysis.Explain`이
  `ExplainAdjacency`로 일반화됐다 — 문서든 콜그래프든 경로 알고리즘은 하나.
- 상수 값 — const 정점이 `value`(Go 리터럴 형태)를 싣고, diff가 공개
  상수의 값 변경을 breaking으로 분류(상수는 소비자 코드에 인라인됨).
  어느 쪽이든 값이 비어 있으면(옛 형식 문서) "몰랐다"로 건너뜀.

남은 것: `HOMEBREW_TAP_TOKEN` 시크릿 등록(사용자 몫), 포인터 분석
(RTA 정밀도가 부족해질 때까지 보류 — 기존 판단 유지).

isthmus 쪽: `platform "go"` 계약이 PR #108로 isthmus main에 머지됐다
(`8ae3bb6`, GLM 리뷰 지적 반영 — go 문서는 target null만 허용,
사실 종류 전면 거부, 구성 요건 미충족, 수신 공백 완화 미적용을
테스트로 고정).

feature/graph-v2 커밋:
- `8cd1f1e` — 스키마 v2. `Edge.Positions`가 그 관계의 모든 사용 지점을
  담는다(import 선언·call 식·signature 타입 식). 간선 정체성은 여전히
  (from,to,kind) — Sort()가 위치를 합치고 결정적 정렬. `contains`/
  `implements`/모듈 간선은 위치 없음. `Violation.Position`이 SARIF
  physicalLocation으로 흐른다.
- `03bee93` — `--tests` 변형(`p [p.test]`·`p.test`)이 별도 import
  정점을 만들어 원 패키지가 자기 변형을 import하는 가짜 순환이 생기던
  것을 PkgPath dedup으로 수정. 변형 import는 원 패키지 간선에 합친다.
- `ecec419` — `independent: [a, b]` 계약 — 목록 내 쌍의 양방향 전이
  도달을 금지. rule:"independence", 위반에 목격 경로.
- `87273b6` — `dead --algo cha|rta`. rta는 SSA 기반(x/tools callgraph/rta,
  InstantiateGenerics)으로 호출 도달성을 좁힌다: 이 레포 실측 CHA 10건 →
  RTA 3건. `rta`는 소스 수확이 필요해 `--graph`와 공존 시 종료코드 2.
  리포트에 `algorithm` 필드, RTA finding은 `ReasonRTA`, 과소근사 limitation.
  func/method는 RTA로, var/const/type은 그래프 도달성으로 판정.
  `--explain`은 항상 수확 그래프(CHA) 경로를 보여준다고 도움말에 명시.

feature/parity-gaps가 추가한 것(비교 대상: goda·go-arch-lint·deadcode·
depguard·dependency-cruiser·import-linter·apidiff):
- `path <from> <to>` — 임의 두 정점 간 최단 의존 경로(deadcode -whylive의
  일반화). found:false는 그래프 사실, 정점 부재는 ErrNotFound.
- `diff <old.json> <new.json>` — 저장 문서 비교. 정점·간선 차이 +
  exported 시그니처 변경을 SignatureChanges로, exported 제거·시그니처
  참조 제거를 `breaking`으로 모음. `--strict`는 breaking에 1.
- `impact --since <rev>`/`--files F` — git diff(`--relative`) 또는 명시
  목록으로 바뀐 .go 파일을 정점(선언 위치 + 패키지 디렉터리)으로 해석해
  합집합 역방향 클로저. 비-.go 파일은 unmappedFiles.
- `rules --baseline/--write-baseline` — 알려진 위반을 합법화.
  fresh 위반만 violations/strict/SARIF에 나오고, 사라진 항목은
  staleBaseline. 파일 버전ed.
- 외부/vendor 규칙 — 컴포넌트 패턴이 --deps 수확 외부 패키지의 전체
  경로를 매칭(`aws: ["github.com/aws/**"]` + deps/deny).
  `unmappedExternal`(외부 미매핑)·`unmatchedComponents`(0 정점 매칭 —
  --deps 누락·오타 신호) 추가. CheckRules는 이제 *RuleReport 반환.
- `graph --format dot` — Graphviz 출력. MCP 도구 `gartograph_path` 추가
  (도구 7개).

같은 브랜치의 두 번째 패스(규칙 표현력 + 메트릭):
- `common: [c]` — 모든 컴포넌트가 deps에 적지 않아도 의존 가능한 공통 목록.
- `visibleTo` — 공급자 측 규칙("누가 나를 쓸 수 있나"). deps의 거울,
  좁히기만 한다. rule:"visibleTo".
- `forbidden: [{from, to}]` — 간접 도달 금지(import-linter 계약).
  위반에 목격 경로 `path`를 싣고 Kind는 비운다.
- `deny` 항목이 `{to, reason}` 맵 형태도 받는다 — reason이 위반에 실린다.
- `Load`가 규칙의 컴포넌트 참조를 검증한다 — 미정의 이름·forbidden
  자기자신 쌍은 설정 오류. Violation에 Path가 생겨 baseline 동일성은
  baselineKey 함수가 정한다(forbidden은 컴포넌트 쌍만).
- `metrics` — 컴포넌트(설정 없으면 패키지) 단위 Ca/Ce/불안정성 +
  orphan(내부 임포터 0, main·보존 루트 제외). --strict 없음, 사실 보고.
- `mapping` — 컴포넌트→패키지 매핑 + unmapped·unmatched 보기.
- `init` — 관찰된 import를 deps로 옮긴 .gartograph.yml 생성.
  생성 즉시 rules 통과가 계약. 이미 있으면 거부.
- MCP 도구 `gartograph_metrics`·`gartograph_mapping` 추가(도구 9개).

v0.2.0이 추가한 것(타 도구 비교 패스, PR #6·#7):
- `impact <id>` 역방향 전이 클로저, `rules --format sarif` (SARIF 2.1.0),
  `mcp` MCP stdio 서버(도구 6종, 기동 시 문서 1회 수확).
- rules에 `deny`(deps보다 우선)·`signature`(exported 시그니처의 타입 누출 —
  `signature` 간선 수확) 규칙, 위반에 `rule` 필드.
- `--tags` 빌드 태그 + 제약으로 빠진 파일 limitations 계수,
  `// Code generated` 정점에 `generated: true`.
- fix: `objectID`가 `Pkg()==nil`인 universe 객체에서 패닉하던 것,
  `--tests`가 Test/Benchmark/Example/Fuzz 진입점을 보존 루트로 안 잡던 것.

릴리스는 `vX.Y.Z` 태그 push → `.github/workflows/release.yml`이 5개 타깃을
크로스컴파일해 릴리스를 만들고, `HOMEBREW_TAP_TOKEN` 시크릿이 있으면
`ictechgy/homebrew-tap`까지 갱신한다(현재 미설정 — 0.1.0·0.2.0 탭 갱신 모두
수동. formula 원본은 `Formula/gartograph.rb`). 릴리스 워크플로우의
교훈 두 개: `run:` 블록 스칼라 안의 heredoc은 열 0에 쓰면 YAML이 파싱
실패하고, `(cd dist && zip)`의 산출물은 dist 안에 생기니 `../`로 빼야 한다.

- `graph` — 순수 도메인. `Document` v1 + `Module`·`Roots`, `Level`(module/package/
  type/symbol), `Vertex`(kind/name/package/position/exported)·`Edge`,
  `Sort`, `Adjacency`/`Incoming`/`EdgeKinds`, `View`(레벨별 투영), `Symbols`.
- `source` — 유일한 x/tools 소비자. `Load`가 `Options.Level`로 수확 깊이 결정.
  package는 import만, type은 타입+구조 간선, symbol은 함수·메서드·var·const와
  call/references까지. `symbols.go`가 수확기: `objectID`(`pkg.Name`/
  `pkg.(Recv).Name`), 인터페이스 호출은 CHA 팬아웃, 승격 메서드는 선언 타입
  아래로 귀속. reflect/linkname/외부참조/무타입 패키지를 실측 limitation으로.
  `module.go`는 패키지의 `Module` 소속을 모아 모듈 정점+크로스 모듈 간선.
- `analysis` — `Cycles`(Tarjan), `Query`(양방향 BFS), `Impact`(역방향 BFS),
  `Dead`/`RetentionRoots`/`Reachable`/`Explain`(도달성 — state+reason,
  삭제 판정 아님), `CheckRules`(allow/deny/signature 규칙, unmapped 보고).
- `config` — 유일한 yaml.v3 소비자. `.gartograph.yml` 읽기, 컴포넌트 패턴
  매칭(exact/`x/**`/세그먼트 글롭, 긴 패턴 우선), `deps` 허용 목록,
  `deny` 금지 목록, `signature` API 누출 규칙.
- `export` — `JSON`/`Mermaid` + `SaveFile`/`LoadFile`(버전ed 영속 문서,
  미래 버전 거부).
- `cli` — `graph`/`cycles`/`dead`/`rules`/`query`/`impact`/`mcp`/`version`.
  `--level`, `--graph`, `--out`, `--retain-public`, `--root`, `--explain`,
  `--config`, `--strict`, `--tags`, `rules --format sarif`. `sarif.go`가
  SARIF 2.1.0 직렬화, `mcp.go`가 stdio NDJSON JSON-RPC 서버.
  종료 코드 0/1/2.
- 이 저장소 자체의 `.gartograph.yml`이 계층 규칙 정본
  (cmd→cli→{analysis,source,config,export}→graph).

## 자기 분석에서 확인된 것

- 심볼 레벨: 156 정점/670 간선, 루트 `cmd/gartograph.main` 인식.
- `dead`가 `flag.Value`·`error` 만족 메서드를 unreachable로 보고 — 외부
  인터페이스 디스패치는 그래프의 blind spot. 보고에 limitation으로 명시됨.
- `KindModule`은 미래 예약 상수로 unreachable이었는데 모듈 레벨 구현으로
  실제 사용처가 생겼다 — dead 보고가 구현 진척을 따라 움직인다.
- `rules --strict` 0 위반 — 계층이 파일로 강제되기 시작했다.

## 알려진 한계

- 인터페이스 디스패치는 모듈 내부만 추적 — 외부 인터페이스 만족 메서드는
  unreachable로 보고될 수 있다(limitation으로 명시).
- 외부 심볼 참조는 개수만 센다 — 외부 정점을 만들지 않는다는 계약.
- 모듈 레벨은 단일 모듈 저장소에서 정점 하나가 정상 결과다 — go.work
  워크스페이스나 `--deps`에서만 간선이 생긴다.

## 다음 할 일 (우선순위 순)

1. ~~배포~~ — v0.2.0 릴리스 + tap formula 배포 완료. 다음 릴리스 전에
   `HOMEBREW_TAP_TOKEN`을 리포 시크릿에 넣으면 탭 갱신이 자동화된다.
2. ~~v0.3.0 릴리스~~ — push + 태그 + tap 갱신 + brew upgrade 실측 완료.
   다음 릴리스 전 HOMEBREW_TAP_TOKEN 시크릿 등록하면 탭 자동화.
3. ~~isthmus 조인~~ — 완료. `bridges` 명령이 bridge-facts v1 문서를
   내고(platform "go", cgo는 unscanned-ffi-interop limitation),
   isthmus 소비자 계약도 PR #108로 머지됐다.
4. ~~정밀도~~ — `dead --algo rta` 구현됨(opt-in). 포인터 분석(Andersen)은
   RTA가 부족해질 때.

## 결정 기록

- **이름**: gograph는 같은 니치 도구 2개가 선점해 `gartograph`로.
- **CHA 팬아웃**: RTA 대신 — SSA/포인터 분석 없이도 "살아 있다" 쪽으로만
  기우는 과대 근사가 dead 오탐을 막는다. 정밀도가 필요해지면 그때 RTA 검토.
- **rules의 deps는 허용 목록**: 항목 없는 컴포넌트는 자기 외 의존 불가 —
  누락이 "허용"으로 새지 않게.
- **메서드 정점은 선언 타입 아래**: `pkg.(Embedded).M`이 진짜 선언 — 승격
  메서드를 임베딩 타입 아래 두면 같은 선언이 둘이 된다.
- **`--graph` 저장 문서 입력**: 재수확 없이 질의만 돌리는 경로 —
  Go에는 index store가 없어 이 문서가 영속 산출물이다.
- **파일→정점 해석은 .go만**: 비-Go 파일을 패키지 정점에 매핑하면
  문서 변경이 코드 영향으로 둔갑한다 — unmappedFiles로 보고.
- **vendor 규칙은 새 키 없이**: `vendors:` 섹션을 따로 두지 않고
  components의 외부 경로 패턴 매칭으로 — 스키마 확장 없이 deps/deny가
  그대로 외부 의존을 통제한다.
- **MCP는 cli 패키지 안 파일(`cli/mcp.go`)**: 별도 패키지로 빼면
  cli→mcp 디스패치와 mcp→cli 인자 파서 공유가 패키지 순환이 된다 —
  rustograph v0.2.0에서 같은 구조가 `cycles --strict`에 잡혀 파서를
  최하층으로 내리는 패치(0.2.1)가 됐다. 여기서는 한 패키지라 문제 없다.
