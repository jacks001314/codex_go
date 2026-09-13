package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRustBaseInstructionsMatchGo is the shared-fixture differential for the
// fallback base instructions: Rust embeds models-manager/prompt.md as
// `BASE_INSTRUCTIONS` and Go vendors the same document (model/prompt.md via
// //go:embed, exported as model.BaseInstructions), which is what
// model_info_from_slug and the bundled catalog use when a model provides no
// `model_messages.instructions_template`.
//
// The Rust blob is read through `git show` so a Windows autocrlf checkout
// cannot mask a real difference, and the Go copy is compared with CRLF
// normalized to LF for the same reason.
func TestRustBaseInstructionsMatchGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	want := string(gitOutput(t, rustRepo, "show", "HEAD:codex-rs/models-manager/prompt.md"))
	if !strings.Contains(want, "# Tool Guidelines") || !strings.Contains(want, "# AGENTS.md spec") {
		t.Fatalf("Rust base instructions lost a known section:\n%s", want)
	}
	data, err := os.ReadFile(filepath.Join("..", "model", "prompt.md"))
	if err != nil {
		t.Fatalf("ReadFile(model/prompt.md): %v", err)
	}
	got := strings.ReplaceAll(string(data), "\r\n", "\n")
	if got != want {
		t.Fatalf("base instructions drift\nRust: %q\nGo:   %q", want, got)
	}
}
