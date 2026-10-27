package agentboard

import (
	"testing"

	"codex_go/state"
)

// Mirrors Rust codex-guardian-v3 local_board_options (#56460): setup, opening
// and cleanup all share the guardian namespace, and reviewers can open the
// same board read-only.
func TestGuardianLocalBoardHelpersLikeRust(t *testing.T) {
	if got := GuardianLocalBoardOptions().StorageNamespace; got != "guardian" {
		t.Fatalf("guardian storage namespace = %q, want %q", got, "guardian")
	}
	board := NewGuardianLocalBoard(state.SqliteConfig{}, "main-thread")
	if board == nil {
		t.Fatal("NewGuardianLocalBoard returned nil")
	}
	if board.identity != "main-thread" {
		t.Fatalf("guardian board identity = %q, want main-thread", board.identity)
	}
	if got := board.options.StorageNamespace; got != GuardianBoardStorageNamespace {
		t.Fatalf("guardian board namespace = %q, want %q", got, GuardianBoardStorageNamespace)
	}
	if board.ReadOnly() == nil {
		t.Fatal("guardian reviewer connection is nil")
	}
}
