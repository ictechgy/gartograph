# gartograph

Go 의존성 그래프 도구 — Go 모듈을 읽어 의존성 그래프를 만들고, 그 위에서
순환·도달성·심볼 이웃·레이어 규칙을 질의합니다.

설계는 한 문장입니다. **그래프가 산출물이고, 나머지는 전부 그 위의 질의입니다.**

자매 프로젝트: cartograph(Swift) · kartograph(Kotlin/Android) ·
dartograph(Dart/Flutter) · schemagraph(DB) · isthmus(언어 경계 조인).

## 상태

동작하는 코어. 패키지/타입/심볼 세 레벨 그래프와 `graph`·`cycles`·`dead`·
`rules`·`query` 명령, 영속 그래프 문서(`--out`/`--graph`).
모듈 레벨·검증 스크립트·CI·배포는 로드맵 — [HANDOFF.md](HANDOFF.md) 참고.

## 설치

```bash
go install github.com/ictechgy/gartograph/cmd/gartograph@latest
```

## 사용

```bash
gartograph graph                              # 패키지 그래프(JSON, 결정적)
gartograph graph --level symbol               # 심볼: call/implements/embeds/references
gartograph graph --level type --format mermaid
gartograph graph --level symbol --out .gartograph/graph.json

gartograph cycles --level symbol --strict     # 순환 검사 — 패키지 순환은 Go가 금지하므로
                                              # 실전 검사는 type/symbol 레벨
gartograph dead                               # main·init에서 도달 불가 심볼 보고
gartograph dead --retain-public               # 라이브러리: 공개 API 보존
gartograph dead --explain my/pkg.F            # 왜 살아 있나 — 도달 경로 출력
gartograph rules --strict                     # .gartograph.yml 레이어 규칙 검사

gartograph query <정점ID> --depth 2            # 이웃 되묻기(에이전트용 JSON)
gartograph dead --graph .gartograph/graph.json # 저장 문서로 분석
```

공통 플래그: `--dir`(모듈 루트), `--pattern`(반복 가능), `--tests`, `--deps`,
`--graph`(저장 문서 읽기).
종료 코드: `0` 정상 · `1` strict 위반 · `2` 사용법/분석 오류.

## 규칙 설정

`gartograph rules`는 모듈 루트의 `.gartograph.yml`을 읽습니다. `components`는
모듈 상대 패키지 경로를 매핑하고, `deps`는 **허용 목록**입니다 — 항목이 없는
컴포넌트는 자기 자신 외에 아무것도 의존할 수 없습니다. 어느 컴포넌트에도
속하지 않은 패키지는 `unmapped`로 보고됩니다.

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
메서드는 `pkg/path.(Recv).Name`. 인터페이스 호출은 CHA 팬아웃으로
인터페이스 메서드와 모든 구현 메서드에 간선을 긋습니다 — 과대 근사는
"살아 있다" 쪽으로만 기울어 `dead`가 도달 가능 코드를 오판하지 않습니다.

## 라이선스

MIT — [LICENSE](LICENSE).
