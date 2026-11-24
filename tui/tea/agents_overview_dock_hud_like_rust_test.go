package tea

import (
	"strings"
	"testing"
	"time"

	"codex_go/tui/keymsg"
)

// Rust workspace_hud.rs anchors the leader guide to the Command Center while
// its dock owns input and keeps both pane directions available (#62990).
func TestAgentsOverviewDockLeaderHUDLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 120)
	_ = model.viewString()
	now := time.Unix(6000, 0)
	model.now = func() time.Time { return now }

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlX})
	if !model.leaderChordPending {
		t.Fatal("leader prefix did not start a chord")
	}
	now = now.Add(keyChordRevealDelay)
	hud := model.renderKeyChordHUD()
	if !strings.Contains(hud, "Command Center") {
		t.Fatalf("guide origin is not the Command Center:\n%s", hud)
	}
	if !strings.Contains(hud, "Pane right") {
		t.Fatalf("guide lost the pane right entry:\n%s", hud)
	}
	if !strings.Contains(hud, "a") || !strings.Contains(hud, "Command Center") {
		t.Fatalf("guide lost leader a:\n%s", hud)
	}

	// From the conversation side the guide anchors there and offers Pane left.
	if !model.focusAgentsOverviewDockConversation() {
		t.Fatal("dock did not hand input to the conversation")
	}
	model.clearLeaderChord()
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlX})
	now = now.Add(keyChordRevealDelay)
	hud = model.renderKeyChordHUD()
	if !strings.Contains(hud, "Conversation") {
		t.Fatalf("guide origin is not the conversation:\n%s", hud)
	}
	if !strings.Contains(hud, "Pane left") {
		t.Fatalf("guide lost the pane left entry:\n%s", hud)
	}
}
