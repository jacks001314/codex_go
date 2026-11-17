package tea

import (
	"time"

	bubbletea "charm.land/bubbletea/v2"

	bottompane "codex_go/tui/bottom_pane"
	keymsg "codex_go/tui/keymsg"
)

// maxEditHistorySteps bounds the composer undo history (Rust MAX_UNDO_STEPS).
const maxEditHistorySteps = 64

// maxEditHistoryBytes bounds the bytes kept across both stacks (Rust
// MAX_UNDO_BYTES, raised to 8 MiB with #61954 so a large paste stays undoable).
const maxEditHistoryBytes = 8 * 1024 * 1024

// maxTypingSpan and maxTypingGap split a typing run into separate undo steps:
// Rust continues one step only while the whole run stays under a second and the
// next keystroke arrives within half a second.
const (
	maxTypingSpan = time.Second
	maxTypingGap  = 500 * time.Millisecond
)

// composerDraftSnapshot captures everything a composer edit can change: text,
// structured text elements and attachments. Restoring one returns the exact
// mention bindings and images the draft had before the edit (Rust
// `ComposerDraft`).
type composerDraftSnapshot struct {
	value       string
	elements    []ComposerTextElement
	attachments []bottompane.ComposerAttachment
}

// historyBytes estimates the snapshot's cost the way Rust's
// `ComposerDraft::history_bytes` does: the text plus the attachments and
// structured elements it keeps alive.
func (s composerDraftSnapshot) historyBytes() int {
	bytes := len(s.value)
	for _, element := range s.elements {
		bytes += len(element.Placeholder) + 16
	}
	for _, attachment := range s.attachments {
		bytes += len(attachment.Path) + len(attachment.URL) + len(attachment.DisplayName) + 16
	}
	return bytes
}

// typingRun is the open typing step: when it began, when it last advanced, and
// the cursor it last reached (Rust `EditHistory::typing`).
type typingRun struct {
	start  time.Time
	last   time.Time
	cursor int
}

// backspaceRun is the last backward deletion, kept so a quick follow-up
// continues the same step (Rust `EditHistory::backspace`).
type backspaceRun struct {
	last   time.Time
	cursor int
}

// typingEditKind names the grouped edit the current key began (Rust
// `TypingEdit`).
type typingEditKind int

const (
	typingEditNone typingEditKind = iota
	typingEditStart
	typingEditContinue
	typingEditBackspaceStart
	typingEditBackspaceContinue
)

// editDirection selects which stack an edit-history step pops from.
type editDirection int

const (
	editUndo editDirection = iota
	editRedo
)

// editHistory is the bounded composer undo/redo history shared by Vim and plain
// editing (Rust `EditHistory`, #61951). One transaction captures the draft
// before an edit; committing it moves that snapshot onto the undo stack and a
// new edit clears redo.
type editHistory struct {
	undo            []composerDraftSnapshot
	redo            []composerDraftSnapshot
	pending         bool
	pendingSnapshot composerDraftSnapshot
	typing          *typingRun
	backspace       *backspaceRun
	edit            typingEditKind
	editCursor      int
}

// begin opens an edit transaction when none is active and reports whether this
// call created it. Insert sessions and multi-key commands reuse the outer
// transaction.
func (h *editHistory) begin(snapshot composerDraftSnapshot) bool {
	if h.pending {
		return false
	}
	h.pending = true
	h.pendingSnapshot = snapshot
	return true
}

// commit closes an open transaction, pushing the pre-edit snapshot when the
// draft actually changed.
func (h *editHistory) commit(current composerDraftSnapshot) bool {
	if !h.pending {
		return false
	}
	h.pending = false
	if h.pendingSnapshot.value == current.value {
		return false
	}
	h.undo = append(h.undo, h.pendingSnapshot)
	h.trim()
	h.redo = nil
	return true
}

// step moves one snapshot between the stacks and returns it for restoration
// (Rust `EditHistory::step`).
func (h *editHistory) step(direction editDirection, current composerDraftSnapshot) (composerDraftSnapshot, bool) {
	h.pending = false
	h.typing = nil
	h.backspace = nil
	h.edit = typingEditNone
	from, to := &h.undo, &h.redo
	if direction == editRedo {
		from, to = &h.redo, &h.undo
	}
	if len(*from) == 0 {
		return composerDraftSnapshot{}, false
	}
	snapshot := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	// A draft too large to keep would blow the byte budget; Rust clears the
	// destination stack instead of pushing it.
	if current.historyBytes() <= maxEditHistoryBytes {
		*to = append(*to, current)
	} else {
		*to = nil
	}
	h.trim()
	return snapshot, true
}

// beginTypingEdit opens a typing step, or continues the open one when Rust's
// window still holds: the run started under a second ago, the previous
// keystroke is under half a second old, and the cursor is where that keystroke
// left it.
func (h *editHistory) beginTypingEdit(current composerDraftSnapshot, cursor int, now time.Time) {
	if h.pending || h.edit != typingEditNone {
		return
	}
	if h.typing != nil &&
		now.Sub(h.typing.start) < maxTypingSpan &&
		now.Sub(h.typing.last) < maxTypingGap &&
		cursor == h.typing.cursor {
		h.edit = typingEditContinue
		h.editCursor = cursor
		return
	}
	h.edit = typingEditStart
	h.editCursor = cursor
	h.begin(current)
}

// beginBackspaceEdit opens a backward-deletion step, continuing the previous
// one only when the Backspaces arrive close together and keep moving left.
func (h *editHistory) beginBackspaceEdit(current composerDraftSnapshot, cursor int, now time.Time) {
	if h.pending || h.edit != typingEditNone {
		return
	}
	h.typing = nil
	h.editCursor = cursor
	if h.backspace != nil && now.Sub(h.backspace.last) < maxTypingGap && cursor == h.backspace.cursor {
		h.edit = typingEditBackspaceContinue
		return
	}
	h.edit = typingEditBackspaceStart
	h.begin(current)
}

// finishTypingEdit records how the grouped keystroke changed the draft. A
// continuation only extends the run when it moved the cursor the expected way;
// otherwise the next keystroke starts a new step (Rust `finish_edit`).
func (h *editHistory) finishTypingEdit(current composerDraftSnapshot, cursor int, now time.Time) {
	switch h.edit {
	case typingEditContinue:
		h.edit = typingEditNone
		if h.typing != nil && cursor > h.typing.cursor {
			h.typing.last = now
			h.typing.cursor = cursor
		} else {
			h.typing = nil
		}
	case typingEditBackspaceContinue:
		previous := h.editCursor
		h.edit = typingEditNone
		if cursor < previous {
			h.backspace = &backspaceRun{last: now, cursor: cursor}
		} else {
			h.backspace = nil
		}
	case typingEditStart, typingEditBackspaceStart:
		kind := h.edit
		h.edit = typingEditNone
		if !h.commit(current) {
			h.typing = nil
			return
		}
		if kind == typingEditStart {
			h.typing = &typingRun{start: now, last: now, cursor: cursor}
		} else {
			h.backspace = &backspaceRun{last: now, cursor: cursor}
		}
	}
}

// beginStandaloneEdit closes any open group and opens a transaction the caller
// commits after the edit, so it cannot merge with neighbouring typing.
func (h *editHistory) beginStandaloneEdit(current composerDraftSnapshot) {
	if h.pending {
		h.commit(current)
	}
	h.typing = nil
	h.backspace = nil
	h.edit = typingEditNone
	h.begin(current)
}

// trim bounds the undo stack, dropping the oldest steps first.
func (h *editHistory) trim() {
	if len(h.undo) > maxEditHistorySteps {
		h.undo = h.undo[len(h.undo)-maxEditHistorySteps:]
	}
	total := 0
	for _, snapshot := range h.undo {
		total += snapshot.historyBytes()
	}
	for _, snapshot := range h.redo {
		total += snapshot.historyBytes()
	}
	for total > maxEditHistoryBytes && (len(h.undo) > 0 || len(h.redo) > 0) {
		if len(h.undo) > 0 {
			total -= h.undo[0].historyBytes()
			h.undo = h.undo[1:]
			continue
		}
		total -= h.redo[0].historyBytes()
		h.redo = h.redo[1:]
	}
}

// reset drops all history, for example when a new prompt replaces the draft.
func (h *editHistory) reset() {
	*h = editHistory{}
}

// currentDraftSnapshot returns the raw composer draft used by undo/redo.
func (m *Model) currentDraftSnapshot() composerDraftSnapshot {
	if m == nil {
		return composerDraftSnapshot{}
	}
	return composerDraftSnapshot{
		value:       m.composer.Value(),
		elements:    cloneComposerTextElements(m.composerElements),
		attachments: cloneComposerAttachments(m.attachments),
	}
}

// restoreDraftSnapshot installs a snapshot's text, elements and attachments.
func (m *Model) restoreDraftSnapshot(snapshot composerDraftSnapshot) {
	if m == nil {
		return
	}
	m.composer.SetValue(snapshot.value)
	m.composerElements = cloneComposerTextElements(snapshot.elements)
	m.attachments = cloneComposerAttachments(snapshot.attachments)
	m.vimSetCursorAtByteOffset(len(snapshot.value))
}

// beginEdit opens an edit transaction when none is active and reports whether
// this call created the transaction.
func (m *Model) beginEdit() bool {
	if m == nil {
		return false
	}
	return m.editHistory.begin(m.currentDraftSnapshot())
}

// commitEdit closes an open transaction and records the pre-edit draft.
func (m *Model) commitEdit() {
	if m == nil {
		return
	}
	m.editHistory.commit(m.currentDraftSnapshot())
}

// finishEdit closes a transaction created by the caller of a mutating helper
// (startedHere) while leaving an enclosing transaction open for its own commit.
func (m *Model) finishEdit(startedHere bool) {
	if startedHere {
		m.commitEdit()
	}
}

// applyUndo restores the newest undo snapshot and moves the current draft onto
// the redo stack (Vim `u`, Rust #41941).
func (m *Model) applyUndo() bool {
	if m == nil {
		return false
	}
	snapshot, ok := m.editHistory.step(editUndo, m.currentDraftSnapshot())
	if !ok {
		return false
	}
	m.restoreDraftSnapshot(snapshot)
	return true
}

// applyRedo restores the newest redo snapshot (Vim ctrl-r, Rust #42140).
func (m *Model) applyRedo() bool {
	if m == nil {
		return false
	}
	snapshot, ok := m.editHistory.step(editRedo, m.currentDraftSnapshot())
	if !ok {
		return false
	}
	m.restoreDraftSnapshot(snapshot)
	return true
}

// resetEditHistory clears undo/redo state when a draft is replaced by a new
// prompt, an external editor edit, or a thread switch.
func (m *Model) resetEditHistory() {
	if m == nil {
		return
	}
	m.editHistory.reset()
}

// resetVimEditHistory keeps the original call sites readable; the history it
// clears is shared, so plain editing resets the same state.
func (m *Model) resetVimEditHistory() {
	m.resetEditHistory()
}

// beginComposerEdit opens the composer edit transaction for one incoming
// message. Contiguous typing and Backspaces share a step; pastes and one-off
// edits get their own (Rust #61954's non-Vim path). Vim mode drives its own
// transactions from vim_mode.go.
func (m *Model) beginComposerEdit(message bubbletea.Msg) {
	if m == nil || m.vimMode {
		return
	}
	key, ok := message.(keymsg.KeyMsg)
	if !ok {
		m.editHistory.beginStandaloneEdit(m.currentDraftSnapshot())
		return
	}
	switch {
	case key.Type == keymsg.KeyRunes && key.Paste:
		m.editHistory.beginStandaloneEdit(m.currentDraftSnapshot())
	case key.Type == keymsg.KeyRunes:
		m.editHistory.beginTypingEdit(m.currentDraftSnapshot(), m.composerCursor(), m.currentTime())
	case key.Type == keymsg.KeyBackspace:
		m.editHistory.beginBackspaceEdit(m.currentDraftSnapshot(), m.composerCursor(), m.currentTime())
	default:
		m.editHistory.beginStandaloneEdit(m.currentDraftSnapshot())
	}
}

// finishComposerEdit records how the keystroke changed the draft. A typing or
// Backspace run stays open only while Rust's window holds; a standalone edit
// commits immediately so it cannot merge with the next typing run.
func (m *Model) finishComposerEdit() {
	if m == nil || m.vimMode {
		return
	}
	if m.editHistory.edit != typingEditNone {
		m.editHistory.finishTypingEdit(m.currentDraftSnapshot(), m.composerCursor(), m.currentTime())
		return
	}
	if m.editHistory.pending {
		m.commitEdit()
	}
}

// beginComposerPasteEdit opens a standalone transaction for one bracketed
// paste. Vim mode keeps its insert-session transaction instead.
func (m *Model) beginComposerPasteEdit() {
	if m == nil || m.vimMode {
		return
	}
	m.editHistory.beginStandaloneEdit(m.currentDraftSnapshot())
}

// applyComposerEditHistoryKey handles non-Vim undo and redo. Both keys are
// consumed even when the history is empty (Rust #61954).
func (m *Model) applyComposerEditHistoryKey(keySpec string) bool {
	if m == nil || m.vimMode {
		return false
	}
	switch {
	case m.keyMatches("composer", "redo", keySpec):
		m.finishComposerEdit()
		m.applyRedo()
	case m.keyMatches("composer", "undo", keySpec):
		m.finishComposerEdit()
		m.applyUndo()
	default:
		return false
	}
	m.refreshSlashPopup()
	m.refreshSkillPopup()
	return true
}
