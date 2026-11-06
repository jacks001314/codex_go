package tea

import (
	"fmt"
	"testing"

	codextui "codex_go/tui"
)

// historyPaginationModel builds a resumable transcript window whose older page
// arrives through LoadOlderHistory.
func historyPaginationModel(t *testing.T, loader OlderHistoryLoadFunc) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-history")
	for index := 0; index < 6; index++ {
		state.AddMessage(codextui.RoleUser, fmt.Sprintf("window-prompt-%d", index))
		for row := 0; row < 4; row++ {
			state.AddMessage(codextui.RoleAssistant, fmt.Sprintf("window-answer-%d row-%d", index, row))
		}
	}
	model := NewModel(state, Options{Width: 80, Height: 12, LoadOlderHistory: loader})
	model.refreshTranscript()
	_ = model.viewString()
	return model
}

// TestOlderHistoryPrependsAndKeepsReadingPositionLikeRust covers Rust
// app/history_pagination.rs: reaching the top row fetches the next page,
// splices it above the window, keeps the reading position, and moves the
// backtrack ordinal with the inserted prompts.
func TestOlderHistoryPrependsAndKeepsReadingPositionLikeRust(t *testing.T) {
	page := OlderHistoryPage{
		Messages: []codextui.Message{
			{Role: codextui.RoleUser, Text: "older-prompt-0"},
			{Role: codextui.RoleAssistant, Text: "older-answer-0"},
			{Role: codextui.RoleUser, Text: "older-prompt-1"},
			{Role: codextui.RoleAssistant, Text: "older-answer-1"},
		},
		Cursor:   "",
		HasOlder: false,
	}
	var requestedThread, requestedCursor string
	loader := func(threadID string, cursor string) (OlderHistoryPage, error) {
		requestedThread, requestedCursor = threadID, cursor
		return page, nil
	}
	model := historyPaginationModel(t, loader)
	model.transcriptHistoryPartial = true
	model.olderHistoryCursor = "cursor-1"
	model.backtrack.Prime("thread-history")
	model.backtrack.NthUserMessage = 2
	rowsBefore := len(model.transcript.lines)
	model.transcript.YOffset = 0

	cmd := model.maybeLoadOlderHistory()
	if cmd == nil {
		t.Fatal("reaching the top of a partial transcript did not request the older page")
	}
	if requestedThread != "" {
		t.Fatal("the loader ran before the command was executed")
	}
	loaded, ok := cmd().(olderHistoryLoadedMsg)
	if !ok {
		t.Fatal("older-history command did not produce a page message")
	}
	if requestedThread != "thread-history" || requestedCursor != "cursor-1" {
		t.Fatalf("loader saw thread=%q cursor=%q", requestedThread, requestedCursor)
	}
	model.Update(loaded)

	if got := len(model.transcript.lines) - rowsBefore; got <= 0 {
		t.Fatal("prepended page added no rows")
	}
	if first := model.State.Messages[0]; first.Text != "older-prompt-0" {
		t.Fatalf("first message = %q, want the oldest prepended prompt", first.Text)
	}
	if model.backtrack.NthUserMessage != 4 {
		t.Fatalf("backtrack ordinal = %d, want 4 after two inserted prompts", model.backtrack.NthUserMessage)
	}
	if model.transcript.YOffset != len(model.transcript.lines)-rowsBefore {
		t.Fatalf("offset = %d, want the inserted rows", model.transcript.YOffset)
	}
	if model.transcriptHistoryPartial {
		t.Fatal("transcript still partial after the last page")
	}
	if model.olderHistoryCursor != "" {
		t.Fatalf("cursor = %q, want cleared", model.olderHistoryCursor)
	}
	if cmd := model.maybeLoadOlderHistory(); cmd != nil {
		t.Fatal("a complete transcript still requested another page")
	}
}

// TestOlderHistoryWaitsForTheTopRowLikeRust keeps the fetch off the tail: the
// reader must scroll to the first row, and a complete transcript never asks.
func TestOlderHistoryWaitsForTheTopRowLikeRust(t *testing.T) {
	calls := 0
	loader := func(threadID string, cursor string) (OlderHistoryPage, error) {
		calls++
		return OlderHistoryPage{}, nil
	}
	model := historyPaginationModel(t, loader)
	if cmd := model.maybeLoadOlderHistory(); cmd != nil || calls != 0 {
		t.Fatal("a complete transcript requested an older page")
	}
	model.transcriptHistoryPartial = true
	model.olderHistoryCursor = "cursor-1"
	model.transcript.GotoBottom()
	if model.transcript.YOffset == 0 {
		t.Skip("transcript fits the viewport")
	}
	if cmd := model.maybeLoadOlderHistory(); cmd != nil {
		t.Fatal("the tail requested an older page before the reader reached the top")
	}
	model.transcript.YOffset = 0
	if cmd := model.maybeLoadOlderHistory(); cmd == nil {
		t.Fatal("the top row did not request the older page")
	}
}

// TestOlderHistoryFollowsTailAndIgnoresStaleThreadLikeRust keeps a
// tail-following reader pinned to the newest rows and drops a page that
// arrived after a thread switch.
func TestOlderHistoryFollowsTailAndIgnoresStaleThreadLikeRust(t *testing.T) {
	page := OlderHistoryPage{
		Messages: []codextui.Message{
			{Role: codextui.RoleUser, Text: "older-prompt"},
			{Role: codextui.RoleAssistant, Text: "older-answer"},
		},
		HasOlder: true,
		Cursor:   "cursor-0",
	}
	model := historyPaginationModel(t, func(string, string) (OlderHistoryPage, error) { return page, nil })
	model.transcriptHistoryPartial = true
	model.olderHistoryCursor = "cursor-1"
	model.transcript.GotoBottom()
	model.transcript.YOffset = 0 // short window: the tail is also the top row

	cmd := model.maybeLoadOlderHistory()
	if cmd == nil {
		t.Fatal("short partial transcript did not request a page")
	}
	model.Update(cmd())

	stale := historyPaginationModel(t, nil)
	stale.olderHistoryLoading = true
	stale.Update(olderHistoryLoadedMsg{threadID: "another-thread", page: page})
	if len(stale.State.Messages) != 30 {
		t.Fatalf("stale page changed a different thread: %d messages", len(stale.State.Messages))
	}
	if stale.olderHistoryLoading {
		t.Fatal("stale page did not clear the in-flight flag")
	}
}
