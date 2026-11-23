package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

// Rust #63419: going offline can reorder the slash menu while the user is
// choosing. The highlighted command must stay highlighted, and a pending Enter
// must never fall through to the first row (the `/export` to `/exit` hazard).

func onlineSlashModel(t *testing.T, text string) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-online")
	model := NewModel(state, Options{Width: 120, Height: 24})
	typeText(t, model, text)
	if !model.slashPopup.Active {
		t.Fatalf("slash popup did not open for %q", text)
	}
	return model
}

// selectSlashPopupName walks the popup until the named row is highlighted.
func selectSlashPopupName(t *testing.T, model *Model, name string) {
	t.Helper()
	if model.selectedSlashPopupName() == name {
		return
	}
	for range len(model.slashPopup.Items) + 1 {
		model.moveSlashPopupSelection(1)
		if model.selectedSlashPopupName() == name {
			return
		}
	}
	t.Fatalf("slash popup row %q not selectable: %#v", name, model.slashPopup.Items)
}

func TestSlashSelectionSurvivesConnectionChangeLikeRust(t *testing.T) {
	model := onlineSlashModel(t, "/ex")
	selectSlashPopupName(t, model, "export")
	model.Update(AppServerDisconnectMsg{})
	if !model.appServerDisconnected {
		t.Fatal("disconnect did not enter the offline composer state")
	}
	if got := model.selectedSlashPopupName(); got != "export" {
		t.Fatalf("highlighted command after disconnect = %q, want export", got)
	}
	// /export needs the server, so Enter keeps the draft and submits nothing.
	_, cmd := model.Update(key(keymsg.KeyEnter))
	if cmd != nil {
		t.Fatal("blocked offline command returned a command")
	}
	if got := model.ComposerValue(); got != "/ex" {
		t.Fatalf("offline Enter changed the draft to %q", got)
	}
	if len(model.queued) != 0 {
		t.Fatalf("offline Enter queued %#v", model.queued)
	}
}

// Rust clears the selection when the highlighted command leaves the menu and
// then refuses to reinterpret a pending submit as the draft.
func TestSlashSelectionClearsWhenCommandDisappearsLikeRust(t *testing.T) {
	model := onlineSlashModel(t, "/ex")
	selectSlashPopupName(t, model, "export")
	model.restoreSlashPopupSelection("vanished-command")
	if model.slashPopup.Selected != -1 {
		t.Fatalf("selection = %d after the command disappeared, want none", model.slashPopup.Selected)
	}
	if model.selectedSlashPopupName() != "" {
		t.Fatalf("empty selection still reports %q", model.selectedSlashPopupName())
	}
	if strings.Contains(utils.StripANSI(model.renderSlashPopup()), "›") {
		t.Fatal("empty selection still rendered a highlighted row")
	}
	model.Update(AppServerDisconnectMsg{})
	_, cmd := model.Update(key(keymsg.KeyEnter))
	if cmd != nil {
		t.Fatal("Enter with no actionable selection returned a command")
	}
	if got := model.ComposerValue(); got != "/ex" {
		t.Fatalf("Enter with no selection changed the draft to %q", got)
	}
	if len(model.queued) != 0 {
		t.Fatalf("Enter with no selection queued %#v", model.queued)
	}
}

// A redraw with the same filter must not reselect the first row; only a new
// filter does (Rust on_composer_text_change).
func TestSlashPopupRefreshKeepsEmptySelectionLikeRust(t *testing.T) {
	model := onlineSlashModel(t, "/ex")
	model.slashPopup.Selected = -1
	model.refreshSlashPopup()
	if model.slashPopup.Selected != -1 {
		t.Fatalf("refresh reselected row %d", model.slashPopup.Selected)
	}
	// Down from an empty selection moves to the first selectable row.
	model.moveSlashPopupSelection(1)
	if got, ok := model.currentSlashPopupItem(); !ok || got.Name != model.slashPopup.Items[0].Name {
		t.Fatalf("Down from empty selection chose %#v", model.slashPopup)
	}
	// Up from an empty selection moves to the last selectable row.
	model.slashPopup.Selected = -1
	model.moveSlashPopupSelection(-1)
	last := model.slashPopup.Items[len(model.slashPopup.Items)-1]
	if got := model.selectedSlashPopupName(); got != last.Name {
		t.Fatalf("Up from empty selection chose %q, want %q", got, last.Name)
	}
}

// Rust #63419: Tab completes a restricted command without queueing it.
func TestRestrictedTabCompletesWithoutQueuingLikeRust(t *testing.T) {
	model, now := newDisconnectedSlashModel(t)
	model.setStatus("running")
	typeText(t, model, "/config")
	pastPasteBurst(now)
	model.Update(key(keymsg.KeyTab))
	// An exact match has nothing to complete, so the draft stays as typed.
	if got := model.ComposerValue(); got != "/config" {
		t.Fatalf("offline Tab completion = %q", got)
	}
	if len(model.queued) != 0 {
		t.Fatalf("offline Tab queued %#v", model.queued)
	}
}

// Rust #63419: an offline /feedback reveals the thread id from older history
// and points at the retry command.
func TestOfflineFeedbackRevealsThreadIDFromOlderHistoryLikeRust(t *testing.T) {
	model, _ := newDisconnectedSlashModel(t)
	for index := 0; index < 60; index++ {
		model.State.AddMessage(codextui.RoleAssistant, "older row")
	}
	model.refreshTranscript()
	model.transcript.SetYOffset(0)
	model.activityFollow = false
	if model.transcript.AtBottom() {
		t.Fatal("transcript did not scroll away from the latest rows")
	}
	typeText(t, model, "/feedback")
	_, cmd := model.Update(key(keymsg.KeyEnter))
	runTeaCmd(t, model, cmd)
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "Thread ID thread-offline Reconnect and retry /feedback.") {
		t.Fatalf("offline /feedback guidance missing from the visible transcript:\n%s", TailLines(view, 12))
	}
	if !model.activityFollow {
		t.Fatal("offline /feedback did not resume following the latest rows")
	}
}

// TailLines returns the last n lines of a rendered view for failure messages.
func TailLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
