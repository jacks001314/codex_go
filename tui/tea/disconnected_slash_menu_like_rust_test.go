package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

// Rust #63258: after the app-server transport fails the "/" menu still opens,
// lists every command, and marks the server-dependent ones as disabled while
// local commands stay runnable.
func newDisconnectedSlashModel(t *testing.T) (*Model, *time.Time) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-offline")
	model := NewModel(state, Options{Width: 120, Height: 24})
	now := time.Unix(0, 0)
	model.now = func() time.Time { return now }
	model.Update(AppServerDisconnectMsg{})
	if !model.appServerDisconnected {
		t.Fatal("app-server disconnect did not enter the offline composer state")
	}
	return model, &now
}

// pastPasteBurst moves the composer clock beyond the paste burst so Tab is a
// completion instead of literal indentation.
func pastPasteBurst(now *time.Time) {
	*now = now.Add(bottompane.PasteBurstCharInterval + time.Millisecond)
}

func TestDisconnectedSlashMenuShowsServerCommandsDisabledLikeRust(t *testing.T) {
	model, _ := newDisconnectedSlashModel(t)
	typeText(t, model, "/")
	if !model.slashPopup.Active || len(model.slashPopup.Items) == 0 {
		t.Fatalf("offline / menu is empty: %#v", model.slashPopup)
	}
	seenDisabled := false
	sawModel := false
	for _, item := range model.slashPopup.Items {
		if item.Unavailable {
			seenDisabled = true
			if item.DisabledReason != slashPopupDisabledReasonDisconnected {
				t.Fatalf("%s disabled reason = %q", item.Name, item.DisabledReason)
			}
			continue
		}
		if seenDisabled {
			t.Fatalf("usable row %q sorted after a disabled row", item.Name)
		}
		switch item.Name {
		case "model":
			sawModel = true
			t.Fatal("model command must be disabled while offline")
		}
	}
	if !seenDisabled {
		t.Fatal("offline menu kept every command usable")
	}
	if sawModel {
		t.Fatal("unreachable")
	}
	if item, ok := model.currentSlashPopupItem(); !ok || item.Unavailable {
		t.Fatalf("offline menu selection = %#v ok=%v, want a local command", item, ok)
	}
	for _, name := range []string{"config", "warnings", "feedback", "exit"} {
		found := false
		for _, item := range model.slashPopup.Items {
			if item.Name == name {
				found = true
				if item.Unavailable {
					t.Fatalf("local command %q is disabled offline", name)
				}
			}
		}
		if !found {
			t.Fatalf("offline menu omitted local command %q", name)
		}
	}
}

func TestDisconnectedSlashSearchExplainsServerRequirementLikeRust(t *testing.T) {
	model, now := newDisconnectedSlashModel(t)
	typeText(t, model, "/model")
	if len(model.slashPopup.Items) != 1 {
		t.Fatalf("offline /model items = %#v", model.slashPopup.Items)
	}
	item := model.slashPopup.Items[0]
	if item.Name != "model" || !item.Unavailable {
		t.Fatalf("offline /model item = %#v", item)
	}
	if description := slashPopupDisplayDescription(item); !strings.Contains(description, "requires a server connection") {
		t.Fatalf("offline /model description = %q", description)
	}
	if _, ok := model.currentSlashPopupItem(); ok {
		t.Fatal("disabled offline row must not be completable or dispatchable")
	}
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "/model (disabled)") || !strings.Contains(view, "requires a server connection") {
		t.Fatalf("offline popup missing the disabled explanation:\n%s", view)
	}
	pastPasteBurst(now)
	model.Update(key(keymsg.KeyTab))
	if got := model.ComposerValue(); got != "/model" {
		t.Fatalf("Tab on a disabled offline row changed the draft to %q", got)
	}
	if !model.slashPopup.Active {
		t.Fatal("Tab on a disabled offline row closed the popup instead of leaving it open")
	}
}

func TestDisconnectedComposerRunsLocalCommandsOnlyLikeRust(t *testing.T) {
	model, now := newDisconnectedSlashModel(t)
	typeText(t, model, "/con")
	if _, ok := model.currentSlashPopupItem(); !ok {
		t.Fatal("offline /config row is not selectable")
	}
	pastPasteBurst(now)
	model.Update(key(keymsg.KeyTab))
	if got := model.ComposerValue(); got != "/config " {
		t.Fatalf("offline /config completion = %q", got)
	}

	model.resetComposer()
	typeText(t, model, "/model")
	model.Update(key(keymsg.KeyEnter))
	if got := model.ComposerValue(); got != "/model" {
		t.Fatalf("offline /model popup submission changed the draft to %q", got)
	}
	model.Update(key(keymsg.KeyEsc))
	model.Update(key(keymsg.KeyEnter))
	if got := model.ComposerValue(); got != "/model" {
		t.Fatalf("offline /model submission changed the draft to %q", got)
	}
	if !strings.Contains(model.notice, "requires a server connection") {
		t.Fatalf("offline /model notice = %q", model.notice)
	}
}

func TestDisconnectedFeedbackReportsThreadAndReconnectLikeRust(t *testing.T) {
	model, _ := newDisconnectedSlashModel(t)
	typeText(t, model, "/feedback")
	_, cmd := model.Update(key(keymsg.KeyEnter))
	runTeaCmd(t, model, cmd)
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "Thread ID thread-offline") {
		t.Fatalf("offline /feedback omitted the thread id:\n%s", view)
	}
	if !strings.Contains(view, "Reconnect to send feedback or save a ZIP.") {
		t.Fatalf("offline /feedback omitted the reconnect guidance:\n%s", view)
	}
}

func TestDisconnectedComposerKeepsMultilineDraftLikeRust(t *testing.T) {
	model, _ := newDisconnectedSlashModel(t)
	typeText(t, model, "/q")
	model.insertComposerNewline()
	typeText(t, model, "rest of the draft")
	if model.slashPopup.Active {
		t.Fatalf("offline popup opened over a multiline draft: %#v", model.slashPopup)
	}
	if !strings.HasPrefix(model.ComposerValue(), "/q\n") {
		t.Fatalf("multiline draft = %q", model.ComposerValue())
	}
}

// A fresh connection restores the full menu.
func TestReconnectRestoresDisconnectedSlashMenuLikeRust(t *testing.T) {
	model, _ := newDisconnectedSlashModel(t)
	model.resetThreadScopedState()
	if model.appServerDisconnected {
		t.Fatal("thread reset kept the offline composer state")
	}
	typeText(t, model, "/")
	for _, item := range model.slashPopup.Items {
		if item.DisabledReason == slashPopupDisabledReasonDisconnected {
			t.Fatalf("restored menu still marks %q disconnected", item.Name)
		}
	}
}
