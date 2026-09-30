#!/bin/bash
# run.sh — 합성 Go 클라이언트에 gartograph routes --role client를 돌리고, 실제 요청을 기록한 모의 서버와 대조한다.
#
# 네트워크는 Go 모듈 프록시(proxy.golang.org)에서 resty v2를 받을 때만 쓴다(go.sum으로 고정). 요청은
# 모두 127.0.0.1의 기록 서버로 간다(HTTP_PROXY). 결과는 recorded/report.json에 결정적으로 쓴다
# (절대 경로·시각·포트 없음). 불일치가 있으면 1이다. CI에는 넣지 않는다 — 제품 테스트
# (source/clientoracle_test.go)가 커밋된 기록을 resty 스텁으로 네트워크 없이 다시 대조한다.
set -euo pipefail
cd "$(dirname "$0")"
export GOPROXY=https://proxy.golang.org GOFLAGS=-mod=mod

work="$(mktemp -d)"
cleanup() { rm -rf "$work"; }
trap cleanup EXIT

(cd ../.. && go build -o "$work/gartograph" ./cmd/gartograph)
"$work/gartograph" routes --role client --dir . --pattern ./fixtures/... --wrappers wrappers.json \
	--generated-at 2026-01-01T00:00:00Z --out "$work/calls.json"
mkdir -p recorded
go run ./cmd/oracle "$work/calls.json" > recorded/report.json
echo "client oracle OK: recorded/report.json"
