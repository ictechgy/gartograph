# Changelog

이 프로젝트의 주요 변경 사항을 기록한다.

## [Unreleased]

### Security

- `impact --since`, `reach`·`impact --format language-traversal`의 `revision` 판독: 분석 대상 저장소의 git
  설정이 정한 명령을 더는 실행하지 않는다. git 호출마다 바로 앞에서 설정된 filter 드라이버(clean·smudge·process)를
  모아 빈 명령으로 덮고, fsmonitor 훅·외부 diff·textconv·서명 표시를 끄며, `git diff`가 index를 갱신하지 않게
  한다(index를 쓰면서 `post-index-change` 훅이 돌았다). 서브모듈 안에서는 git을 띄우지 않아 서브모듈 저장소의
  설정도 실행하지 않는다. 부분 clone(promisor remote)에서 빠진 객체를 받으려는 `git fetch`도 띄우지 않는다
  (`GIT_NO_LAZY_FETCH=1`, 빈 `GIT_ALLOW_PROTOCOL`) — 그 fetch가 분석 대상 설정의 전송 명령
  (`remote.<이름>.uploadpack`, `core.sshCommand` 등)을 실행했다. 드라이버를 모으지 못하거나 이름에 `=`·제어
  문자가 있어 덮을 수 없으면 git을 실행하지 않는다. 자세한 내용은 README의 Security 절에 있다.
- `graph --level type|symbol`과 그 수확을 쓰는 명령(`dead`, `query`, `impact`, `reach`, `metrics`, `schema`,
  `routes`, MCP 등): 앞 선언을 두 번 쓰는 중첩 타입 별칭(`type A1 = struct{ a, b A0 }`, `type F1 = func(F0, F0)`)과
  유니언 제약(`type U1 interface{ U0 | U0 }`)이 정규 타입 표기를 깊이마다 두 배로 키워 메모리와 출력이 지수적으로
  늘던 문제를 막는다. 타입마다 표기 길이와 구조 해시를 메모하고, 표기 하나를 64KiB로 묶고(항을 이은 유니언
  원소와 이름 없는 인터페이스 이름도 전체를 묶는다), 유니언의 인터페이스 항은 한 번만 펼치고, 의존 소스의 이름
  없는 인터페이스를 거를 때 타입마다 한 번만 본다. 항이 각각 상한 아래인 100항 유니언(소스 1.3KB)의 `typeSet`
  항목은 3.5MB에서 328바이트가 되고, 서명이 각각 상한 아래인 이름 없는 인터페이스의 `satisfies` 이름은 구현
  메서드마다 실려 메서드 수의 제곱으로 커지던 문서(메서드 20개에서 13.9MB)가 선형이 된다(27.6KB). `graph --level type`
  기준(3회 중앙값) 별칭 struct 사슬 깊이 24는 4.1초·RSS 1.6GB·출력 285MB에서 0.17초·17MB·33KB가 되고, 함수
  별칭 사슬 깊이 24는 3.8초·1.1GB·185MB에서 0.02초·17MB·44KB가 되며(깊이 60도 0.03초), 유니언 사슬 깊이 22는
  4.5초·615MB에서 2.6초·17MB가 된다(출력은 같다). 타입 검사(`go/types`) 자체의 지수 시간(유니언·struct 사슬)은
  남고, 수확 전체의 시간·메모리 예산은 없다 — README의 Security 절.
- 수확하는 모든 명령(`bridges`·`diff`를 뺀 전부, `dead --algo rta`·`routes --pattern`·`mcp` 포함): `GOTOOLCHAIN`
  환경 변수가 없거나 비었으면 go 명령(go/packages의 `go list`)을 `GOTOOLCHAIN=local`로 실행한다. 분석 대상
  `go.mod`·`go.work`의 `go`·`toolchain` 줄이 더는 다른 Go 툴체인을 내려받아 실행하게 하지 못한다. 예전에는 호스트
  기본값(`auto`)을 물려받아, 받을 수 있는 것은 공식 릴리스 툴체인뿐이지만 신뢰하지 않는 저장소 하나가 호스트에서
  툴체인 다운로드와 다른 Go 바이너리 실행을 일으킬 수 있었다. 환경 변수로 준 값은 그대로 넘긴다. require 모듈
  다운로드는 그대로다(`GOPROXY=off`로 막는다) — README의 Security 절.

### Changed

- `impact --since`: git-lfs처럼 filter로 정규화되던 파일은 git의 stat 캐시가 낡으면 바뀐 파일로 나온다 — filter를
  거치지 않은 원문으로 비교하기 때문이다. 내용이 같아도 시각이 바뀐 파일도 바뀐 파일로 나온다(git이 다시 읽어
  거르지 않는다). 영향 집합이 넓어지는 쪽이다. 신뢰하는 checkout이면 먼저 `git status`로 stat 캐시를 새로 고친다.
- `impact --since`: 서브모듈 안의 커밋하지 않은 변경은 더는 바뀐 경로로 내지 않는다. 서브모듈이 기록된 커밋과
  다른 커밋에 있으면 그 경로는 지금처럼 나온다.
- `impact --since`: filter 드라이버를 모으지 못하거나 덮을 수 없는 이름이 있으면 git diff를 실행하지 않고, 원인과
  `--files` 안내를 담은 오류(종료 코드 2)로 끝난다. 바뀐 파일은 `--files`로 줄 수 있다.
- `reach`·`impact --format language-traversal`: filter로 정규화되던 파일의 stat 캐시가 낡으면 작업 트리를
  깨끗하다고 보지 않아 `revision`을 싣지 않는다(fail-safe). 서브모듈이 있는 작업 트리와 덮을 수 없는 filter
  드라이버 이름이 있는 저장소는 `revision`을 싣지 않고, `revision-unverified:` limitation으로 이유를 알린다.
  `revision`이 필요하면 `--revision`으로 준다.
- `impact --since`·`reach`·`impact --format language-traversal`(부분 clone): git이 clone에 없는 객체를 더는
  받아 오지 않는다. `--since` revision의 트리가 로컬에 없으면(treeless clone, `--filter=tree:0`)
  `impact --since`는 git 오류와 부분 clone 안내(`--files` 포함)를 담아 종료 코드 2로 끝나고, `HEAD` 트리가
  없으면 순회 문서는 `revision`을 싣지 않는다. 예전에는 git이 빠진 트리를 promisor remote에서 받아 와 성공했다.
  blobless clone(`--filter=blob:none`)은 트리를 모두 가지고 있어 출력이 같다. 신뢰하는 checkout이면 같은
  `git diff`를 한 번 직접 실행해 받아 두거나 `--files`를 쓴다.
- `schema`: SQL 리터럴의 관계 판독이 계열 생산자 공통 규칙(isthmus GRAPH-EXCHANGE)을 따른다. 같은 문장의 CTE
  이름, 함수 인자 안의 `FROM`(`EXTRACT`·`TRIM`·`SUBSTRING` 등), `IS [NOT] DISTINCT FROM`의 피연산자를
  relation-use로 내지 않는다 — isthmus가 이 이름들을 선언과 잇지 못해 완전한 카탈로그와 맞추면 거짓
  `relation-use-without-decl` error를 냈다. 규칙이 문장 단위라 자기 본문에서 같은 이름의 실제 테이블을 읽는
  CTE(`WITH users AS (SELECT * FROM users …)`)는 그 사용도 내지 않는다.
- `schema`: 관계 자리의 절 키워드(`DO UPDATE SET`의 `SET`, 산문의 `the` 등)는 관계 이름이 되지 않고,
  `GRANT`·`REVOKE` 문장의 `FROM`은 권한 주체라 관계가 아니다(tsograph 판독과 같다).
- `schema`: 관계 자리의 자리표시자(`%s`·`{}`·`?`·`$1`·`:name`·`@name`)를 관계로 내지 않는다(예전에는
  `FROM %s`가 관계 `s`를, `FROM ? WHERE`가 `WHERE`를 냈다). 그 리터럴은 위치의 `dynamic` 사실 하나로 남고, 수는
  새 limitation `unjoined-dynamic-relations: N SQL placeholder operand(s) …`로 테이블 값 함수 수와 따로 센다.
  인용 식별자 안이나 이름 접두 뒤의 자리표시자(`"%s"`, `log_%s`)는 지금처럼 이름으로 읽는다.
  이전 판독으로 돌리는 옵션은 없다.
- `graph`(type·symbol 레벨): 정규 타입 표기 하나(타입 하나, 메서드 서명 하나, 항을 모두 이은 유니언 원소
  하나, 이름 없는 인터페이스 이름 하나)가 64KiB(65536바이트)를 넘으면 `fields`·`methods`·`typeSet`·`satisfies`
  항목에 처음 256바이트, `…sha256:`, 구조의 SHA-256(hex)으로 축약해 적는다. 같은 타입은 같은 축약, 다른 타입은
  다른 축약이라 `diff`의 문자열 비교는 그대로이고, 필드 이름(`name:`)과 메서드 이름은 앞에 남는다. 유니언
  원소와 이름 없는 인터페이스 이름은 항·메서드 서명 하나하나가 상한 아래여도 이은 전체가 넘으면 통째로
  축약한다. 축약이 든 항목 수는 새 limitation `N fields/methods/typeSet/satisfies
  entries abbreviate a type spelling over 65536 bytes as its first 256 bytes followed by …sha256: and a structural
  hash`로 센다. 실측한 실제 저장소(x/tools·pgx·Kubernetes 모듈·이 저장소)의 가장 긴 표기는 400바이트 미만이라 그
  문서는 바이트 단위로 같다. 전체 표기로 돌리는 옵션은 없고, 이 변경 전에 만든 문서와 `diff`하면 그런 항목은 바뀐
  것으로 나온다.
- `reach`·`impact --format language-traversal`의 `--type-edges members`: 축약된 필드 타입은 앞부분 밖의 어떤
  타입이든 담을 수 있다고 보고 구조 간선을 그 필드로도 옮긴다 — 앞부분만 보면 그 필드를 읽는 선언이 뒤쪽 타입에
  닿지 못한다.
- **Breaking**: MCP `gartograph_cycles`: 맨 순환 배열 대신 CLI `cycles --format json`과 같은 `{cycles, limitations}`
  객체를 돌려준다. 맨 배열에는 문서 limitation(생략한 외부 import 등)을 실을 자리가 없어, 순환 0개가 다 보고 낸
  결과인지 소비자가 가를 수 없었다. 예전 목록은 `.cycles`에 있다.
- MCP `gartograph_dead`·`gartograph_rules`·`gartograph_metrics`: CLI `dead`·`rules`·`metrics`와 같은 보고 조립
  함수를 써서 같은 문서에서 같은 JSON을 낸다(서버에는 baseline이 없어 `baselined`·`staleBaseline`만 빠진다).
  `gartograph_dead`는 `algorithm: "cha"`(예전에는 빈 문자열)와 알 수 없는 루트·main/init 루트 없음·외부
  디스패치·필드 도달성 limitation을, `gartograph_rules`는 signature 규칙 미검사(`mcp --level package`)·unmapped·
  fileScope 미검사 limitation을, `gartograph_metrics`는 abstractness/distance 생략 limitation을 더 싣고, 세 도구
  모두 limitation을 정렬한다. 예전 MCP는 main 없는 라이브러리의 공개 API 전부를 unreachable로 내면서
  `--retain-public` 안내도, reflection 디스패치를 못 본다는 말도 싣지 않았고, signature 규칙을 검사하지 못한
  package 레벨 문서에서 위반 0을 아무 limitation 없이 돌려줬다.
- MCP `gartograph_dead`·`gartograph_cycles`: 형식이 틀린 인자(`{"retainPublic": "true"}`, `{"level": 3}`)를 버리고
  기본값으로 답하지 않고, 원인을 담은 `invalid arguments: …` `isError` 결과로 거부한다. `arguments`가 없거나
  `null`인 호출은 빈 인자와 같다.
- `rules`·`metrics`·`mapping`·`mcp`: `--config FILE`을 주면 그 파일의 `exclude`로 수확하고 `--dir`의
  `.gartograph.yml`은 읽지 않는다. 예전에는 규칙은 `--config` 파일에서, 수확 범위는 `.gartograph.yml`에서 왔다.
  그래서 `.gartograph.yml`이 뺀 패키지가 `--config` 규칙의 검사에서 조용히 빠졌고(`rules --strict`가 위반을 두고
  0으로 끝났다), `--config` 파일의 `exclude`는 효과가 없었다. 두 파일의 `exclude`가 다르면 수확되는 패키지가
  바뀌고, `--config`를 줄 때는 `--dir`의 `.gartograph.yml`이 깨져 있어도 오류가 아니다. `--config`가 없으면
  지금처럼 `.gartograph.yml`이 규칙과 수확 범위를 함께 정한다.
- `mcp`: 수확할 때는 규칙 파일(`--config`, 없으면 `--dir`의 `.gartograph.yml`)을 수확 전에 읽는다. 없거나 깨진
  `--config`는 도구 호출 때의 `isError` 대신 기동 오류(종료 코드 2)가 된다. `--graph`로 저장 문서를 읽으면
  지금처럼 도구를 부를 때 읽는다.
- `rules`·`dead`의 `--write-baseline`: `position.file`을 수확한 머신의 절대 경로 대신 모듈 루트(`go.mod`가 있는
  디렉터리) 기준 `/` 구분 상대 경로로 쓴다. 커밋한 baseline에 사용자 이름과 디렉터리 구조가 남지 않는다. 모듈
  루트 밖 파일(`type T time.Time`의 필드처럼 표준 라이브러리에 선언된 것, 의존 모듈, 다른 go.work 모듈)은 파일
  이름만 남긴다 — `../` 경로는 머신마다 다른 GOROOT·모듈 캐시 위치를 담는다. 형식 버전은 1 그대로이고 짝짓기
  키가 경로를 보지 않으므로(fileScope는 파일 이름) 절대 경로로 쓴 기존 baseline도 그대로 맞는다. `staleBaseline`은
  항목을 파일에 적힌 그대로 돌려주므로 새 baseline의 항목은 상대 경로로 나온다. v0.9.2까지의 바이너리는 fileScope
  위반을 경로 전체로 짝지어 새 baseline의 fileScope 항목을 맞추지 못한다 — baseline을 쓰는 쪽과 `--baseline`으로
  읽는 쪽을 이 판 이후로 맞춘다.
- `rules`·`cycles`·`dead`의 `--format sarif`: `artifactLocation.uri`가 절대 경로 대신 모듈 루트 기준 상대 경로를
  세그먼트마다 RFC 3986으로 퍼센트 인코딩한 값이고 `"uriBaseId": "%SRCROOT%"`를 단다. `run.originalUriBaseIds`는
  `%SRCROOT%`를 절대 경로 없이 설명만 한다(SARIF 2.1.0 §3.14.14가 허용하는 형태). 파일이 모듈 루트 밖에 있는
  결과는 `physicalLocation`을 싣지 않고 메시지 끝에 `(location omitted: the file is not under the module root)`를
  붙인다. GitHub code scanning은 상대 URI를 저장소 루트 기준으로 읽으므로, `go.mod`가 저장소의 하위 디렉터리에
  있으면 업로드 전에 그 디렉터리를 URI 앞에 붙인다(README의 `jq` 예). 물리 위치가 하나도 없는 로그는 바이트가
  같다.
- **Breaking**: `rules`·`mapping`·`metrics`·`init`·`impact --files`(go.work 워크스페이스): 컴포넌트·`fileRules`
  패턴과 파일→패키지 해석이 `exclude`처럼 패키지(파일)마다 그것이 속한 워크스페이스 모듈 기준 상대 경로를 쓴다.
  예전에는 `exclude`만 패키지마다 자기 모듈을 벗기고 나머지는 문서의 첫 모듈만 기준으로 했다. 그래서 `use ./a`·
  `use ./b`를 `--dir a`로 수확하면 `util` 컴포넌트가 `example.com/b/util`에 맞지 않았고, 형제 모듈 패키지가
  외부(`unmappedExternal`)로 분류돼 `rules --strict`가 그쪽으로의 금지 의존을 두고 0으로 끝났으며,
  `impact --files ../b/util/util.go`는 그 파일을 `unmappedFiles`로 냈다. 이제 형제 모듈 패키지는 내부라서
  `rules`가 그 의존을 검사하고(새 위반이 나올 수 있다), `mapping`·`metrics`는 그 패키지를 매핑·`unmapped`·
  `orphans`로 세고, `init`은 그 패키지의 컴포넌트를 만든다. 두 모듈의 같은 상대 경로(`a/util`·`b/util`)는 같은
  패턴에 맞고 `init`도 한 컴포넌트로 묶는다. 형제 모듈 패키지를 전체 import 경로(`example.com/b/util`)로 적은
  컴포넌트 패턴은 더는 맞지 않으므로 모듈 상대 경로(`util`)로 바꾼다. 전체 import 경로로 맞는 것은 `--deps` 의존
  (경로가 워크스페이스 모듈 밑에 있는 중첩 모듈 포함)뿐이다. 주 모듈이 하나인 수확의 출력은 같다.
- `graph`(go.work 워크스페이스): 그래프에 패키지가 있는 워크스페이스 모듈이 둘 이상이면 문서에
  `workspaceModules`(모듈마다 `path`·`dir`, 경로순)를, 문서 `module`이 아닌 모듈의 패키지 정점에 `module`을
  싣는다. 형식 버전은 2 그대로다(선택 필드만 더했다). 이 목록이 없는 문서(단일 모듈, 이 판 이전에 저장한
  워크스페이스 문서)는 예전처럼 문서 `module` 하나로 해석하므로, `--graph`로 읽는 옛 워크스페이스 문서는 다시
  수확해야 새 해석을 쓴다.
- `unused-deps`: 이 플랫폼·빌드 태그로 빌드할 파일이 하나도 없는 디렉터리(예: `_windows.go`만 있는 패키지)를 새
  limitation `N directories have Go files but none for this platform or build tags, so go list loaded no package
  from them; …`으로 센다. go list는 그런 디렉터리를 패키지로 내지 않아 그 import를 읽지 못한다 — 거기서만 쓰는
  require는 지금처럼 `unused`에 남고(판정과 `--strict` 종료 코드는 그대로다), limitation이 그 공백을 알린다.
  `--goos`·`--goarch`·`--tags`로 그 플랫폼을 수확해 다시 확인한다. 순회는 go list `./...`와 tidy의 규칙을 따른다.
  `.`·`_`로 시작하는 디렉터리, `testdata`, 하위 모듈, vendor 밑, go.mod `ignore` 지시문이 가리키는 디렉터리는
  건너뛰고, 디렉터리 심링크는 따라가지 않으며, tidy도 읽지 않는 `//go:build ignore` 파일만 있는 디렉터리는 세지
  않는다. 범위는 `/...`로 끝나는 패턴(기본 `./...`, `./x/...`, 주 모듈을 덮는 import 경로 `…/...`)과 `all`·`work`다.
  와일드카드 없는 패턴은 걷지 않는다 — go list가 파일 없는 디렉터리도 오류 패키지로 실어 지금처럼 로드 오류
  limitation이 센다. 목록을 읽지 못한 디렉터리에서는 그 패턴의 순회를 멈추고(go list도 같은 자리에서 멈춘다)
  `N directories could not be read, …`로 센다. 그런 디렉터리가 없는 모듈의 출력은 같다(이 저장소, x/tools, pgx,
  Kubernetes 계열 모듈, fsnotify에서 바이트 단위로 확인했다). golang.org/x/sys는 darwin·linux 수확에서 8개
  (`plan9`, `windows/...`)를 센다.
- **Breaking**: 수확하는 모든 명령: 설치된 Go보다 새 Go를 요구하는 저장소(`go` 줄이 더 높은 `go.mod`·`go.work`)는
  그 툴체인을 받아 실행하는 대신 go 명령의 `requires go >= … (running go …; GOTOOLCHAIN=local)` 오류와 해결
  방향(그 Go 설치, 신뢰하는 저장소면 `GOTOOLCHAIN=auto`로 다시 실행)을 담아 종료 코드 2로 끝난다. 설치된 Go보다
  새 `toolchain` 줄만 있는 저장소는 그 툴체인으로 바꾸지 않고 설치된 Go로 수확한다. 예전 동작이 필요하면
  `GOTOOLCHAIN=auto`를 환경 변수로 준다 — `go env -w GOTOOLCHAIN=…`로 저장한 값은 gartograph가 넣는 환경 변수에
  덮여 쓰이지 않는다. go 줄이 설치된 Go 이하이고 더 새 `toolchain` 줄이 없는 저장소의 출력은 같다.
- `dead --algo rta`(`--explain` 포함): 문서를 수확한 적재 결과로 SSA를 만들어 프로그램을 한 번만 타입 검사한다
  (예전에는 같은 패키지를 다시 적재했다). 출력과 종료 코드는 같고, x/tools·pgx·이 저장소에서 실행 시간이
  31.0~40.0% 줄었다.
- `schema`·`routes`(`--role server`·`--role client`): 사실의 `usr`를 정하려고 만드는 symbol 문서에서 정점만
  모으고 쓰지 않는 간선 단계를 건너뛴다. 출력과 종료 코드는 같고, 실행 시간이 합성 2000·4000 패키지 모듈에서
  24.0~43.9%, x/tools·pgx에서 2.2~10.5% 줄었다.
- Go 패키지 `source`를 직접 import하는 코드: 자유 함수 `RTAReachable(opts, …)`·`RTAAdjacency(opts, …)`를
  지웠다. `LoadSession(opts)`가 symbol 문서(`opts.Level`과 무관)와 함께 돌려주는 `*Session`의 메서드
  `RTAReachable(doc, roots)`·`RTAAdjacency(doc, roots)`를 쓴다. 문서와 SSA가 한 적재 결과를 쓰게 해 두 번째
  적재로 돌아갈 길을 남기지 않으려는 것이다. CLI와 출력은 그대로다.
