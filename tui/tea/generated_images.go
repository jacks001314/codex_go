package tea

import (
	"strings"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

type pendingGeneratedImage struct {
	id   string
	cell historycell.ImageGenerationHistoryCell
}

func (m *Model) generatedImageKey(turn string) string {
	return m.currentThreadID() + "\x00" + strings.TrimSpace(turn)
}

// Successful artifacts belong to the answer in an owned transcript. Failed,
// unsaved and terminal-scrollback results remain immediate history entries.
func (m *Model) recordGeneratedImage(id string, cell historycell.ImageGenerationHistoryCell) {
	turn := strings.TrimSpace(m.currentTurnID)
	if !m.ownedScreen || m.rawOutput || turn == "" || cell.ImagePath() == "" {
		m.applyHistoryCell(cell)
		return
	}
	if m.pendingGeneratedImages == nil {
		m.pendingGeneratedImages = map[string][]pendingGeneratedImage{}
	}
	key := m.generatedImageKey(turn)
	images := m.pendingGeneratedImages[key]
	for index := range images {
		if images[index].id == id {
			images[index].cell = cell
			m.pendingGeneratedImages[key] = images
			return
		}
	}
	m.pendingGeneratedImages[key] = append(images, pendingGeneratedImage{id: id, cell: cell})
}

// flushGeneratedImages releases every queued image for turn into the
// conversation that recorded it. Rust keeps one PendingGeneratedImages per
// display and takes it on that display's completion, so a side turn finishing
// while the parent conversation is in front still lands with its own answer.
func (m *Model) flushGeneratedImages(turn string) {
	if m == nil {
		return
	}
	turn = strings.TrimSpace(turn)
	if turn == "" {
		return
	}
	suffix := "\x00" + turn
	for key, images := range m.pendingGeneratedImages {
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		delete(m.pendingGeneratedImages, key)
		thread := strings.TrimSuffix(key, suffix)
		for _, image := range images {
			m.applyGeneratedImageToConversation(thread, image.cell)
		}
	}
}

// applyGeneratedImageToConversation renders a released image in the transcript
// that owns it, falling back to the visible one when that conversation is gone.
func (m *Model) applyGeneratedImageToConversation(thread string, cell historycell.ImageGenerationHistoryCell) {
	thread = strings.TrimSpace(thread)
	if thread == "" || thread == m.currentThreadID() {
		m.applyHistoryCell(cell)
		return
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	if m.appendGeneratedImageToSideConversation(thread, historyMessageFromCell(cell, width)) {
		return
	}
	m.applyHistoryCell(cell)
}

// appendGeneratedImageToSideConversation appends an image to the side or parent
// transcript that owns thread. It reports whether the conversation is still
// open, so a closed side chat falls back to the visible transcript.
func (m *Model) appendGeneratedImageToSideConversation(thread string, message codextui.Message) bool {
	side := m.activeSide
	if side == nil {
		return false
	}
	switch {
	case strings.TrimSpace(side.SideThreadID) == thread:
		if side.ShowingSide && m.currentThreadID() == thread {
			m.State.AddHistoryMessage(message)
			m.refreshTranscript()
			return true
		}
		side.SideMessages = append(side.SideMessages, message)
		return true
	case strings.TrimSpace(side.ParentThreadID) == thread:
		if side.ShowingSide {
			side.ParentMessages = append(side.ParentMessages, message)
			return true
		}
		m.State.AddHistoryMessage(message)
		m.refreshTranscript()
		return true
	}
	return false
}

func (m *Model) resetPendingGeneratedImages() {
	prefix := m.currentThreadID() + "\x00"
	for key := range m.pendingGeneratedImages {
		if strings.HasPrefix(key, prefix) {
			delete(m.pendingGeneratedImages, key)
		}
	}
}
