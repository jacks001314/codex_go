package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRustTuiAssetsMatchGo is the shared-fixture differential for the bundled
// TUI resources that Rust moved under codex-rs/tui/assets in #41477: Rust
// embeds them with include_str! and Go vendors the same bytes via //go:embed.
// The Rust blobs are read from the frozen snapshot so upstream edits break the
// contract instead of drifting silently, and both sides are LF-normalized so a
// Windows autocrlf checkout cannot mask a real difference.
func TestRustTuiAssetsMatchGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	assets := []struct {
		name     string
		rustPath string
		goPath   string
	}{
		{"tooltips", "codex-rs/tui/assets/tooltips.txt", filepath.Join("..", "tui", "tooltips.txt")},
		{"prompt_for_init_command", "codex-rs/tui/assets/prompt_for_init_command.md", filepath.Join("..", "tui", "tea", "prompt_for_init_command.md")},
	}
	for _, asset := range assets {
		want := strings.ReplaceAll(string(gitOutput(t, rustRepo, "show", candidateRustTo+":"+asset.rustPath)), "\r\n", "\n")
		if want == "" {
			t.Fatalf("Rust %s asset is empty", asset.name)
		}
		data, err := os.ReadFile(asset.goPath)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", asset.goPath, err)
		}
		got := strings.ReplaceAll(string(data), "\r\n", "\n")
		if got != want {
			t.Fatalf("%s drift from %s", asset.goPath, asset.rustPath)
		}
	}
}
