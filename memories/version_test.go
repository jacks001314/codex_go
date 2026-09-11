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
	if got := BuildMemoryToolDeveloperInstructions(home); !strings.Contains(got, "legacy-only-marker") {
		t.Fatalf("default instructions = %q, want v1", got)
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
