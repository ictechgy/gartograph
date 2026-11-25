# HANDOFF

세션 이어받기용 상태 파일이다. 지금 어디까지 왔고 다음이 무엇인지만 적는다. 작업 규칙은
[AGENTS.md](AGENTS.md), 이전 세션 원문(v0.2~v0.11 준비 기록·결정의 경위)은 [HANDOFF-HISTORY.md](HANDOFF-HISTORY.md)에
있다. 이력의 "진행 중"·"다음 할 일"·수치는 당시 기록이며 현재 지시가 아니다.

## 현재 상태 (2026-11-25)

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

- PR #58(2026-11-22, squash 머지 `25b1f25`)과 #59(`345dfe4`, go.work 판정 결정 기록). 아래 해시는 머지 전 브랜치
  커밋이라 main에 없다 — PR 번호로 찾는다. 자기 분석·실제 모듈(GOMODCACHE의 x/tools·x/mod·x/sync·x/text·x/crypto·x/net·
  x/telemetry·pgx·puddle·go-sql-driver/mysql·go-mssqldb·go-ora·modernc sqlite·libc, 스크래치 사본) 실행·두 번 실행
  비교에서 나온 것 중 사용자 결정이 필요 없는 넷을 고쳤다(CHANGELOG Fixed 첫 네 항목). 두 번 실행한 graph(세 레벨)·
  dead 출력은 x/mod·x/sync·x/text(dead는 x/tools도)에서 바이트가 같았고, x/mod·x/tools 심볼 문서에 유령 간선·중복
  정점·중복 간선이 없었으며, 실행한 명령(graph·cycles·dead·rta·query·impact·path·shared·routes·schema·bridges·
  unused-deps·metrics·init·mcp)에 크래시는 없었다.
  - 패턴 오류로 센 풀리지 않은 import를 `--deps`·심볼 레벨·unused-deps가 로드 오류로 다시 세던 것(아래 "남은 일"에
    있던 이중 계수). 정점 집합은 그대로다.
  - module 레벨이 로드 오류를 하나도 세지 않고 풀리지 않은 import를 `(builtin)`으로 세던 것. 이제 정점 모듈 패키지의
    오류와 풀리지 않은 import(`--deps` 없이도)를 센다 — 패키지 레벨과 기준이 같지는 않다(패키지 레벨은 `--deps` 없이
    풀리지 않은 import를 "주 모듈 밖 import 생략"으로 센다). 오류를 낸 std(`syscall/js`)는 builtin 그대로다. 오류 없는
    문서는 바이트가 같다(이 저장소와 실제 모듈 14개의 module·package 문서 전후 동일).
  - unused-deps가 의존 패키지의 빌드 제약 제외 파일 import를 보지 않아 tidy가 지키는 require를 미사용 indirect로
    오보하던 것(x/tools의 x/sys, modernc sqlite의 go-isatty·go-strftime — `go mod tidy -diff` 차이 없음). 같은
    코퍼스에서 판정이 바뀐 것 셋(모두 사용 쪽), limitation 수만 바뀐 것 넷, 나머지 여덟은 같다.
  - MCP `tools/list`의 일곱 도구가 `"required": null`(무효 JSON Schema)을 싣던 것 — ajv 6.15가 거부했다.
  - PR #58 독립 리뷰 후속(같은 브랜치): 앞 항목의 "tidy는 의존 패키지 테스트 import를 세지 않는다"는 의존 모듈의
    go.mod가 그 모듈을 제공할 때만 맞았다. tidy는 테스트만의 의존 모듈을, "all"의 모듈과 그 go.mod require로 만든
    그래프가 고른 버전 이상으로 제공하지 못하면 남긴다(go1.27.1 modload `tidyPrunedRoots`). unused-deps가 그 규칙으로
    의존 패키지 테스트를 판정한다(`source/unusedtests.go`) — 리뷰 fixture f7·f7b·f7c(tidy가 지킴)는 미사용 → 사용,
    f7d·f1·f2·f4(tidy가 지움)는 미사용 그대로, pgx의 `gopkg.in/check.v1`은 사용(tidy가 지킴). 이번에 적재하지 않은
    테스트 의존의 import(pgx `kr/pretty`는 check.v1이 import한다)는 못 봐서, 미사용이 남을 때 그 테스트 파일 수를
    limitation으로 센다(pgx 22, go-ora 62). 코퍼스의 나머지 열셋은 PR 머리(`3eeb840`) 대비 unused-deps 출력이 같다. 함께: module 레벨 std 오류는 builtin,
    cgo 제외 파일 안내에 `CGO_ENABLED=1`, 제외·테스트 파일은 import 선언까지만 읽기(sqlite 의존 제외 파일 767개
    약 47 MB → 첫 16 KiB 안, 상한 약 6 MB).
  - PR #58 델타 리뷰 후속(같은 브랜치, fixture는 리뷰 하네스 `cmp.sh`로 go1.27.1 `go mod tidy -diff`와 대조):
    그래프 근사(`source/tidygraph.go`)가 주 go.mod의 `exclude`를 따른다(fx/excl: tidy가 지키는데 미사용 → 사용),
    go 1.17 미만 루트의 전이 require를 모듈 캐시 go.mod(replace 적용)로 따라간다(fx/unpr: tidy가 지우는데 사용 →
    미사용), go 명령이 무시하는 `_`·`.` 이름의 테스트 파일을 읽지 않는다(fx/under·fx/dot: 사용 → 미사용, tidy가
    지움), vendor/로 적재한 의존 모듈 수를 limitation으로 센다(fx/vend: tidy는 vendor/를 보지 않고 지키는데 판정
    재료가 없다). module 레벨은 go list가 디렉터리를 찾지 못한 점 없는 경로(`fmtt`, `myapp/missing`)를 builtin이
    아니라 로드 오류로 센다(fx/modlvl·fx/modlvl2 — main에서도 builtin이었다). 이 저장소와 실제 모듈 14개의 unused-deps·
    module 출력은 PR 머리(`e49b034`) 대비 같다. fx/other·fx/ign·fx/eq·fx/lower·fx/xtest·fx/cgo도 tidy와 같다.
  - PR #58 세 번째 델타 리뷰 후속(리뷰 하네스 `cmp2.sh` — 로컬 파일 프록시·전용 모듈 캐시로 go1.27.1 tidy와 대조):
    - 차단 문제: 같은 입력의 판정이 실행마다 달랐다(fx2/nd: a230a8f로 30번 중 14번 사용·16번 미사용, tidy는 z를
      지움). 루트 읽기와 가지치기 없는 걸음이 방문 집합 하나를 함께 써서, 가지치기된 루트 xm을 루트로 먼저 읽으면
      go 1.16 루트 a의 걸음이 xm에서 멈췄다 — 순서는 walkImports의 import 맵 순회였다. go1.27.1 `readModGraph`는
      (모듈, 가지치기)로 중복을 거른다(dedupKey). 이제 두 읽기를 따로 세고 루트·테스트 파일을 정렬한다 — fx2/nd
      20번(그리고 30번) 모두 tidy와 같다.
    - go.work의 replace가 바꾼 루트의 go.mod·go 지시문을 읽어(Module.GoMod·GoVersion) tidy가 지키는 require를
      미사용으로 냈다(fx2/wsrep). 이제 루트 go.mod도 주 go.mod의 replace로만 골라 읽는다(go 지시문도 그 파일에서).
    - 모듈 캐시 루트를 정확한 꼬리로 떼어 얻는다(캐시 경로에 cache/download가 든 fx2/base: 사용 → 미사용, tidy가
      지움). 그런 모듈이 없으면 `go env GOMODCACHE`(임시 디렉터리에서, GOTOOLCHAIN=local)로 묻는다 — `go env -w` 값.
    - go 지시문 `1.17rc1`은 1.17 앞이라 가지치기 안 됨(go/version.Compare, fx2/g117rc: 사용 → 미사용, tidy가 지움).
    - 남은 변이(한 단계만 걷기, 걸음의 방문 검사 제거)를 잡는 두 단계 사슬·c1⇄c2 순환 단위 테스트를 더했다.
    - fx2 20개 전부와 앞 리뷰 fixture가 tidy와 같다(fx/ws·fx/stale 제외 — 아래·로드 실패). 이 저장소와 실제 모듈 14개의 unused-deps
      출력은 a230a8f 대비 같고 두 번 돌려도 같다.
  - PR #58 마지막 리뷰 후속: go.work가 적용된 적재(`go env GOWORK`를 go list와 같은 디렉터리·환경으로 묻는다)에서 미사용이
    남으면 `N requires reported unused were judged with a go.work file in effect, which go mod tidy ignores; …`를 단다.
    앞 커밋의 go.work replace 전용 계수는 원래 모듈의 go.mod가 캐시에 있을 때만 나와서(리뷰 wsdir) 이것으로 대신했다.
    리뷰 사례를 로컬 파일 프록시로 다시 만들어 대조했다(tidy는 GOWORK=off): wsuse(dep2를 `use` — tidy는 dep2·z를
    지키는데 dep2 미사용 직접·z 미사용 indirect)·wsver(`use ./other`가 t를 v1.1.0으로 올림 — tidy는 z를 지키는데 미사용)·
    wsdir(go.work replace, 빈 캐시와 데운 캐시)·fx/ws 모두 판정은 그대로이고 이제 limitation이 붙는다. `GOWORK=off`로 돌리면
    wsuse·wsver·wsdir·dbl·fx2/wsrep 모두 tidy와 같다. 같은 모듈 버전의 go.mod는 한 번만 읽어(go의 loadCache처럼) 못 읽은
    파일을 두 번 세지 않는다(dbl, 빈 캐시: `2 … could not be read` → `1`). 캐시가 파일 시스템 루트(`/`, `D:\`)면 루트를
    그대로 둔다(끝 구분자를 떼면 빈 문자열·드라이브 상대 경로가 됐다).
  - 검증: `go vet`·gofmt 통과, `Scripts/coverage.sh` 92.8%(기준 90%), `Scripts/verify-cli-contract.sh` OK, 자기 분석
    dead 12건(main과 같은 목록)·rules/cycles type·symbol `--strict` 0, conformance drift current. Windows·macOS 테스트는
    CI에서만 돈다. 독립 리뷰(GLM `packet-ask`)는 이 환경에 없어 실행하지 못했다.

- PR #60(2026-11-22, squash 머지 `20c4479`, main `345dfe4` 기준 브랜치였다 — 아래 해시는 머지 전 브랜치 커밋이다).
  둘을 고쳤다(CHANGELOG Changed의 **Breaking** 규칙 파일 항목, Fixed 첫 항목). 찾은 방법: 실제 모듈 dead를
  deadcode(x/tools RTA, `-generated`)와 대조(아래), 명령별 적대 입력, 공식 SARIF 2.1.0 스키마 검증.
  - 규칙 파일 키 검사는 **Breaking**이다 — 예전에 읽히던 설정(오타·남은 키, `x-` 앵커 키, `---` 뒤 내용)이 이제 종료
    2이고, 규칙 파일을 읽는 명령(`rules`·`metrics`·`mapping`·`mcp`와 exclude를 읽는 수확 명령)이 모두 멈춘다. 그래도
    둔 근거는 거짓 사실이다: 오타 하나가 사용자가 쓴 규칙을 읽지도 않고 버려 "0 violations"를 냈다. 아래 "남은 일"의
    `independent: [web]`은 다르다 — 파일에 쓴 그대로 읽혀 버려지는 것이 없고, 거부해도 거짓 보고를 고치지 않고 종료
    코드만 바뀐다. Breaking 여부는 코디네이터가 사용자에게 알린다. `x-` 키도 거부를 유지한다(나중에 푸는 것은 추가
    변경이고, rustograph도 serde `deny_unknown_fields`다). 수요가 생기면 최상위 `x-*`만 허용하는 것이 그 길이다.
  - 규칙 파일의 모르는 키·둘째 YAML 문서를 거부한다(`config/keys.go`). 재현: `forbiden:`(forbidden 오타)인
    설정에서 main은 `rules --strict`가 `0 violations`로 0, 철자를 고치면 1. 이제 종료 2와 `line 3: unknown key
    "forbiden" (known keys: …)`. 적대 입력 21개를 main과 대조했다 — 바뀐 것은 모르는 키(최상위·deny·forbidden·
    fileRules·limits 항목·`<<` 병합·따옴표 친 `"<<"`·끝 공백 키·`Deps` 대소문자·`x-` 앵커 키)와 내용 있는 둘째 문서뿐이고,
    따옴표 키·JSON 형식·BOM·`version`·null 섹션·빈 끝 문서·알려진 키만 병합한 앵커·탭 들여쓰기 오류는 main과 같은
    결과다. 지수적으로 겹친 병합 앵커는 main과 같이 디코딩의 `excessive aliasing`으로 즉시 거부된다(검사가 디코딩 뒤라
    부풀지 않는다).
  - `limits` 위반의 텍스트·SARIF(거짓 fileScope 문구, 빈 논리 위치 둘 — 공식 스키마의 `uniqueItems` 위반으로 main의
    로그가 무효였다)와 SARIF 규칙 색인에 없던 `layer-stability`·`layer-limit`. 규칙 여덟 종류(allow·deny·visibleTo·
    forbidden·independence·fileScope·stability·limit)를 내는 fixture에서 결과 10개 중 main 대비 바뀐 것은 limit 셋뿐이고
    나머지 일곱 결과와 JSON 보고는 바이트가 같다. 새 로그는 공식 스키마
    (oasis-tcs/sarif-spec `sarif-schema-2.1.0.json`, jsonschema Draft4) 오류 0, main은 3.
  - 회귀 테스트는 고치기 전 코드에서 실패한다(스크래치 사본에 변이 18개 — 모두 잡힘): `config/keys_test.go`,
    `cli/rulesconfig_test.go`, `cli/rules_sarif_test.go`. `Scripts/verify-cli-contract.sh`에 모르는 키 종료 2 사례를
    더했다(main 바이너리는 0으로 그 사례만 실패한다).
  - PR #60 독립 리뷰 후속(같은 브랜치): CHANGELOG 항목을 Fixed에서 Changed의 **Breaking**(영향 명령·앞으로의 호환성·
    마이그레이션)으로 옮겼다. 별칭 키(`forbidden: [{&k from: a, to: b}, {*k : b, to: a}]` — main은 두 규칙을 다 읽는데
    `line 3: unknown key "k"`로 거부했다)는 yaml.v3처럼 대상 이름으로 읽고 오류 줄은 별칭을 쓴 자리다. 둘째 문서
    오류가 내용 줄(`---` 다음 줄)을 가리키던 것을 `---` 줄로 고치고 문구를 "everything from this line on would be
    ignored"로 바꿨다(`--- {}`처럼 규칙이 없는 문서에도 맞는다). 살아남던 변이 셋(항목·섹션 별칭 풀기 제거,
    `sarifLogical.Kind`의 omitempty 제거)을 잡는 사례를 더했다.
  - dead 오라클 대조(스크래치 사본, 고친 곳과 무관한 기존 동작 확인): x/tools v0.53.0·x/text v0.42.0·x/net v0.59.0·
    x/crypto v0.57.0·x/mod v0.43.0과 이 저장소에서 gartograph(CHA)만 unreachable로 낸 func·method를 deadcode가 그
    패키지를 프로그램에 담은 경우로 좁혀 하나씩 봤다. 남은 것은 모두 deadcode의 반사 규칙(인터페이스로 변환된 타입의
    공개 메서드를 살린다 — gartograph는 `N packages import reflect` limitation으로 센다), 호출되지 않는 표지 메서드
    (`nodeImpl`·`isJSONRPC2Message`), 아무도 읽지 않는 패키지 변수에 담긴 함수 값(x/text collate `ignoreCaseF` —
    RTA는 주소를 딴 함수를 살린다)이었다. 거짓 unreachable은 찾지 못했다. x/tools의 패키지 import 간선 632개는 `go
    list`와 정확히 같다.
  - 검증(리뷰 후속 뒤 다시 돌림): `go vet`·gofmt 통과, `Scripts/coverage.sh` 92.9%(기준 90%, 패키지 여섯 모두 ok),
    `Scripts/verify-cli-contract.sh` OK, 자기 분석 dead 12건(main과 같은 목록·같은 limitation·루트 3)·rules `--strict` 0·cycles package/type/symbol
    `--strict` 0, conformance drift current(isthmus `c76e105`). 자기 분석이 잡은 것 하나: 병합 키 펼침을 처음에 자기
    재귀 함수로 써서 `cycles --level symbol --strict`가 1-순환으로 실패했다 — 작업 목록으로 바꿨다. 이 저장소·x/mod·
    x/text의 graph(심볼)·dead JSON은 main 바이너리와 바이트가 같다(규칙 파일이 없는 입력). Windows·macOS 테스트는
    CI에서만 돈다. 독립 리뷰(GLM `packet-ask`)는 이 환경에 없어 실행하지 못했다.

- 브랜치 `ccr-37904670-hxdjsx`(PR #61, 2026-11-25, main `20c4479` 기준, 머지 전 — squash 머지되면 PR 번호로 찾는다).
  둘을 고쳤다(CHANGELOG Fixed 첫 두 항목 — 둘째는 리뷰 후속이다). 첫째를 찾은 방법: 자기 분석 심볼 문서에서 `export.JSON → graph.(Document).Sort` references
  간선의 위치가 같은 식 `d.Sort()`의 두 열(18:2·18:4)이었다.
  - 선택자 `x.Sel` 하나를 참조 지점 둘로 기록했다. `selectorEdge`가 선택자 시작으로 기록한 뒤 `ast.Inspect`가 Sel 식별자로
    내려가 `identEdge`가 같은 객체를 다시 기록했다(`sigTypeEdges`의 한정 타입 이름 `pkg.T`도 같다). 그래서 references·
    signature 간선 위치가 겹쳤고 모듈 밖 참조 수가 선택자마다 한 번 더 늘었다(`fmt.Println("x")` 3, 점 import로 쓴 같은
    호출 `Println("x")` 2). 이제 `inspect`·`sigTypeEdges`가 만난 선택자의 Sel 식별자를 집합(`selectorIdents`)으로 기억해
    건너뛴다 — 중첩 선택자(`w.Inner.Field`) 때문에 마지막 하나가 아니라 집합이다. X 쪽 식별자(`cfg.Name`의 `cfg`)는 그대로다.
  - 그대로 둔 단위: 호출 하나는 call과 references 두 사실이라 2로 센다(`TestSharedPromotedImplementationCountedOnce`가
    고정한 단위다). `selectorEdge` 주석은 "비호출 위치"라고 하지만 호출 위치에서도 references를 긋는 것은 main부터의
    동작이고, 바꾸면 간선이 사라져 두었다. 여러 이름 ValueSpec(`var a, b = …`)은 이름마다 같은 식을 훑어 이름마다 센다
    (간선도 이름마다 따로다 — 한 번만 훑으면 이름별 간선이 달라진다).
  - 회귀 테스트 `source/selectorsite_test.go`: main의 `symbols.go`로 두 테스트 모두 실패하고, 변이 넷(inspect의 건너뛰기
    제거, sigTypeEdges의 건너뛰기 제거, X 식별자도 건너뛰기, 마지막 선택자 하나만 기억)이 모두 잡힌다.
  - 대조(스크래치 사본의 main 바이너리와 비교): 이 저장소(`--tests` 포함)·x/mod v0.41.0·x/sync v0.23.0·x/text v0.42.0·
    x/net v0.59.0·x/crypto v0.57.0·x/tools v0.50.0·pgx v5.11.0의 심볼 문서에서 정점·간선(`candidate` 포함)·다른 limitation·
    최상위 키가 같고, 사라진 위치는 모두 바로 앞이 `.`인 Sel 식별자이며 같은 간선에 그보다 앞선 위치가 남는다(그래서 첫
    위치를 쓰는 순환 근거·위반 위치는 그대로다). 적대 fixture(메서드 표현식 `(*lib.T).PM`, 제네릭 `lib.F[int]`·`lib.G[lib.T]`,
    호출 결과 선택 `mk().In.V`, 타입 단언·type switch, 승격 메서드·필드, 인터페이스 메서드 값, 여러 이름 ValueSpec, 배열
    길이의 한정 상수, 익명 struct 필드, 레이블)도 같다. 사라진 위치·수(심볼 레벨, `5065cfa` 시점): 이 저장소 5196개·
    9185 → 5711, x/mod 1873·2318 → 1497, x/sync 122·173 → 112, x/text 10304·6566 → 4148, x/net 14090·15644 → 9677,
    x/crypto 9300·16077 → 10197, x/tools 23500·56411 → 34525, pgx 8555·11458 → 7197. type 레벨은 이 저장소 142 → 71.
  - 살펴봤지만 고치지 않은 것: `diff`를 apidiff(x/exp `a4bb9ff`)와 사례 46개로 대조했다 — 거짓 breaking과 놓친 breaking은
    아래 "남은 일"에 있다.
  - 검증(`5065cfa` 시점): `go vet`·gofmt 통과, `Scripts/coverage.sh` 92.9%(기준 90%, 패키지 여섯 모두 ok — 이 기계의 부하가 커서
    `GOFLAGS=-timeout=90m`으로 돌렸다; 기본 10분 제한으로는 cli 패키지가 시간 초과로 멈췄다), `Scripts/verify-cli-contract.sh`
    OK, 자기 분석 dead 12건(main 바이너리와 목록·루트 3이 같고 references 수 줄만 9185 → 5711)·rules `--strict` 0·cycles
    package/type/symbol `--strict` 0(symbol 출력도 수 줄만 다르다), conformance drift current(isthmus `c76e105`). Windows·
    macOS 테스트는 CI에서만 돈다. 독립 리뷰(GLM `packet-ask`)는 이 환경에 없어 실행하지 못했다.
  - PR #61 독립 리뷰 후속(같은 브랜치, `5065cfa` 위): 고칠 것 둘과 사소한 것 셋을 반영했다.
    - 정점 없는 **모듈** 심볼 참조를 "outside the module"로 세던 것(main부터의 동작, CHANGELOG Fixed 둘째 항목). type
      레벨에는 함수·메서드·변수·상수·필드 정점이 없어 `type A [lib.N]int`의 `lib.N`이, `--pattern` 밖이거나 exclude에 걸린
      모듈 패키지의 심볼 참조 전부가 모듈 밖으로 세였다. 이제 `omitRef`가 대상 패키지(참조는 객체의 패키지, 간선은 정점
      ID에서 뗀 경로 `idPackagePath`)가 적재한 주 모듈 패키지(`Module.Main` — `mainModulePackages`)면 `N references to
      module symbols that have no vertex in this document were omitted (…)`로 센다. 리뷰가 제안한 `modulePkgs`(수확한
      패키지만)로 가르면 `--pattern`·exclude 밖 모듈 패키지가 계속 "모듈 밖"이라 적재 사실을 썼다. 초기화 루트 재방문의
      되돌리기는 두 수 모두에 적용한다. 새 문장은 AGENTS·HANDOFF가 사용자 결정으로 두는 계약(종료 코드, `query` 스키마,
      키 이름)이 아니고 #57·#58·#60도 센 사실을 새 문장으로 더했다 — 수를 버리지 않고 옮겨 두 수의 합은 전과 같다.
    - 재현(main → `5065cfa` → 지금, 모듈 쪽 + 밖): `lib.N` 배열 길이 fixture type 2 → 1 → 1 + 0; 리뷰 f12 type 10 → 6 →
      2 + 4, `--pattern .` 심볼 37 → 21 → 17 + 4; exclude fixture 심볼 5 → 3 → 3 + 0; 이 저장소 `--pattern ./cli` 심볼
      2496 → 1572 → 387 + 1185, type 51 → 26 → 21 + 5. 실제 모듈 type 레벨: x/mod 52 → 27 → 1 + 26, x/text 123 → 67 →
      10 + 57, x/net 671 → 353 → 11 + 342, x/crypto 647 → 343 → 17 + 326, x/tools 1119 → 566 → 3 + 563. x/sync·pgx type과
      이 저장소·실제 모듈 7개의 심볼 레벨(`./...`)은 모듈 쪽이 0이라 `5065cfa`와 같다. 문서의 나머지는 `5065cfa`와 같다.
    - 회귀 테스트 `source/omittedrefs_test.go`(type 레벨, 심볼 레벨, `--pattern .` — 메서드 ID와 초기화 루트 재방문 포함)는
      `5065cfa`에서 실패하고, 변이 일곱(수확 패키지만으로 가르기, 간선 쪽을 늘 밖으로, 참조 쪽을 늘 밖으로, 초기화 루트에서
      모듈 수 되돌리기 빠짐, 메서드 ID 파싱 제거, 모듈 쪽을 밖으로 세기, revisit 무시 — 마지막은 기존
      `TestTestsCountsEachFileOnce`가 잡는다)이 모두 잡힌다.
    - CHANGELOG의 "그 수는 지점·간선 종류마다 하나다"는 여러 이름 선언에서 틀렸다(`var c, d = fmt.Sprint(1), 2`는 main 6,
      `5065cfa` 4). 이름마다 출발 정점이 다른 간선을 긋는 것이라 판정은 두고, 단위를 "출발 정점·간선 종류·지점마다"로
      고쳐 CHANGELOG·README·README.ko에 적었다.
    - 사소한 것: CHANGELOG의 `d.Sort()` 예(`10:2`·`10:4`는 어느 파일에도 없었다)를 `export/export.go`의 `18:2`·`18:4`로
      고쳤다. 객체로 거르는 잘못된 수정(`map[types.Object]bool`)이 `5065cfa`의 두 테스트를 통과했다 —
      `TestSelectorIsOneReferenceSite`에 같은 객체를 선택자 밖에서 다시 쓰는 키(`lib.Doc{Name: "k"}`)를 더해 그 변이가 이제
      실패한다. 수가 바뀌면 문자열 정렬 때문에 limitation 줄 순서가 바뀔 수 있다(리뷰 f08: `30 references …`는
      `3 imports …` 뒤, `15 references …`는 앞) — CHANGELOG에 적고 "수 줄만 바뀐다" 문구를 고쳤다.
    - 검증(이 트리): `go vet`·gofmt 통과, `Scripts/coverage.sh` 92.9%(기준 90%, 패키지 여섯 모두 ok — 부하 평균이 25~30이라
      `GOFLAGS=-timeout=90m`), `Scripts/verify-cli-contract.sh` OK, 자기 분석 dead 12건(main 바이너리와 목록·루트 3이 같고
      수 줄만 9202 → 5722)·rules `--strict` 0·cycles package/type/symbol `--strict` 0, conformance drift current(isthmus
      `c76e105`). 코퍼스: main 대비 심볼 문서 대조를 이 트리로 다시 돌려 같은 결과다(이 저장소 사라진 위치 5203개·9202 →
      5722, `--tests` 9900개·24772 → 15726, 실제 모듈 7개는 위 `5065cfa` 시점 수와 같다). `5065cfa` 대비 문서는 수 문장 밖이
      같다(이 저장소 type·symbol·`--pattern ./cli`, 실제 모듈 7개 type·symbol). Windows·macOS 테스트는 CI에서만 돈다.

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
- 자기 분석 `dead`는 `--strict` 없이 근거만 본다(CI도 같다). 2026-11-25 기준 12건(11-03·11-15·11-22와 같다) — 테스트에서만
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
- unused-deps의 테스트 의존 판정은 한 단계뿐이다. 의존 패키지 테스트가 import한 패키지가 이번 로드에 없으면(테스트만의
  의존) 그 패키지의 import와, tidy가 그 모듈을 루트로 더하며 늘어나는 그래프를 보지 못한다 — pgx v5.11.0의
  `github.com/kr/pretty`(yaml.v3 테스트 → check.v1 → kr/pretty, tidy는 지킨다)가 미사용 indirect로 남고 limitation이
  붙는다. 메우려면 그 패키지 소스(모듈 캐시에 없을 수 있다)를 읽어 tidy의 단계별 루프를 따라가야 한다. vendor/
  모드도 같은 공백이다(모듈 캐시를 읽어 메울 수 있으나 vendor는 보통 캐시 없이 쓴다 — 지금은 limitation만 센다).
  그래프 근사가 고른 버전을 주 go.mod의 require 버전으로 대신하는 점, 읽지 못한 go.mod는 사용 쪽으로 기우는 점(그
  수는 limitation)도 남는다.
- unused-deps(main부터의 기존 동작): go.work가 `./dep`를 `use`하면 example.com/dep을 미사용 직접 require로 낸다(리뷰
  fx/ws, wsuse) — tidy는 지킨다. 워크스페이스 모듈의 패키지는 Module.Main이라 "사용한 비주 모듈"에 들지 않기 때문이다.
  go.work의 `use`(버전 상향, wsver)·`replace`(wsdir)도 같은 계열이다 — 적재 자체가 tidy가 보지 않는 go.work를 따른다.
  이제 그런 미사용 판정에는 go.work limitation이 붙지만 판정은 그대로라 fx/ws·wsuse에서 `--strict`는 여전히 1로 끝난다.
  판정까지 tidy에 맞추려면 unused-deps가 GOWORK=off로 다시 적재하는 변경이 후보다(워크스페이스가 내려받지 못하는 모듈을
  로컬 디렉터리로만 제공하면 그 적재가 실패할 수 있다). 2026-11-22 사용자가 판단을 맡겨 지금은 바꾸지 않고 limitation만
  둔다 — 다시 적재가 실패하면 판정 자체를 잃고, limitation이 `GOWORK=off` 재실행을 안내해 사용자가 tidy와 같은 판정을 얻을 수 있다.
- SARIF: cycles의 논리 위치 kind가 type·symbol 레벨 멤버에도 `module`이고, dead는 `func`·`var`처럼 SARIF 권장
  어휘(function·variable·member·type) 밖의 kind를 싣는다(권장 사항이라 무효는 아니다). 바꾸면 SARIF 소비자 출력이
  바뀐다.
- `routes`의 계약 밖 동사(ServeMux `"PURGE /x"`·`"CONNECT /x"`·소문자 `"get /x"`, gin `Handle("PURGE", …)`·
  `Match([]string{"GET", "PROPFIND"}, …)`, echo `Add`·`Match`, chi `Method`): 그 동사의 사실도 공백도 내지 않는다.
  #35(`062fc4f`)부터의 설계다 — `registrationMethods` 주석("모델링된 호출이 보낼 수 없어"), 당시 GLM 리뷰의 같은
  제안을 반박한 기록(HANDOFF-HISTORY). tsograph는 같은 등록을 `route-coverage: … HTTP verbs outside the contract's
  method set …`로 센다. 계열을 맞추려면 isthmus 스코프 `methods`가 계약 동사만 담을 수 있어 그 공백의 스코프가 그
  템플릿의 모든 동사가 된다(같은 경로의 GET 호출 `route-call-without-decl`이 unverified로 내려간다)는 점까지 사용자가
  고른다. 2026-11-22 재현: ServeMux·gin·echo·chi fixture에서 그런 등록 8개가 limitation 없이 빠진다(둘은 함께 쓴 계약
  동사의 사실만 남는다).
- `routes`의 리터럴 `ANY` 동사: ServeMux `"ANY /x"`(요청 동사가 정확히 `ANY`일 때만 맞는다)·gin `Handle("ANY", …)`·
  echo `Add("ANY", …)`가 모든 동사 decl(`method: "ANY"`)이 된다 — 패턴에 동사가 없을 때의 내부 표지와 같은 문자열이라
  구분하지 않는다. 위 설계대로면 사실을 내지 않아야 한다(거짓 사실 제거라 반대 코퍼스와 isthmus 영향 증거와 함께).
- 규칙 파일의 `independent: [web]`처럼 쌍을 하나도 만들지 않는 목록은 조용히 아무것도 검사하지 않는다. 모르는 키와
  달리 파일 내용은 빠짐없이 읽혀 거짓 보고가 아니고, 거부하면 지금 통과하는 설정이 종료 2가 되기만 해서 사용자가 고른다.
- 규칙 파일의 `x-*` 확장 키: 지금은 모르는 키로 거부한다. docker-compose식 앵커 보관 키 수요가 생기면 최상위 `x-*`만
  허용하는 것이 후보다(추가 변경이라 하위 호환).
- `diff`의 봉인(sealed) 인터페이스: 공개 인터페이스의 메서드 집합에 자기 패키지의 비공개 메서드가 있으면 패키지 밖
  타입은 그 패키지의 타입이나 인터페이스를 임베드해야만 구현할 수 있어, apidiff는 메서드 추가를 호환으로 본다.
  gartograph는 `exported interface I gained method N — implementers no longer satisfy it`를 breaking으로 내 `--strict`가
  1이다(2026-11-25 재현 여덟 사례 — 직접 선언, 비공개 임베드 인터페이스가 얻음, 새 임베드, 제네릭 인터페이스, 비공개
  메서드 추가, 구현 타입(공개·비공개)도 함께 얻음 — 모두 apidiff는 호환). 문서만으로 호환을 증명하려면 그 비공개
  메서드를 가진 모든 패키지 타입이 새 메서드를 같은 서명으로 얻었는지 알아야 한다: 외부 타입이 임베드해 구현에 쓰는
  `Base`·gRPC `Unimplemented…` 꼴 타입은 혼자서는 I를 구현하지 않아 implements 간선이 없고, 문서에는 구체 메서드의
  서명이 없으며, 제네릭 타입의 implements는 이름·개수만 맞춘다. 그래서 판정을 바꾸지 않았다 — 메서드 서명 사실을 더할지, apidiff처럼
  호환으로 볼지 사용자가 고른다. 지금의 breaking이 참인 경우도 있다: 구현 타입이 새 메서드를 얻지 못하면 그 타입을
  I로 쓰는 외부 코드가 깨진다(값 리시버 `T`는 apidiff도 `T: no longer implements I`로 잡고, 포인터 리시버 `*T`는 놓친다).
- `diff`의 다른 차이(README의 breaking 목록대로의 동작이라 바꾸면 계약 변경이다): `func F` → `var F = func(){}`는 kind
  변경 breaking인데 호출은 그대로 컴파일된다(apidiff 호환 — 다만 제네릭 함수는 var가 될 수 없어 깨지고 문서에 제네릭
  표시가 없다). 함수 타입 파라미터 제약을 넓히는 변경(`F[T C]` → `F[T any]`)은 `no longer references C`로 breaking이다
  (apidiff 호환). 놓치는 breaking(apidiff는 잡는다): 별칭 → 정의 타입(`type A = B` → `type A B`), `F(T)` → `F(*T)`, 값 →
  포인터 리시버, 공개 변수의 비공개 인터페이스 타입이 메서드를 얻는 경우 — 시그니처 참조 집합만 보는 휴리스틱의 범위다.
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
