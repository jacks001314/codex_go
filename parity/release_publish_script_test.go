package parity

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const releaseScriptStubGH = `#!/usr/bin/env bash
set -euo pipefail
state="${STUB_STATE:?}"
printf '%s\n' "$*" >> "${state}/calls.log"
if [ "${1:-}" = "api" ] && [ "${2:-}" = "--silent" ]; then
  # Tag-ref guard: a missing ref exits 5, like Rust's test_releases.py stub.
  [[ ! -f "${state}/missing_tag" ]] || exit 5
  exit 0
fi
if [ "${1:-}" = "api" ]; then
  if [ "${2:-}" = "-X" ]; then
    method="$3"
    path="$4"
  else
    method="GET"
    path="$2"
  fi
  case "$method" in
    GET)
      if [ -f "${state}/existing_id" ]; then cat "${state}/existing_id"; else exit 1; fi
      ;;
    POST)
      cat "${state}/created_id"
      ;;
    DELETE)
      printf '%s\n' "$path" >> "${state}/deleted.log"
      ;;
    PATCH)
      if [ -f "${state}/fail_patch" ]; then exit 1; fi
      printf '%s\n' "$path" >> "${state}/patched.log"
      ;;
  esac
  exit 0
fi
if [ "${1:-}" = "release" ] && [ "${2:-}" = "upload" ]; then
  if [ -f "${state}/fail_upload" ]; then exit 1; fi
  printf '%s\n' "$*" >> "${state}/uploaded.log"
  exit 0
fi
exit 0
`

type releaseRun struct {
	state  string
	err    error
	stdout string
}

func runReleaseScript(t *testing.T, files map[string]string, prerelease string) releaseRun {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the release publisher is a bash script run by the linux release workflow")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	state := filepath.Join(root, "state")
	dist := filepath.Join(root, "dist")
	for _, dir := range []string{bin, state, dist} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(releaseScriptStubGH), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "codex-linux-x86_64.tar.gz"), []byte("asset"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(root, "notes.md")
	if err := os.WriteFile(notes, []byte("release notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(state, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bash, filepath.Join(".github", "scripts", "publish-github-release.sh"))
	cmd.Dir = filepath.Join("..")
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_STATE="+state,
		"GITHUB_REPOSITORY=openai/codex-go",
		"TAG_NAME=v1.2.3",
		"RELEASE_NAME=v1.2.3",
		"NOTES_FILE="+notes,
		"DIST_DIR="+dist,
		"PRERELEASE="+prerelease,
	)
	out, err := cmd.CombinedOutput()
	return releaseRun{state: state, err: err, stdout: string(out)}
}

func readReleaseState(t *testing.T, state, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, name))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

// TestPublishReleaseCleansUpFailedPrereleaseById mirrors Rust #59887/#59903: the
// release is created and remembered by id before the assets are uploaded, a
// failed upload deletes exactly that release (the tag stays), an existing
// release is never deleted, and a failure after a completed upload keeps the
// release so the published assets do not disappear.
func TestPublishReleaseCleansUpFailedPrereleaseById(t *testing.T) {
	t.Run("failed upload deletes the release this run created", func(t *testing.T) {
		run := runReleaseScript(t, map[string]string{"created_id": "1001", "fail_upload": ""}, "true")
		if run.err == nil {
			t.Fatalf("script succeeded, want the upload failure to propagate: %s", run.stdout)
		}
		deleted := readReleaseState(t, run.state, "deleted.log")
		if strings.TrimSpace(deleted) != "repos/openai/codex-go/releases/1001" {
			t.Fatalf("deleted = %q, want the created release id", deleted)
		}
		if calls := readReleaseState(t, run.state, "calls.log"); strings.Contains(calls, "DELETE repos/openai/codex-go/git/refs") {
			t.Fatalf("the tag must survive the cleanup: %s", calls)
		}
	})

	t.Run("an existing release is not deleted", func(t *testing.T) {
		run := runReleaseScript(t, map[string]string{"existing_id": "42", "fail_upload": ""}, "true")
		if run.err == nil {
			t.Fatalf("script succeeded, want the upload failure to propagate: %s", run.stdout)
		}
		if deleted := readReleaseState(t, run.state, "deleted.log"); deleted != "" {
			t.Fatalf("deleted %q, want the pre-existing release untouched", deleted)
		}
	})

	t.Run("a completed upload publishes the stable release", func(t *testing.T) {
		run := runReleaseScript(t, map[string]string{"created_id": "1002"}, "false")
		if run.err != nil {
			t.Fatalf("script failed: %v\n%s", run.err, run.stdout)
		}
		uploaded := readReleaseState(t, run.state, "uploaded.log")
		if !strings.Contains(uploaded, "release upload v1.2.3") {
			t.Fatalf("uploaded = %q", uploaded)
		}
		// Rust publish-github-release.sh passes --repo on upload; without it the
		// command depends on whatever remote the checkout happens to have.
		if !strings.Contains(uploaded, "--repo openai/codex-go") {
			t.Fatalf("uploaded = %q, want --repo openai/codex-go", uploaded)
		}
		if patched := readReleaseState(t, run.state, "patched.log"); !strings.Contains(patched, "releases/1002") {
			t.Fatalf("patched = %q, want the draft published after the upload", patched)
		}
		if deleted := readReleaseState(t, run.state, "deleted.log"); deleted != "" {
			t.Fatalf("deleted %q after a successful upload", deleted)
		}
	})

	t.Run("a metadata failure after the upload keeps the release", func(t *testing.T) {
		run := runReleaseScript(t, map[string]string{"created_id": "1003", "fail_patch": ""}, "false")
		if run.err == nil {
			t.Fatalf("script succeeded, want the metadata failure to propagate: %s", run.stdout)
		}
		if deleted := readReleaseState(t, run.state, "deleted.log"); deleted != "" {
			t.Fatalf("deleted %q, want the uploaded release kept", deleted)
		}
	})
}

// TestPublishReleaseRefusesMissingTagLikeRust mirrors Rust
// publish-github-release.sh:15-16: the create API would create a missing tag
// from the default branch, so a missing ref aborts (exit 5) before anything is
// created. The workflow's `validate` job already rejects non-tag refs, which
// makes this a safety net for a direct script run -- exactly why it has to be
// covered here instead of being waved through as "unreachable".
func TestPublishReleaseRefusesMissingTagLikeRust(t *testing.T) {
	run := runReleaseScript(t, map[string]string{"created_id": "2001", "missing_tag": ""}, "true")
	if run.err == nil {
		t.Fatalf("script succeeded, want the missing tag to abort: %s", run.stdout)
	}
	var exitErr *exec.ExitError
	if !errors.As(run.err, &exitErr) || exitErr.ExitCode() != 5 {
		t.Fatalf("exit = %v, want status 5 (the Rust test_releases.py missing_tag case)", run.err)
	}
	calls := readReleaseState(t, run.state, "calls.log")
	if !strings.Contains(calls, "repos/openai/codex-go/git/ref/tags/v1.2.3") {
		t.Fatalf("calls = %q, want the tag ref lookup", calls)
	}
	if strings.Contains(calls, "POST repos/openai/codex-go/releases") {
		t.Fatalf("the missing tag must abort before the release is created: %s", calls)
	}
	if deleted := readReleaseState(t, run.state, "deleted.log"); deleted != "" {
		t.Fatalf("deleted %q, want nothing created and nothing deleted", deleted)
	}
}

// TestPublishReleaseStableDraftFailureCleanupDiffersFromRust records an
// intentional divergence instead of a bug: Rust installs its cleanup trap only
// when `PRERELEASE == true`, so a failed *stable* upload leaves an invisible
// draft behind, while Go deletes the draft it just created. The contract is
// about prerelease cleanup, the draft is invisible either way, and deleting it
// keeps the next run from silently reusing a half-built draft -- so Go's
// stricter cleanup is kept on purpose. Do not "fix" this back to Rust's shape
// without an owner decision.
func TestPublishReleaseStableDraftFailureCleanupDiffersFromRust(t *testing.T) {
	run := runReleaseScript(t, map[string]string{"created_id": "3001", "fail_upload": ""}, "false")
	if run.err == nil {
		t.Fatalf("script succeeded, want the upload failure to propagate: %s", run.stdout)
	}
	deleted := readReleaseState(t, run.state, "deleted.log")
	if strings.TrimSpace(deleted) != "repos/openai/codex-go/releases/3001" {
		t.Fatalf("deleted = %q, want the draft created by this run (Rust keeps it; see the comment above)", deleted)
	}
	if calls := readReleaseState(t, run.state, "calls.log"); strings.Contains(calls, "DELETE repos/openai/codex-go/git/refs") {
		t.Fatalf("the tag must survive the stable cleanup: %s", calls)
	}
}
