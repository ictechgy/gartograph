#!/bin/bash
# verify-workflow-hardening.sh — GitHub 워크플로의 공급망 강화 불변식을 검사한다.
#
# 아래 셋 중 하나라도 어긋나면 위치·원인·고칠 방향을 출력하고 1로 끝난다.
#   1. 모든 uses가 40자 커밋 SHA와 버전 주석(owner/repo@<sha> # vX.Y.Z)으로 고정돼 있다.
#      태그와 브랜치는 상류에서 옮길 수 있어, 오염된 코드가 다음 실행에 그대로 들어온다.
#   2. 모든 actions/checkout이 with: persist-credentials: false를 준다.
#      기본값(true)은 잡 토큰을 git 설정(v6부터는 설정이 가리키는 임시 파일)에 남겨,
#      뒤 단계에서 도는 코드가 읽을 수 있다.
#   3. ci.yml의 최상위 permissions는 contents: read 하나뿐이고, 어느 잡도 write를 받지 않는다.
#      ci는 PR 코드를 돌리므로 저장소 기본 토큰 권한에 기대지 않는다.
#
# 특정 SHA나 버전은 적지 않는다. Dependabot이 SHA와 버전 주석을 함께 올려도 그대로 통과해야 한다.
# 러너에 YAML 도구가 없어도 돌도록 bash만으로 줄 단위로 읽는다. 대신 읽지 못하는 표기는
# 통과시키지 않고 실패로 알린다.
# 사용: Scripts/verify-workflow-hardening.sh [저장소 루트]   (기본값: 이 스크립트의 상위 디렉터리)
set -euo pipefail

root=${1:-$(cd "$(dirname "$0")/.." && pwd)}

# 정규식은 변수에 담아 bash 3.2(macOS /bin/bash)와 5.x에서 같은 뜻으로 읽히게 한다.
re_blank='^[[:space:]]*(#.*)?$'
re_item='^-([[:space:]]+(.*))?$'
re_key='^([A-Za-z0-9_-]+):([[:space:]]+(.*))?$'
re_block='^[|>][-+0-9]*([[:space:]]+#.*)?$'
re_pinned='^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}[[:space:]]+#[[:space:]]*v[0-9]'
re_local='^\./'
re_stray='[[:space:]{,]uses:'
re_inline_persist="[{,][[:space:]]*persist-credentials:[[:space:]]*[\"']?false[\"']?[[:space:]]*[,}]"
re_flow_read='^\{[[:space:]]*contents:[[:space:]]*read[[:space:]]*\}$'

failures=0 workflows=0 references=0 checkouts=0 ci_seen=0

# report <파일> <줄> <원인과 고칠 방향> — 위반을 모두 모아 마지막에 한 번에 실패시킨다.
report() {
  printf '%s:%s: %s\n' "${1#"$root"/}" "$2" "$3" >&2
  failures=$((failures + 1))
}

# set_plain <값> — 끝 주석·공백과 감싼 따옴표를 뗀 비교용 값을 plain에 둔다.
set_plain() {
  case $1 in '#'*) plain=''; return 0;; esac
  plain=${1%%[[:space:]]#*}
  plain=${plain%"${plain##*[![:space:]]}"}
  plain=${plain#[\"\']}
  plain=${plain%[\"\']}
}

# check_uses — uses 값이 커밋 SHA와 버전 주석으로 고정됐는지 본다.
# 저장소 안 액션(./)은 체크아웃한 커밋에 묶여 있어 고정할 대상이 아니다.
check_uses() {
  references=$((references + 1))
  if [[ $value =~ $re_pinned ]] || [[ $value =~ $re_local ]]; then return 0; fi
  report "$file" "$lineno" "'uses: ${value:-<empty>}' is not pinned. Use owner/repo@<40-character commit SHA> # vX.Y.Z; tags and branches can be moved upstream, so a compromised release would run on the next build."
}

# check_stray_uses — 키로 읽지 못한 자리(흐름 표기 등)의 uses는 고정 여부를 볼 수 없다.
check_stray_uses() {
  local bare=" ${text%%[[:space:]]#*}"
  if [[ $bare =~ $re_stray ]]; then
    report "$file" "$lineno" "'uses:' appears in a form this script cannot read (for example a flow mapping). Write the step in block style so its pin can be checked."
  fi
}

# track_step — 단계 바로 아래 키와 with 안의 persist-credentials를 기록한다.
track_step() {
  if [ "$item" -lt 0 ]; then return 0; fi
  if [ "$indent" -eq "$item_key" ]; then
    item_field=$key
    if [ "$key" = uses ]; then
      # GitHub는 owner/repo 대소문자를 가리지 않으므로 소문자로 맞춰 비교한다.
      lower=$(printf '%s' "$value" | tr '[:upper:]' '[:lower:]')
      if [[ $lower == actions/checkout@* ]]; then
        item_checkout=1
        checkouts=$((checkouts + 1))
      fi
    elif [ "$key" = with ] && [[ $value =~ $re_inline_persist ]]; then
      item_persist=1
    fi
  elif [ "$indent" -gt "$item_key" ] && [ "$item_field" = with ] && [ "$key" = persist-credentials ]; then
    set_plain "$value"
    if [ "$plain" = false ]; then item_persist=1; fi
  fi
}

# close_item — 끝난 단계가 checkout이면 자격 증명을 남기지 않는지 판정한다.
close_item() {
  if [ "$item_checkout" = 1 ] && [ "$item_persist" = 0 ]; then
    report "$file" "$item_line" "actions/checkout must set 'with: persist-credentials: false'. The default keeps the job token reachable from the checkout's git config (v6 and later store it in a file that config includes), where every later step, including pull request code, can read it."
  fi
  item=-1 item_key=-1 item_field='' item_checkout=0 item_persist=0
}

# track_permissions — ci.yml이 받는 토큰 권한을 모은다. 최상위는 contents: read 하나여야 하고,
# 잡 단위 permissions는 최상위를 덮어쓰므로 write를 줄 수 없다.
track_permissions() {
  if [ "$is_ci" = 0 ]; then return 0; fi
  set_plain "$value"
  if [ "$perm" -ge 0 ]; then
    if [ "$perm_top" = 1 ]; then
      perm_entries="$perm_entries$key: $plain;"
    elif [ "$plain" = write ]; then
      report "$file" "$lineno" "ci.yml job permissions grant '$key: write'. ci runs pull request code, so keep every job read-only and move write work to another workflow."
    fi
    return 0
  fi
  if [ "$key" != permissions ]; then return 0; fi
  if [ "$indent" -eq 0 ]; then
    top_permissions=1
    if [ -z "$plain" ]; then
      perm=0 perm_line=$lineno perm_top=1 perm_entries=''
    elif ! [[ $plain =~ $re_flow_read ]]; then
      report "$file" "$lineno" "ci.yml top-level permissions must be exactly 'contents: read' (found '$plain'). Every ci job only reads the repository."
    fi
  elif [ -z "$plain" ]; then
    perm=$indent perm_line=$lineno perm_top=0
  elif [[ $plain == *write* ]]; then
    report "$file" "$lineno" "ci.yml job permissions '$plain' grant write access. ci runs pull request code, so keep every job read-only and move write work to another workflow."
  fi
}

# close_permissions — 끝난 최상위 permissions 블록이 contents: read 하나뿐인지 판정한다.
close_permissions() {
  if [ "$perm_top" = 1 ] && [ "$perm_entries" != 'contents: read;' ]; then
    local found=${perm_entries%;}
    report "$file" "$perm_line" "ci.yml top-level permissions must be exactly 'contents: read' (found '${found//;/, }'). Every ci job only reads the repository, and wider scopes would reach pull request code."
  fi
  perm=-1 perm_top=0 perm_entries=''
}

# check_line — 들여쓰기(indent)와 본문(text)이 정해진 한 줄을 판정한다.
check_line() {
  key='' value=''
  if [[ $text =~ $re_key ]]; then key=${BASH_REMATCH[1]} value=${BASH_REMATCH[3]-}; fi
  # run: | 같은 블록 스칼라 본문은 셸 스크립트라 다음 줄부터 키로 읽지 않는다.
  if [[ $value =~ $re_block ]]; then block=$indent; fi
  if [ "$key" = uses ]; then check_uses; else check_stray_uses; fi
  track_step
  track_permissions
}

# check_workflow <파일> — 워크플로 하나를 줄 단위로 읽으며 세 불변식을 확인한다.
# 단계는 "- "로 시작하는 목록 항목이고, 그보다 깊게 들여 쓴 줄이 끝날 때까지 이어진다.
check_workflow() {
  file=$1 lineno=0 block=-1 top_permissions=0 is_ci=0
  item=-1 item_key=-1 item_line=0 item_field='' item_checkout=0 item_persist=0
  perm=-1 perm_line=0 perm_top=0 perm_entries=''
  if [ "${file##*/}" = ci.yml ]; then is_ci=1 ci_seen=1; fi
  while IFS= read -r line || [ -n "$line" ]; do
    lineno=$((lineno + 1))
    line=${line%$'\r'}
    if [[ $line =~ $re_blank ]]; then continue; fi
    text=${line#"${line%%[! ]*}"}
    indent=$((${#line} - ${#text}))
    if [ "$block" -ge 0 ]; then
      if [ "$indent" -gt "$block" ]; then continue; fi
      block=-1
    fi
    if [ "$item" -ge 0 ] && [ "$indent" -le "$item" ]; then close_item; fi
    if [ "$perm" -ge 0 ] && [ "$indent" -le "$perm" ]; then close_permissions; fi
    if [[ $text =~ $re_item ]]; then
      rest=${BASH_REMATCH[2]-}
      if [ "$item" -lt 0 ]; then item=$indent item_line=$lineno; fi
      indent=$((indent + ${#text} - ${#rest}))
      text=$rest
      if [ -z "$text" ]; then continue; fi
    fi
    if [ "$item" -ge 0 ] && [ "$item_key" -lt 0 ]; then item_key=$indent; fi
    check_line
  done < "$file"
  close_item
  close_permissions
  if [ "$is_ci" = 1 ] && [ "$top_permissions" = 0 ]; then
    report "$file" 1 "ci.yml must declare top-level 'permissions: contents: read'. Without it the token falls back to the repository default, which may grant write access to pull request code."
  fi
}

dir="$root/.github/workflows"
for file in "$dir"/*.yml "$dir"/*.yaml; do
  if [ -f "$file" ]; then
    workflows=$((workflows + 1))
    check_workflow "$file"
  fi
done

# 아무것도 읽지 못한 채 통과하면 검사가 고장 난 것과 구별되지 않는다.
if [ "$workflows" -eq 0 ]; then
  report "$dir" 0 "no workflow files found. Pass the repository root as the first argument."
else
  if [ "$ci_seen" = 0 ]; then
    report "$dir" 0 "ci.yml not found, so the read-only token check verified nothing. Update this script if the workflow was renamed."
  fi
  if [ "$references" -eq 0 ] || [ "$checkouts" -eq 0 ]; then
    report "$dir" 0 "read $references uses references and $checkouts checkout steps. The line reader no longer matches the workflow layout; update this script before trusting a pass."
  fi
fi
if [ "$failures" -gt 0 ]; then
  printf '%s workflow hardening violation(s) found.\n' "$failures" >&2
  exit 1
fi
printf 'Workflow hardening verified: %s workflows, %s pinned uses references, %s checkout steps without persisted credentials.\n' \
  "$workflows" "$references" "$checkouts"
