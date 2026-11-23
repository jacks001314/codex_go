package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	keymsg "codex_go/tui/keymsg"
)

// Rust parity: codex-rs/tui/src/app/owned_transcript_input_tests.rs and
// select_mode.rs (#63047). The transcript keymap binds mark_markdown to plain
// `m`; a transcript action that claims the same key makes that default yield,
// while an explicitly configured leader chord still dispatches.

func markdownBlockModel(t *testing.T, config *codextui.KeymapConfig) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-markdown-blocks")
	state.AddMessage(codextui.RoleAssistant, "# Heading\n\nparagraph text\n\n- item one\n- item two")
	model := NewModel(state, Options{Width: 80, Height: 18, KeymapConfig: config})
	model.now = fixedTeaTime
	return model
}

func startMarkdownSelection(t *testing.T, model *Model) {
	t.Helper()
	model.Update(key(keymsg.KeyCtrlX))
	model.Update(runes("["))
	if model.overlay == nil || !model.overlay.SelectionActive() {
		t.Fatalf("selection mode did not start (overlay=%v)", model.overlay != nil)
	}
}

func TestMarkdownMarkDefaultsToPlainMLikeRust(t *testing.T) {
	model := markdownBlockModel(t, nil)
	startMarkdownSelection(t, model)
	model.Update(runes("m"))
	if !model.overlay.SelectionMarked() {
		t.Fatal("m did not mark a Markdown block")
	}
	if got := strings.TrimSpace(model.overlay.SelectedText()); got != "- item two" {
		t.Fatalf("marked block = %q, want the list item under the caret", got)
	}
	// The block footer names the block and its controls.
	if hint := model.overlay.BlockMarkHint(); !strings.Contains(hint, "list item") {
		t.Fatalf("block hint = %q", hint)
	}
	// A second m clears the mark (Rust toggle_markdown).
	model.Update(runes("m"))
	if model.overlay.SelectionMarked() {
		t.Fatal("second m did not clear the block mark")
	}
}

func TestMarkdownMarkYieldsToRemappedFindLikeRust(t *testing.T) {
	config := codextui.NewKeymapConfig()
	if err := config.Set("pager", "find", []string{"m"}); err != nil {
		t.Fatalf("remap pager.find: %v", err)
	}
	model := markdownBlockModel(t, config)
	startMarkdownSelection(t, model)
	model.Update(runes("m"))
	if !model.overlay.FindActive() {
		t.Fatal("remapped find did not open the search bar")
	}
	if model.overlay.SelectionMarked() {
		t.Fatal("default mark_markdown still claimed the remapped find key")
	}
}

func TestMarkdownMarkExplicitLeaderChordLikeRust(t *testing.T) {
	config := codextui.NewKeymapConfig()
	if err := config.Set("pager", "mark_markdown", []string{"leader v"}); err != nil {
		t.Fatalf("configure mark_markdown: %v", err)
	}
	model := markdownBlockModel(t, config)
	startMarkdownSelection(t, model)
	model.Update(key(keymsg.KeyCtrlX))
	if !model.leaderChordPending {
		t.Fatal("leader prefix was not recorded in selection mode")
	}
	model.Update(runes("v"))
	if model.leaderChordPending {
		t.Fatal("leader chord stayed pending after v")
	}
	if !model.overlay.SelectionMarked() {
		t.Fatal("explicit leader chord did not dispatch mark_markdown")
	}
}
