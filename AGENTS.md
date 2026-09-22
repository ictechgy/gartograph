# AGENTS.md

이 저장소에서 작업하는 코딩 에이전트를 위한 안내입니다.
도구 사용법은 [README.md](README.md)를 보세요.

> This file is written in Korean because it is the maintainer's working language.
> For the project overview in English, see [README.md](README.md).

**지금 어디까지 왔고 다음이 무엇인지는 [HANDOFF.md](HANDOFF.md)에 있습니다.**
세션을 이어받을 때 먼저 읽으세요.

자매 프로젝트가 바탕화면에 있습니다: [cartograph](../cartograph)(Swift) ·
[kartograph](../kartograph)(Kotlin/Android) · [dartograph](../dartograph)(Dart/Flutter) ·
[schemagraph](../schemagraph)(DB) · [isthmus](../isthmus)(언어 경계 조인).
`query` 출력 스키마와 출력 철학(`state`/`edges`/`limitations`)은 계열과 같게 유지합니다.
브리지 교환이 생기면 `../isthmus/docs/GRAPH-EXCHANGE.md`가 정본입니다.

---

## 이 프로젝트가 하는 일

Go 저장소를 읽어 의존성 그래프를 만들고, 그 위에서 순환(cycles)·미사용(dead)·
레이어 규칙(rules)·심볼 되묻기(query)를 질의합니다.

핵심 설계는 한 문장입니다. **그래프가 산출물이고, 나머지는 전부 그 위의 질의입니다.**
새 기능을 넣을 때 "이것도 그래프 질의로 표현되는가"를 먼저 물어보세요.

Go에는 Swift의 index store 같은 컴파일러 산출 영속 인덱스가 없습니다.
그래서 우리의 `graph.Document` 자체가 그 산출물 역할을 합니다 — 결정성과
버전ed 형식이 계약인 이유입니다.

## 명령

```bash
go build ./...            # 빌드
go test ./...             # 전체 테스트
go test ./analysis -run TestCycles   # 특정 테스트만
go vet ./...              # 정적 검사

go run ./cmd/gartograph graph                     # 패키지 그래프(JSON)
go run ./cmd/gartograph graph --format mermaid    # Mermaid
go run ./cmd/gartograph cycles --strict           # 순환 검사(위반 시 종료 1)
go run ./cmd/gartograph query <정점ID> --depth 2  # 이웃 되묻기(JSON)
```

완료를 보고할 때는 빌드·vet·테스트 통과와 **자기 분석**(이 도구로 이 저장소를
돌려보는 것)의 출력 근거가 있어야 합니다. 입력이 바뀌지 않은 검사는 기존 근거를
재사용합니다. 문서만 바꾸면 구조·링크·적용 범위를 검증하고, 제품 검사를 새로
실행했다고 쓰지 마세요.

**순환 검사는 패키지 레벨만으로 끝내지 않습니다.** Go 컴파일러가 패키지 순환
import를 막으므로, 패키지 레벨 "순환 없음"은 빈 결과가 정상입니다. 타입·심볼
레벨이 실전이고, 그 레벨이 아직 없다는 것을 결과에 밝히세요.

## 절대 하지 말 것

- **`graph` 패키지에 외부 의존성을 추가하지 마세요.** 도메인이 순수해야
  분석·출력 계층 전체를 go/packages 없이 파일 fixture로 테스트할 수 있습니다.
- **`golang.org/x/tools`를 `source` 밖에서 import 하지 마세요.** 수확 기술이
  새 나가면 같은 저장소가 어디서 스캔됐냐에 따라 다른 그래프가 됩니다.
  수확은 원문을 옮기기만 하고, 판정·의미론은 `analysis`에 둡니다.
- **JSON 출력을 비결정적으로 만들지 마세요.** 내보내기 전에 `Document.Sort`.
  같은 입력이 매번 다른 파일이 되면 리포트 diff와 캐시가 무의미해집니다.
- **삭제 판정을 내지 마세요.** `unreachable`은 "보존 루트에서 도달할 수 없다"는
  그래프 사실이지 "지워도 된다"가 아닙니다. Go에도 `main()`이 없는 라이브러리가
  있고, 공개 API는 저장소 안에 호출자가 없어도 살아 있을 수 있습니다.
  확신이 없으면 살리는 쪽을 고르고 이유를 남기세요.
- **`limitations`를 장식으로 쓰지 마세요.** 생략한 외부 import 수, 로드 오류 수
  — 그 저장소에서 **실제로 세어서** 만듭니다. 알릴 것이 없으면 조용해야 합니다.
- **유령 정점을 만들지 마세요.** 간선은 양쪽 정점이 문서에 있을 때만 씁니다.
  끝이 없는 간선은 소비자가 존재하지 않는 코드를 찾게 만듭니다.
- **`contains`를 의존으로 섞지 마세요.** 소유(담기) 관계와 의존 관계는 다른
  사실입니다. 섞으면 dependsOn이 "잘못 채워져" 소비자를 오도합니다.
- **커버리지 숫자를 올리려고 아무것도 검증하지 않는 테스트를 쓰지 마세요.**
  CLI와 수확 입출력은 실제 fixture 모듈로 검증합니다(`internal/testutil`).

## 에이전트가 소비하는 출력

`query`와 JSON 리포트는 사람이 아니라 코딩 에이전트가 읽는다고 전제합니다.

- **잘렸으면 잘렸다고**(`truncated`), **몇 걸음인지**(`depth`), **어느 레벨인지**(`level`) 씁니다.
- **이웃에 닿는 간선은 전부 줍니다**(`edges: ["call", "implements"]`).
- **없는 선택 필드는 키가 빠집니다**(`omitempty`).
- **없는 것과 못 본 것을 구분합니다.** `notFound`는 정점이 문서에 없다는
  사실이고, limitation은 도구가 보지 못한 영역입니다.
- 종료 코드 계약: `0` 정상, `1` `--strict` 위반, `2` 사용법·분석 오류.

## Go 특화 주의점

- **`flag` 패키지는 첫 비플래그 인자에서 파싱을 멈춥니다.** `query <id> --dir .`
  같은 호출이 동작하려면 `parseInterspersed`처럼 positional을 수집하며
  재파싱해야 합니다.
- **`go.mod`가 없는 디렉터리와 빌드 태그 뒤의 파일은 `packages.Load`가
  다른 패키지 집합을 돌려줍니다.** `--pattern`과 `--dir`를 명시해서
  수확 범위를 확정하세요.
- **`Tests: true`는 외부 테스트 패키지(`x_test`)를 별도 정점으로 만듭니다.**
  이들은 원 패키지를 import하므로 패키지 레벨에서 가짜 순환처럼 보일 수
  있습니다. 켤 때는 그 사실을 결과에 남기세요.
- **`Module.Main`이 없는 패키지는 정점 필터의 기준이 됩니다.** std 패키지는
  `Module == nil`입니다. 외부 의존의 정점 포함 여부는 `--deps`로 소비자가
  선택하게 하고, 생략한 개수는 limitation으로 남기세요.

## 커밋

Conventional Commits, 본문은 한국어. 스코프는 패키지 이름을 씁니다
(`graph`, `source`, `analysis`, `export`, `cli`).

```
feat(analysis): 타입 레벨 implements 간선 수확 추가
fix(source): --deps에서 테스트 변형 패키지가 중복 정점이 되던 문제 수정
```

커밋은 작고 한 가지 목적만 담습니다. 본문에는 *왜*를 쓰세요.
`main`에 직접 커밋하지 말고 `feature/…`, `fix/…`, `refactor/…` 브랜치에서
작업하세요.

## 코드 스타일

- gofmt가 형식의 정본입니다. 주석은 한국어, 식별자는 영어.
  사용자에게 보이는 출력 문자열은 영어(오픈소스 대상).
- 모든 export 타입·함수에 문서 주석. *무엇을*이 아니라 *왜*를 적으세요.
- 함수는 하나의 역할만. 본문이 길어지면 분리를 검토하세요.
- 빈 error 무시 금지. 오류 메시지에는 원인과 해결 방향을 함께 담습니다.
