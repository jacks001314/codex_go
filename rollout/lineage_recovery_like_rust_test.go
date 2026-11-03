package rollout

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestResolveHistoryPrefixRecoversFromAlternateRolloutCopyLikeRust covers Rust
// #58959: a truncated preferred ancestor rollout must not hide a usable
// archived copy of the same thread.
func TestResolveHistoryPrefixRecoversFromAlternateRolloutCopyLikeRust(t *testing.T) {
	home := t.TempDir()
	parentID := "0192f0aa-0000-7000-8000-000000000071"
	stamp := "2025-01-03T12-00-00"
	archived := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, parentID))
	base := writePaginatedAncestor(t, home, archived, parentID)

	active := filepath.Join(home, SessionsSubdir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, parentID))
	writeTruncatedRolloutCopy(t, archived, active)

	_, turns, err := ResolveHistoryPrefix(home, base)
	if err != nil {
		t.Fatalf("ResolveHistoryPrefix() error = %v", err)
	}
	if len(turns) != 1 || turns[0].ID != "parent-turn" {
		t.Fatalf("resolved turns = %#v", turns)
	}
}

// TestResolveHistoryPrefixRejectsUnmanagedReplacementLikeRust keeps the
// preferred copy's error when the alternate copy belongs to another thread.
func TestResolveHistoryPrefixRejectsUnmanagedReplacementLikeRust(t *testing.T) {
	home := t.TempDir()
	parentID := "0192f0aa-0000-7000-8000-000000000072"
	otherID := "0192f0aa-0000-7000-8000-000000000073"
	stamp := "2025-01-03T12-00-00"
	active := filepath.Join(home, SessionsSubdir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, parentID))
	base := writePaginatedAncestor(t, home, active, parentID)
	writeTruncatedRolloutCopy(t, active, active)
	archived := filepath.Join(home, ArchivedSessionsSubdir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, parentID))
	writePaginatedAncestor(t, home, archived, otherID)

	_, _, err := ResolveHistoryPrefix(home, base)
	if err == nil || !strings.Contains(err.Error(), "exceeds durable rollout") {
		t.Fatalf("ResolveHistoryPrefix() error = %v, want the preferred copy's boundary error", err)
	}
}

// writePaginatedAncestor writes one paginated turn at path and returns the
// history position that covers it.
func writePaginatedAncestor(t *testing.T, home, path, threadID string) HistoryPosition {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 1, 3, 12, 0, 0, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, ThreadID: threadID, Source: "cli", CWD: "/repo",
		ModelProvider: "openai", HistoryMode: "paginated", Path: path, Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendTurnStarted("parent-turn", now); err != nil {
		t.Fatalf("AppendTurnStarted() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	steps, nextOffset, err := ReadProjectionSteps(path, 0, 0)
	if err != nil {
		t.Fatalf("ReadProjectionSteps() error = %v", err)
	}
	endOrdinal := uint64(0)
	for _, step := range steps {
		if step.Kind == ProjectionLine && step.Line != nil && step.Line.Ordinal != nil {
			endOrdinal = *step.Line.Ordinal + 1
		}
	}
	return HistoryPosition{ThreadID: threadID, EndOrdinalExclusive: endOrdinal, EndByteOffset: nextOffset}
}

// writeTruncatedRolloutCopy copies source's metadata line into target and
// stops there, so a reader can identify the rollout but not read its history.
func writeTruncatedRolloutCopy(t *testing.T, source, target string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	metaEnd := bytes.IndexByte(data, '\n') + 1
	if metaEnd <= 1 {
		t.Fatalf("rollout %s has no metadata line", source)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data[:metaEnd], 0o644); err != nil {
		t.Fatal(err)
	}
}
