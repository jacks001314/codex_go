package tea

import (
	bubbletea "charm.land/bubbletea/v2"

	bottompane "codex_go/tui/bottom_pane"
	keymsg "codex_go/tui/keymsg"
)

// maxEditHistorySteps bounds the composer undo history (Rust MAX_UNDO_STEPS).
const maxEditHistorySteps = 64

// composerDraftSnapshot captures everything a composer edit can change: text,
// structured text elements and attachments. Restoring one returns the exact
// mention bindings and images the draft had before the edit (Rust
// `ComposerDraft`).
type composerDraftSnapshot struct {
	value       string
	elements    []ComposerTextElement
	attachments []bottompane.ComposerAttachment
}

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
	group           editGroup
}

// editGroup names a run of related keystrokes that undo as one step.
type editGroup int

const (
	editGroupNone editGroup = iota
	editGroupTyping
	editGroupBackspace
)

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
	h.group = editGroupNone
	return true
}

// step moves one snapshot between the stacks and returns it for restoration
// (Rust `EditHistory::step`).
func (h *editHistory) step(direction editDirection, current composerDraftSnapshot) (composerDraftSnapshot, bool) {
	h.pending = false
	from, to := &h.undo, &h.redo
	if direction == editRedo {
		from, to = &h.redo, &h.undo
	}
	if len(*from) == 0 {
		return composerDraftSnapshot{}, false
	}
	snapshot := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, current)
	h.trim()
	h.group = editGroupNone
	return snapshot, true
}

// beginGroupedEdit opens or continues a grouped transaction: contiguous typing
// or Backspaces share one undo step, while any other edit closes the open group
// (Rust #61954).
func (h *editHistory) beginGroupedEdit(group editGroup, current composerDraftSnapshot) {
	if h.pending {
		if h.group == group {
			return
		}
		h.commit(current)
	}
	h.begin(current)
	h.group = group
}

// beginStandaloneEdit closes any open group and opens a transaction the caller
// commits after the edit, so it cannot merge with neighbouring typing.
func (h *editHistory) beginStandaloneEdit(current composerDraftSnapshot) {
	if h.pending {
		h.commit(current)
	}
	h.begin(current)
}

// trim bounds the undo stack, dropping the oldest steps first.
func (h *editHistory) trim() {
	if len(h.undo) > maxEditHistorySteps {
		h.undo = h.undo[len(h.undo)-maxEditHistorySteps:]
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
		m.editHistory.beginGroupedEdit(editGroupTyping, m.currentDraftSnapshot())
	case key.Type == keymsg.KeyBackspace:
		m.editHistory.beginGroupedEdit(editGroupBackspace, m.currentDraftSnapshot())
	default:
		m.editHistory.beginStandaloneEdit(m.currentDraftSnapshot())
	}
}

// finishComposerEdit commits a standalone edit so it cannot merge with the next
// typing run. Grouped typing and Backspace transactions stay open.
func (m *Model) finishComposerEdit() {
	if m == nil || m.vimMode {
		return
	}
	if m.editHistory.pending && m.editHistory.group == editGroupNone {
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
		m.commitEdit()
		m.applyRedo()
	case m.keyMatches("composer", "undo", keySpec):
		m.commitEdit()
		m.applyUndo()
	default:
		return false
	}
	m.editHistory.group = editGroupNone
	m.refreshSlashPopup()
	m.refreshSkillPopup()
	return true
}
