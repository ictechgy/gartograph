# gartograph

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
gartograph graph --level type --format mermaid
gartograph graph --level symbol --out .gartograph/graph.json

gartograph cycles --level symbol --strict     # 순환 검사 — 패키지 순환은 Go가 금지하므로
                                              # 실전 검사는 type/symbol 레벨
gartograph dead                               # main·init에서 도달 불가 심볼 보고
gartograph dead --retain-public               # 라이브러리: 공개 API 보존
gartograph dead --root my/pkg.Setup           # 추가 보존 루트
gartograph dead --explain my/pkg.F            # 왜 살아 있나 — 도달 경로 출력
gartograph rules --strict                     # .gartograph.yml 레이어 규칙 검사

gartograph query <정점ID> --depth 2            # 이웃 되묻기(에이전트용 JSON)
gartograph impact <정점ID> --depth 2           # 역방향 전이 — 바꾸면 뭐가 깨지나
gartograph mcp --level symbol                  # MCP stdio로 에이전트에 서빙
gartograph dead --graph .gartograph/graph.json # 저장 문서로 분석
```

공통 플래그: `--dir`(모듈 루트), `--pattern`(반복 가능), `--tests`(테스트
진입점이 보존 루트가 됨), `--deps`, `--tags`(빌드 태그; 제약으로 빠진 파일은
limitations에 셈), `--graph`(저장 문서 읽기).
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
signature:                # 공개 API 타입 누출 (심볼 레벨 필요)
  api:      ["core"]      # api의 공개 시그니처는 core 타입만 가리킬 수 있음
```

- `deny`는 `deps`를 이깁니다 — "보통 허용, 이 조합은 금지".
- `signature`는 **exported** 심볼의 `signature` 간선을 검사합니다 — 본문 의존은
  허용하면서 공개 API의 타입 누출만 막을 때 씁니다. 위반은 `rule` 필드로
  구분됩니다(`allow`/`deny`/`signature`).

패턴: 정확 일치, `x/**` 재귀 접두사, `*` 세그먼트 글롭.
`rules --format sarif`는 CI 코드 스캐닝용 SARIF 2.1.0을 냅니다.

## 출력 계약(에이전트용)

- 결정적 JSON — 같은 입력, 같은 바이트.
- `query`는 `depth`·`truncated`와 이웃 간선 종류 전부를 싣습니다.
- `dead`는 finding마다 `state`+`reason`을, 보고에 사용한 `roots`를 항상 싣습니다.
- `limitations`는 매 실행에서 실제로 세어 만듭니다(생략한 외부 참조 수,
  `reflect` 사용, `//go:linkname`) — 없으면 키가 빠집니다.
- 삭제 판정은 없습니다. `unreachable`은 그래프 사실이지 "지워도 됨"이 아닙니다.
  모듈 밖에서 선언된 인터페이스를 만족하는 메서드는 알려진 blind spot입니다 —
  해당하면 보고가 그 사실을 밝힙니다.

## 그래프 문서

정점 ID: 패키지는 `pkg/path`, 패키지 수준 심볼은 `pkg/path.Name`,
메서드는 `pkg/path.(Recv).Name`. `// Code generated ... DO NOT EDIT.`
마커 파일 출신 정점은 `generated: true`를 답니다 — 숨기지 않고 표시합니다.
간선 종류: `import`/`contains`/`embeds`/`implements`/`references`/`call`/
`signature`(선언 시그니처 안의 타입 참조 — `contains`와 달리 의존 관계).
인터페이스 호출은 CHA 팬아웃으로 인터페이스 메서드와 모든 구현 메서드에
간선을 긋습니다 — 과대 근사는 "살아 있다" 쪽으로만 기울어 `dead`가
도달 가능 코드를 오판하지 않습니다.

## MCP 서버

`gartograph mcp`는 수확한 문서를 MCP stdio(개행 구분 JSON-RPC)로 서빙합니다:
`gartograph_summary`, `gartograph_query`, `gartograph_impact`,
`gartograph_cycles`, `gartograph_dead`, `gartograph_rules`. 문서는 기동 시
한 번 수확해 모든 호출이 같은 스냅샷 위에서 답합니다.

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
