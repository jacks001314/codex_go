#!/bin/sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${CODEX_GO_VERSION:-}
OUTPUT=""
TARGET_GOOS=${GOOS:-}
TARGET_GOARCH=${GOARCH:-}
CGO_MODE=auto
RACE=0
REBUILD=0

usage() {
  cat <<'EOF'
Usage: scripts/build.sh [options]

Options:
  --version VERSION   Version embedded in the binary.
  --output PATH       Output binary path (default: bin/codex[.exe]).
  --goos GOOS         Target operating system.
  --goarch GOARCH     Target architecture.
  --cgo auto|on|off   CGO mode (cross builds default to off).
  --race              Enable the Go race detector.
  --rebuild           Force rebuilding all packages.
  -h, --help          Show this help.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) VERSION=${2:?"--version requires a value"}; shift ;;
    --output) OUTPUT=${2:?"--output requires a value"}; shift ;;
    --goos) TARGET_GOOS=${2:?"--goos requires a value"}; shift ;;
    --goarch) TARGET_GOARCH=${2:?"--goarch requires a value"}; shift ;;
    --cgo) CGO_MODE=${2:?"--cgo requires a value"}; shift ;;
    --race) RACE=1 ;;
    --rebuild) REBUILD=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

case "$CGO_MODE" in auto|on|off) ;; *) echo "Invalid --cgo value: $CGO_MODE" >&2; exit 2 ;; esac

HOST_GOOS=$(go env GOOS)
HOST_GOARCH=$(go env GOARCH)
TARGET_GOOS=${TARGET_GOOS:-$HOST_GOOS}
TARGET_GOARCH=${TARGET_GOARCH:-$HOST_GOARCH}

if [ -z "$VERSION" ] && [ -f "$ROOT/VERSION" ]; then
  # Single source of truth: the root VERSION file (strip a leading 'v').
  VERSION=$(sed 's/^v//' "$ROOT/VERSION" | tr -d ' \t\r\n')
fi

if [ -z "$VERSION" ]; then
  VERSION=$(git -C "$ROOT" describe --tags --exact-match HEAD 2>/dev/null || true)
  VERSION=$(printf '%s' "$VERSION" | sed -E 's/^(go-)?v//')
fi
if [ -z "$VERSION" ]; then
  COMMIT=$(git -C "$ROOT" rev-parse --short=12 HEAD 2>/dev/null || true)
  VERSION="0.0.0-dev${COMMIT:++$COMMIT}"
fi
VERSION=$(printf '%s' "$VERSION" | sed 's/^v//')

EXT=""
[ "$TARGET_GOOS" = windows ] && EXT=.exe
OUTPUT=${OUTPUT:-$ROOT/bin/codex$EXT}
case "$OUTPUT" in /*) ;; *) OUTPUT=$ROOT/$OUTPUT ;; esac
mkdir -p "$(dirname -- "$OUTPUT")"

case "$CGO_MODE" in
  on) CGO_ENABLED=1 ;;
  off) CGO_ENABLED=0 ;;
  auto)
    if [ "$TARGET_GOOS/$TARGET_GOARCH" != "$HOST_GOOS/$HOST_GOARCH" ]; then
      CGO_ENABLED=0
    else
      CGO_ENABLED=${CGO_ENABLED:-$(go env CGO_ENABLED)}
    fi
    ;;
esac
export GOOS=$TARGET_GOOS GOARCH=$TARGET_GOARCH CGO_ENABLED

set -- build -trimpath -buildvcs=false -ldflags "-s -w -X codex_go/doctor.buildVersion=$VERSION -X codex_go/appserver.buildVersion=$VERSION -X codex_go/mcp.buildVersion=$VERSION" -o "$OUTPUT"
[ "$RACE" -eq 1 ] && set -- "$@" -race
[ "$REBUILD" -eq 1 ] && set -- "$@" -a
set -- "$@" ./cmd/codex

echo "==> Building Codex Go $VERSION for $TARGET_GOOS/$TARGET_GOARCH"
(cd "$ROOT" && go "$@")
echo "==> Built $OUTPUT"
if [ "$TARGET_GOOS" = windows ]; then
  RESOURCES_DIR=$(dirname -- "$OUTPUT")/codex-resources
  mkdir -p "$RESOURCES_DIR"
  for HELPER in codex-command-runner codex-windows-sandbox-setup; do
    set -- build -trimpath -buildvcs=false -ldflags "-s -w -X codex_go/doctor.buildVersion=$VERSION -X codex_go/appserver.buildVersion=$VERSION -X codex_go/mcp.buildVersion=$VERSION" -o "$RESOURCES_DIR/$HELPER.exe"
    [ "$RACE" -eq 1 ] && set -- "$@" -race
    [ "$REBUILD" -eq 1 ] && set -- "$@" -a
    set -- "$@" "./cmd/$HELPER"
    (cd "$ROOT" && go "$@")
    echo "==> Built $RESOURCES_DIR/$HELPER.exe"
  done
fi
HOST_OUTPUT=$(dirname -- "$OUTPUT")/codex-code-mode-host$EXT
set -- build -trimpath -buildvcs=false -ldflags "-s -w -X codex_go/doctor.buildVersion=$VERSION -X codex_go/appserver.buildVersion=$VERSION -X codex_go/mcp.buildVersion=$VERSION" -o "$HOST_OUTPUT"
[ "$RACE" -eq 1 ] && set -- "$@" -race
[ "$REBUILD" -eq 1 ] && set -- "$@" -a
set -- "$@" ./cmd/codex-code-mode-host
(cd "$ROOT" && go "$@")
echo "==> Built $HOST_OUTPUT"
if [ "$TARGET_GOOS/$TARGET_GOARCH" = "$HOST_GOOS/$HOST_GOARCH" ]; then
  "$OUTPUT" --version
fi
