package tea

import (
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/utils"
)

func newPasteTestModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-paste")
	model := NewModel(state, Options{Width: 100, Height: 40})
	model.ensureSize()
	return model
}

// bracketedPaste feeds a paste the way Bubble Tea delivers a terminal bracketed
// paste: one KeyRunes message with Paste set.
func bracketedPaste(model *Model, text string) {
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune(text), Paste: true})
}

// TestLargePastePlaceholderSubmitLikeRust drives a real bracketed paste through
// Model.Update: the composer must show Rust's "[Pasted Content N chars]"
// placeholder (chat_composer.rs:471 LARGE_PASTE_CHAR_THRESHOLD), the view must
// not paint the pasted text, and Enter must submit the original content byte for
// byte (user_messages.rs:331-354 expands pending_pastes on submit).
func TestLargePastePlaceholderSubmitLikeRust(t *testing.T) {
	model := newPasteTestModel(t)
	// Keep the paste free of edge whitespace: Rust expands pending_pastes and
	// only then applies trim_submission, so a paste ending in a space is
	// legitimately trimmed before it is sent.
	paste := strings.Repeat("abc ", 399) + "xyz" // 1599 characters
	placeholder := "[Pasted Content 1599 chars]"

	bracketedPaste(model, paste)

	view := utils.StripANSI(model.View())
	if !strings.Contains(view, placeholder) {
		t.Fatalf("composer view is missing the paste placeholder %q:\n%s", placeholder, view)
	}
	if strings.Contains(view, strings.Repeat("abc ", 40)) {
		t.Fatalf("the large paste was rendered inline instead of as a placeholder:\n%s", view)
	}
	if got := model.composer.Value(); got != placeholder {
		t.Fatalf("composer value = %q, want the placeholder only", got)
	}

	model.clearComposerPasteWindow()
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if len(model.submitRequests) != 1 {
		t.Fatalf("submissions = %d, want 1", len(model.submitRequests))
	}
	request := model.submitRequests[0]
	if got, want := sha256.Sum256([]byte(request.Prompt)), sha256.Sum256([]byte(paste)); got != want {
		t.Fatalf("submitted prompt sha256 = %x, want the pasted content sha256 %x (len %d vs %d)",
			got, want, len(request.Prompt), len(paste))
	}
	if len(request.TextElements) != 0 {
		t.Fatalf("paste placeholders must not be sent as text elements: %#v", request.TextElements)
	}
	if model.composer.Value() != "" || len(model.pendingPastes) != 0 {
		t.Fatalf("draft survived the submit: value=%q pastes=%d", model.composer.Value(), len(model.pendingPastes))
	}
}

// TestLargePastePlaceholderNumberingLikeRust covers Rust
// next_large_paste_placeholder: repeated sizes take a " #N" suffix, and the
// label starts over once no placeholder of that size is pending.
func TestLargePastePlaceholderNumberingLikeRust(t *testing.T) {
	model := newPasteTestModel(t)
	paste := strings.Repeat("x", 1001)
	base := "[Pasted Content 1001 chars]"

	bracketedPaste(model, paste)
	bracketedPaste(model, paste)
	if got, want := model.composer.Value(), base+base+" #2"; got != want {
		t.Fatalf("composer = %q, want %q", got, want)
	}

	model.clearComposerPasteWindow()
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	request := model.submitRequests[0]
	if request.Prompt != paste+paste {
		t.Fatalf("submitted prompt = %q, want both pastes expanded", request.Prompt)
	}

	bracketedPaste(model, paste)
	if got := model.composer.Value(); got != base {
		t.Fatalf("composer after clearing = %q, want the base label %q", got, base)
	}
}

// TestPasteAtThresholdStaysInlineLikeRust pins the Rust comparison: the
// placeholder only applies past LARGE_PASTE_CHAR_THRESHOLD.
func TestPasteAtThresholdStaysInlineLikeRust(t *testing.T) {
	model := newPasteTestModel(t)
	paste := strings.Repeat("z", 1000)
	bracketedPaste(model, paste)
	if got := model.composer.Value(); got != paste {
		t.Fatalf("a paste at the threshold must stay inline, got %q", got)
	}
	if len(model.pendingPastes) != 0 {
		t.Fatalf("pending pastes = %d, want 0", len(model.pendingPastes))
	}
}

// TestLargePasteDraftRestoreLikeRust covers the restore half of
// ThreadComposerState::pending_pastes: a draft saved with a placeholder keeps
// its mapping across a thread switch, and a mapping whose placeholder is gone is
// dropped.
func TestLargePasteDraftRestoreLikeRust(t *testing.T) {
	model := newPasteTestModel(t)
	paste := strings.Repeat("draft-paste ", 119) + "draft-tail" // 1438 characters
	bracketedPaste(model, paste)
	placeholder := "[Pasted Content 1438 chars]"
	if got := model.composer.Value(); got != placeholder {
		t.Fatalf("composer = %q, want %q", got, placeholder)
	}

	model.captureAgentsOverviewDraft("thread-paste")
	if kept := model.agentsOverviewDraftPastes["thread-paste"]; len(kept) != 1 {
		t.Fatalf("saved draft pastes = %#v, want one mapping", kept)
	}

	// Simulate the switch: the live draft goes away, the saved draft returns.
	model.resetComposer()
	saved := model.agentsOverviewDrafts["thread-paste"]
	model.agentsOverviewPendingDraft = &saved
	model.agentsOverviewPendingDraftPastes = append([]composerPendingPaste(nil), model.agentsOverviewDraftPastes["thread-paste"]...)
	model.restorePendingAgentsOverviewDraft()

	model.clearComposerPasteWindow()
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if len(model.submitRequests) != 1 {
		t.Fatalf("submissions = %d, want 1", len(model.submitRequests))
	}
	if got := model.submitRequests[0].Prompt; got != paste {
		t.Fatalf("restored draft submitted %q, want the expanded paste", got)
	}

	// A mapping whose placeholder was edited out of the draft must not come back.
	model2 := newPasteTestModel(t)
	bracketedPaste(model2, paste)
	model2.captureAgentsOverviewDraft("thread-paste")
	model2.resetComposer()
	edited := "placeholder removed"
	model2.agentsOverviewPendingDraft = &edited
	model2.agentsOverviewPendingDraftPastes = append([]composerPendingPaste(nil), model2.agentsOverviewDraftPastes["thread-paste"]...)
	model2.restorePendingAgentsOverviewDraft()
	if len(model2.pendingPastes) != 0 {
		t.Fatalf("a stale paste mapping was restored: %#v", model2.pendingPastes)
	}
}

// TestLargePasteTrimAfterExpansionLikeRust pins the ordering in Rust's
// prepare_submission_text_with_options (chat_composer.rs): pending_pastes are
// expanded first and trim_submission trims the expanded text afterwards.
// Trimming the placeholder first would leave the pasted edge whitespace in the
// submission.
func TestLargePasteTrimAfterExpansionLikeRust(t *testing.T) {
	model := newPasteTestModel(t)
	body := strings.Repeat("trim ", 249) + "trim" // 1249 characters, no edge space
	bracketedPaste(model, "\n"+body+"\n")
	placeholder := "[Pasted Content 1251 chars]"
	if got := model.composer.Value(); got != placeholder {
		t.Fatalf("composer = %q, want %q", got, placeholder)
	}

	model.clearComposerPasteWindow()
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if len(model.submitRequests) != 1 {
		t.Fatalf("submissions = %d, want 1", len(model.submitRequests))
	}
	if got := model.submitRequests[0].Prompt; got != body {
		t.Fatalf("submitted %d chars, want the expanded paste trimmed to %d chars", len(got), len(body))
	}
}

// TestLargePasteAndImagePasteStaySeparateLikeRust pins that the pending-paste
// carrier does not collide with the attachment carrier: a clipboard image goes
// to attachments (Rust's local_image_paths) and never becomes a pending paste,
// while a large text paste goes to pending_pastes and never into attachments.
func TestLargePasteAndImagePasteStaySeparateLikeRust(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "screenshot.png")
	writeComposerTestPNG(t, imagePath)
	previousImage, previousText := readClipboardImageForPaste, readClipboardTextForPaste
	readClipboardImageForPaste = func() (string, error) { return imagePath, nil }
	t.Cleanup(func() {
		readClipboardImageForPaste, readClipboardTextForPaste = previousImage, previousText
	})

	model := newPasteTestModel(t)
	model.pasteClipboardShortcut()
	if len(model.attachments) != 1 || model.attachments[0].Kind != bottompane.AttachmentImage {
		t.Fatalf("attachments after image paste = %#v, want one image", model.attachments)
	}
	if len(model.pendingPastes) != 0 {
		t.Fatalf("an image paste became a pending text paste: %#v", model.pendingPastes)
	}
	if got := model.composer.Value(); strings.Contains(got, "Pasted Content") {
		t.Fatalf("image paste wrote a text placeholder into the composer: %q", got)
	}

	model.clearComposerPasteWindow()
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if len(model.submitRequests) != 1 {
		t.Fatalf("submissions = %d, want 1", len(model.submitRequests))
	}
	request := model.submitRequests[0]
	if request.Prompt != "" || len(request.TextElements) != 0 {
		t.Fatalf("image-only submit = %#v, want an empty prompt with no text elements", request)
	}
	if len(request.Attachments) != 1 || request.Attachments[0].Path != imagePath {
		t.Fatalf("image attachments = %#v, want %q", request.Attachments, imagePath)
	}
}
