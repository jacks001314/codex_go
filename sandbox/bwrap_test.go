package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/sandbox/sandboxpath"
)

// TestShouldRequirePlatformSandboxLikeRust mirrors Rust
// policy_transforms::should_require_platform_sandbox for the profiles the
// startup warning can receive.
func TestShouldRequirePlatformSandboxLikeRust(t *testing.T) {
	readOnly := ReadOnlyPermissionProfile()
	if !ShouldRequirePlatformSandbox(&readOnly) {
		t.Fatal("a read-only profile requires the platform sandbox")
	}
	workspaceWrite := WorkspaceWritePermissionProfile()
	if !ShouldRequirePlatformSandbox(&workspaceWrite) {
		t.Fatal("a workspace-write profile requires the platform sandbox")
	}
	fullAccess := FullAccessPermissionProfile()
	if ShouldRequirePlatformSandbox(&fullAccess) {
		t.Fatal("a full-access profile needs no platform sandbox")
	}
	// A restricted profile with the network disabled needs a sandbox to enforce
	// that restriction (Rust's `!network_policy.is_enabled()` branch).
	readOnlyNoNetwork := ReadOnlyPermissionProfile()
	readOnlyNoNetwork.NetworkEnabled = false
	readOnlyNoNetwork.SandboxPolicy = NewReadOnlyPolicy()
	if !ShouldRequirePlatformSandbox(&readOnlyNoNetwork) {
		t.Fatal("a restricted profile without network access requires the platform sandbox")
	}
	// An executor-managed sandbox never needs bubblewrap, with or without network.
	external := PermissionProfile{SandboxPolicy: NewExternalSandboxPolicy(NetworkEnabled)}
	if ShouldRequirePlatformSandbox(&external) {
		t.Fatal("an external sandbox needs no platform sandbox")
	}
	external.SandboxPolicy = NewExternalSandboxPolicy(NetworkRestricted)
	if ShouldRequirePlatformSandbox(&external) {
		t.Fatal("an external sandbox without network access still needs no platform sandbox")
	}
	// A nil profile behaves like the read-only default.
	if !ShouldRequirePlatformSandbox(nil) {
		t.Fatal("a missing profile defaults to the read-only profile")
	}
}

// TestSystemBwrapWarningIsLinuxOnlyLikeRust pins Rust's non-Linux stub.
func TestSystemBwrapWarningIsLinuxOnlyLikeRust(t *testing.T) {
	readOnly := ReadOnlyPermissionProfile()
	if runtime.GOOS == "linux" {
		// Steady state on Linux depends on the host; only the gate is asserted.
		_ = SystemBwrapWarning(&readOnly)
		return
	}
	if warning := SystemBwrapWarning(&readOnly); warning != "" {
		t.Fatalf("SystemBwrapWarning() = %q on %s, want no warning off Linux", warning, runtime.GOOS)
	}
}

// TestSystemBwrapWarningForPathLikeRust mirrors Rust
// sandboxing::bwrap::system_bwrap_warning_for_path: WSL1 first, then a missing
// system launcher, then the user-namespace probe.
func TestSystemBwrapWarningForPathLikeRust(t *testing.T) {
	healthy := func(string) bool { return true }
	unusable := func(string) bool { return false }

	if got := systemBwrapWarning("", false, healthy); got != missingBwrapWarning {
		t.Fatalf("missing launcher warning = %q, want %q", got, missingBwrapWarning)
	}
	if got := systemBwrapWarning("/usr/bin/bwrap", true, healthy); got != wsl1BwrapWarning {
		t.Fatalf("WSL1 warning = %q, want %q", got, wsl1BwrapWarning)
	}
	if got := systemBwrapWarning("/usr/bin/bwrap", false, unusable); got != userNamespaceWarning {
		t.Fatalf("user namespace warning = %q, want %q", got, userNamespaceWarning)
	}
	if got := systemBwrapWarning("/usr/bin/bwrap", false, healthy); got != "" {
		t.Fatalf("usable launcher warning = %q, want none", got)
	}
}

// TestIsUserNamespaceFailureLikeRust pins the exact markers Rust treats as a
// user-namespace failure; other non-zero exits stay usable.
func TestIsUserNamespaceFailureLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		stderr string
		want   bool
	}{
		{"bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted", true},
		{"bwrap: loopback: Failed RTM_NEWLINK: Operation not permitted", true},
		{"bwrap: setting up uid map: Permission denied", true},
		{"bwrap: No permissions to create a new namespace", true},
		{"bwrap: Unknown option --argv0", false},
		{"bwrap: creating new namespace failed: Operation not permitted", false},
		{"", false},
	} {
		if got := isUserNamespaceFailure(testCase.stderr); got != testCase.want {
			t.Errorf("isUserNamespaceFailure(%q) = %v, want %v", testCase.stderr, got, testCase.want)
		}
	}
}

// TestFindSystemBwrapInPathLikeRust pins that a project cannot shadow the
// sandbox launcher: a bwrap inside the working directory is skipped.
func TestFindSystemBwrapInPathLikeRust(t *testing.T) {
	directory := t.TempDir()
	binDir := filepath.Join(directory, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	bwrap := filepath.Join(binDir, "bwrap")
	if err := os.WriteFile(bwrap, []byte("x"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if runtime.GOOS == "windows" {
		// The launcher name is platform specific; the PATH walk itself is the
		// behaviour under test on every host.
		t.Skip("the system launcher is named bwrap on Unix only")
	}
	// A read-only policy grants no writes, so only the cwd exclusion can skip a
	// candidate here.
	noWrites := sandboxpath.FilesystemPolicy{}
	if got := findSystemBwrapInPath(binDir, directory, noWrites); got != "" {
		t.Fatalf("findSystemBwrapInPath(shadowed) = %q, want the project copy skipped", got)
	}
	if got := findSystemBwrapInPath(binDir, filepath.Join(directory, "elsewhere"), noWrites); got == "" {
		t.Fatal("findSystemBwrapInPath(outside cwd) skipped a legitimate launcher")
	}
	if got := findSystemBwrapInPath("", directory, noWrites); got != "" {
		t.Fatalf("findSystemBwrapInPath(empty PATH) = %q, want none", got)
	}
}

// TestCanonicalHelpersKeepAbsolutes pins the small path helpers the search uses.
func TestCanonicalHelpersKeepAbsolutes(t *testing.T) {
	directory := t.TempDir()
	if got := sandboxpath.CanonicalDirectory(directory); !filepath.IsAbs(got) || !strings.HasSuffix(got, filepath.Base(directory)) {
		t.Fatalf("CanonicalDirectory(%q) = %q", directory, got)
	}
	if got := sandboxpath.CanonicalDirectory(""); got != "" {
		t.Fatalf("CanonicalDirectory(empty) = %q, want empty", got)
	}
	if !sandboxpath.PathWithin(filepath.Join(directory, "child"), directory) {
		t.Fatal("PathWithin(child) = false")
	}
	if sandboxpath.PathWithin(directory, filepath.Join(directory, "child")) {
		t.Fatal("PathWithin(parent) = true")
	}
}

// TestFindSystemBwrapInPathSkipsWritableRootLikeRust wires the startup-warning
// probe to the filesystem policy (Rust #51211): a bwrap inside the profile's own
// writable root is skipped, while a read-only profile still finds it.
func TestFindSystemBwrapInPathSkipsWritableRootLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the system launcher is named bwrap on Unix only")
	}
	workspace := t.TempDir()
	binDir := filepath.Join(workspace, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	bwrap := filepath.Join(binDir, "bwrap")
	if err := os.WriteFile(bwrap, []byte("x"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	workspaceWrite := WorkspaceWritePermissionProfile()
	writable := preSandboxFilesystemPolicy(&workspaceWrite, workspace)
	if got := findSystemBwrapInPath(binDir, "/", writable); got != "" {
		t.Fatalf("findSystemBwrapInPath(writable root) = %q, want the writable copy skipped", got)
	}
	readOnly := ReadOnlyPermissionProfile()
	if got := findSystemBwrapInPath(binDir, "/", preSandboxFilesystemPolicy(&readOnly, workspace)); got == "" {
		t.Fatal("findSystemBwrapInPath(read-only profile) skipped a legitimate candidate")
	}
}
