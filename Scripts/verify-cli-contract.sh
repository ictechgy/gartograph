#!/bin/bash
# verify-cli-contract.sh — 빌드된 바이너리로 종료 코드 계약(0/1/2)을 검증한다.
# 인자 없으면 go build로 새 바이너리를 만든다 — 낡은 바이너리로 검증해
# "수정이 안 먹는다"를 오해하는 사고를 막기 위함이다(cartograph 교훈).
set -euo pipefail
cd "$(dirname "$0")/.."

BIN="${1:-}"
if [ -z "$BIN" ]; then
	BIN="$(mktemp -d)/gartograph"
	go build -o "$BIN" ./cmd/gartograph
fi
trap 'rm -rf "$(dirname "$BIN")" "$FIX"' EXIT

# fixture: main → lib.Run 도달, lib.Unused 미도달, web→db 금지 의존
FIX="$(mktemp -d)"
cat > "$FIX/go.mod" <<'EOF'
module example.com/contract

go 1.27
EOF
cat > "$FIX/main.go" <<'EOF'
package main

import "example.com/contract/lib"

func main() { lib.Run() }
EOF
mkdir -p "$FIX/lib" "$FIX/web" "$FIX/db"
cat > "$FIX/lib/lib.go" <<'EOF'
package lib

func Run() {}
func Unused() {}
EOF
cat > "$FIX/web/web.go" <<'EOF'
package web

import _ "example.com/contract/db"
EOF
cat > "$FIX/db/db.go" <<'EOF'
package db
EOF
cat > "$FIX/.gartograph.yml" <<'EOF'
components:
  web: ["web"]
  db: ["db"]
deps: {}
EOF

fails=0
check() { # check <기대 코드> <설명> <인자...>
	local want="$1" desc="$2"; shift 2
	local got=0
	# || 로 잡아야 set -e가 비0 종료에서 스크립트를 죽이지 않는다 —
	# 종료 코드 1 자체가 검증 대상이다.
	"$BIN" "$@" --dir "$FIX" >/dev/null 2>&1 || got=$?
	if [ "$got" -ne "$want" ]; then
		echo "FAIL $desc: expected $want, got $got" >&2
		fails=$((fails+1))
	fi
}

check 0 "graph"            graph
check 0 "graph symbol"     graph --level symbol
check 0 "cycles strict"    cycles --level symbol --strict
check 1 "dead strict"      dead --strict
check 0 "dead retain"      dead --retain-public
check 1 "rules strict"     rules --strict
check 0 "query"            query example.com/contract/lib
check 2 "query missing"    query example.com/missing
check 0 "impact"           impact example.com/contract/lib
check 2 "impact missing"   impact example.com/missing
check 0 "rules sarif"      rules --format sarif
check 2 "unknown command"  frobnicate
check 2 "usage"            ""
check 2 "bad format"       cycles --format xml
check 2 "bad rules format" rules --format xml
check 2 "bad level"        graph --level bogus
check 2 "level mismatch"   cycles --graph /dev/null
check 0 "tags flag"        graph --tags customtag

if [ "$fails" -gt 0 ]; then
	echo "$fails contract checks failed" >&2
	exit 1
fi
echo "cli contract OK"
