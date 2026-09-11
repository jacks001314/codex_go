package memories

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validV2Summary() string {
	return "v1\n" +
		"## User Profile\nprofile\n" +
		"## User preferences\nprefs\n" +
		"## General Tips\ntips\n" +
		"## What's in Memory\nmemory\n"
}

func TestIsValidV2Summary(t *testing.T) {
	if !IsValidV2Summary(validV2Summary()) {
		t.Fatal("complete v2 summary must be valid")
	}
	if !IsValidV2Summary(strings.ReplaceAll(validV2Summary(), "\n", "\r\n")) {
		t.Fatal("CRLF v2 summary must be valid")
	}
	if IsValidV2Summary(strings.Replace(validV2Summary(), "v1\n", "v2\n", 1)) {
		t.Fatal("summary must start with v1")
	}
	if IsValidV2Summary(strings.Replace(validV2Summary(), "## General Tips\n", "", 1)) {
		t.Fatal("summary must contain every required heading")
	}
	if IsValidV2Summary(validV2Summary() + strings.Repeat("x", maxV2SummaryBytes)) {
		t.Fatal("oversized summary must be invalid")
	}
	if IsValidV2Summary("") {
		t.Fatal("empty summary must be invalid")
	}
}

func TestConsolidationProgressPersistsLargestCount(t *testing.T) {
	home := t.TempDir()
	if got := MaxConsolidatedThreadCount(home); got != 0 {
		t.Fatalf("initial count = %d, want 0", got)
	}
	if err := RecordConsolidatedThreadCount(home, 12); err != nil {
		t.Fatalf("RecordConsolidatedThreadCount(12) error = %v", err)
	}
	if got := MaxConsolidatedThreadCount(home); got != 12 {
		t.Fatalf("count = %d, want 12", got)
	}
	// Pruning must not lower the recorded maximum.
	if err := RecordConsolidatedThreadCount(home, 5); err != nil {
		t.Fatalf("RecordConsolidatedThreadCount(5) error = %v", err)
	}
	if got := MaxConsolidatedThreadCount(home); got != 12 {
		t.Fatalf("count after smaller record = %d, want 12", got)
	}
	if err := RecordConsolidatedThreadCount(home, 30); err != nil {
		t.Fatalf("RecordConsolidatedThreadCount(30) error = %v", err)
	}
	if got := MaxConsolidatedThreadCount(home); got != 30 {
		t.Fatalf("count after larger record = %d, want 30", got)
	}
}

func TestClearRootsContentsClearsV2Progress(t *testing.T) {
	home := t.TempDir()
	if err := RecordConsolidatedThreadCount(home, 25); err != nil {
		t.Fatalf("record progress: %v", err)
	}
	summaryPath := filepath.Join(V2Root(home), MemorySummaryFilename)
	if err := os.WriteFile(summaryPath, []byte(validV2Summary()), 0o600); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	if err := ClearRootsContents(home); err != nil {
		t.Fatalf("ClearRootsContents() error = %v", err)
	}
	if got := MaxConsolidatedThreadCount(home); got != 0 {
		t.Fatalf("count after reset = %d, want 0", got)
	}
	if _, err := os.Stat(summaryPath); !os.IsNotExist(err) {
		t.Fatalf("v2 summary still present after reset: %v", err)
	}
}
