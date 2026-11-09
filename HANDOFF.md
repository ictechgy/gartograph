# HANDOFF

세션 이어받기용 상태 파일이다. 지금 어디까지 왔고 다음이 무엇인지만 적는다. 작업 규칙은
[AGENTS.md](AGENTS.md), 이전 세션 원문(v0.2~v0.11 준비 기록·결정의 경위)은 [HANDOFF-HISTORY.md](HANDOFF-HISTORY.md)에
있다. 이력의 "진행 중"·"다음 할 일"·수치는 당시 기록이며 현재 지시가 아니다.

## 현재 상태 (2026-11-09)

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
- 자기 분석 `dead`는 `--strict` 없이 근거만 본다(CI도 같다). 2026-11-03 기준 12건 — 테스트에서만 쓰는 공개
  함수(`analysis.DependencyReachable` 등), `internal/testutil`, 직렬화 태그 필드다. 지우라는 뜻이 아니다.
  수가 늘면 테스트 전용 코드가 제품 파일에 들어온 것인지 먼저 본다(스코프 적용 오라클은 그래서
  `source/routescope_applies_test.go`에 있다). `rules --strict`·`cycles --level type/symbol --strict`는 0이다.

- 위 항목은 #55(`dcc4189`, 2026-11-04 squash 머지)로 main에 들어갔다. 독립 Claude 리뷰·재검증을 거쳤고
  (AGENTS.md의 GLM 리뷰는 `packet-ask`가 없는 환경이라 실행하지 못했다), 함수 안 인터페이스 식별 수정과 Windows
  순회 위치 수정(`filepath.IsLocal`)도 함께 들어갔다. 함수 안 `V`가 패키지 `V`를 가리던 거짓 간선으로만 살아 있던
  메서드는 이제 보고된다 — main 대비 보존이 좁아지는 유일한 경우이며 CHANGELOG에 밝혔다.

## 남은 일 (사용자가 고른다)

- 공개 API 보존 기본값(`--retain-public` 기본 꺼짐)이 dartograph(기본 보존)와 다르다 — 의도라면 문서로 남긴다.
- `http-limitation-scope`의 `scope.dynamic-*`: gartograph가 `dynamicScope`를 내지 않아 건너뛴다.
- 릴리스(0.12.0)와 tap 갱신은 승인된 별도 작업이다. 새 자격 증명은 만들지 않는다.

## 주의

- linkname 루트는 "살린다" 쪽 과대 근사다 — 본문 없는 쪽이 죽었어도 묶인 구현은 루트다(RTA가 본문 없는
  선언에서 멈추기 때문). 좁히려면 RTA에 linkname 간선 전파를 넣어야 한다.
- 모듈 안 이름 없는 인터페이스 팬아웃은 단언한 메서드 집합을 만족하는 **모든** 모듈 구체 타입으로 퍼진다(CHA).
  RTA는 인터페이스로 변환된 타입의 메서드를 따로 살리므로 대조군 결과가 CHA와 다를 수 있다.
