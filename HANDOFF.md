# HANDOFF

세션 이어받기용 상태 파일이다. 지금 어디까지 왔고 다음이 무엇인지만 적는다. 작업 규칙은
[AGENTS.md](AGENTS.md), 이전 세션 원문(v0.2~v0.11 준비 기록·결정의 경위)은 [HANDOFF-HISTORY.md](HANDOFF-HISTORY.md)에
있다. 이력의 "진행 중"·"다음 할 일"·수치는 당시 기록이며 현재 지시가 아니다.

## 현재 상태 (2026-11-22)

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
- PR #57(2026-11-15, squash 머지 `b7e771d`):
  - `--retain-public` 기본 꺼짐이 의도임을 이력으로 확인하고 README·README.ko "출력 계약"(라이브러리 모듈 사용법),
    AGENTS의 dead 절, 아래 결정 기록에 남겼다. 동작은 그대로다.
  - 개요 도움말(`gartograph help`)이 명령이 받는 플래그를 빠짐없이 싣게 고쳤다 — `dead --algo`, 공통 `--tags`,
    `mcp --level/--config`, `routes --tags`, `reach --format`이 빠져 있었다. 회귀 테스트 `TestUsageListsEveryFlag`가
    명령별 `-h`와 개요를 대조한다(CHANGELOG Fixed).
  - 패턴 오류를 센다: `--pattern` 오타·없는 디렉터리·`go.mod` 없는 `--dir`·없는 replace 대상·읽을 수 없는 하위
    디렉터리면 go list가 돌려주는 패턴 오류 가짜 패키지를 `keep`이 버려 `cycles --strict`·`dead --strict`·MCP가
    limitation 없이 "0"으로 성공했다. 이제 모든 수확 문서가 `N package patterns reported errors from go list (first: …)`
    뒤에 본 것만 붙인다 — 다른 패키지가 하나도 없으면 `no packages were loaded, so nothing was harvested`, 있으면
    `packages they could not resolve were not analyzed`(같은 패턴이 실제 패키지도 고를 수 있다: replace 대상이 없는
    `example.com/...`, permission denied 하위 디렉터리가 있는 `./...`). 패턴이 고른 패키지가 없으면 `no packages
    matched the patterns (…)`. 종료 코드와 `--strict` 판정은 그대로다. `unused-deps`·`dead --algo rta`·`init`·ID
    명령(CLI·MCP)은 원인을 바로 말하고, `schema`·`routes`는 기존 isthmus 접두사로 센다(`routes`가 주 모듈 패키지를
    하나도 스캔하지 못하면 `route-coverage:`·`route-call-coverage: no package of the module was scanned …`).
    `init`은 성공해도 limitation을 stderr에 낸다.
  - `bridges`·`schema --generated-at`: routes·reach와 같은 문법·문구·종료 2. 영 시각은 모든 통로(reach 포함)에서
    사용법 오류다 — source가 영 시각을 "주지 않음"으로 읽기 때문이다.
  - stderr 오류 문구를 줄마다 textLine 규칙으로 거른다: 첫 줄 `error: …`, 나머지 줄은 두 칸 들여쓰기(yaml 오류 목록·
    go.mod 파싱 오류가 읽히는 모양으로 남고, 인자 속 개행 뒤 `::error::`는 들여쓴 줄에서도 무력화된다).
  - 독립 리뷰(r2) 지적 1–4·6–8·10–13을 반영했다. 5의 `; to add it:` 꼬리는 뗐고, 5의 경로 상대화와 9는 아래
    "남은 일"에 이유와 함께 남겼다.
  - 남은 설계 후보는 아래 "남은 일"에 있다. 두 번 돌린 출력은 이제 `bridges`·`schema`도 `--generated-at`을 주면 같다.

- 브랜치 `ccr-37904670-hxdjsx`(2026-11-22, main `b7e771d` 기준, 머지 전 — squash 머지되면 브랜치 커밋 해시는
  사라지므로 PR 번호로 찾는다). 자기 분석·실제 모듈(GOMODCACHE의 x/tools·x/mod·x/sync·x/text·x/crypto·x/net·
  x/telemetry·pgx·puddle·go-sql-driver/mysql·go-mssqldb·go-ora·modernc sqlite·libc, 스크래치 사본) 실행·두 번 실행
  비교에서 나온 것 중 사용자 결정이 필요 없는 넷을 고쳤다(CHANGELOG Fixed 첫 네 항목). 두 번 실행한 graph(세 레벨)·
  dead 출력은 x/mod·x/sync·x/text(dead는 x/tools도)에서 바이트가 같았고, x/mod·x/tools 심볼 문서에 유령 간선·중복
  정점·중복 간선이 없었으며, 실행한 명령(graph·cycles·dead·rta·query·impact·path·shared·routes·schema·bridges·
  unused-deps·metrics·init·mcp)에 크래시는 없었다.
  - 패턴 오류로 센 풀리지 않은 import를 `--deps`·심볼 레벨·unused-deps가 로드 오류로 다시 세던 것(아래 "남은 일"에
    있던 이중 계수). 정점 집합은 그대로다.
  - module 레벨이 로드 오류를 하나도 세지 않고 풀리지 않은 import를 `(builtin)`으로 세던 것. 오류 없는 문서는
    바이트가 같다(이 저장소와 실제 모듈 14개의 module·package 문서 전후 동일).
  - unused-deps가 의존 패키지의 빌드 제약 제외 파일 import를 보지 않아 tidy가 지키는 require를 미사용 indirect로
    오보하던 것(x/tools의 x/sys, modernc sqlite의 go-isatty·go-strftime — `go mod tidy -diff` 차이 없음). 같은
    코퍼스에서 판정이 바뀐 것 셋(모두 사용 쪽), limitation 수만 바뀐 것 넷, 나머지 여덟은 같다.
  - MCP `tools/list`의 일곱 도구가 `"required": null`(무효 JSON Schema)을 싣던 것 — ajv 6.15가 거부했다.
  - 검증: `go vet`·gofmt 통과, `Scripts/coverage.sh` 92.8%(기준 90%), `Scripts/verify-cli-contract.sh` OK, 자기 분석
    dead 12건(main과 같은 목록)·rules/cycles type·symbol `--strict` 0, conformance drift current. Windows·macOS 테스트는
    CI에서만 돈다. 독립 리뷰(GLM `packet-ask`)는 이 환경에 없어 실행하지 못했다.

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
- 자기 분석 `dead`는 `--strict` 없이 근거만 본다(CI도 같다). 2026-11-22 기준 12건(11-03·11-15와 같다) — 테스트에서만
  쓰는 공개 함수(`analysis.DependencyReachable` 등), `internal/testutil`, 직렬화 태그 필드다. 지우라는 뜻이 아니다.
  전부 exported라 `--retain-public`이면 0건이 된다(결정 기록).
  수가 늘면 테스트 전용 코드가 제품 파일에 들어온 것인지 먼저 본다(스코프 적용 오라클은 그래서
  `source/routescope_applies_test.go`에 있다). `rules --strict`·`cycles --level type/symbol --strict`는 0이다.

- 위 항목은 #55(`dcc4189`, 2026-11-04 squash 머지)로 main에 들어갔다. 독립 Claude 리뷰·재검증을 거쳤고
  (AGENTS.md의 GLM 리뷰는 `packet-ask`가 없는 환경이라 실행하지 못했다), 함수 안 인터페이스 식별 수정과 Windows
  순회 위치 수정(`filepath.IsLocal`)도 함께 들어갔다. 함수 안 `V`가 패키지 `V`를 가리던 거짓 간선으로만 살아 있던
  메서드는 이제 보고된다 — main 대비 보존이 좁아지는 유일한 경우이며 CHANGELOG에 밝혔다.

## 남은 일 (사용자가 고른다)

- PR #57 리뷰에서 미룬 것 둘(작지 않거나 보존 범위를 바꾼다):
  - 패턴 오류 `first:` 발췌의 기계별 경로(`--dir` 절대 경로, GOROOT): 그래프 문서가 이미 절대 `root`와 위치를 싣고
    있어 발췌만 상대화해도 문서가 기계 사이에서 같아지지 않고, go 명령 문구를 고쳐 쓰면 잘못 인용할 수 있다.
    바꾸려면 문서 전체의 경로 표기(root·positions)와 함께 정한다.
  - `--deps`에서 루트가 import하는 풀리지 않은 경로는 이름 없는 정점이 된다(main에서도 같은 기존 동작). 같은
    경로를 `--pattern`으로도 주면 두 번 세던 계수는 2026-11-22 브랜치에서 하나로 맞췄다(패턴 오류만 센다). 이름
    없는 정점 자체를 없애면 `--deps` 그래프의 정점 집합이 바뀌므로 반대 코퍼스 증거와 함께 별도로 다룬다. 같은
    패키지는 심볼 레벨에서 `1 dependency packages had no syntax or type information; …`(익명 인터페이스 limitation)로도
    세인다 — 다른 사실(그 의존의 익명 인터페이스를 못 봤다)이라 두었다.

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
- `http-limitation-scope`의 `scope.dynamic-*`: gartograph가 `dynamicScope`를 내지 않아 건너뛴다. GRAPH-EXCHANGE
  "dynamic 선언의 스코프"상 생산자가 상한을 **증명하면** 싣는 선택 필드이고(없으면 scope 전체 공백 — 하위 호환),
  생산자 의무는 "실으면 모든 요청을 포함해야 한다"뿐이다. kartograph·pythograph·rustograph도 내지 않는다(kartograph는
  검증기만 벡터로 고정), tsograph만 낸다. 내려면 프레임워크별 상한 증명(chi `Route` 접두사 등)과 isthmus 쪽 영향
  (스코프 밖 호출이 error가 된다) 증거가 필요한 기능 추가라 사용자가 고른다.
- `schema`가 SQL 게이트 없는 모든 문자열 리터럴을 동사 하나(대소문자 무시)로 SQL로 읽어 산문에서 거짓 relation-use를
  낸다: x/mod v0.41.0 `zip.go`의 `"create zip from directory"` → `directory`·`version`, x/tools v0.50.0은 사실 22개가
  전부 사용법·오류 문구에서 왔다(`"Mmap of %s for UUID update failed"` → `failed`, 그리고 거짓 `unjoined-dynamic-relations:
  3 SQL placeholder …` limitation). tsograph는 게이트 없는 리터럴에 대문자 동사만 받고 나머지를 skipped로 센다. 같은
  규칙을 쓰면 소문자 SQL 리터럴(Go에서 흔하다)의 참 사실도 빠지므로 반대 코퍼스와 isthmus dead-schema 영향 증거와
  함께 사용자가 고른다.
- unused-deps: pgx v5.11.0에서 `github.com/kr/pretty`·`gopkg.in/check.v1`을 미사용 indirect로 내지만 `go mod tidy
  -diff`는 둘을 지킨다(`go mod why -m`이 `gopkg.in/yaml.v3.test` 경로를 보인다). 의존 패키지 테스트만의 import는
  아니다 — 의존 모듈(go 1.27·go 1.12)의 테스트 파일만 import하는 require는 fixture에서 tidy가 지웠다. go 지시문이
  없는 yaml.v3 go.mod(가지치기 안 된 그래프)와 관련 있어 보이나 원인을 확정하지 못했다. 판정은 indirect라 `--strict`에
  들지 않는다.
- SARIF: cycles의 논리 위치 kind가 type·symbol 레벨 멤버에도 `module`이고, dead는 `func`·`var`처럼 SARIF 권장
  어휘(function·variable·member·type) 밖의 kind를 싣는다(권장 사항이라 무효는 아니다). 바꾸면 SARIF 소비자 출력이
  바뀐다.
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
