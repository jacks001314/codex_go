package tea

import (
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Mirrors Rust #18593 (`tui/src/key_hint.rs`): a NUL character (0x00)
// normalizes to Ctrl+Space (`0x00 => Some(' ')`), while the C0 unit-separator
// byte (0x1f) normalizes to Ctrl+7 for Ctrl+/ compatibility
// (`0x1c..=0x1f => code - 0x1c + '4'`).
func TestCtrlSpaceNormalizationLikeRust(t *testing.T) {
	cases := []struct {
		name string
		msg  keymsg.KeyMsg
		want string
	}{
		{"ansi NUL byte (KeyCtrlAt)", keymsg.KeyMsg{Type: keymsg.KeyCtrlAt}, "ctrl-space"},
		{"keyNUL alias (KeyNull)", keymsg.KeyMsg{Type: keymsg.KeyNull}, "ctrl-space"},
		{"windows coninput NUL rune (KeyRunes{0})", keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{0}}, "ctrl-space"},
		{"C0 unit separator (KeyCtrlUnderscore)", keymsg.KeyMsg{Type: keymsg.KeyCtrlUnderscore}, "ctrl-7"},
	}
	for _, c := range cases {
		if got := keySpecFromKeyMsg(c.msg); got != c.want {
			t.Fatalf("%s: keySpecFromKeyMsg = %q, want %q", c.name, got, c.want)
		}
	}
}

// Mirrors Rust `owned_transcript_input_tests.rs`:
// `configured_ctrl_space_submit_wins_over_transcript_selection`. With
// composer.submit bound to ctrl-space, a Ctrl+Space press (in either terminal
// encoding) must submit the draft instead of being a dead key.
func TestConfiguredCtrlSpaceSubmitLikeRust(t *testing.T) {
	encodings := []struct {
		name string
		msg  keymsg.KeyMsg
	}{
		{"ansi NUL byte (KeyCtrlAt)", keymsg.KeyMsg{Type: keymsg.KeyCtrlAt}},
		{"windows coninput NUL rune (KeyRunes{0})", keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{0}}},
	}
	for _, encoding := range encodings {
		var requests []SubmitRequest
		model := NewModel(codextui.NewState(nil), Options{
			KeymapConfig: func() *codextui.KeymapConfig {
				cfg := codextui.NewKeymapConfig()
				if err := cfg.Set("composer", "submit", []string{"ctrl-space"}); err != nil {
					t.Fatal(err)
				}
				return cfg
			}(),
			OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
				requests = append(requests, request)
				return nil
			},
		})
		typeText(t, model, "send once")
		model.Update(encoding.msg)
		if len(requests) != 1 || requests[0].Prompt != "send once" {
			t.Fatalf("%s: Ctrl+Space should submit, requests=%#v composer=%q", encoding.name, requests, model.ComposerValue())
		}
		if got := model.ComposerValue(); got != "" {
			t.Fatalf("%s: composer not cleared after submit: %q", encoding.name, got)
		}
	}
}
