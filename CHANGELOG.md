# Changelog

이 프로젝트의 주요 변경 사항을 기록한다.

## [Unreleased]

### Security

- `impact --since`, `reach`·`impact --format language-traversal`의 `revision` 판독: 분석 대상 저장소의 git
  설정이 정한 명령을 더는 실행하지 않는다. git 호출마다 바로 앞에서 설정된 filter 드라이버(clean·smudge·process)를
  모아 빈 명령으로 덮고, fsmonitor 훅·외부 diff·textconv·서명 표시를 끄며, `git diff`가 index를 갱신하지 않게
  한다(index를 쓰면서 `post-index-change` 훅이 돌았다). 서브모듈 안에서는 git을 띄우지 않아 서브모듈 저장소의
  설정도 실행하지 않는다. 드라이버를 모으지 못하거나 이름에 `=`·제어 문자가 있어 덮을 수 없으면 git을 실행하지
  않는다. 자세한 내용은 README의 Security 절에 있다.
- `graph --level type|symbol`과 그 수확을 쓰는 명령(`dead`, `query`, `impact`, `reach`, `metrics`, `schema`,
  `routes`, MCP 등): 앞 선언을 두 번 쓰는 중첩 타입 별칭(`type A1 = struct{ a, b A0 }`, `type F1 = func(F0, F0)`)과
  유니언 제약(`type U1 interface{ U0 | U0 }`)이 정규 타입 표기를 깊이마다 두 배로 키워 메모리와 출력이 지수적으로
  늘던 문제를 막는다. 타입마다 표기 길이와 구조 해시를 메모하고, 표기 하나를 64KiB로 묶고, 유니언의 인터페이스
  항은 한 번만 펼치고, 의존 소스의 이름 없는 인터페이스를 거를 때 타입마다 한 번만 본다. `graph --level type`
  기준(3회 중앙값) 별칭 struct 사슬 깊이 24는 4.1초·RSS 1.6GB·출력 285MB에서 0.17초·17MB·33KB가 되고, 함수
  별칭 사슬 깊이 24는 3.8초·1.1GB·185MB에서 0.02초·17MB·44KB가 되며(깊이 60도 0.03초), 유니언 사슬 깊이 22는
  4.5초·615MB에서 2.6초·17MB가 된다(출력은 같다). 타입 검사(`go/types`) 자체의 지수 시간(유니언·struct 사슬)은
  남고, 수확 전체의 시간·메모리 예산은 없다 — README의 Security 절.

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
- `graph`(type·symbol 레벨): 정규 타입 표기 하나(타입 하나 또는 메서드 서명 하나)가 64KiB(65536바이트)를 넘으면
  `fields`·`methods`·`typeSet`·`satisfies` 항목에 처음 256바이트, `…sha256:`, 타입 구조의 SHA-256(hex)으로
  축약해 적는다. 같은 타입은 같은 축약, 다른 타입은 다른 축약이라 `diff`의 문자열 비교는 그대로이고, 필드
  이름(`name:`)과 메서드 이름은 앞에 남는다. 축약이 든 항목 수는 새 limitation `N fields/methods/typeSet/satisfies
  entries abbreviate a type spelling over 65536 bytes as its first 256 bytes followed by …sha256: and a structural
  hash`로 센다. 실측한 실제 저장소(x/tools·pgx·Kubernetes 모듈·이 저장소)의 가장 긴 표기는 400바이트 미만이라 그
  문서는 바이트 단위로 같다. 전체 표기로 돌리는 옵션은 없고, 이 변경 전에 만든 문서와 `diff`하면 그런 항목은 바뀐
  것으로 나온다.
- `reach`·`impact --format language-traversal`의 `--type-edges members`: 축약된 필드 타입은 앞부분 밖의 어떤
  타입이든 담을 수 있다고 보고 구조 간선을 그 필드로도 옮긴다 — 앞부분만 보면 그 필드를 읽는 선언이 뒤쪽 타입에
  닿지 못한다.
