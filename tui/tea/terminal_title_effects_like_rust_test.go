package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/anim"
)

func terminalTitleEffectsModel(t *testing.T, animations bool, effects EffectsSettings) (*Model, *[]string) {
	t.Helper()
	writes := []string{}
	model := NewModel(codextui.NewState(&codextui.Options{Model: "gpt-5"}), Options{
		Width:              90,
		Height:             24,
		AnimationsEnabled:  &animations,
		Effects:            &effects,
		TerminalTitleItems: []string{"activity", "project-name"},
		OnWriteTerminalTitle: func(sequence string) bubbletea.Cmd {
			writes = append(writes, sequence)
			return nil
		},
	})
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	model.now = func() time.Time { return now }
	model.terminalTitleAnimationOrigin = now
	return model, &writes
}

// Rust #46832: the terminal-title action-required prefix blinks only while
// animations and effects.title are on; either switch keeps the static prefix.
func TestTerminalTitleActionRequiredBlinkHonorsTitleEffectLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		animations bool
		title      bool
		wantBlink  bool
	}{
		{name: "title effect on", animations: true, title: true, wantBlink: true},
		{name: "title effect off", animations: true, title: false},
		{name: "animations off", animations: false, title: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			effects := DefaultEffectsSettings()
			effects.Title = testCase.title
			model, writes := terminalTitleEffectsModel(t, testCase.animations, effects)
			now := model.currentTime()
			model.modal = &modalState{kind: ModalKindApproval}

			model.Update(anim.TickMsg{Tick: 0})
			if len(*writes) == 0 || !strings.Contains((*writes)[len(*writes)-1], "[ ! ] Action Required") {
				t.Fatalf("static action-required title missing: %#v", *writes)
			}

			model.now = func() time.Time { return now.Add(1100 * time.Millisecond) }
			model.Update(anim.TickMsg{Tick: 1})
			last := (*writes)[len(*writes)-1]
			if gotBlink := strings.Contains(last, "[ . ] Action Required"); gotBlink != testCase.wantBlink {
				t.Fatalf("blink = %v, want %v; writes = %#v", gotBlink, testCase.wantBlink, *writes)
			}
		})
	}
}

// Rust #46832: the terminal-title spinner frame advances under
// `animations && effects.progress` and stays absent when either is off.
func TestTerminalTitleSpinnerHonorsProgressEffectLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		animations bool
		progress   bool
		wantFrame  string
	}{
		{name: "progress on", animations: true, progress: true, wantFrame: "⠋"},
		{name: "progress off", animations: true, progress: false},
		{name: "animations off", animations: false, progress: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			effects := DefaultEffectsSettings()
			effects.Progress = testCase.progress
			model, writes := terminalTitleEffectsModel(t, testCase.animations, effects)
			model.State.Status = "running"

			model.Update(anim.TickMsg{Tick: 0})
			if len(*writes) == 0 {
				t.Fatal("running task did not refresh the terminal title")
			}
			last := (*writes)[len(*writes)-1]
			hasFrame := strings.Contains(last, "⠋")
			if testCase.wantFrame != "" && !hasFrame {
				t.Fatalf("spinner frame missing: %q", last)
			}
			if testCase.wantFrame == "" && hasFrame {
				t.Fatalf("spinner frame rendered with the effect off: %q", last)
			}

			if testCase.wantFrame != "" {
				model.Update(anim.TickMsg{Tick: 1})
				model.Update(anim.TickMsg{Tick: 2})
				last = (*writes)[len(*writes)-1]
				if !strings.Contains(last, "⠙") {
					t.Fatalf("spinner did not advance: %q (writes %#v)", last, *writes)
				}
			}
		})
	}
}
