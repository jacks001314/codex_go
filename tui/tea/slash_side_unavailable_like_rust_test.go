package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/utils"
)

// Rust #50756 (cd7d9e128c, codex-rs/tui/src/bottom_pane/{command_popup.rs,
// slash_commands.rs, chat_composer/slash_input.rs}): a side conversation shows
// a known-but-unavailable slash command as a disabled row once it is searched,
// keeps it hidden from the unfiltered `/` menu, ranks available matches first,
// and never lets a disabled row be selected, completed, queued or dispatched.
func newSideConversationModel(t *testing.T) (*Model, *time.Time) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
	})
	now := time.Unix(0, 0)
	model.now = func() time.Time { return now }
	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if !model.inSideConversation() {
		t.Fatal("failed to enter a side conversation")
	}
	return model, &now
}

func TestSlashSideUnavailableHiddenFromUnfilteredMenuLikeRust(t *testing.T) {
	model, _ := newSideConversationModel(t)
	typeText(t, model, "/")
	if !model.slashPopup.Active {
		t.Fatal("slash popup should be active after typing /")
	}
	if len(model.slashPopup.Items) == 0 {
		t.Fatal("side conversation unfiltered menu is empty")
	}
	for _, item := range model.slashPopup.Items {
		if item.Name == "archive" || item.Unavailable {
			t.Fatalf("unfiltered side menu must hide unavailable commands: %#v", model.slashPopup.Items)
		}
	}
}

func TestSlashSideUnavailableShownDisabledWhenSearchedLikeRust(t *testing.T) {
	model, now := newSideConversationModel(t)
	typeText(t, model, "/arch")
	if len(model.slashPopup.Items) != 1 {
		t.Fatalf("side /arch items = %#v, want exactly one disabled archive", model.slashPopup.Items)
	}
	item := model.slashPopup.Items[0]
	if item.Name != "archive" || !item.Unavailable {
		t.Fatalf("side /arch item = %#v, want disabled archive", item)
	}
	if got := model.selectedSlashPopupName(); got != "" {
		t.Fatalf("disabled archive must not be selected, got %q", got)
	}
	if _, ok := model.currentSlashPopupItem(); ok {
		t.Fatal("disabled archive must not be completable or dispatchable")
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "/archive (disabled)") || !strings.Contains(view, "not available in a side conversation") {
		t.Fatalf("disabled row missing from side view:\n%s", view)
	}

	// Tab with only a disabled match visible must swallow instead of completing
	// or queuing the unavailable command.
	*now = now.Add(bottompane.PasteBurstCharInterval + time.Millisecond)
	model.Update(key(bubbletea.KeyTab))
	if got := model.ComposerValue(); got != "/arch" {
		t.Fatalf("composer after Tab on a disabled match = %q, want /arch", got)
	}
	if !model.slashPopup.Active {
		t.Fatal("popup should stay open after a swallowed Tab")
	}

	// `/` still inserts normally when there is no selectable command.
	model.Update(runes("/"))
	if got := model.ComposerValue(); got != "/arch/" {
		t.Fatalf("composer after / = %q, want /arch/", got)
	}
}

func TestSlashSideRanksAvailableMatchesFirstLikeRust(t *testing.T) {
	model, now := newSideConversationModel(t)
	typeText(t, model, "/s")
	items := model.slashPopup.Items
	if len(items) < 2 || items[0].Name != "status" || items[0].Unavailable {
		t.Fatalf("side /s items = %#v, want available status first", items)
	}
	if got := model.selectedSlashPopupName(); got != "status" {
		t.Fatalf("side /s selected = %q, want available status", got)
	}
	sawDisabled := false
	for _, item := range items[1:] {
		if item.Unavailable {
			sawDisabled = true
		}
	}
	if !sawDisabled {
		t.Fatalf("side /s should still list disabled matches: %#v", items)
	}

	*now = now.Add(bottompane.PasteBurstCharInterval + time.Millisecond)
	model.Update(key(bubbletea.KeyTab))
	if got := model.ComposerValue(); got != "/status " {
		t.Fatalf("composer after Tab = %q, want /status ", got)
	}
	if model.slashPopup.Active {
		t.Fatal("popup should close after completing the available match")
	}
}
