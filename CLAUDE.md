# CLAUDE.md

Claude Code로 이 저장소에서 작업할 때의 안내입니다.

**작업 규칙의 정본은 [AGENTS.md](AGENTS.md)입니다. 먼저 읽으세요.**
**세션을 이어받는다면 [HANDOFF.md](HANDOFF.md)부터 읽으세요.**

---

## 세션을 시작할 때

`git status --short --branch`로 브랜치를 확인하세요. `main`에서는 작업하지 않습니다.

```bash
go build ./... && go test ./...
```

## 완료를 주장하기 전에

아래를 **실제로 실행하고 출력을 확인한 뒤에만** 완료라고 말하세요.

```bash
go vet ./... && go test ./...
go run ./cmd/gartograph cycles --strict   # 자기 분석(도그푸딩)
```

두 번째가 이 도구로 이 저장소를 분석하는 단계입니다. 계열 규칙상
"이 도구가 실제로 발견한 결함은 여기서만 드러났다"는 전제로 유지합니다.

`go test ./...`는 패키지별 결과를 따로 출력합니다. 마지막 줄만 보면
다른 패키지의 실패를 놓칩니다. `FAIL`/`ok` 줄을 함께 보세요.
