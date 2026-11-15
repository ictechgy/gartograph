# HANDOFF

세션 이어받기용 상태 파일이다. 지금 어디까지 왔고 다음이 무엇인지만 적는다. 작업 규칙은
[AGENTS.md](AGENTS.md), 이전 세션 원문(v0.2~v0.11 준비 기록·결정의 경위)은 [HANDOFF-HISTORY.md](HANDOFF-HISTORY.md)에
있다. 이력의 "진행 중"·"다음 할 일"·수치는 당시 기록이며 현재 지시가 아니다.

## 현재 상태 (2026-11-15)

- 최신 발행본은 **v0.11.0**(태그 = main `aa69cc7`, GitHub Release 자산 6개). Homebrew tap은 `HOMEBREW_TAP_TOKEN`이
  없어 수동으로 0.11.0에 맞췄다 — 절차는 [RELEASING.md](RELEASING.md)의 "Homebrew tap 수동 갱신".
- isthmus `compatibility.json`(isthmus 0.14.0)의 gartograph 핀은 0.9.1이다. 핀 갱신은 isthmus 저장소의 공식 설치·
  cold-cache 검증 절차를 따른다(이 저장소 작업이 아니다).
- `main` 위의 미발행 변경은 CHANGELOG `[Unreleased]`에 있다. 2026-11-03 계열 검토에서 나온 항목을 반영했다:
  - 모듈 안 이름 없는 인터페이스(단언·type switch 리터럴·함수 안 인터페이스·이름 없는 제약) 호출의 CHA 팬아웃과
    문서 표시 `moduleLocalDispatch`(없는 옛 문서는 dead가 재수확을 권한다).
  - `//go:wasmexport` 루트, `//go:linkname`은 본문 없는 선언 → 본문 한 방향 후보 간선으로 잇고 본문 쪽을 루트로
    (본문은 패키지의 모든 파일에서 찾는다) — 잇지 못한
    지시문만 limitation으로 센다.
  - func main 없는 main 패키지의 루트 부재 안내, 빌드 제약 안내에 `--goos/--goarch`.
  - isthmus 적합성 벡터 재벤더링(`c76e105`, `http-limitation-scope`·`sql-relations` 추가, 생산자 사례 155건).
    소비자가 거부할 limitationScopes는 문서에 싣지 않는다.
  - CI: gofmt, CLI 계약, Windows·macOS 테스트. Release: tap 미갱신 경고·실행 요약.
- 2026-11-15 브랜치 `ccr-37904670-hxdjsx`(main `1eb292b` 기준, push·PR 전):
  - `--retain-public` 기본 꺼짐이 의도임을 이력으로 확인하고 README·README.ko "출력 계약"(라이브러리 모듈 사용법),
    AGENTS의 dead 절, 아래 결정 기록에 남겼다(`240c2f8`). 동작은 그대로다.
  - 개요 도움말(`gartograph help`)이 명령이 받는 플래그를 빠짐없이 싣게 고쳤다 — `dead --algo`, 공통 `--tags`,
    `mcp --level/--config`, `routes --tags`, `reach --format`이 빠져 있었다. 회귀 테스트 `TestUsageListsEveryFlag`가
    명령별 `-h`와 개요를 대조한다(`c36e25f`, CHANGELOG Fixed).
  - 빈 수확을 센다(`fbfa500`): `--pattern` 오타·없는 디렉터리·`go.mod` 없는 `--dir`이면 go list가 돌려주는 패턴 오류
    가짜 패키지를 `keep`이 버려 `cycles --strict`·`dead --strict`·MCP가 limitation 없이 "0"으로 성공했다. 이제 모든 수확
    문서가 `N package patterns reported load errors and loaded no packages (first: …)`(패턴이 고른 패키지가 없으면
    `no packages matched the patterns (…)`)를 싣고, 정점이 없으면 `(use --retain-public)` 안내를 붙이지 않는다. 종료
    코드와 `--strict` 판정은 그대로다(limitation은 판정에 들어가지 않는다). 원래 2로 거부하던 `unused-deps`·`dead --algo
    rta`·`init`·ID 명령은 원인을 바로 말하고, `schema`·`routes`는 기존 isthmus 접두사의 로드 오류 수로 센다.
  - `bridges`·`schema --generated-at`(`5f89c0c`): routes·reach와 같은 문법·문구·종료 2. 같은 입력과 값이면 같은
    바이트다.
  - stderr 오류 문구를 textLine 규칙으로 거른다(`419698f`): 경로·ID·플래그 이름·저장 문서 값의 개행·ESC가 워크플로
    명령·터미널 시퀀스가 되던 문제다. 오류 하나는 한 줄이다(가운데 줄바꿈은 `\x0a`).
  - 남은 설계 후보는 아래 "남은 일"에 있다. 두 번 돌린 출력은 이제 `bridges`·`schema`도 `--generated-at`을 주면 같다.

## 검증 방법

```sh
go vet ./... && test -z "$(gofmt -l .)"
Scripts/coverage.sh                       # 기준 90%
Scripts/verify-cli-contract.sh            # 새로 빌드한 바이너리로 0/1/2 계약
go run ./cmd/gartograph dead              # 자기 분석 — 보고 수와 limitation을 함께 본다
go run ./cmd/gartograph rules --strict
go run ./cmd/gartograph cycles --level type --strict
go run ./cmd/gartograph cycles --level symbol --strict
node ../isthmus/scripts/conformance-drift.mjs gartograph=.   # 벤더링 사본 드리프트
```

- `go.mod`가 Go 1.27.1을 요구한다. 오래된 go가 PATH에 있으면 `GOTOOLCHAIN=auto`가 받아 오지만,
  `verify-cli-contract.sh`가 띄우는 바이너리는 PATH의 go로 `go list`를 부르므로 1.27.1을 PATH 앞에 둔다
  (`PATH="$(go env GOROOT)/bin:$PATH"`).
- 자기 분석 `dead`는 `--strict` 없이 근거만 본다(CI도 같다). 2026-11-15 기준 12건(11-03과 같다) — 테스트에서만
  쓰는 공개 함수(`analysis.DependencyReachable` 등), `internal/testutil`, 직렬화 태그 필드다. 지우라는 뜻이 아니다.
  전부 exported라 `--retain-public`이면 0건이 된다(결정 기록).
  수가 늘면 테스트 전용 코드가 제품 파일에 들어온 것인지 먼저 본다(스코프 적용 오라클은 그래서
  `source/routescope_applies_test.go`에 있다). `rules --strict`·`cycles --level type/symbol --strict`는 0이다.

- 위 항목은 #55(`dcc4189`, 2026-11-04 squash 머지)로 main에 들어갔다. 독립 Claude 리뷰·재검증을 거쳤고
  (AGENTS.md의 GLM 리뷰는 `packet-ask`가 없는 환경이라 실행하지 못했다), 함수 안 인터페이스 식별 수정과 Windows
  순회 위치 수정(`filepath.IsLocal`)도 함께 들어갔다. 함수 안 `V`가 패키지 `V`를 가리던 거짓 간선으로만 살아 있던
  메서드는 이제 보고된다 — main 대비 보존이 좁아지는 유일한 경우이며 CHANGELOG에 밝혔다.

## 남은 일 (사용자가 고른다)

- 빈 수확을 종료 2로 거부할지: 지금은 limitation으로 세고 종료 코드는 그대로다(`cycles --strict`·`dead --strict`는
  빈 그래프에서도 0). CI가 패턴 오타를 실패로 잡게 하려면 "주 모듈 패키지가 하나도 없으면 2"를 더할 수 있다 —
  종료 코드 계약 변경이다.
- 루트 부재 안내 기준: 지금은 정점은 있는데 심볼 루트가 하나도 없을 때만 나와, `init`·함수를 부르는 초기화식·keep·지시문 루트가
  있거나 `--tests`인 라이브러리는 공개 API 전체가 안내 없이 보고된다(README에 적었다). rustograph처럼 루트 없는
  패키지의 공개 심볼 수를 세는 limitation으로 바꿀지 고른다 — 보고가 늘어나는 변경이다.
- `--retain-public`은 `internal/`·`main` 패키지의 공개 심볼도 루트로 삼는다. 중첩 모듈은 `internal/`을 import할 수
  있고 플러그인 호스트는 `main` 심볼을 이름으로 찾으므로 좁히려면 반대 코퍼스 증거가 먼저다(지금은 바꿀 근거 없음).
- graph 문서의 빈 `vertices`/`edges`가 `null`이다(단일 패키지 모듈의 package 레벨이 흔한 경우). #27이 보고서는
  `[]`로 바꾸면서 `export`(graph 문서)는 범위 밖으로 뒀다. 바꾸면 그런 문서와 순회 `graphRevision`이 달라진다.
- `http-limitation-scope`의 `scope.dynamic-*`: gartograph가 `dynamicScope`를 내지 않아 건너뛴다.
- 릴리스(0.12.0)와 tap 갱신은 승인된 별도 작업이다. 새 자격 증명은 만들지 않는다.

## 주의

- linkname 루트는 "살린다" 쪽 과대 근사다 — 본문 없는 쪽이 죽었어도 묶인 구현은 루트다(RTA가 본문 없는
  선언에서 멈추기 때문). 좁히려면 RTA에 linkname 간선 전파를 넣어야 한다.
- 모듈 안 이름 없는 인터페이스 팬아웃은 단언한 메서드 집합을 만족하는 **모든** 모듈 구체 타입으로 퍼진다(CHA).
  RTA는 인터페이스로 변환된 타입의 메서드를 따로 살리므로 대조군 결과가 CHA와 다를 수 있다.

## 결정 기록

이전 결정(이름, CHA 팬아웃, deps 허용 목록, MCP 배치 등)은 [HANDOFF-HISTORY.md](HANDOFF-HISTORY.md)의 "결정 기록"에
있다.

- **`dead --retain-public`은 기본 꺼짐이 의도다**(2026-11-15 확인, 동작 변경 없음). dartograph(기본 보존)와 다른 점을
  실수로 볼 근거는 없었다.
  - 처음부터 옵트인이었다: 초기 스캐폴드 `49b8e0c`의 로드맵이 "exported API behind `retain_public`", 첫 `dead` 구현
    `8daa730`이 기본 false·도움말 "use for libraries without main"·루트 없는 보고의 `(use --retain-public)`
    limitation을 함께 넣었다. `2a6964f`는 공개 심볼을 루트로 삼는 대신 finding에 `exported` 사실을 실어 분류를
    소비자에게 맡겼고, #49(`fd3a336`)는 MCP 안내를 CLI와 같은 바이트로, #55(`dcc4189`)는 안내 판정을 고쳤다. #53 본문은
    태그 필드 보존을 "`--retain-public`처럼 옵트인 루트로" 붙일 수 있다고 적었다. 기본값을 바꾸자는 커밋·PR은 없다
    (GitHub에는 이슈가 없고, PR 본문은 플래그·안내만 언급한다).
  - Go는 패키지 사이 호출에 공개 이름이 필요해, 응용 모듈의 공개 심볼 대부분이 같은 모듈의 다른 패키지용이다. 이
    저장소(`1eb292b`) 자기 분석: 기본 12건(전부 exported, 루트 3) → `--retain-public` 0건(루트 663). 기본 보존이었다면
    이 저장소의 보고는 하나도 남지 않는다.
  - Go에는 Dart의 `lib/<package>.dart` 같은 공개 API 선언 파일이 없어 dartograph의 기본 보존 근거가 성립하지 않는다.
    cartograph(`retain_public: false # turn on for libraries`)·rustograph(`--retain-public`)도 기본 꺼짐이다.
  - 라이브러리 사용자 안내는 README "Output contract"·README.ko "출력 계약"에 있다. 바꾸려면 모든 사용자의 보고가
    바뀌므로 CONTRIBUTING의 증거(영향받는 소비자, 오탐·누락 양쪽)가 필요하다.
