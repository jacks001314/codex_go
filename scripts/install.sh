#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
VERSION=${CODEX_GO_VERSION:-}
INSTALL_DIR=${CODEX_INSTALL_DIR:-$HOME/.local/bin}
CGO_MODE=auto
FORCE=0

usage() {
  cat <<'EOF'
Usage: scripts/install.sh [options]

Options:
  --version VERSION    Version embedded in the binary.
  --install-dir PATH   Install directory (default: ~/.local/bin).
  --cgo auto|on|off    CGO mode.
  --force              Replace an existing binary without notice.
  -h, --help           Show this help.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) VERSION=${2:?"--version requires a value"}; shift ;;
    --install-dir) INSTALL_DIR=${2:?"--install-dir requires a value"}; shift ;;
    --cgo) CGO_MODE=${2:?"--cgo requires a value"}; shift ;;
    --force) FORCE=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

EXT=""
[ "$(go env GOOS)" = windows ] && EXT=.exe
DESTINATION=$INSTALL_DIR/codex$EXT
STAGING=$(mktemp "${TMPDIR:-/tmp}/codex-go-install.XXXXXX")
trap 'rm -f "$STAGING"' EXIT HUP INT TERM

if [ -e "$DESTINATION" ] && [ "$FORCE" -ne 1 ]; then
  echo "==> Updating existing installation at $DESTINATION"
fi

set -- --output "$STAGING" --cgo "$CGO_MODE"
[ -n "$VERSION" ] && set -- "$@" --version "$VERSION"
"$SCRIPT_DIR/build.sh" "$@"
mkdir -p "$INSTALL_DIR"
chmod 755 "$STAGING"
mv -f "$STAGING" "$DESTINATION"
trap - EXIT HUP INT TERM

echo "==> Installed $DESTINATION"
"$DESTINATION" --version
case ":$PATH:" in
  *:"$INSTALL_DIR":*) ;;
  *) echo "WARNING: $INSTALL_DIR is not on PATH." >&2 ;;
esac
