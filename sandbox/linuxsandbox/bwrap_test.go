package linuxsandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseBwrapHelpLikeRust mirrors Rust system_bwrap_capabilities: a launcher
// without `--as-pid-1` is unusable, and the remaining flags select compatibility
// paths.
func TestParseBwrapHelpLikeRust(t *testing.T) {
	capabilities, ok := parseBwrapHelp("--as-pid-1\n--perms\n--argv0\n")
	if !ok || !capabilities.SupportsPerms || !capabilities.SupportsArgv0 || capabilities.SupportsRoBindFD {
		t.Fatalf("capabilities = %+v, ok = %v", capabilities, ok)
	}
	capabilities, ok = parseBwrapHelp("--as-pid-1\n--perms\n--ro-bind-fd\n")
	if !ok || !capabilities.SupportsRoBindFD || capabilities.SupportsArgv0 {
		t.Fatalf("capabilities = %+v, ok = %v", capabilities, ok)
	}
	if _, ok := parseBwrapHelp("--argv0\n--perms\n"); ok {
		t.Fatal("a launcher without --as-pid-1 must be rejected")
	}
}

// TestSelectBwrapLauncherLikeRust mirrors Rust preferred_bwrap_launcher: a system
// launcher passes only with the required capability, the packaged launcher is the
// fallback, and neither means unavailable.
func TestSelectBwrapLauncherLikeRust(t *testing.T) {
	lookupHit := func(string) (string, error) { return "/usr/bin/bwrap", nil }
	lookupMiss := func(string) (string, error) { return "", os.ErrNotExist }
	usable := func(string) (bwrapCapabilities, bool) {
		return bwrapCapabilities{SupportsPerms: true, SupportsArgv0: true}, true
	}
	withoutPerms := func(string) (bwrapCapabilities, bool) {
		return bwrapCapabilities{SupportsArgv0: true}, true
	}
	withoutPid1 := func(string) (bwrapCapabilities, bool) { return bwrapCapabilities{}, false }
	bundled := func() (string, bool) { return "/pkg/codex-resources/bwrap", true }
	noBundled := func() (string, bool) { return "", false }

	launcher := selectBwrapLauncher(lookupHit, usable, bundled)
	if launcher.Program != "/usr/bin/bwrap" || launcher.Bundled || !launcher.SupportsArgv0 {
		t.Fatalf("system launcher = %+v", launcher)
	}
	// A system launcher without --perms is ignored in favour of the packaged one.
	launcher = selectBwrapLauncher(lookupHit, withoutPerms, bundled)
	if launcher.Program != "/pkg/codex-resources/bwrap" || !launcher.Bundled || !launcher.SupportsArgv0 {
		t.Fatalf("fallback launcher = %+v", launcher)
	}
	launcher = selectBwrapLauncher(lookupHit, withoutPid1, bundled)
	if launcher.Program != "/pkg/codex-resources/bwrap" || !launcher.Bundled {
		t.Fatalf("fallback launcher = %+v", launcher)
	}
	if launcher := selectBwrapLauncher(lookupMiss, usable, noBundled); launcher.available() {
		t.Fatalf("launcher = %+v, want unavailable", launcher)
	}
	if launcher := selectBwrapLauncher(nil, nil, nil); launcher.available() {
		t.Fatalf("launcher = %+v, want unavailable", launcher)
	}
}

// TestInjectAsPid1LikeRust mirrors Rust exec_bwrap: an argv that unshares the pid
// namespace gains --as-pid-1 right after argv[0], and nothing else changes.
func TestInjectAsPid1LikeRust(t *testing.T) {
	got := injectAsPid1([]string{"bwrap", "--die-with-parent", "--unshare-pid", "--", "true"})
	want := []string{"bwrap", "--as-pid-1", "--die-with-parent", "--unshare-pid", "--", "true"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("injectAsPid1() = %#v, want %#v", got, want)
	}
	withoutPidNamespace := []string{"bwrap", "--die-with-parent", "--", "true"}
	if got := injectAsPid1(withoutPidNamespace); !reflect.DeepEqual(got, withoutPidNamespace) {
		t.Fatalf("injectAsPid1(without --unshare-pid) = %#v", got)
	}
	// A flag after the command separator does not belong to bubblewrap.
	afterSeparator := []string{"bwrap", "--", "sh", "-c", "--unshare-pid"}
	if got := injectAsPid1(afterSeparator); !reflect.DeepEqual(got, afterSeparator) {
		t.Fatalf("injectAsPid1(command flag) = %#v", got)
	}
}

// TestApplyInnerCommandArgv0LikeRust mirrors Rust apply_inner_command_argv0: the
// canonical sandbox name is passed through --argv0 when supported, otherwise the
// inner command is the caller's own argv[0].
func TestApplyInnerCommandArgv0LikeRust(t *testing.T) {
	argv := []string{"bwrap", "--unshare-pid", "--", "/usr/bin/codex", "--sandbox-policy-cwd", "/"}
	got, err := applyInnerCommandArgv0(argv, true, "/usr/bin/codex")
	if err != nil {
		t.Fatalf("applyInnerCommandArgv0 error = %v", err)
	}
	want := []string{"bwrap", "--unshare-pid", "--argv0", LinuxSandboxArg0, "--", "/usr/bin/codex", "--sandbox-policy-cwd", "/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv0 argv = %#v, want %#v", got, want)
	}
	legacy, err := applyInnerCommandArgv0(argv, false, "/opt/codex/bin/codex")
	if err != nil {
		t.Fatalf("applyInnerCommandArgv0(legacy) error = %v", err)
	}
	wantLegacy := []string{"bwrap", "--unshare-pid", "--", "/opt/codex/bin/codex", "--sandbox-policy-cwd", "/"}
	if !reflect.DeepEqual(legacy, wantLegacy) {
		t.Fatalf("legacy argv = %#v, want %#v", legacy, wantLegacy)
	}
	if _, err := applyInnerCommandArgv0([]string{"bwrap", "true"}, true, "codex"); err == nil {
		t.Fatal("an argv without the command separator must fail")
	}
	if _, err := applyInnerCommandArgv0([]string{"bwrap", "--"}, false, "codex"); err == nil {
		t.Fatal("an argv without an inner command must fail")
	}
}

// TestVerifyBundledBwrapDigestLikeRust pins the release digest check.
func TestVerifyBundledBwrapDigestLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bwrap")
	content := []byte("bundled bubblewrap")
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	digest := sha256.Sum256(content)
	if err := verifyBundledBwrapDigest(path, hex.EncodeToString(digest[:])); err != nil {
		t.Fatalf("verifyBundledBwrapDigest(matching) error = %v", err)
	}
	// No pinned digest means the release did not expect one.
	if err := verifyBundledBwrapDigest(path, ""); err != nil {
		t.Fatalf("verifyBundledBwrapDigest(unpinned) error = %v", err)
	}
	if err := verifyBundledBwrapDigest(path, hex.EncodeToString(make([]byte, 32))); err == nil ||
		!strings.Contains(err.Error(), "does not match the digest pinned for this release") {
		t.Fatalf("verifyBundledBwrapDigest(mismatch) error = %v", err)
	}
	if err := verifyBundledBwrapDigest(path, "not-a-digest"); err == nil ||
		!strings.Contains(err.Error(), "is not a SHA-256 digest") {
		t.Fatalf("verifyBundledBwrapDigest(malformed) error = %v", err)
	}
}

// TestBundledBwrapResourceFallsBackToTheExecutableDirectory pins the legacy
// layout lookup next to the running executable.
func TestBundledBwrapResourceFallsBackToTheExecutableDirectory(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable error = %v", err)
	}
	candidates := bundledBwrapCandidates()
	want := filepath.Join(filepath.Dir(executable), "codex-resources", "bwrap")
	for _, candidate := range candidates {
		if candidate == want {
			return
		}
	}
	t.Fatalf("bundledBwrapCandidates() = %#v, want the executable-directory candidate %q", candidates, want)
}
