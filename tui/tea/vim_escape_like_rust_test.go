package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

func altRuneMsg(r rune) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{r}, Alt: true}
}

// Rust #42584: Vim insert/replace/operator-pending consumes Esc, so an
// unclaimed Alt+character in those states is really Esc plus the character.
func TestShouldRecoverVimInsertEscapeLikeRust(t *testing.T) {
	model := NewModel(nil, Options{})
	model.vimMode = true
	model.vimInsert = true
	if !model.shouldRecoverVimInsertEscape(altRuneMsg('h'), true) {
		t.Fatal("insert mode should recover Alt+h")
	}

	model.vimInsert = false
	model.vimReplaceMode = true
	if !model.shouldRecoverVimInsertEscape(altRuneMsg('h'), true) {
		t.Fatal("replace mode should recover Alt+h")
	}

	model.vimReplaceMode = false
	model.vimPendingOp = "d"
	if !model.shouldRecoverVimInsertEscape(altRuneMsg('h'), true) {
		t.Fatal("operator-pending mode should recover Alt+h")
	}

	model.vimPendingOp = ""
	if model.shouldRecoverVimInsertEscape(altRuneMsg('h'), true) {
		t.Fatal("plain normal mode must keep Alt+h unclaimed")
	}
}

func TestVimEscapeRecoveryPreservesAltBindingsAndSurfacesLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Model)
		msg    bubbletea.KeyMsg
		unix   bool
		expect bool
	}{
		{
			name: "editor word motion binding",
			setup: func(model *Model) {
				model.vimInsert = true
			},
			msg:    altRuneMsg('b'),
			unix:   true,
			expect: false,
		},
		{
			name: "image paste",
			setup: func(model *Model) {
				model.vimInsert = true
			},
			msg:    altRuneMsg('v'),
			unix:   true,
			expect: false,
		},
		{
			name: "modal",
			setup: func(model *Model) {
				model.vimInsert = true
				model.modal = &modalState{}
			},
			msg:    altRuneMsg('h'),
			unix:   true,
			expect: false,
		},
		{
			name: "overlay",
			setup: func(model *Model) {
				model.vimInsert = true
				model.overlay = &chatwidget.TranscriptOverlay{}
			},
			msg:    altRuneMsg('h'),
			unix:   true,
			expect: false,
		},
		{
			name: "windows",
			setup: func(model *Model) {
				model.vimInsert = true
			},
			msg:    altRuneMsg('h'),
			unix:   false,
			expect: false,
		},
		{
			name: "plain rune",
			setup: func(model *Model) {
				model.vimInsert = true
			},
			msg:    bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'h'}},
			unix:   true,
			expect: false,
		},
	}
	for _, testCase := range tests {
		model := NewModel(nil, Options{})
		model.vimMode = true
		testCase.setup(model)
		if got := model.shouldRecoverVimInsertEscape(testCase.msg, testCase.unix); got != testCase.expect {
			t.Fatalf("%s: recovery = %v, want %v", testCase.name, got, testCase.expect)
		}
	}
}

// The full Update path splits the stroke: Esc leaves insert mode before the
// character is dispatched as a normal-mode command.
func TestLegacyVimEscapeRecoveryReplaysEscapeThenCharacterLikeRust(t *testing.T) {
	model := NewModel(nil, Options{})
	model.legacyVimEscapeRecovery = true
	model.vimMode = true
	model.setComposerPrompt("abc")
	model.composer.SetCursor(len("abc"))
	model.vimInsert = true

	model.Update(altRuneMsg('h'))
	if model.vimInsert {
		t.Fatal("recovered Esc did not leave Vim insert mode")
	}
	if got := model.composerText(); got != "abc" {
		t.Fatalf("draft = %q, want abc after the h motion", got)
	}
	if model.backtrack.Primed {
		t.Fatal("recovered Esc must not prime backtrack")
	}
}

func TestVimInsertEscapeWinsOverBacktrackLikeRust(t *testing.T) {
	model := NewModel(nil, Options{})
	model.vimMode = true
	model.vimInsert = true
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.vimInsert {
		t.Fatal("Esc did not exit insert mode")
	}
	if model.backtrack.Primed {
		t.Fatal("Vim insert Esc primed backtrack instead of leaving insert mode")
	}

	operator := NewModel(nil, Options{})
	operator.vimMode = true
	operator.vimPendingOp = "d"
	operator.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if operator.vimPendingOp != "" || operator.vimPendingObject != "" {
		t.Fatal("Esc did not cancel the pending Vim operator")
	}
	if operator.backtrack.Primed {
		t.Fatal("pending-operator Esc primed backtrack")
	}
}

// A user binding on the Alt stroke keeps its meaning instead of being split.
func TestVimEscapeRecoveryRespectsCustomAltBindingLikeRust(t *testing.T) {
	config := codextui.NewKeymapConfig()
	if err := config.Set("editor", "move_line_start", []string{"alt-h"}); err != nil {
		t.Fatalf("bind alt-h: %v", err)
	}
	model := NewModel(nil, Options{KeymapConfig: config})
	model.legacyVimEscapeRecovery = true
	model.vimMode = true
	model.vimInsert = true
	if model.shouldRecoverVimInsertEscape(altRuneMsg('h'), true) {
		t.Fatal("a configured alt-h binding must not be split")
	}
}
