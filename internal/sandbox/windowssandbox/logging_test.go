package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogFilePathForUTCDateMatchesRustName(t *testing.T) {
	got := LogFilePathForUTCDate("logs", time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	want := filepath.Join("logs", "sandbox.2026-05-21.log")
	if got != want {
		t.Fatalf("LogFilePathForUTCDate() = %q, want %q", got, want)
	}
}

func TestCurrentLogFilePathForCodexHomeUsesSandboxDir(t *testing.T) {
	home := filepath.Join("codex-home")
	got := CurrentLogFilePathForCodexHome(home)
	if !strings.HasPrefix(got, SandboxDir(home)+string(os.PathSeparator)) {
		t.Fatalf("CurrentLogFilePathForCodexHome() = %q", got)
	}
}

func TestLogNoteWritesDailyLog(t *testing.T) {
	home := t.TempDir()
	if err := LogNote(home, "hello daily log"); err != nil {
		t.Fatalf("LogNote() error = %v", err)
	}
	entries, err := os.ReadDir(SandboxDir(home))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "sandbox.") || !strings.HasSuffix(entries[0].Name(), ".log") {
		t.Fatalf("log filename = %q", entries[0].Name())
	}
	data, err := os.ReadFile(filepath.Join(SandboxDir(home), entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile(log) error = %v", err)
	}
	if !strings.Contains(string(data), "hello daily log") {
		t.Fatalf("log content = %q", string(data))
	}
}

func TestCommandPreviewDoesNotSplitUTF8Boundary(t *testing.T) {
	prefix := strings.Repeat("x", LogCommandPreviewLimit-1)
	got := CommandPreview([]string{prefix + "香"})
	if len(got) > LogCommandPreviewLimit || !utf8Valid(got) {
		t.Fatalf("preview len=%d valid=%v", len(got), utf8Valid(got))
	}
}

func utf8Valid(value string) bool {
	return strings.ToValidUTF8(value, "") == value
}
