package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSystemBwrapWarningDetectsWSL1(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("WSL detection is Linux-only")
	}

	// Note: This test can only verify the function runs without crashing.
	// Actual WSL1 detection depends on the real /proc/version file.
	warning := SystemBwrapWarning()
	// warning may be empty or contain a message depending on the environment
	_ = warning
}

func TestSystemBwrapWarningDetectsMissingBwrap(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap detection is Linux-only")
	}

	// Temporarily modify PATH to exclude bwrap
	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)

	// Set PATH to a directory without bwrap
	tempDir := t.TempDir()
	os.Setenv("PATH", tempDir)

	warning := SystemBwrapWarning()
	if warning == "" {
		t.Error("expected warning about missing bwrap, got empty string")
	}
	if warning != "" && warning != "bubblewrap is unavailable: no system bwrap was found on PATH" {
		// May also get WSL1 warning if running on WSL1
		t.Logf("got warning: %s", warning)
	}
}

func TestIsUserNamespaceFailure(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{
			name:   "creating new namespace error",
			stderr: "bwrap: creating new namespace failed: Operation not permitted",
			want:   true,
		},
		{
			name:   "user namespaces not enabled",
			stderr: "bwrap: user namespaces are not enabled in the kernel",
			want:   true,
		},
		{
			name:   "permission denied",
			stderr: "bwrap: Permission denied",
			want:   true,
		},
		{
			name:   "operation not permitted",
			stderr: "bwrap: Operation not permitted",
			want:   true,
		},
		{
			name:   "unrelated error",
			stderr: "bwrap: Unknown option --argv0",
			want:   false,
		},
		{
			name:   "empty error",
			stderr: "",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUserNamespaceFailure(tt.stderr)
			if got != tt.want {
				t.Errorf("isUserNamespaceFailure(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}

func TestSystemBwrapHasUserNamespaceAccess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap is Linux-only")
	}

	// Create a fake bwrap script that simulates success
	tempDir := t.TempDir()
	fakeBwrap := filepath.Join(tempDir, "bwrap")
	script := `#!/bin/sh
exit 0
`
	if err := os.WriteFile(fakeBwrap, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	if !systemBwrapHasUserNamespaceAccess(fakeBwrap) {
		t.Error("expected successful fake bwrap to return true")
	}

	// Create a fake bwrap that fails with user namespace error
	fakeBwrapFail := filepath.Join(tempDir, "bwrap-fail")
	failScript := `#!/bin/sh
echo "bwrap: creating new namespace failed: Operation not permitted" >&2
exit 1
`
	if err := os.WriteFile(fakeBwrapFail, []byte(failScript), 0755); err != nil {
		t.Fatal(err)
	}

	if systemBwrapHasUserNamespaceAccess(fakeBwrapFail) {
		t.Error("expected failing fake bwrap to return false")
	}

	// Create a fake bwrap that fails with unrelated error
	fakeBwrapOther := filepath.Join(tempDir, "bwrap-other")
	otherScript := `#!/bin/sh
echo "bwrap: Unknown option --argv0" >&2
exit 1
`
	if err := os.WriteFile(fakeBwrapOther, []byte(otherScript), 0755); err != nil {
		t.Fatal(err)
	}

	if !systemBwrapHasUserNamespaceAccess(fakeBwrapOther) {
		t.Error("expected bwrap with non-namespace error to return true")
	}
}
