//go:build windows

package windowssandbox

import (
	"os"
	"os/exec"
	"testing"
)

const testPrivateDesktopEnv = "CODEX_TEST_PRIVATE_DESKTOP_NAME"

// TestPrivateDesktopOutlivesWrapperProcessLikeRust pins Rust #44658: the caller
// caches the desktop, so a separate (short-lived) wrapper process can still
// open it after the desktop was created.
func TestPrivateDesktopOutlivesWrapperProcessLikeRust(t *testing.T) {
	desktop, err := PrepareLaunchDesktop(true)
	if err != nil {
		if IsUnsupported(err) {
			t.Skipf("private desktops unsupported on this host: %v", err)
		}
		t.Fatalf("PrepareLaunchDesktop() error = %v", err)
	}
	defer desktop.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestPrivateDesktopOpenChildHelper$")
	cmd.Env = append(os.Environ(), testPrivateDesktopEnv+"="+desktop.Name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child process failed to open %q: %v\n%s", desktop.Name, err, out)
	}
}

func TestPrivateDesktopOpenChildHelper(t *testing.T) {
	name := os.Getenv(testPrivateDesktopEnv)
	if name == "" {
		t.Skip("helper process only")
	}
	opened, err := OpenPrivateLaunchDesktop(name)
	if err != nil {
		t.Fatalf("OpenPrivateLaunchDesktop(%q) error = %v", name, err)
	}
	_ = opened.Close()
}
