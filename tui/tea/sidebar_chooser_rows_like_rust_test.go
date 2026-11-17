package tea

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"codex_go/tui/keymsg"
)

// Rust app/snapshots/codex_tui__app__tests__saved_chat_discovery_states.snap:
// a wide pane paints the loading row and the inline new-chat subtitle, with the
// body inset by one column and two reserved trailing columns.
func TestSidebarChooserLoadingRowsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	model.transcript.Height = 24
	model.sidebarDiscovery = SideChatDiscoveryLoading
	model.sidebarDiscoveryStarted = time.Now()
	model.animationsEnabled = false

	panel := model.renderSidebarChooser(80, 24)
	rows := strings.Split(ansi.Strip(panel), "\n")
	if len(rows) != 24 {
		t.Fatalf("chooser painted %d rows, want the pane height", len(rows))
	}
	if rows[2] != " Open in sidebar" {
		t.Fatalf("title row = %q", rows[2])
	}
	if rows[3] != " ◌ Loading side chats…" {
		t.Fatalf("loading row = %q", rows[3])
	}
	newChat := rows[4]
	if !strings.HasPrefix(newChat, " › + New side chat") || !strings.HasSuffix(newChat, "Start from this conversation") {
		t.Fatalf("new-chat row = %q", newChat)
	}
	// The inline subtitle ends at the body's last usable column, so the row
	// fills the body width including its leading inset column.
	if ansi.StringWidth(newChat) != sidebarChooserBodyWidth(80) {
		t.Fatalf("new-chat row width = %d, want %d", ansi.StringWidth(newChat), sidebarChooserBodyWidth(80))
	}
	footer := rows[23]
	for _, want := range []string{"enter open", "esc back", "↑/↓ move", "hide pane"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer = %q, want %q", footer, want)
		}
	}
}

// Rust saved_chat_discovery_states.snap: a failed discovery paints the error
// verbatim on row 3, leaves the blank gap rows around the group header, and
// selects the inline retry row, whose accept hint reads "retry".
func TestSidebarChooserFailedRetryRowsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.onListSideChats = func(string) ([]SideChatChoice, error) {
		return nil, errors.New("Permission denied by storage")
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	if model.sidebarDiscovery != SideChatDiscoveryFailed {
		t.Fatalf("discovery = %q, want failed", model.sidebarDiscovery)
	}
	model.transcript.Height = 20
	panel := model.renderSidebarChooser(40, 20)
	rows := strings.Split(ansi.Strip(panel), "\n")
	if rows[3] != " Permission denied by storage" {
		t.Fatalf("error row = %q", rows[3])
	}
	if rows[4] != "   + New side chat" || rows[5] != "     Start from this conversation" {
		t.Fatalf("new-chat rows = %q / %q", rows[4], rows[5])
	}
	if strings.TrimSpace(rows[6]) != "" || strings.TrimSpace(rows[7]) != "" {
		t.Fatalf("gap rows = %q / %q, want blanks", rows[6], rows[7])
	}
	if rows[8] != " ›   Retry loading side chats" {
		t.Fatalf("retry row = %q", rows[8])
	}
	if footer := rows[19]; !strings.Contains(footer, "enter retry") {
		t.Fatalf("footer = %q, want the retry accept hint", footer)
	}
}

// Rust saved_chat_loading__waiting_and_retry_rows.snap: the pending attach state
// is appended to its chat row and the footer flips to "cancel opening"; a
// failure paints "Retry · {error}" and accept flips to "retry".
func TestSidebarChooserOpenStatesLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	choices := make([]SideChatChoice, 0, 4)
	for index := 1; index <= 4; index++ {
		choices = append(choices, SideChatChoice{ThreadID: fmt.Sprintf("saved-%d", index)})
	}
	model.onListSideChats = func(string) ([]SideChatChoice, error) { return choices, nil }
	model.onOpenSideChat = func(parentThreadID, sideThreadID string) (AgentThreadSwitchResponse, error) {
		return AgentThreadSwitchResponse{Status: "idle"}, nil
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	model.transcript.Height = 20
	model.sidebarChoice = 4
	model.sideOpeningThreadID = "saved-4"
	model.sideOpenState = sideOpenWaiting

	panel := ansi.Strip(model.renderSidebarChooser(80, 20))
	rows := strings.Split(panel, "\n")
	waiting := false
	for _, row := range rows {
		if strings.Contains(row, "○ Side chat 4  Waiting to open…") {
			waiting = true
		}
	}
	if !waiting {
		t.Fatalf("waiting row missing:\n%s", panel)
	}
	if footer := rows[19]; !strings.Contains(footer, "enter open") || !strings.Contains(footer, "esc cancel opening") {
		t.Fatalf("waiting footer = %q", footer)
	}

	model.sideOpenState = sideOpenFailed
	model.sideOpenError = "thread not loaded: offline"
	panel = ansi.Strip(model.renderSidebarChooser(80, 20))
	rows = strings.Split(panel, "\n")
	retry := false
	for _, row := range rows {
		if strings.Contains(row, "○ Side chat 4  Retry · thread not loaded: offline") {
			retry = true
		}
	}
	if !retry {
		t.Fatalf("failed row missing:\n%s", panel)
	}
	if footer := rows[19]; !strings.Contains(footer, "enter retry") {
		t.Fatalf("failed footer = %q", footer)
	}

	_, cmd := model.Update(key(keymsg.KeyEnter))
	if model.sideOpenState != sideOpenLoading || cmd == nil {
		t.Fatalf("Enter on a failed row state=%q cmd=%t, want a retry", model.sideOpenState, cmd != nil)
	}
	runTeaCmdChain(t, model, cmd, 0)
	if model.State.ThreadID != "saved-4" {
		t.Fatalf("retry opened thread %q, want saved-4", model.State.ThreadID)
	}
}
