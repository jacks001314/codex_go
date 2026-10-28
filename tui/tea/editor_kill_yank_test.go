package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// These tests verify the composer editor kill/yank actions (ctrl-k / ctrl-u /
// kill_whole_line / ctrl-y) that the bubbletea textarea does not handle
// natively, mirroring Rust bottom_pane/textarea.rs: kills cut into a
// single-entry kill buffer and yank pastes it at the cursor.

func editorKey(keyType bubbletea.KeyType) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: keyType}
}

// TestEditorKillBufferSurvivesNewSessionLikeRust pins #44742: replacing the
// composer for a new session keeps the editor kill buffer, so a line killed
// before the switch can still be yanked into the replacement composer.
func TestEditorKillBufferSurvivesNewSessionLikeRust(t *testing.T) {
	m := vimTestModel("saved line")
	m.composer.SetCursor(len("saved line"))
	updated, _ := m.Update(editorKey(bubbletea.KeyCtrlU))
	m = updated.(*Model)
	if got := m.composerKillBuffer; got != "saved line" {
		t.Fatalf("kill buffer = %q, want saved line", got)
	}
	m.State.SetThreadID("thread-old")

	m.startFreshNamedSession("", "Started a new local thread.")
	if got := m.composerKillBuffer; got != "saved line" {
		t.Fatalf("kill buffer after /new = %q, want saved line", got)
	}

	m.composer.SetValue("new line")
	m.composer.SetCursor(len("new line"))
	updated, _ = m.Update(editorKey(bubbletea.KeyCtrlY))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "new linesaved line" {
		t.Fatalf("value after yank = %q, want new linesaved line", got)
	}
}

// TestVimLineYankSurvivesNewSessionAndThreadSwitchLikeRust pins #44742's Vim
// path: dd populates the linewise yank, and both /new and a root thread switch
// keep it available for p in the replacement composer.
func TestVimLineYankSurvivesNewSessionAndThreadSwitchLikeRust(t *testing.T) {
	m := vimTestModel("saved line")
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'd')
	if got := m.composer.Value(); got != "" {
		t.Fatalf("dd value = %q, want empty composer", got)
	}
	if got := m.vimYank; got != "saved line" || !m.vimYankLinewise {
		t.Fatalf("dd yank = %q linewise=%v, want saved line / true", got, m.vimYankLinewise)
	}
	m.startFreshNamedSession("", "Started a new local thread.")
	m.composer.SetValue("new line")
	m.composer.SetCursor(len("new line"))
	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "new line\nsaved line" {
		t.Fatalf("/new p value = %q, want new line\\nsaved line", got)
	}

	m = vimTestModel("saved line")
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'd')
	m.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "target-thread",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "target-thread"},
			Status: "idle",
		},
	})
	m.composer.SetValue("new line")
	m.composer.SetCursor(len("new line"))
	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "new line\nsaved line" {
		t.Fatalf("thread-switch p value = %q, want new line\\nsaved line", got)
	}
}

// TestEditorKillLineEndAndStartLikeRust pins ctrl-k (kill to line end) and
// ctrl-u (kill to line start) on a single line.
func TestEditorKillLineEndAndStartLikeRust(t *testing.T) {
	m := vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimEscape(m) // no-op; vimTestModel starts in normal mode with Vim on
	updated, _ := m.Update(editorKey(bubbletea.KeyCtrlK))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "hello " {
		t.Fatalf("ctrl-k value = %q, want hello ", got)
	}
	if got := m.composerKillBuffer; got != "brave world" {
		t.Fatalf("kill buffer = %q, want brave world", got)
	}

	m = vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	updated, _ = m.Update(editorKey(bubbletea.KeyCtrlU))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "brave world" {
		t.Fatalf("ctrl-u value = %q, want brave world", got)
	}
	if got := m.composerKillBuffer; got != "hello " {
		t.Fatalf("kill buffer = %q, want hello ", got)
	}
}

// TestEditorKillAtLineBoundaryMergesLinesLikeRust pins the boundary behavior:
// ctrl-k at the line end kills the trailing newline, ctrl-u at the line start
// kills the preceding newline, merging the lines.
func TestEditorKillAtLineBoundaryMergesLinesLikeRust(t *testing.T) {
	m := vimTestModel("line one\nline two")
	// Move to the end of line zero (row 0, col 8): SetValue leaves the cursor
	// on the last row, so move up first.
	m.composer.CursorUp()
	m.composer.SetCursor(0)
	m = vimKeyPress(m, '$')
	updated, _ := m.Update(editorKey(bubbletea.KeyCtrlK))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "line oneline two" {
		t.Fatalf("ctrl-k at EOL value = %q, want line oneline two", got)
	}
	if got := m.composerKillBuffer; got != "\n" {
		t.Fatalf("kill buffer = %q, want newline", got)
	}

	m = vimTestModel("line one\nline two")
	// Move to the start of line one (row 1, col 0).
	// Cursor is already on the last row (line one).
	m.composer.SetCursor(0)
	updated, _ = m.Update(editorKey(bubbletea.KeyCtrlU))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "line oneline two" {
		t.Fatalf("ctrl-u at BOL value = %q, want line oneline two", got)
	}
}

// TestEditorKillWholeLineAndYankLikeRust pins kill_whole_line and ctrl-y.
func TestEditorKillWholeLineAndYankLikeRust(t *testing.T) {
	m := vimTestModel("line zero\nline one\nline two")
	m.composer.CursorUp()
	m.composer.CursorUp()
	m.composer.SetCursor(0)
	// Kill the whole first line (ctrl-k twice? no - use the whole-line action;
	// it has no default binding in the keymap, so drive editorKillRange via
	// the helper through a ctrl-k at BOL is not whole-line. Call the model
	// method directly for the no-default-binding action.)
	m.killComposerWholeLine()
	if got := m.composer.Value(); got != "line one\nline two" {
		t.Fatalf("kill_whole_line value = %q, want line one\\nline two", got)
	}
	if got := m.composerKillBuffer; got != "line zero\n" {
		t.Fatalf("kill buffer = %q, want line zero\\n", got)
	}

	// yank pastes the buffer at the cursor (row 0, col 0).
	m.composer.SetCursor(0)
	updated, _ := m.Update(editorKey(bubbletea.KeyCtrlY))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "line zero\nline one\nline two" {
		t.Fatalf("ctrl-y value = %q, want restored line zero\\nline one\\nline two", got)
	}
}

// TestKillBufferRestoresLocalImagesLikeRust pins #57502: killing text that
// contains an image placeholder keeps the image path, and yanking it back after
// the composer was replaced re-attaches the image with a fresh label.
func TestKillBufferRestoresLocalImagesLikeRust(t *testing.T) {
	const killedImage = "/tmp/killed-image.png"
	m := vimTestModel("[Image #1] restore me ")
	m.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: killedImage}}
	m.composerElements = []ComposerTextElement{{Start: 0, End: 10, Placeholder: "[Image #1]"}}
	m.composer.SetCursor(len(m.composer.Value()))

	m.killComposerToLineStart()
	if got := m.composer.Value(); got != "" {
		t.Fatalf("value after kill = %q, want empty composer", got)
	}
	if got := m.composerKillBuffer; got != "[Image #1] restore me " {
		t.Fatalf("kill buffer = %q, want [Image #1] restore me ", got)
	}
	if len(m.composerKillBufferImages) != 1 {
		t.Fatalf("kill buffer images = %#v, want one image", m.composerKillBufferImages)
	}
	if image := m.composerKillBufferImages[0]; image != (composerKillBufferImage{Start: 0, End: 10, Path: killedImage}) {
		t.Fatalf("kill buffer image = %#v, want placeholder range of %s", image, killedImage)
	}
	if len(m.composerElements) != 0 {
		t.Fatalf("elements after kill = %#v, want none", m.composerElements)
	}

	// Submitting clears the draft and its attachments but keeps the kill buffer.
	m.resetComposer()
	m.composerElements = nil
	m.attachments = nil

	const otherImage = "/tmp/other-image.png"
	m.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: otherImage}}
	m.composer.SetValue("[Image #1] ")
	m.composerElements = []ComposerTextElement{{Start: 0, End: 10, Placeholder: "[Image #1]"}}
	m.composer.SetCursor(len(m.composer.Value()))

	m.yankComposerKillBuffer()
	if got := m.composer.Value(); got != "[Image #1] [Image #2] restore me " {
		t.Fatalf("value after yank = %q, want [Image #1] [Image #2] restore me ", got)
	}
	if len(m.attachments) != 2 || m.attachments[0].Path != otherImage || m.attachments[1].Path != killedImage {
		t.Fatalf("attachments after yank = %#v, want %s then %s", m.attachments, otherImage, killedImage)
	}
	if len(m.composerElements) != 2 {
		t.Fatalf("elements after yank = %#v, want two image elements", m.composerElements)
	}
	if element := m.composerElements[1]; element.Start != 11 || element.End != 21 || element.Placeholder != "[Image #2]" {
		t.Fatalf("restored element = %#v, want [Image #2] at 11..21", element)
	}
}

// TestKillBufferImageYankRestoresRichObjectsInVimReplaceLikeRust pins #57564's
// replacement for #57502's plain-text guard: a rich yank rehydrates its image
// element atomically in Vim Replace mode instead of inserting placeholder text.
func TestKillBufferImageYankRestoresRichObjectsInVimReplaceLikeRust(t *testing.T) {
	m := vimTestModel("")
	m.vimReplaceMode = true
	m.composerKillBuffer = "[Image #1] plain"
	m.composerKillBufferImages = []composerKillBufferImage{{Start: 0, End: 10, Path: "/tmp/killed-image.png"}}
	m.composer.SetValue("hi")
	m.composer.SetCursor(len("hi"))

	m.yankComposerKillBuffer()
	if got := m.composer.Value(); got != "hi[Image #1] plain" {
		t.Fatalf("value after replace-mode yank = %q, want hi[Image #1] plain", got)
	}
	if len(m.attachments) != 1 || m.attachments[0].Path != "/tmp/killed-image.png" {
		t.Fatalf("attachments after replace-mode yank = %#v, want the killed image", m.attachments)
	}
	if len(m.composerElements) != 1 || m.composerElements[0].Placeholder != "[Image #1]" {
		t.Fatalf("elements after replace-mode yank = %#v, want one [Image #1] element", m.composerElements)
	}
}
