#!/usr/bin/env bash
#
# Publish a GitHub release in stages so a failed asset upload cannot leave a
# half-populated public release behind (Rust upstream #59887 and #59903).
#
# The previous shape was a single `softprops/action-gh-release` step: it created
# the release and uploaded the assets at once. When the upload failed the public
# prerelease stayed published, and because `released`/`prereleased` events only
# fire for a newly created release, rerunning the workflow could not fix it.
#
# Here the release is created first (as a draft when it is a stable release), the
# upload happens next, and only a failure before the upload finished deletes the
# release this run created. A release that already existed is never deleted, and
# the tag is only ever read: if it is missing the run aborts (exit 5) instead of
# letting the create API build one from the default branch.
set -euo pipefail

log() { printf 'publish-github-release: %s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

repo="${GITHUB_REPOSITORY:-}"
tag="${TAG_NAME:-${GITHUB_REF_NAME:-}}"
name="${RELEASE_NAME:-$tag}"
notes="${NOTES_FILE:-}"
dist="${DIST_DIR:-dist}"
prerelease_flag="${PRERELEASE:-false}"
gh_bin="${GH_BIN:-gh}"

[ -n "$repo" ] || die "GITHUB_REPOSITORY is required"
[ -n "$tag" ] || die "TAG_NAME is required"
[ -n "$notes" ] || die "NOTES_FILE is required"
[ -f "$notes" ] || die "release notes file not found: $notes"

case "${prerelease_flag,,}" in
  true | 1 | yes) prerelease=true ;;
  *) prerelease=false ;;
esac

created=false
uploaded=false
release_id=""

# Delete only the release this run created, and only while the upload has not
# completed: once the assets are in place a later failure (for example a
# metadata patch) must not throw the uploaded release away.
cleanup() {
  status=$?
  if [ "$status" -ne 0 ] && [ "$created" = true ] && [ "$uploaded" = false ]; then
    log "upload did not complete; deleting release ${release_id} created by this run"
    if ! "$gh_bin" api -X DELETE "repos/${repo}/releases/${release_id}" >/dev/null; then
      log "warning: could not delete release ${release_id}; it is left in place"
    fi
  fi
  exit "$status"
}
trap cleanup EXIT

existing="$("$gh_bin" api "repos/${repo}/releases/tags/${tag}" --jq '.id' 2>/dev/null || true)"
if [ -n "$existing" ] && [ "$existing" != "null" ]; then
  release_id="$existing"
  log "release for ${tag} already exists (id ${release_id}); uploading into it"
else
  # The create API would create a missing tag from the default branch (Rust
  # publish-github-release.sh:15-16), so verify the ref before creating
  # anything. The workflow's `validate` job already rejects non-tag refs; this
  # guard is the safety net for a direct script run, and it is covered by
  # TestPublishReleaseRefusesMissingTagLikeRust.
  if ! "$gh_bin" api --silent "repos/${repo}/git/ref/tags/${tag}" >/dev/null; then
    log "error: tag ${tag} does not exist in ${repo}; refusing to create one from the default branch"
    exit 5
  fi
  # Stable releases stay invisible until their assets are uploaded; a prerelease
  # is public by design and relies on the cleanup below instead.
  draft=false
  [ "$prerelease" = true ] || draft=true
  release_id="$("$gh_bin" api -X POST "repos/${repo}/releases" \
    -f "tag_name=${tag}" \
    -f "name=${name}" \
    -F "body=@${notes}" \
    -F "prerelease=${prerelease}" \
    -F "draft=${draft}" \
    --jq '.id')"
  created=true
  log "created release ${release_id} for ${tag} (prerelease=${prerelease}, draft=${draft})"
fi

shopt -s nullglob
assets=("${dist}"/*)
shopt -u nullglob
[ "${#assets[@]}" -gt 0 ] || die "no release assets found in ${dist}"

# `--repo` keeps the upload pointed at the run's repository even when the
# checkout remote differs (Rust publish-github-release.sh passes it too).
"$gh_bin" release upload "$tag" "${assets[@]}" --repo "$repo" --clobber
uploaded=true

if [ "$prerelease" = false ]; then
  "$gh_bin" api -X PATCH "repos/${repo}/releases/${release_id}" \
    -F draft=false \
    -F make_latest=true >/dev/null
fi

log "published ${tag} (id ${release_id})"
