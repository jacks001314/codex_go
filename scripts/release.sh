#!/bin/sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SCRIPT_DIR=$ROOT/scripts
VERSION=""
OUTPUT_DIR=""
TARGETS="windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"
SKIP_TESTS=0

usage() {
  cat <<'EOF'
Usage: scripts/release.sh --version VERSION [options]

Options:
  --version VERSION    Semantic release version (required).
  --output-dir PATH    Release output directory.
  --targets LIST       Space- or comma-separated GOOS/GOARCH targets.
  --skip-tests         Skip the all-package compile check.
  -h, --help           Show this help.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) VERSION=${2:?"--version requires a value"}; shift ;;
    --output-dir) OUTPUT_DIR=${2:?"--output-dir requires a value"}; shift ;;
    --targets) TARGETS=$(printf '%s' "${2:?--targets requires a value}" | tr ',' ' '); shift ;;
    --skip-tests) SKIP_TESTS=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

VERSION=$(printf '%s' "$VERSION" | sed 's/^v//')
if ! printf '%s\n' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'; then
  echo "Version must be semantic version text such as 1.2.3 or 1.2.3-beta.1." >&2
  exit 2
fi

OUTPUT_DIR=${OUTPUT_DIR:-$ROOT/dist/v$VERSION}
case "$OUTPUT_DIR" in /*) ;; *) OUTPUT_DIR=$ROOT/$OUTPUT_DIR ;; esac
mkdir -p "$OUTPUT_DIR"

if [ "$SKIP_TESTS" -ne 1 ]; then
  echo "==> Running release compile checks"
  (cd "$ROOT" && go test -run '^$' ./...)
fi

ARTIFACT_LIST=$OUTPUT_DIR/.artifacts
: > "$ARTIFACT_LIST"
trap 'rm -f "$ARTIFACT_LIST"' EXIT HUP INT TERM

for TARGET in $TARGETS; do
  case "$TARGET" in */*) ;; *) echo "Invalid target '$TARGET'; expected GOOS/GOARCH." >&2; exit 2 ;; esac
  GOOS_VALUE=${TARGET%/*}
  GOARCH_VALUE=${TARGET#*/}
  NAME=codex-go-v$VERSION-$GOOS_VALUE-$GOARCH_VALUE
  STAGE_DIR=$OUTPUT_DIR/$NAME
  mkdir -p "$STAGE_DIR"
  EXT=""
  [ "$GOOS_VALUE" = windows ] && EXT=.exe

  "$SCRIPT_DIR/build.sh" --version "$VERSION" --goos "$GOOS_VALUE" --goarch "$GOARCH_VALUE" --cgo off --output "$STAGE_DIR/codex$EXT"
  cp "$ROOT/README.md" "$STAGE_DIR/"
  [ ! -f "$ROOT/LICENSE" ] || cp "$ROOT/LICENSE" "$STAGE_DIR/"
  [ ! -f "$ROOT/NOTICE" ] || cp "$ROOT/NOTICE" "$STAGE_DIR/"

  if [ "$GOOS_VALUE" = windows ]; then
    command -v zip >/dev/null 2>&1 || { echo "zip is required for Windows release archives." >&2; exit 1; }
    ARCHIVE=$OUTPUT_DIR/$NAME.zip
    rm -f "$ARCHIVE"
    (cd "$STAGE_DIR" && zip -qr "$ARCHIVE" .)
  else
    ARCHIVE=$OUTPUT_DIR/$NAME.tar.gz
    tar -czf "$ARCHIVE" -C "$OUTPUT_DIR" "$NAME"
  fi
  rm -rf "$STAGE_DIR"
  printf '%s\n' "$ARCHIVE" >> "$ARTIFACT_LIST"
done

checksum() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; return; fi
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'; return; fi
  if command -v openssl >/dev/null 2>&1; then openssl dgst -sha256 "$1" | awk '{print $NF}'; return; fi
  echo "sha256sum, shasum, or openssl is required." >&2
  exit 1
}

: > "$OUTPUT_DIR/SHA256SUMS"
while IFS= read -r ARTIFACT; do
  printf '%s  %s\n' "$(checksum "$ARTIFACT")" "$(basename "$ARTIFACT")" >> "$OUTPUT_DIR/SHA256SUMS"
done < "$ARTIFACT_LIST"

GENERATED_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
{
  printf '{\n  "version": "%s",\n  "generatedAt": "%s",\n  "artifacts": [' "$VERSION" "$GENERATED_AT"
  FIRST=1
  while IFS= read -r ARTIFACT; do
    [ "$FIRST" -eq 1 ] || printf ','
    printf '\n    "%s"' "$(basename "$ARTIFACT")"
    FIRST=0
  done < "$ARTIFACT_LIST"
  printf '\n  ]\n}\n'
} > "$OUTPUT_DIR/release.json"

rm -f "$ARTIFACT_LIST"
trap - EXIT HUP INT TERM
echo "==> Release artifacts written to $OUTPUT_DIR"
ls -lh "$OUTPUT_DIR"
