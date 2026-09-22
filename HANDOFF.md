# HANDOFF

세션 이어받기용 상태 파일. 지금 어디까지 왔고 다음이 무엇인지만 적는다.

## 현재 상태 (2026-09-22)

초기 스캐폴드 완성. `go build ./... && go test ./...` 전부 통과,
`gartograph cycles --strict` 자기 분석 0.

- `graph` — 순수 도메인. `Document` v1, `Level`(module/package/type/symbol),
  `Vertex`·`Edge`, 결정적 `Sort`, `Adjacency`/`Incoming`/`EdgeKinds`.
- `source` — 유일한 x/tools 소비자. `LoadPackageGraph`가 `go/packages`로
  패키지 레벨 그래프 수확. 기본은 모듈 내부만(`Module.Main`),
  `--deps`는 import 그래프 BFS로 외부 의존까지. 생략한 외부 import 수와
  로드 오류 수를 limitation으로 기록.
- `analysis` — `Cycles`(Tarjan SCC, 자기루프 인정, evidence 간선 첨부),
  `Query`(양방향 BFS 이웃, `depth`/`truncated`/`edges[]` 계약).
- `export` — `JSON`(정렬 보장), `Mermaid`(contains 간선 제외).
- `cli` — `graph`/`cycles`/`query`/`version`. 종료 코드 0/1/2.
  `parseInterspersed`로 positional 뒤 플래그도 파싱.
- `internal/testutil` — 임시 모듈 fixture 작성기.

## 알려진 한계

- 패키지 레벨뿐. 타입/심볼 레벨 정점·간선은 아직 없다.
- `dead`/`rules` 명령 없음.
- `graph`는 매번 `packages.Load`를 새로 돌린다 — 영속 그래프 파일은 아직.
- Go 컴파일러가 패키지 순환을 막으므로 package 레벨 cycles는 정상적으로 빈다.
  실전 순환 검사는 type/symbol 레벨이 와야 한다.

## 다음 할 일 (우선순위 순)

1. **심볼/타입 수확** — `packages.Load`에 `NeedSyntax|NeedTypes|NeedTypesInfo` 추가,
   `go/ssa`+`callgraph/rta`로 call 간선(RTA — `x/tools/cmd/deadcode` 방식),
   타입 임베드·인터페이스 implements·레퍼런스 간선. 정점 ID 규약:
   `pkgpath.Name`, `pkgpath.(Recv).Name`. **주의: `source` 경계 안에서만.**
2. **`dead` 명령** — 보존 루트(`main`·`init`·`retain_public`일 때 공개 API)에서
   도달 집합을 계산하고 `state`(`reachable`/`unreachable`)+`reason`으로 보고.
   삭제 판정 금지. `--explain`으로 왜 살아 있는지 경로 출력.
3. **`rules` 명령** — 컴포넌트 매핑 + `mayDependOn` 설정 파일
   (YAML이면 `config` 패키지 신설 후 의존 격리).
4. **영속 산출물** — `graph -o .gartograph/graph.json` 쓰기/읽기 경로.
   Go에는 index store가 없으므로 이 문서가 그 역할.
5. **검증 스크립트** — `Scripts/coverage.sh`(커버리지 게이트, 계열 기준 90%),
   `Scripts/verify-cli-contract.sh`(종료 코드 계약).
6. **CI + 배포** — GitHub Actions, `go install` 경로 검증, Homebrew tap.
7. **isthmus 조인** — Go가 브리지(cgo/gomobile)를 타면 bridge facts producer.
   필요성 자체는 미정.

## 결정 기록

- **이름**: gograph는 같은 니치 도구 2개(ozgurcd, compozy)가 선점해 `gartograph`로 —
  계열 패턴(언어 이니셜+artograph) 그대로.
- **경쟁 지형**: ozgurcd/gograph·compozy/gograph·xdotech/goatlas 모두
  "Go 그래프→에이전트 MCP" 니치. 차별점은 계열의 판정 계약
  (삭제 판정 아닌 그래프 사실, limitations 실측, 다단계 레벨).
- **`--deps` 기본 off**: 외부 의존까지 담으면 정점이 폭증해 저장소 구조 질의가
  흐려진다. 생략 개수는 limitation으로.
