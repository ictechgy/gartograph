#!/bin/bash
# coverage.sh — 테스트 + 커버리지 게이트(기준 90%).
# -coverpkg로 크로스 패키지 실행을 인정한다 — cli/analysis 테스트가
# graph 코드를 실제로 돌리는 것을 빼면 단위 커버리지가 과소 측정된다.
# 사용: Scripts/coverage.sh [--report]
set -euo pipefail
cd "$(dirname "$0")/.."

GATE=90
OUT=coverage/cover.out
mkdir -p coverage
trap 'rm -f "$OUT"' EXIT

PKGS=./graph,./source,./analysis,./config,./export,./cli
go test -coverpkg="$PKGS" -coverprofile="$OUT" ./...

TOTAL=$(go tool cover -func="$OUT" | awk '/^total:/ {gsub(/%/,"",$3); print $3}')
if [ "${1:-}" = "--report" ]; then
	go tool cover -func="$OUT" | grep -v '^github.*100.0%'
fi

echo "total coverage: ${TOTAL}% (gate ${GATE}%)"
awk -v t="$TOTAL" -v g="$GATE" 'BEGIN{exit (t+0 >= g+0) ? 0 : 1}' || {
	echo "coverage below gate" >&2
	exit 1
}
