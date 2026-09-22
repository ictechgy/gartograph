# gartograph

Go 의존성 그래프 도구 — Go 모듈을 읽어 의존성 그래프를 만들고, 그 위에서
순환·도달성·심볼 이웃·레이어 규칙을 질의합니다.

설계는 한 문장입니다. **그래프가 산출물이고, 나머지는 전부 그 위의 질의입니다.**

자매 프로젝트: cartograph(Swift) · kartograph(Kotlin/Android) ·
dartograph(Dart/Flutter) · schemagraph(DB) · isthmus(언어 경계 조인).

## 상태

초기 스캐폴드. 패키지 레벨 import 그래프와 `graph`·`cycles`·`query` 명령.
타입/심볼 레벨과 `dead`·`rules`는 로드맵 — [HANDOFF.md](HANDOFF.md) 참고.

## 설치

```bash
go install github.com/ictechgy/gartograph/cmd/gartograph@latest
```

## 사용

```bash
gartograph graph                    # 패키지 그래프(JSON, 결정적)
gartograph graph --format mermaid   # Mermaid
gartograph cycles --strict          # 순환 검사(위반 시 종료 1)
gartograph query <정점ID> --depth 2 # 이웃 되묻기(에이전트용 JSON)
```

공통 플래그: `--dir`(모듈 루트), `--pattern`(반복 가능), `--tests`, `--deps`.
종료 코드: `0` 정상 · `1` strict 위반 · `2` 사용법/분석 오류.

## 출력 계약(에이전트용)

- 결정적 JSON — 같은 입력, 같은 바이트.
- `query`는 `depth`·`truncated`와 이웃 간선 종류 전부를 싣습니다.
- `limitations`는 매 실행에서 실제로 세어 만듭니다 — 없으면 키가 빠집니다.
- 삭제 판정은 없습니다. `unreachable`은 그래프 사실이지 "지워도 됨"이 아닙니다.

## 라이선스

MIT — [LICENSE](LICENSE).
