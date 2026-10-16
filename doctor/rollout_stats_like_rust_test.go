package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRolloutStatsDetailsCountPlainAndCompressedFilesLikeRust mirrors upstream #54003
// (0c02887a0c, codex-rs/cli/src/doctor.rs): doctor storage statistics must recognise
// compressed `.jsonl.zst` rollouts in the active and archived counts, count both physical
// files when plain and compressed representations coexist, and still ignore unrelated
// files and compression temporary files.
func TestRolloutStatsDetailsCountPlainAndCompressedFilesLikeRust(t *testing.T) {
	temp := t.TempDir()
	nested := filepath.Join(temp, "sessions", "2026", "05", "13")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create nested sessions dir: %v", err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(filepath.Join(nested, "rollout-2026-05-13T00-00-00-test.jsonl"), "12345")
	// Storage totals include both physical files when both representations exist.
	write(filepath.Join(nested, "rollout-2026-05-13T00-00-00-test.jsonl.zst"), "1234567")
	write(filepath.Join(nested, "rollout-2026-05-13T01-00-00-compressed.jsonl.zst"), "123")
	write(filepath.Join(nested, "not-a-rollout.jsonl"), "ignored")
	write(filepath.Join(nested, "not-a-rollout.jsonl.zst"), "ignored")
	write(filepath.Join(nested, "rollout-2026-05-13T01-00-00-compressed.jsonl.zst.tmp"), "ignored")
	archived := filepath.Join(temp, "archived_sessions")
	if err := os.MkdirAll(archived, 0o755); err != nil {
		t.Fatalf("create archived dir: %v", err)
	}
	write(filepath.Join(archived, "rollout-2026-05-13T02-00-00-archived.jsonl.zst"), "1234")

	var details []string
	rolloutStatsDetailsForDoctor(&details, temp)
	got := strings.Join(details, "\n")
	want := strings.Join([]string{
		"active rollout files: 3 files, 15 total bytes, 5 average bytes",
		"archived rollout files: 1 files, 4 total bytes, 4 average bytes",
	}, "\n")
	if got != want {
		t.Fatalf("rollout stats details = %q, want %q", got, want)
	}
}
