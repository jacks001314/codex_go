package appserver

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFeedbackReadAttachmentPathLogsSkipDiagnosticsLikeRust covers Rust #49852:
// the reader logs why a path-backed attachment was skipped (missing rollout,
// non-regular file, size limit).
func TestFeedbackReadAttachmentPathLogsSkipDiagnosticsLikeRust(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	// Missing source.
	if _, ok, err := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: filepath.Join(dir, "missing.jsonl")}, 1024); err != nil || ok {
		t.Fatalf("missing path: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(logs.String(), "feedback attachment skipped: rollout is missing or not a regular file") {
		t.Fatalf("missing path logs = %q", logs.String())
	}

	// Directory (non-regular file).
	logs.Reset()
	if _, ok, err := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: dir}, 1024); err != nil || ok {
		t.Fatalf("directory: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(logs.String(), "feedback attachment skipped: not a regular file") {
		t.Fatalf("directory logs = %q", logs.String())
	}

	// Oversized source.
	logs.Reset()
	oversized := filepath.Join(dir, "oversized.jsonl")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), 64), 0o600); err != nil {
		t.Fatalf("write oversized: %v", err)
	}
	if _, ok, err := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: oversized}, 8); err != nil || ok {
		t.Fatalf("oversized: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(logs.String(), "feedback attachment skipped: size limit exceeded") {
		t.Fatalf("oversized logs = %q", logs.String())
	}
}
