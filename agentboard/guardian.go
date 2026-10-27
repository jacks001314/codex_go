package agentboard

import "codex_go/state"

// GuardianBoardStorageNamespace is Guardian's durable evidence board namespace
// (Rust codex-guardian-v3 local_board_options, #56460). Setup, opening and
// cleanup share it even when Guardian is disabled.
const GuardianBoardStorageNamespace = "guardian"

// GuardianLocalBoardOptions selects Guardian's evidence board.
func GuardianLocalBoardOptions() LocalBoardOptions {
	return LocalBoardOptions{StorageNamespace: GuardianBoardStorageNamespace}
}

// NewGuardianLocalBoard selects the board for the owning main thread, never a
// temporary reviewer or housekeeper. Hosts use the returned board read-write;
// reviewers call ReadOnly on it, which still permits their own subscriptions.
func NewGuardianLocalBoard(sqlite state.SqliteConfig, root string) *LocalBoard {
	return NewLocalBoard(sqlite, root).WithOptions(GuardianLocalBoardOptions())
}
