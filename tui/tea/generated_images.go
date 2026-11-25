package tea

import (
	historycell "codex_go/tui/history_cell"
	"strings"
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

func (m *Model) flushGeneratedImages(turn string) {
	if m == nil {
		return
	}
	key := m.generatedImageKey(turn)
	images := m.pendingGeneratedImages[key]
	delete(m.pendingGeneratedImages, key)
	for _, image := range images {
		m.applyHistoryCell(image.cell)
	}
}

func (m *Model) resetPendingGeneratedImages() {
	prefix := m.currentThreadID() + "\x00"
	for key := range m.pendingGeneratedImages {
		if strings.HasPrefix(key, prefix) {
			delete(m.pendingGeneratedImages, key)
		}
	}
}
