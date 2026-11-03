#!/usr/bin/env bash
# Counterpart of scripts/test_hostile_env.ps1: run the packages that have shown
# ambient-environment dependencies with a deliberately hostile environment.
#
# Usage:
#   ./scripts/test_hostile_env.sh
#   ./scripts/test_hostile_env.sh --packages ./tui/... --count 1
set -uo pipefail

packages=('./tui/...' './app/...' './appserver/...')
count=1
while [[ $# -gt 0 ]]; do
    case "$1" in
        --packages|-Packages)
            IFS=',' read -r -a packages <<< "${2:-}"
            shift 2
            ;;
        --count|-Count)
            count="${2:-1}"
            shift 2
            ;;
        *)
            echo "unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

# Colour detection (NO_COLOR/WT_SESSION/TERM/FORCE_COLOR/CLICOLOR_FORCE) and
# clipboard/SSH detection (SSH_CONNECTION/SSH_TTY) all have bitten this suite.
hostile=(
    NO_COLOR=1
    WT_SESSION=x
    SSH_CONNECTION='10.0.0.1 51234 10.0.0.2 22'
    SSH_TTY=/dev/pts/9
    TERM=dumb
    FORCE_COLOR=1
    CLICOLOR_FORCE=1
    CLICOLOR=1
)

echo "Hostile environment: ${hostile[*]}"

failed=()
for package in "${packages[@]}"; do
    echo "==> go test ${package} -count=${count}"
    output=$(env "${hostile[@]}" go test "${package}" -count="${count}" 2>&1)
    status=$?
    if [[ ${status} -eq 0 ]]; then
        echo "PASS ${package}"
    else
        echo "FAIL ${package}"
        grep -E '^[[:space:]]*--- FAIL:' <<< "${output}" | sed 's/^/    /'
        failed+=("${package}")
    fi
done

echo
echo "Summary"
for package in "${packages[@]}"; do
    if printf '%s\n' "${failed[@]:-}" | grep -qx -- "${package}"; then
        echo "  FAIL    ${package}"
    else
        echo "  PASS    ${package}"
    fi
done

if [[ ${#failed[@]} -gt 0 ]]; then
    echo
    echo "${#failed[@]} package(s) failed under the hostile environment." >&2
    exit 1
fi

echo
echo "All packages passed under the hostile environment."
