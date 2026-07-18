package chatcomposer

import (
	"reflect"
	"testing"
	"time"
)

func TestPopupStateLifecycleDismissTokensMatchRustCore(t *testing.T) {
	popup := NewPopupState()
	if popup.ActivePopup() {
		t.Fatal("new popup state should be inactive")
	}
	popup.OpenFile("@")
	if !popup.ActivePopup() || popup.Active != ActivePopupFile || popup.CurrentFileQuery != "@" {
		t.Fatalf("file popup state = %#v", popup)
	}
	popup.DismissFileToken("@foo")
	if popup.ActivePopup() || popup.ShouldShowFilePopup("@foo", "foo") {
		t.Fatalf("dismissed file token should suppress popup: %#v", popup)
	}
	if !popup.ShouldShowFilePopup("@bar", "bar") {
		t.Fatal("different file token should be allowed")
	}
	popup.OpenMentionV2()
	popup.DismissMentionToken("@skill")
	if popup.ShouldShowMentionPopup("@skill") || !popup.ShouldShowMentionPopup("@other") {
		t.Fatalf("mention dismissal mismatch: %#v", popup)
	}
	popup.OpenCommand()
	if !popup.ActivePopup() || popup.Active != ActivePopupCommand {
		t.Fatalf("command popup state = %#v", popup)
	}
	popup.Clear()
	if popup.ActivePopup() || popup.Active != ActivePopupNone {
		t.Fatalf("clear popup state = %#v", popup)
	}
}

func TestFooterStateFlashContextAndQuitReminderMatchRustCore(t *testing.T) {
	now := time.Unix(100, 0)
	footer := NewFooterState()
	footer.StatusLineEnabled = true
	footer.StatusLineValue = "model: gpt-5"
	footer.ActiveAgentLabel = "Robie [explorer]"
	footer.SideConversationContextLabel = "side"
	footer.IDEContextActive = true
	footer.GoalStatusIndicator = "active"
	if got := footer.ContextIndicators(); !reflect.DeepEqual(got, []string{"model: gpt-5", "Robie [explorer]", "side", "active", "IDE context"}) {
		t.Fatalf("context indicators = %#v", got)
	}
	if got := footer.ContextLine(); got != "model: gpt-5 \u00b7 Robie [explorer] \u00b7 side \u00b7 active \u00b7 IDE context" {
		t.Fatalf("context line = %q", got)
	}
	if text, ok := footer.StatusLineText(); !ok || text != "model: gpt-5" {
		t.Fatalf("status line text=%q ok=%v", text, ok)
	}
	footer.ShowFlash("Saved", time.Second, now)
	if !footer.FlashVisibleAt(now.Add(500 * time.Millisecond)) {
		t.Fatal("flash should be visible before expiry")
	}
	footer.SetQuitShortcutReminder("Ctrl+D", time.Second, now)
	if footer.Mode != ComposerFooterModeQuitShortcutReminder || !footer.QuitShortcutActiveAt(now.Add(500*time.Millisecond)) {
		t.Fatalf("quit reminder = %#v", footer)
	}
	if !footer.ClearExpiredTransientState(now.Add(2 * time.Second)) {
		t.Fatal("expired transient state should report changed")
	}
	if footer.Flash != nil || footer.QuitShortcutExpiresAt != nil || footer.Mode != ComposerFooterModeComposerEmpty {
		t.Fatalf("expired state not cleared: %#v", footer)
	}
}
