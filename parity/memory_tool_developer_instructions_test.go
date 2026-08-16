package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/memories"
)

// TestRustMemoryToolDeveloperInstructionsMatchesGo is the djalign
// dynamic-layer method-1 shared-fixture differential for the memory-tool
// developer policy fragment: Rust embeds ext/memories/templates/memories/
// read_path.md via include_str! and renders it with {{ base_path }} and
// {{ memory_summary }} substitutions (ext/memories/src/prompts.rs
// build_memory_tool_developer_instructions). Go mirrors the template and
// render in memories/prompts.go (BuildMemoryToolDeveloperInstructions). The
// rendered model-visible fragment must match the Rust blob rendered with the
// same inputs byte-for-byte (trailing newline included).
//
// The Rust side is pinned by blob content (candidateRustTo), so upstream edits
// break the contract instead of silently drifting.
func TestRustMemoryToolDeveloperInstructionsMatchesGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)

	blob := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/ext/memories/templates/memories/read_path.md")

	home := t.TempDir()
	memoriesDir := filepath.Join(home, "memories")
	if err := os.MkdirAll(memoriesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll memories: %v", err)
	}
	// Keep the summary far below the 2,500-token truncation limit so the
	// render comparison pins the template and substitution semantics, not the
	// truncation path (which is pinned separately by unit tests).
	summary := "v1\n\nproject conventions: use tabs\n"
	if err := os.WriteFile(filepath.Join(memoriesDir, memories.MemorySummaryFilename), []byte(summary), 0o600); err != nil {
		t.Fatalf("WriteFile summary: %v", err)
	}
	got := memories.BuildMemoryToolDeveloperInstructions(home)
	if got == "" {
		t.Fatal("BuildMemoryToolDeveloperInstructions returned empty fragment with summary present")
	}

	// Render the Rust blob with the same substitutions Go applies
	// (renderMemoryTemplate replaces every "{{ key }}" occurrence; Rust
	// build_memory_tool_developer_instructions trims the summary first).
	basePath := filepath.Join(home, "memories")
	want := strings.ReplaceAll(string(blob), "{{ base_path }}", basePath)
	want = strings.ReplaceAll(want, "{{ memory_summary }}", strings.TrimSpace(summary))

	if got != want {
		t.Fatalf("Go memory developer instructions differ from rendered Rust blob:\n--- go (%d bytes) ---\n%s\n--- rust (%d bytes) ---\n%s",
			len(got), got, len(want), want)
	}
}
