# HANDOFF

세션 이어받기용 상태 파일. 지금 어디까지 왔고 다음이 무엇인지만 적는다.

## 현재 상태 (2026-09-22)

**v0.2.0 릴리스·Homebrew tap 배포까지 완료.** 공개 리포
https://github.com/ictechgy/gartograph. `go vet`·`go test ./...` 통과,
커버리지 90.5%(게이트 90), `Scripts/verify-cli-contract.sh` 통과.
`brew upgrade`로 0.1.0→0.2.0 실측, `gartograph 0.2.0` 보고,
`brew test` 통과.

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
2. **isthmus 조인** — cgo/gomobile 브리지가 생기면 bridge facts producer.
3. **정밀도** — RTA/포인터 분석으로 CHA 오탐을 좁히는 것은 필요해질 때.
   dead의 "살아 있다" 편향이 계약이라 급하지 않다.

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
