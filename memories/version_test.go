package memories

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

// Mirrors the Rust memory_read v2 suite: the selected memory version contributes
// only its own summary to the model request.
func TestBuildMemoryToolDeveloperInstructionsForVersionSelectsRoot(t *testing.T) {
	home := t.TempDir()
	writeSummary := func(root string, body string) {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
		if err := os.WriteFile(filepath.Join(root, MemorySummaryFilename), []byte(body), 0o600); err != nil {
			t.Fatalf("write summary: %v", err)
		}
	}
	writeSummary(RootForVersion(home, config.MemoryVersionV1), "legacy-only-marker")
	writeSummary(RootForVersion(home, config.MemoryVersionV2), "v2-only-marker")

	v1 := BuildMemoryToolDeveloperInstructionsForVersion(home, config.MemoryVersionV1)
	if !strings.Contains(v1, "legacy-only-marker") || strings.Contains(v1, "v2-only-marker") {
		t.Fatalf("v1 instructions = %q", v1)
	}
	v2 := BuildMemoryToolDeveloperInstructionsForVersion(home, config.MemoryVersionV2)
	if !strings.Contains(v2, "v2-only-marker") || strings.Contains(v2, "legacy-only-marker") {
		t.Fatalf("v2 instructions = %q", v2)
	}
	if !strings.Contains(v2, "Memory citations:") || !strings.Contains(v2, "<oai-mem-citation>") {
		t.Fatalf("v2 instructions did not use the dedicated v2 template: %q", v2)
	}
	if strings.Contains(v1, "Memory citations:") {
		t.Fatalf("v1 instructions must not use the v2 template: %q", v1)
	}
	if got := BuildMemoryToolDeveloperInstructions(home); !strings.Contains(got, "legacy-only-marker") {
		t.Fatalf("default instructions = %q, want v1", got)
	}
}

// The embedded v2 template is byte-pinned to Rust's
// ext/memories/templates/memories/read_path_v2.md (#43813). The Rust checkout
// may be CRLF-converted by git on Windows; the canonical content is LF.
func TestMemoryV2TemplateMatchesRust(t *testing.T) {
	root := ""
	for _, candidate := range []string{
		filepath.Join("..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "..", "git", "codex", "codex-rs"),
	} {
		if _, err := os.Stat(filepath.Join(candidate, "Cargo.toml")); err == nil {
			root = candidate
			break
		}
	}
	if root == "" {
		t.Skip("Rust checkout not available")
	}
	data, err := os.ReadFile(filepath.Join(root, "ext", "memories", "templates", "memories", "read_path_v2.md"))
	if err != nil {
		t.Skipf("Rust v2 template unavailable: %v", err)
	}
	want := strings.ReplaceAll(string(data), "\r\n", "\n")
	if memoryToolDeveloperInstructionsV2Template != want {
		t.Fatalf("embedded v2 template differs from Rust:\n--- go ---\n%s\n--- rust ---\n%s", memoryToolDeveloperInstructionsV2Template, want)
	}
}

func TestRootForVersion(t *testing.T) {
	home := t.TempDir()
	if got := RootForVersion(home, config.MemoryVersionV1); got != filepath.Join(home, "memories") {
		t.Fatalf("v1 root = %q", got)
	}
	if got := RootForVersion(home, config.MemoryVersionV2); got != filepath.Join(home, "memories_v2") {
		t.Fatalf("v2 root = %q", got)
	}
}

// Both memory roots feed shell memory-usage telemetry (Rust #43797).
func TestUsageKindFromPathRecognizesBothMemoryRoots(t *testing.T) {
	for path, want := range map[string]UsageKind{
		"/home/user/.codex/memories/memory_summary.md":         UsageKindMemorySummary,
		"/home/user/.codex/memories_v2/memory_summary.md":      UsageKindMemorySummary,
		`C:\Users\user\.codex\memories_v2\raw_memories.md`:     UsageKindRawMemories,
		"/home/user/.codex/memories_v2/rollout_summaries/x.md": UsageKindRolloutSummaries,
	} {
		kind, ok := UsageKindFromPath(path)
		if !ok || kind != want {
			t.Fatalf("UsageKindFromPath(%q) = %q,%v want %q", path, kind, ok, want)
		}
	}
}
