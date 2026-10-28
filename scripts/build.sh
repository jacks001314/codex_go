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

# Linux packages pin the digest of the bundled bubblewrap they carry, so the
# sandbox refuses to run a replaced launcher (Rust build.rs stamps
# CODEX_BWRAP_SHA256 the same way).
BWRAP_DIGEST_FLAG=""
if [ "$TARGET_GOOS" = linux ]; then
  BWRAP_SHA_FILE="$ROOT/third_party/bwrap/build/$TARGET_GOOS-$TARGET_GOARCH/bundled-bwrap.sha256"
  BWRAP_SHA=$(tr -d ' \t\r\n' < "$BWRAP_SHA_FILE" 2>/dev/null || true)
  [ -n "$BWRAP_SHA" ] && BWRAP_DIGEST_FLAG="-X codex_go/sandbox/linuxsandbox.bundledBwrapSHA256=$BWRAP_SHA"
fi

set -- build -trimpath -buildvcs=false -ldflags "-s -w -X codex_go/doctor.buildVersion=$VERSION -X codex_go/appserver.buildVersion=$VERSION -X codex_go/mcp.buildVersion=$VERSION $BWRAP_DIGEST_FLAG" -o "$OUTPUT"
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

# The voice helper ships inside the package's private voice runtime. It is
# stamped with the same identity as the CLI so the same-build handshake holds.
VOICE_BIN_DIR=$(dirname -- "$OUTPUT")/codex-resources/voice/bin
mkdir -p "$VOICE_BIN_DIR"
VOICE_OUTPUT="$VOICE_BIN_DIR/codex-voice-host$EXT"
set -- build -trimpath -buildvcs=false -ldflags "-s -w -X main.buildCommit=$VERSION" -o "$VOICE_OUTPUT"
[ "$RACE" -eq 1 ] && set -- "$@" -race
[ "$REBUILD" -eq 1 ] && set -- "$@" -a
set -- "$@" ./cmd/codex-voice-host
(cd "$ROOT" && go "$@")
echo "==> Built $VOICE_OUTPUT"

# The packaged codec ships beside the helper. It is prepared per platform by
# third_party/voice/prepare_opus.py; a build without it produces a helper that
# runs the control plane but carries no audio.
case "$TARGET_GOOS" in
  windows) VOICE_LIBRARY=libopus.dll ;;
  darwin) VOICE_LIBRARY=libopus.0.dylib ;;
  *) VOICE_LIBRARY=libopus.so.0 ;;
esac
PREPARED_CODEC="$ROOT/third_party/voice/build/$TARGET_GOOS-$TARGET_GOARCH/lib/$VOICE_LIBRARY"
if [ -f "$PREPARED_CODEC" ]; then
  VOICE_LIB_DIR=$(dirname -- "$OUTPUT")/codex-resources/voice/lib
  mkdir -p "$VOICE_LIB_DIR"
  cp -f "$PREPARED_CODEC" "$VOICE_LIB_DIR/$VOICE_LIBRARY"
  echo "==> Staged voice codec $VOICE_LIBRARY"
else
  echo "==> WARNING: voice codec for $TARGET_GOOS/$TARGET_GOARCH is not prepared; run third_party/voice/prepare_opus.py --platform $TARGET_GOOS-$TARGET_GOARCH"
fi
if [ "$TARGET_GOOS/$TARGET_GOARCH" = "$HOST_GOOS/$HOST_GOARCH" ]; then
  "$OUTPUT" --version
fi

# A managed daemon seeds itself from a complete CLI package, so the build marks
# the tree it just produced with the manifest that names the entrypoint and the
# packaged resources (Rust codex-package.json). The executable sits at the
# package root, which is the layout install.PackageLayoutFromExe resolves for a
# locally built CLI.
OUTPUT_DIR=$(dirname -- "$OUTPUT")
if [ "$(basename -- "$OUTPUT_DIR")" = bin ]; then
  case "$TARGET_GOOS/$TARGET_GOARCH" in
    windows/amd64) TRIPLE=x86_64-pc-windows-msvc ;;
    windows/arm64) TRIPLE=aarch64-pc-windows-msvc ;;
    darwin/amd64) TRIPLE=x86_64-apple-darwin ;;
    darwin/arm64) TRIPLE=aarch64-apple-darwin ;;
    linux/amd64) TRIPLE=x86_64-unknown-linux-musl ;;
    linux/arm64) TRIPLE=aarch64-unknown-linux-musl ;;
    *) TRIPLE="$TARGET_GOOS/$TARGET_GOARCH" ;;
  esac
  MANIFEST_PATH=$OUTPUT_DIR/codex-package.json
  cat > "$MANIFEST_PATH" <<EOF
{
  "layoutVersion": 1,
  "version": "$VERSION",
  "target": "$TRIPLE",
  "variant": "codex",
  "entrypoint": "codex$EXT",
  "resourcesDir": "codex-resources",
  "pathDir": "codex-path"
}
EOF
  echo "==> Wrote $MANIFEST_PATH"

  # The packaged CLI resolves its own search backend from the package's
  # codex-path directory (Rust `package_layout.path_dir`), so every package
  # carries the pinned ripgrep binary; a package without it cannot be installed
  # as a managed daemon.
  RIPGREP_NAME=rg$EXT
  PREPARED_RIPGREP="$ROOT/third_party/ripgrep/build/$TARGET_GOOS-$TARGET_GOARCH/bin/$RIPGREP_NAME"
  if [ -f "$PREPARED_RIPGREP" ]; then
    mkdir -p "$OUTPUT_DIR/codex-path"
    cp -f "$PREPARED_RIPGREP" "$OUTPUT_DIR/codex-path/$RIPGREP_NAME"
    echo "==> Staged $RIPGREP_NAME"
  else
    echo "==> WARNING: ripgrep for $TARGET_GOOS/$TARGET_GOARCH is not prepared; run third_party/ripgrep/prepare_ripgrep.py --platform $TARGET_GOOS-$TARGET_GOARCH"
  fi

  # Linux packages carry the sandbox launcher the CLI runs when no system bwrap
  # is on PATH (Rust codex-resources/bwrap). A package without it cannot be
  # installed as a managed daemon.
  if [ "$TARGET_GOOS" = linux ]; then
    PREPARED_BWRAP="$ROOT/third_party/bwrap/build/$TARGET_GOOS-$TARGET_GOARCH/bin/bwrap"
    if [ -f "$PREPARED_BWRAP" ]; then
      mkdir -p "$OUTPUT_DIR/codex-resources"
      cp -f "$PREPARED_BWRAP" "$OUTPUT_DIR/codex-resources/bwrap"
      echo "==> Staged bwrap"
    else
      echo "==> WARNING: bubblewrap for $TARGET_GOOS/$TARGET_GOARCH is not prepared; run third_party/bwrap/prepare_bwrap.py --platform $TARGET_GOOS-$TARGET_GOARCH"
    fi
  fi
fi
