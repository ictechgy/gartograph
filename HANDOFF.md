# HANDOFF

세션 이어받기용 상태 파일. 지금 어디까지 왔고 다음이 무엇인지만 적는다.

## 현재 상태 (2026-09-22)

**네 레벨(module/package/type/symbol) + dead + rules + 영속 문서 + 검증 체계 완성.**
브랜치 `feature/symbol-type-level`. `go vet`·`go test ./...` 통과,
커버리지 90.1%(게이트 90), `Scripts/verify-cli-contract.sh` 통과,
자기 분석(rules/cycles×2/dead) 정상. `go install ./cmd/gartograph`로
설치된 바이너리까지 검증됨. 리모트가 없어 push·Homebrew tap·GitHub
릴리스는 미착수 — 리모트 생성 후 가능.

- `graph` — 순수 도메인. `Document` v1 + `Module`·`Roots`, `Level`(module/package/
  type/symbol), `Vertex`(kind/name/package/position/exported)·`Edge`,
  `Sort`, `Adjacency`/`Incoming`/`EdgeKinds`, `View`(레벨별 투영), `Symbols`.
- `source` — 유일한 x/tools 소비자. `Load`가 `Options.Level`로 수확 깊이 결정.
  package는 import만, type은 타입+구조 간선, symbol은 함수·메서드·var·const와
  call/references까지. `symbols.go`가 수확기: `objectID`(`pkg.Name`/
  `pkg.(Recv).Name`), 인터페이스 호출은 CHA 팬아웃, 승격 메서드는 선언 타입
  아래로 귀속. reflect/linkname/외부참조/무타입 패키지를 실측 limitation으로.
  `module.go`는 패키지의 `Module` 소속을 모아 모듈 정점+크로스 모듈 간선.
- `analysis` — `Cycles`(Tarjan), `Query`(양방향 BFS), `Dead`/`RetentionRoots`/
  `Reachable`/`Explain`(도달성 — state+reason, 삭제 판정 아님),
  `CheckRules`(컴포넌트 규칙, unmapped 보고).
- `config` — 유일한 yaml.v3 소비자. `.gartograph.yml` 읽기, 컴포넌트 패턴
  매칭(exact/`x/**`/세그먼트 글롭, 긴 패턴 우선), `deps` 허용 목록.
- `export` — `JSON`/`Mermaid` + `SaveFile`/`LoadFile`(버전ed 영속 문서,
  미래 버전 거부).
- `cli` — `graph`/`cycles`/`dead`/`rules`/`query`/`version`. `--level`,
  `--graph`(저장 문서 입력), `--out`, `--retain-public`, `--root`,
  `--explain`, `--config`, `--strict`. 종료 코드 0/1/2.
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

1. **배포** — `go install` 경로 검증, Homebrew tap(계열 저장소 방식 참고).
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
