package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	bottompane "codex_go/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer/vim_register.rs
// (#57564). A Vim copy keeps the image payload, and pasting after a draft
// clear re-attaches the image with a fresh label. Repeated pastes and `.` keep
// allocating fresh labels.
func TestVimRegisterReattachesImagesWithFreshLabelsLikeRust(t *testing.T) {
	m := vimTestModel("[Image #1] hello")
	m.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: "/tmp/photo.png"}}
	m.composerElements = []ComposerTextElement{{Start: 0, End: 10, Placeholder: "[Image #1]"}}
	m.composer.SetCursor(0)

	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, '$')
	if got := m.vimYank; got != "[Image #1] hello" {
		t.Fatalf("register text = %q, want [Image #1] hello", got)
	}
	if len(m.composerKillBufferImages) != 1 || m.composerKillBufferImages[0].Path != "/tmp/photo.png" {
		t.Fatalf("register images = %#v, want the copied image", m.composerKillBufferImages)
	}
	if len(m.vimRegisterElements) != 1 || m.vimRegisterElements[0].Placeholder != "[Image #1]" {
		t.Fatalf("register elements = %#v, want the image element", m.vimRegisterElements)
	}

	// The register survives a draft clear, like a submission.
	m.resetComposer()
	m.composerElements = nil
	m.attachments = nil

	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "[Image #1] hello" {
		t.Fatalf("value after paste = %q, want [Image #1] hello", got)
	}
	if len(m.attachments) != 1 || m.attachments[0].Path != "/tmp/photo.png" {
		t.Fatalf("attachments after paste = %#v, want the copied image re-attached", m.attachments)
	}
	if len(m.composerElements) != 1 || m.composerElements[0].Placeholder != "[Image #1]" {
		t.Fatalf("elements after paste = %#v, want one [Image #1] element", m.composerElements)
	}

	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "[Image #1] hello[Image #2] hello" {
		t.Fatalf("value after second paste = %q, want a fresh [Image #2] label", got)
	}
	if len(m.attachments) != 2 || m.attachments[1].Path != "/tmp/photo.png" {
		t.Fatalf("attachments after second paste = %#v, want two images", m.attachments)
	}
	if !hasComposerElement(m, "[Image #2]") {
		t.Fatalf("elements after second paste = %#v, want [Image #2]", m.composerElements)
	}

	// Rust's y$p. test: the repeat pastes a third copy with a fresh label.
	m = vimKeyPress(m, '.')
	if got := m.composer.Value(); got != "[Image #1] hello[Image #2] hello[Image #3] hello" {
		t.Fatalf("value after dot = %q, want a fresh [Image #3] label", got)
	}
	if len(m.attachments) != 3 || m.attachments[2].Path != "/tmp/photo.png" {
		t.Fatalf("attachments after dot = %#v, want three images", m.attachments)
	}
}

// Deleting with a Vim operator fills the register, and pasting keeps the
// mention element so it reaches the turn input as text_elements.
func TestVimDeleteOperatorFillsRichRegisterLikeRust(t *testing.T) {
	m := vimTestModel("$tool rest")
	m.composerElements = []ComposerTextElement{{Start: 0, End: 5, Placeholder: "$tool"}}
	m.composer.SetCursor(0)

	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'w')
	if got := m.vimYank; got != "$tool " {
		t.Fatalf("register text = %q, want $tool ", got)
	}
	if got := m.composer.Value(); got != "rest" {
		t.Fatalf("value after dw = %q, want rest", got)
	}

	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "$tool rest" {
		t.Fatalf("value after paste = %q, want $tool rest", got)
	}
	if !hasComposerElement(m, "$tool") {
		t.Fatalf("elements after paste = %#v, want $tool", m.composerElements)
	}
}

// Undo/redo restores the exact composer elements and attachments, mirroring
// Rust's snapshot handoff (#57564).
func TestVimUndoRedoRestoresRichDraftLikeRust(t *testing.T) {
	m := vimTestModel("[Image #1] hello")
	m.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: "/tmp/photo.png"}}
	m.composerElements = []ComposerTextElement{{Start: 0, End: 10, Placeholder: "[Image #1]"}}
	m.composer.SetCursor(0)

	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'd')
	if got := m.composer.Value(); got != "" {
		t.Fatalf("value after dd = %q, want empty", got)
	}
	if len(m.composerElements) != 0 {
		t.Fatalf("dd left stale elements behind: %#v", m.composerElements)
	}

	m = vimKeyPress(m, 'u')
	if got := m.composer.Value(); got != "[Image #1] hello" {
		t.Fatalf("value after undo = %q, want [Image #1] hello", got)
	}
	if !hasComposerElement(m, "[Image #1]") {
		t.Fatalf("undo did not restore the element: %#v", m.composerElements)
	}
	if len(m.attachments) != 1 || m.attachments[0].Path != "/tmp/photo.png" {
		t.Fatalf("undo did not restore the attachment: %#v", m.attachments)
	}

	updated, _ := m.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlR})
	m = updated.(*Model)
	if got := m.composer.Value(); got != "" {
		t.Fatalf("value after redo = %q, want empty", got)
	}
	if len(m.composerElements) != 0 {
		t.Fatalf("redo kept elements: %#v", m.composerElements)
	}
}

// A deleted rich register survives a draft clear and pastes linewise with the
// mention element preserved and the image re-attached (Rust
// deleted_rich_register_survives_clear_and_linewise_paste).
func TestVimDeletedRichRegisterSurvivesClearAndLinewisePasteLikeRust(t *testing.T) {
	m := vimTestModel("$tool [Image #1] done")
	m.composerElements = []ComposerTextElement{
		{Start: 0, End: 5, Placeholder: "$tool"},
		{Start: 6, End: 16, Placeholder: "[Image #1]"},
	}
	m.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: "/tmp/photo.png"}}
	m.composer.SetCursor(0)

	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'd')
	if got := m.composer.Value(); got != "" {
		t.Fatalf("value after dd = %q, want empty", got)
	}
	if len(m.composerElements) != 0 {
		t.Fatalf("dd left stale elements behind: %#v", m.composerElements)
	}
	if got := m.vimYank; got != "$tool [Image #1] done" || !m.vimYankLinewise {
		t.Fatalf("register = %q linewise=%v, want the deleted line", got, m.vimYankLinewise)
	}
	if len(m.composerKillBufferImages) != 1 || len(m.vimRegisterElements) != 2 {
		t.Fatalf("register payloads = images:%#v elements:%#v, want both", m.composerKillBufferImages, m.vimRegisterElements)
	}

	// Submitting clears the draft and its attachments; the register stays.
	m.resetComposer()
	m.composerElements = nil
	m.attachments = nil
	m.composer.SetValue("new")
	m.composer.SetCursor(len("new"))

	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "new\n$tool [Image #1] done" {
		t.Fatalf("value after linewise paste = %q, want the register on a new line", got)
	}
	if len(m.attachments) != 1 || m.attachments[0].Path != "/tmp/photo.png" {
		t.Fatalf("attachments after paste = %#v, want the image re-attached", m.attachments)
	}
	if !hasComposerElementAt(m, "$tool", len("new\n")) {
		t.Fatalf("elements after paste = %#v, want $tool at the pasted line", m.composerElements)
	}
	if !hasComposerElementAt(m, "[Image #1]", len("new\n$tool ")) {
		t.Fatalf("elements after paste = %#v, want the fresh image label", m.composerElements)
	}
}

// The live register, including its rich payload, travels across a composer
// replacement (Rust composer_handoff_preserves_only_the_live_rich_register).
func TestVimRichRegisterSurvivesComposerReplacementLikeRust(t *testing.T) {
	m := vimTestModel("$tool [Image #1]")
	m.composerElements = []ComposerTextElement{
		{Start: 0, End: 5, Placeholder: "$tool"},
		{Start: 6, End: 16, Placeholder: "[Image #1]"},
	}
	m.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: "/tmp/photo.png"}}
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, '$')

	m.startFreshNamedSession("", "Started a new local thread.")
	m.composerElements = nil
	m.attachments = nil
	m.composer.SetValue("new")
	m.composer.SetCursor(len("new"))

	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "new$tool [Image #1]" {
		t.Fatalf("value after handoff paste = %q, want the register appended at the cursor", got)
	}
	if len(m.attachments) != 1 || m.attachments[0].Path != "/tmp/photo.png" {
		t.Fatalf("attachments after handoff paste = %#v, want the image re-attached", m.attachments)
	}
	if !hasComposerElementAt(m, "$tool", len("new")) || !hasComposerElementAt(m, "[Image #1]", len("new$tool ")) {
		t.Fatalf("elements after handoff paste = %#v, want both payloads", m.composerElements)
	}
}

// The characterwise p inserts at the caret, and a plain delete replaces the
// rich register with plain text (Rust plain_delete_replaces_rich_register).
func TestVimPlainDeleteReplacesRichRegisterLikeRust(t *testing.T) {
	m := vimTestModel("[Image #1] hello")
	m.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: "/tmp/photo.png"}}
	m.composerElements = []ComposerTextElement{{Start: 0, End: 10, Placeholder: "[Image #1]"}}
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, '$')

	m.composerElements = nil
	m.attachments = nil
	m.composer.SetValue("abc")
	m.composer.SetCursor(0)

	m = vimKeyPress(m, 'x')
	if got := m.vimYank; got != "a" || m.vimYankLinewise {
		t.Fatalf("register after x = %q linewise=%v, want plain a", got, m.vimYankLinewise)
	}
	if m.composerKillBuffer != "a" {
		t.Fatalf("ctrl-y buffer after x = %q, want a", m.composerKillBuffer)
	}
	if len(m.composerKillBufferImages) != 0 || len(m.vimRegisterElements) != 0 {
		t.Fatalf("x kept rich payloads: images:%#v elements:%#v", m.composerKillBufferImages, m.vimRegisterElements)
	}

	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "abc" {
		t.Fatalf("value after xp = %q, want abc", got)
	}
	if len(m.attachments) != 0 || len(m.composerElements) != 0 {
		t.Fatalf("xp restored stale payloads: attachments:%#v elements:%#v", m.attachments, m.composerElements)
	}
}

// Ctrl-Y shares the single-entry register with Vim yanks, so it restores the
// rich payload too (Rust textarea kill_buffer).
func TestVimCtrlYPastesRichRegisterLikeRust(t *testing.T) {
	m := vimTestModel("$tool [Image #1]")
	m.composerElements = []ComposerTextElement{
		{Start: 0, End: 5, Placeholder: "$tool"},
		{Start: 6, End: 16, Placeholder: "[Image #1]"},
	}
	m.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: "/tmp/photo.png"}}
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, '$')

	m.resetComposer()
	m.composerElements = nil
	m.attachments = nil
	updated, _ := m.Update(editorKey(bubbletea.KeyCtrlY))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "$tool [Image #1]" {
		t.Fatalf("value after ctrl-y = %q, want the rich register", got)
	}
	if len(m.attachments) != 1 || m.attachments[0].Path != "/tmp/photo.png" {
		t.Fatalf("attachments after ctrl-y = %#v, want the image re-attached", m.attachments)
	}
	if !hasComposerElementAt(m, "$tool", 0) || !hasComposerElementAt(m, "[Image #1]", len("$tool ")) {
		t.Fatalf("elements after ctrl-y = %#v, want both payloads", m.composerElements)
	}
}

// A rich yank in Replace mode pastes atomically and resets the per-character
// recovery, so Backspace cannot corrupt the pasted objects (Rust #57564).
func TestVimReplaceModeRichYankClearsStaleRecoveryLikeRust(t *testing.T) {
	m := vimTestModel("abc")
	m.vimReplaceMode = true
	m.composer.SetCursor(0)
	m.storeComposerRegister("[Image #1]", false, nil, []composerKillBufferImage{{Start: 0, End: 10, Path: "/tmp/photo.png"}})

	m = vimKeyPress(m, 'Q')
	if got := m.composer.Value(); got != "Qbc" {
		t.Fatalf("value after replace = %q, want Qbc", got)
	}
	if len(m.vimReplaceRecovery) != 1 {
		t.Fatalf("recovery after replace = %#v, want one entry", m.vimReplaceRecovery)
	}

	updated, _ := m.Update(editorKey(bubbletea.KeyCtrlY))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "Q[Image #1]bc" {
		t.Fatalf("value after rich yank = %q, want Q[Image #1]bc", got)
	}
	if len(m.vimReplaceRecovery) != 0 {
		t.Fatalf("rich yank kept stale recovery: %#v", m.vimReplaceRecovery)
	}
	if len(m.attachments) != 1 || len(m.composerElements) != 1 {
		t.Fatalf("rich yank payloads = attachments:%#v elements:%#v", m.attachments, m.composerElements)
	}

	updated, _ = m.Update(bubbletea.KeyMsg{Type: bubbletea.KeyBackspace})
	m = updated.(*Model)
	if got := m.composer.Value(); got != "Q[Image #1]bc" {
		t.Fatalf("backspace corrupted a pasted rich object: %q", got)
	}
	if len(m.attachments) != 1 || len(m.composerElements) != 1 {
		t.Fatalf("backspace dropped the pasted payload: attachments:%#v elements:%#v", m.attachments, m.composerElements)
	}
}

// A linewise paste leaves the cursor on the leading pasted element, matching
// Rust's linewise_rich_paste_preserves_the_cursor_on_the_leading_element.
func TestVimLinewiseRichPasteKeepsCursorOnLeadingElementLikeRust(t *testing.T) {
	m := vimTestModel("[Image #1]")
	m.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: "/tmp/photo.png"}}
	m.composerElements = []ComposerTextElement{{Start: 0, End: 10, Placeholder: "[Image #1]"}}
	m.composer.SetCursor(0)

	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "[Image #1]\n[Image #2]" {
		t.Fatalf("value after yyp = %q, want a fresh label on the next line", got)
	}
	if got := m.vimValueByteOffset(); got != len("[Image #1]\n") {
		t.Fatalf("cursor after yyp = %d, want the leading element at %d", got, len("[Image #1]\n"))
	}
}

func hasComposerElement(m *Model, placeholder string) bool {
	for _, element := range m.composerElements {
		if element.Placeholder == placeholder {
			return true
		}
	}
	return false
}

func hasComposerElementAt(m *Model, placeholder string, start int) bool {
	for _, element := range m.composerElements {
		if element.Start == start && element.End == start+len(placeholder) && element.Placeholder == placeholder {
			return true
		}
	}
	return false
}
