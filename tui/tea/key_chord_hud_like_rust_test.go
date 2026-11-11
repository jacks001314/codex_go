package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// TestPendingKeyChordGuideRevealsAfterDelayLikeRust mirrors Rust #60637: the
// first stroke stays silent for the reveal delay, then a floating guide lists
// the completions and the Esc cancellation hint. Cancelling removes it.
func TestPendingKeyChordGuideRevealsAfterDelayLikeRust(t *testing.T) {
	config := keyChordConfig(t, "composer", "submit", "ctrl-x ctrl-s")
	if err := config.Set("global", "open_transcript", []string{"ctrl-x ctrl-t"}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(200, 0)
	model := NewModel(codextui.NewState(nil), Options{
		KeymapConfig: config,
		Width:        100,
		Height:       24,
	})
	model.now = func() time.Time { return now }

	model.Update(key(keymsg.KeyCtrlX))
	if model.pendingKeyChordPrefix != "ctrl-x" {
		t.Fatalf("pending prefix = %q", model.pendingKeyChordPrefix)
	}
	if hud := model.renderKeyChordHUD(); hud != "" {
		t.Fatalf("guide revealed before its delay:\n%s", hud)
	}

	now = now.Add(keyChordRevealDelay)
	hud := model.renderKeyChordHUD()
	if !strings.Contains(hud, "ctrl+x") {
		t.Fatalf("guide title missing:\n%s", hud)
	}
	if !strings.Contains(hud, "ctrl+s · Submit") || !strings.Contains(hud, "ctrl+t · Open Transcript") {
		t.Fatalf("guide completions missing:\n%s", hud)
	}
	if !strings.Contains(hud, keyChordHUDCancel) {
		t.Fatalf("guide cancellation missing:\n%s", hud)
	}
	if view := model.viewString(); !strings.Contains(view, keyChordHUDCancel) {
		t.Fatalf("pending guide not painted in the frame:\n%s", view)
	}

	model.Update(key(keymsg.KeyEsc))
	if model.pendingKeyChordPrefix != "" {
		t.Fatalf("pending prefix survived Esc: %q", model.pendingKeyChordPrefix)
	}
	if hud := model.renderKeyChordHUD(); hud != "" {
		t.Fatalf("guide survived Esc:\n%s", hud)
	}
}

// TestPendingKeyChordGuideSupportsCustomPrefixAndCompactLayout covers a custom
// leader prefix and the short-viewport presentation.
func TestPendingKeyChordGuideSupportsCustomPrefixAndCompactLayout(t *testing.T) {
	config := keyChordConfig(t, "composer", "submit", "ctrl-k ctrl-s")
	model := NewModel(codextui.NewState(nil), Options{
		KeymapConfig: config,
		Width:        40,
		Height:       6,
	})
	now := time.Unix(300, 0)
	model.now = func() time.Time { return now }
	model.Update(key(keymsg.KeyCtrlK))
	now = now.Add(keyChordRevealDelay)
	hud := model.renderKeyChordHUD()
	if !strings.Contains(hud, "ctrl+k") || !strings.Contains(hud, keyChordHUDCancel) {
		t.Fatalf("compact guide = %q", hud)
	}
	if strings.Count(hud, "\n")+1 > model.height {
		t.Fatalf("compact guide exceeds the viewport: %q", hud)
	}
}

// TestPendingKeyChordTimeoutClearsGuideLikeRust asserts a stale timeout tick
// cannot clear a newer pending prefix.
func TestPendingKeyChordTimeoutClearsGuideLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		KeymapConfig: keyChordConfig(t, "composer", "submit", "ctrl-x ctrl-s"),
	})
	now := time.Unix(400, 0)
	model.now = func() time.Time { return now }
	model.Update(key(keymsg.KeyCtrlX))
	stale := model.pendingKeyChordStarted
	model.Update(key(keymsg.KeyEsc))
	now = now.Add(time.Second)
	model.Update(key(keymsg.KeyCtrlX))
	if model.pendingKeyChordStarted.Equal(stale) {
		t.Fatal("new prefix reused the stale start time")
	}
	model.Update(KeyChordTimeoutMsg{Started: stale})
	if model.pendingKeyChordPrefix != "ctrl-x" {
		t.Fatalf("stale timeout cleared the new prefix: %q", model.pendingKeyChordPrefix)
	}
	model.Update(KeyChordTimeoutMsg{Started: model.pendingKeyChordStarted})
	if model.pendingKeyChordPrefix != "" {
		t.Fatalf("matching timeout did not clear the prefix: %q", model.pendingKeyChordPrefix)
	}
}

// TestPendingLeaderChordGuideGroupsWorkspaceActionsLikeRust mirrors Rust
// #60637's workspace HUD: the pressed leader prefix reveals grouped pane
// actions and the Esc cancellation hint after the reveal delay.
func TestPendingLeaderChordGuideGroupsWorkspaceActionsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 120, Height: 30})
	now := time.Unix(500, 0)
	model.now = func() time.Time { return now }
	model.Update(key(keymsg.KeyCtrlX))
	if !model.leaderChordPending || model.leaderChordPrefix != "ctrl-x" {
		t.Fatalf("leader prefix state pending=%v prefix=%q", model.leaderChordPending, model.leaderChordPrefix)
	}
	if hud := model.renderKeyChordHUD(); hud != "" {
		t.Fatalf("leader guide revealed before its delay:\n%s", hud)
	}
	now = now.Add(keyChordRevealDelay)
	hud := model.renderKeyChordHUD()
	for _, want := range []string{
		"ctrl+x · Conversation",
		"Panes",
		"Main composer",
		"Open in sidebar",
		"Move",
		"Previous tab",
		"This pane",
		"Zoom pane",
		"Presets ›",
		keyChordHUDCancel,
	} {
		if !strings.Contains(hud, want) {
			t.Fatalf("leader guide missing %q:\n%s", want, hud)
		}
	}
	if strings.Contains(hud, "Close tab") {
		t.Fatalf("leader guide offered a close tab without a side chat:\n%s", hud)
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'p'}})
	if hud := model.renderKeyChordHUD(); hud != "" {
		t.Fatalf("leader guide survived completion:\n%s", hud)
	}
}

// TestPendingLeaderChordGuideUsesSidebarOriginLikeRust covers the sidebar
// origin label and the pane actions that only exist while the sidebar owns
// input.
func TestPendingLeaderChordGuideUsesSidebarOriginLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 120, Height: 30})
	now := time.Unix(600, 0)
	model.now = func() time.Time { return now }
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.Update(key(keymsg.KeyCtrlX))
	model.sidebarChooserOpen = true
	model.sidebarChooserParent = "thread-1"
	model.sidebarChooserFocused = true
	now = now.Add(keyChordRevealDelay)
	hud := model.renderKeyChordHUD()
	for _, want := range []string{"ctrl+x · Sidebar", "Hide pane", "Pane left"} {
		if !strings.Contains(hud, want) {
			t.Fatalf("sidebar leader guide missing %q:\n%s", want, hud)
		}
	}
	if strings.Contains(hud, "Pane right") {
		t.Fatalf("sidebar leader guide offered pane right:\n%s", hud)
	}
}

// TestPendingLeaderChordGuideFitsShortViewportLikeRust clips the guide to the
// terminal and merges the cancel hint into the title when space is tight.
func TestPendingLeaderChordGuideFitsShortViewportLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 60, Height: 6})
	now := time.Unix(700, 0)
	model.now = func() time.Time { return now }
	model.Update(key(keymsg.KeyCtrlX))
	now = now.Add(keyChordRevealDelay)
	hud := model.renderKeyChordHUD()
	if lines := strings.Count(hud, "\n") + 1; lines > model.height {
		t.Fatalf("short guide has %d lines for height %d:\n%s", lines, model.height, hud)
	}
	if !strings.Contains(hud, keyChordHUDCancel) {
		t.Fatalf("short guide dropped the cancel hint:\n%s", hud)
	}
}
