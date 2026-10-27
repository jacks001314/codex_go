package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func newSelectionModel(t *testing.T, options Options) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-selection")
	state.AddMessage(codextui.RoleUser, "alpha beta")
	state.AddMessage(codextui.RoleAssistant, "gamma delta\nepsilon zeta")
	model := NewModel(state, options)
	model.now = fixedTeaTime
	return model
}

// Rust #57223: the implicit `leader [` shortcut follows the configured leader
// and opens the transcript with selection active.
func TestSelectTranscriptLeaderOpensSelectionLikeRust(t *testing.T) {
	model := newSelectionModel(t, Options{Width: 60, Height: 12})
	model.Update(key(bubbletea.KeyCtrlX))
	if !model.leaderChordPending {
		t.Fatal("leader prefix was not recorded")
	}
	model.Update(runes("["))
	if model.leaderChordPending {
		t.Fatal("leader chord stayed pending after `[`")
	}
	if model.overlay == nil || !model.overlay.SelectionActive() {
		t.Fatalf("selection mode did not start (overlay=%v)", model.overlay != nil)
	}
}

// Rust #57223: an explicit remap or unbind replaces the implicit shortcut.
func TestSelectTranscriptLeaderRespectsRemapAndUnbindLikeRust(t *testing.T) {
	config := codextui.NewKeymapConfig()
	if err := config.Set("global", "select_transcript", []string{"ctrl-s"}); err != nil {
		t.Fatalf("remap: %v", err)
	}
	remapped := newSelectionModel(t, Options{Width: 60, Height: 12, KeymapConfig: config})
	remapped.Update(key(bubbletea.KeyCtrlX))
	remapped.Update(runes("["))
	if remapped.overlay != nil {
		t.Fatal("implicit leader [` still opened selection after a remap")
	}
	remapped.Update(key(bubbletea.KeyCtrlS))
	if remapped.overlay == nil || !remapped.overlay.SelectionActive() {
		t.Fatal("remapped select_transcript did not open selection")
	}

	unbound := codextui.NewKeymapConfig()
	if err := unbound.Set("global", "select_transcript", []string{}); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	model := newSelectionModel(t, Options{Width: 60, Height: 12, KeymapConfig: unbound})
	model.Update(key(bubbletea.KeyCtrlX))
	model.Update(runes("["))
	if model.overlay != nil {
		t.Fatal("explicit unbind still installed the implicit shortcut")
	}
}

// Rust #57224: selection keystrokes never reach the composer draft, and the
// held exit key is consumed instead of submitting it.
func TestTranscriptSelectionPreservesDraftLikeRust(t *testing.T) {
	model := newSelectionModel(t, Options{Width: 60, Height: 12})
	typeText(t, model, "draft")
	model.Update(key(bubbletea.KeyCtrlX))
	model.Update(runes("["))
	if model.overlay == nil || !model.overlay.SelectionActive() {
		t.Fatal("selection mode did not start")
	}
	for _, r := range "hjklvyo" {
		model.Update(runes(string(r)))
	}
	model.Update(key(bubbletea.KeyEnter))
	if model.overlay != nil {
		t.Fatal("Enter without a mark did not close the selection overlay")
	}
	if got := model.composerText(); got != "draft" {
		t.Fatalf("composer draft = %q, want draft", got)
	}
	// A held/repeated exit key is consumed rather than submitting the draft.
	model.Update(key(bubbletea.KeyEnter))
	if got := model.composerText(); got != "draft" {
		t.Fatalf("held exit key submitted the draft: %q", got)
	}
	if model.selectExitGuardSpec != "enter" {
		t.Fatalf("exit guard spec = %q, want enter while the key is held", model.selectExitGuardSpec)
	}
}

// Rust #57223: Enter copies the marked range and a confirmed copy-and-follow
// closes the overlay.
func TestTranscriptSelectionCopyAndFollowLikeRust(t *testing.T) {
	model := newSelectionModel(t, Options{Width: 60, Height: 12})
	var copied string
	model.clipboardWrite = func(text string) error {
		copied = text
		return nil
	}
	model.Update(key(bubbletea.KeyCtrlX))
	model.Update(runes("["))
	if model.overlay == nil || !model.overlay.SelectionActive() {
		t.Fatal("selection mode did not start")
	}
	model.Update(runes("V"))
	if !model.overlay.SelectionMarked() {
		t.Fatal("V did not mark a whole line")
	}
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if strings.TrimSpace(copied) == "" {
		t.Fatal("copy-and-follow did not write the marked text")
	}
	if model.overlay != nil {
		t.Fatal("confirmed copy-and-follow did not close the overlay")
	}
}

// Rust #57223: Esc clears an active mark before it leaves selection mode.
func TestTranscriptSelectionEscapeClearsMarkThenExitsLikeRust(t *testing.T) {
	model := newSelectionModel(t, Options{Width: 60, Height: 12})
	model.Update(key(bubbletea.KeyCtrlX))
	model.Update(runes("["))
	model.Update(runes("V"))
	if !model.overlay.SelectionMarked() {
		t.Fatal("V did not mark a line")
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.overlay == nil || model.overlay.SelectionMarked() {
		t.Fatal("Esc did not clear the mark")
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.overlay != nil {
		t.Fatal("second Esc did not close the selection overlay")
	}
}
