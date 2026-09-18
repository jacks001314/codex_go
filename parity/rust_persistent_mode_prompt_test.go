package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRustPersistentModePromptMatchesGo is the shared-fixture differential for
// the persistent-mode developer instructions: Rust embeds
// prompts/templates/persistent_mode.md via include_str! and Go vendors the same file
// (context/templates/persistent_mode.md via //go:embed), which
// PersistentModeInstructions renders when the model catalog provides no
// persistent_instructions.
//
// The Rust blob is read through `git show` so a Windows autocrlf checkout
// cannot mask a real difference, and the Go copy is compared with CRLF
// normalized to LF for the same reason.
func TestRustPersistentModePromptMatchesGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	want := string(gitOutput(t, rustRepo, "show", "HEAD:codex-rs/prompts/templates/persistent_mode.md"))
	if !strings.Contains(want, "{{ approval_request_channel }}") {
		t.Fatalf("Rust persistent-mode asset lost its channel placeholder:\n%s", want)
	}
	data, err := os.ReadFile(filepath.Join("..", "context", "templates", "persistent_mode.md"))
	if err != nil {
		t.Fatalf("ReadFile(context/templates/persistent_mode.md): %v", err)
	}
	got := strings.ReplaceAll(string(data), "\r\n", "\n")
	if got != want {
		t.Fatalf("persistent-mode prompt drift\nRust: %q\nGo:   %q", want, got)
	}
}
