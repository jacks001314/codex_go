package tea

import (
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func leaderChordModel(t *testing.T, config *codextui.KeymapConfig) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	return NewModel(state, Options{
		Width:        100,
		Height:       30,
		KeymapConfig: config,
	})
}

// Rust #54381: `leader p` opens the preset HUD; an explicit plain binding also
// works.
func TestPresetsLeaderChordOpensQuickDialLikeRust(t *testing.T) {
	model := leaderChordModel(t, nil)
	model.Update(key(bubbletea.KeyCtrlX))
	if !model.leaderChordPending || model.quickDialMenu {
		t.Fatalf("prefix state pending=%v menu=%v", model.leaderChordPending, model.quickDialMenu)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'p'}})
	if model.leaderChordPending || !model.quickDialMenu {
		t.Fatalf("completion state pending=%v menu=%v", model.leaderChordPending, model.quickDialMenu)
	}

	config := codextui.NewKeymapConfig()
	if err := config.Set("chat", "open_presets", []string{"ctrl-p"}); err != nil {
		t.Fatalf("remap presets: %v", err)
	}
	remapped := leaderChordModel(t, config)
	remapped.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlP})
	if !remapped.quickDialMenu {
		t.Fatal("plain open_presets binding did not open the menu")
	}
}

func TestPresetsLeaderChordCancelsOnOtherKeyAndTimeoutLikeRust(t *testing.T) {
	model := leaderChordModel(t, nil)
	model.Update(key(bubbletea.KeyCtrlX))
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'a'}})
	if model.leaderChordPending || model.quickDialMenu {
		t.Fatalf("other key state pending=%v menu=%v", model.leaderChordPending, model.quickDialMenu)
	}
	if got := model.composerText(); got != "a" {
		t.Fatalf("cancelled completion did not reach the composer: %q", got)
	}

	now := time.Unix(100, 0)
	timeout := leaderChordModel(t, nil)
	timeout.now = func() time.Time { return now }
	timeout.Update(key(bubbletea.KeyCtrlX))
	now = now.Add(codextui.LeaderChordTimeout + time.Second)
	timeout.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'p'}})
	if timeout.quickDialMenu {
		t.Fatal("expired leader chord still opened the menu")
	}
	if got := timeout.composerText(); got != "p" {
		t.Fatalf("expired completion text = %q, want p", got)
	}

	blurred := leaderChordModel(t, nil)
	blurred.Update(key(bubbletea.KeyCtrlX))
	blurred.Update(bubbletea.BlurMsg{})
	if blurred.leaderChordPending {
		t.Fatal("blur did not cancel the pending leader chord")
	}
}
