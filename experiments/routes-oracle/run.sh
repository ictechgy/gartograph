#!/bin/bash
# run.sh — 합성 fixture에 gartograph routes를 돌리고 실제 라우터 오라클로 정밀도·재현율을 잰다.
#
# 네트워크는 Go 모듈 프록시(proxy.golang.org)에서 chi·gin·echo를 받을 때만 쓴다(go.sum으로 고정).
# 결과는 recorded/report.json에 결정적으로 쓴다(절대 경로·시각 없음). 정밀도나 재현율이 1 미만이면 1이다.
# CI에는 넣지 않는다 — 제품 테스트는 네트워크 없이 스텁 모듈로 돈다(source/routes_test.go).
set -euo pipefail
cd "$(dirname "$0")"
export GOPROXY=https://proxy.golang.org GOFLAGS=-mod=mod

work="$(mktemp -d)"
cleanup() { rm -rf "$work"; }
trap cleanup EXIT

(cd ../.. && go build -o "$work/gartograph" ./cmd/gartograph)
"$work/gartograph" routes --role server --dir . --pattern ./fixtures/... \
	--generated-at 2026-01-01T00:00:00Z --out "$work/routes.json"
mkdir -p recorded
go run ./cmd/oracle "$work/routes.json" > recorded/report.json
echo "oracle OK: recorded/report.json"
