package tui

import (
	"path/filepath"
	"testing"
)

func TestGoalObjectiveFileReferenceRoundTrip(t *testing.T) {
	codexHome := t.TempDir()
	path := filepath.Join(codexHome, GoalAttachmentDir, "00000000-0000-4000-8000-000000000000", GoalFileName)
	reference, err := GoalObjectiveFileReference(path)
	if err != nil {
		t.Fatalf("reference: %v", err)
	}
	got, ok := GoalObjectiveFilePath(reference, codexHome)
	if !ok || filepath.Clean(got) != filepath.Clean(path) {
		t.Fatalf("round trip = %q ok=%v want %q", got, ok, path)
	}
}

func TestGoalObjectiveFilePathRejectsUnmanagedPaths(t *testing.T) {
	codexHome := t.TempDir()
	uuid := "00000000-0000-4000-8000-000000000000"
	reference := GoalFilePrefix +
		filepath.Join(codexHome, GoalAttachmentDir, uuid, GoalFileName) +
		GoalFileSuffix

	if _, ok := GoalObjectiveFilePath(reference, t.TempDir()); ok {
		t.Fatalf("wrong codex home accepted")
	}
	if _, ok := GoalObjectiveFilePath("plain objective", codexHome); ok {
		t.Fatalf("plain objective accepted")
	}
	if _, ok := GoalObjectiveFilePath("", codexHome); ok {
		t.Fatalf("empty objective accepted")
	}
	badUUID := GoalFilePrefix +
		filepath.Join(codexHome, GoalAttachmentDir, "not-a-uuid", GoalFileName) +
		GoalFileSuffix
	if _, ok := GoalObjectiveFilePath(badUUID, codexHome); ok {
		t.Fatalf("invalid attachment uuid accepted")
	}
	otherName := GoalFilePrefix +
		filepath.Join(codexHome, GoalAttachmentDir, uuid, "other.md") +
		GoalFileSuffix
	if _, ok := GoalObjectiveFilePath(otherName, codexHome); ok {
		t.Fatalf("non-goal file name accepted")
	}
	if _, ok := GoalObjectiveFilePath(GoalFilePrefix+"relative/path"+GoalFileSuffix, codexHome); ok {
		t.Fatalf("relative reference accepted")
	}
}
