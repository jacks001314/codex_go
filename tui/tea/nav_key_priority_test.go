package tea

import (
	"fmt"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func navKeyPriorityConfig(t *testing.T, context string, action string, binding string) *codextui.KeymapConfig {
	t.Helper()
	cfg := codextui.NewKeymapConfig()
	if err := cfg.Set(context, action, []string{binding}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func navKeyPriorityState(messageCount int) *codextui.State {
	state := codextui.NewState(nil)
	for i := 0; i < messageCount; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	return state
}

// Mirrors Rust #50389 (`app/owned_transcript.rs`): an explicitly configured
// binding for an active action wins over transcript navigation, so the
// configured shortcut runs (submit / queue / interrupt) and the transcript does
// not scroll. The composer -> global fallback is covered by `global.submit`.
func TestConfiguredNavKeyWinsOverTranscriptScrollLikeRust(t *testing.T) {
	t.Run("composer.submit", func(t *testing.T) {
		var requests []SubmitRequest
		model := NewModel(navKeyPriorityState(30), Options{
			Width:           60,
			Height:          10,
			KeymapConfig:    navKeyPriorityConfig(t, "composer", "submit", "page-up"),
			OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd { requests = append(requests, request); return nil },
		})
		bottom := model.transcript.YOffset
		if bottom <= 0 {
			t.Fatalf("initial transcript offset = %d, want scrollable bottom", bottom)
		}
		typeText(t, model, "send once")
		model.Update(key(bubbletea.KeyPgUp))
		if len(requests) != 1 || requests[0].Prompt != "send once" {
			t.Fatalf("configured PageUp should submit, requests=%#v", requests)
		}
		if !model.transcript.AtBottom() {
			t.Fatalf("configured PageUp scrolled the transcript away from the bottom: offset %d", model.transcript.YOffset)
		}
	})

	t.Run("global.submit fallback", func(t *testing.T) {
		var requests []SubmitRequest
		model := NewModel(navKeyPriorityState(30), Options{
			Width:           60,
			Height:          10,
			KeymapConfig:    navKeyPriorityConfig(t, "global", "submit", "page-up"),
			OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd { requests = append(requests, request); return nil },
		})
		if model.transcript.YOffset <= 0 {
			t.Fatalf("initial transcript offset = %d, want scrollable bottom", model.transcript.YOffset)
		}
		typeText(t, model, "send once")
		model.Update(key(bubbletea.KeyPgUp))
		if len(requests) != 1 || requests[0].Prompt != "send once" {
			t.Fatalf("global.submit PageUp should submit, requests=%#v", requests)
		}
		if !model.transcript.AtBottom() {
			t.Fatalf("global.submit PageUp scrolled away from the bottom: offset %d", model.transcript.YOffset)
		}
	})

	t.Run("chat.interrupt_turn", func(t *testing.T) {
		interrupts := 0
		state := navKeyPriorityState(30)
		state.SetStatus("running")
		model := NewModel(state, Options{
			Width:        60,
			Height:       10,
			KeymapConfig: navKeyPriorityConfig(t, "chat", "interrupt_turn", "page-down"),
			OnInterrupt:  func() bubbletea.Cmd { interrupts++; return nil },
		})
		bottom := model.transcript.YOffset
		model.Update(key(bubbletea.KeyPgDown))
		if interrupts != 1 {
			t.Fatalf("configured PageDown should interrupt, interrupts=%d", interrupts)
		}
		if got := model.transcript.YOffset; got != bottom {
			t.Fatalf("configured PageDown scrolled the transcript: offset %d -> %d", bottom, got)
		}
	})
}

// Rust #50389 keeps default navigation: without an explicit binding the
// transcript still consumes PageUp.
func TestDefaultNavKeyStillScrollsLikeRust(t *testing.T) {
	model := NewModel(navKeyPriorityState(30), Options{Width: 60, Height: 10})
	bottom := model.transcript.YOffset
	if bottom <= 0 {
		t.Fatalf("initial transcript offset = %d, want scrollable bottom", bottom)
	}
	model.Update(key(bubbletea.KeyPgUp))
	if got := model.transcript.YOffset; got >= bottom {
		t.Fatalf("default PageUp offset = %d, want less than bottom %d", got, bottom)
	}
}
