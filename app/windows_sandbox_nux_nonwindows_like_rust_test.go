//go:build !windows

package app

import (
	"context"
	"testing"
	"time"

	"codex_go/appserverdaemon"
)

// TestInteractiveRemoteWindowsSandboxPromptSkippedOffWindowsLikeRust pins
// Rust's cfg(target_os = "windows") gate around the startup Windows sandbox
// NUX (codex-rs/tui/src/lib.rs should_prompt_windows_sandbox_nux_at_startup and
// codex-rs/tui/src/projectless.rs prompt_windows_sandbox). A TUI connected to a
// local app server on Linux must go straight to the thread, not block on
// Windows sandbox setup.
func TestInteractiveRemoteWindowsSandboxPromptSkippedOffWindowsLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws://127.0.0.1:1", nil)
	if prompt := interactiveRemoteWindowsSandboxStartupPrompt(ctx, nil, endpoint, nil); prompt != nil {
		t.Fatalf("prompt = %#v, want nil on non-Windows hosts", prompt)
	}
}
