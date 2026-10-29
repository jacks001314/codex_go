#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: stability.sh [suite] [duration] [--json] [--shard i/n] [--race] [--trend]

suite:    smoke|soak|large|faults|ui|memory|disk|all   (default: smoke)
duration: soak duration, e.g. 2h                      (default: 2h)
EOF
}

suite="smoke"
duration="2h"
json=0
race=0
trend=0
shard=""

positional=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --json) json=1; shift ;;
    --race) race=1; shift ;;
    --trend) trend=1; shift ;;
    --shard) shard="${2:-}"; shift 2 ;;
    --duration) duration="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
    *)
      positional=$((positional + 1))
      if [[ $positional -eq 1 ]]; then suite="$1"; else duration="$1"; fi
      shift
      ;;
  esac
done

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

unset TERM || true
export GOCACHE="${GOCACHE:-$root/.gocache}"
export CODEX_STABILITY_DURATION="$duration"
export CODEX_STABILITY_REPORT_DIR="${CODEX_STABILITY_REPORT_DIR:-$root/stability/reports}"
if [[ -z "${CODEX_STABILITY_COMMIT:-}" ]]; then
  CODEX_STABILITY_COMMIT="$(git -C "$root" rev-parse --short HEAD 2>/dev/null || true)"
  export CODEX_STABILITY_COMMIT
fi

# Scenarios live in two places: the black-box package, plus the packages that
# own private helpers. Only stability-shaped test names run in those packages.
case "$suite" in
  smoke)  packages="./stability/scenarios";                              run='TestMockModel|TestMetricsDiskAndReport' ;;
  soak)   packages="./stability/scenarios";                              run='TestSoak' ;;
  large)  packages="./stability/scenarios ./memories";                   run='TestLarge' ;;
  faults) packages="./stability/scenarios ./turn ./state";                 run='TestFault' ;;
  ui)     packages="./stability/scenarios ./tui/tea";                    run='TestUI' ;;
  memory) packages="./stability/scenarios";                              run='TestMemory' ;;
  disk)   packages="./stability/scenarios ./state";                      run='TestDisk' ;;
  all)    packages="./stability/scenarios ./memories ./tui/tea ./state ./turn"; run='^(TestSoak|TestLarge|TestFault|TestUI|TestMemory|TestDisk|TestMockModel|TestMetricsDiskAndReport)' ;;
  *) echo "unknown stability suite: $suite" >&2; exit 2 ;;
esac

timeout="2h"
if [[ "$suite" == "soak" || "$suite" == "all" ]]; then
  timeout="6h"
fi

if [[ -n "$shard" ]]; then
  if [[ ! "$shard" =~ ^([0-9]+)/([0-9]+)$ ]]; then
    echo "--shard must look like 1/3, got '$shard'" >&2
    exit 2
  fi
  index="${BASH_REMATCH[1]}"
  count="${BASH_REMATCH[2]}"
  if (( count < 1 || index < 1 || index > count )); then
    echo "--shard index must be between 1 and $count" >&2
    exit 2
  fi
  names=()
  for package in $packages; do
    while IFS= read -r name; do
      [[ "$name" == Test* ]] && names+=("$name")
    done < <(go test -tags stability -list "$run" "$package")
  done
  if (( ${#names[@]} == 0 )); then
    echo "no tests matched '$run' in $packages" >&2
    exit 2
  fi
  mapfile -t names < <(printf '%s\n' "${names[@]}" | sort -u)
  selected=()
  position=0
  for name in "${names[@]}"; do
    if (( position % count == index - 1 )); then
      selected+=("$name")
    fi
    position=$((position + 1))
  done
  if (( ${#selected[@]} == 0 )); then
    echo "shard $index/$count selected no tests out of ${#names[@]}" >&2
    exit 2
  fi
  joined="$(IFS='|'; echo "${selected[*]}")"
  run="^($joined)$"
  echo "shard $index/$count: ${#selected[@]} of ${#names[@]} test(s)"
fi

# -p 1: scenarios assert process-level heap, RSS, handle counts and frame
# throughput, so packages must not run next to each other.
args=(test -p 1 -tags stability $packages -count=1 -v -timeout "$timeout" -run "$run")
if (( race )); then
  args=(test -p 1 -race -tags stability $packages -count=1 -v -timeout "$timeout" -run "$run")
fi

echo "stability suite=$suite duration=$duration race=$race shard=$shard packages=$packages"

code=0
if (( json )); then
  mkdir -p "$CODEX_STABILITY_REPORT_DIR"
  log="$CODEX_STABILITY_REPORT_DIR/.tmp-stability-$(date +%Y%m%dT%H%M%S%N).json.log"
  go "${args[@]}" -json > "$log" 2>&1 || code=$?
  echo "json log: $log"
  passed=$(grep -c '"Action":"pass".*"Test"' "$log" || true)
  failed=$(grep -c '"Action":"fail".*"Test"' "$log" || true)
  skipped=$(grep -c '"Action":"skip".*"Test"' "$log" || true)
  echo "summary: pass=$passed fail=$failed skip=$skipped"
  grep -o '"Action":"fail","Package":"[^"]*","Test":"[^"]*"' "$log" |
    sed -E 's/.*"Package":"([^"]*)","Test":"([^"]*)"/FAIL \1\/\2/' || true
else
  go "${args[@]}" || code=$?
fi

if (( code == 0 && trend )); then
  go run ./stability/cmd/stability-trend -dir "$CODEX_STABILITY_REPORT_DIR" || code=$?
fi

exit "$code"
