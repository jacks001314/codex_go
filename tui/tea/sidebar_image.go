package tea

import (
	codextui "codex_go/tui"
	"codex_go/tui/pets"
	"codex_go/tui/transcript_image"
)

// Sidebar images arrive as authorized bytes, including remote sessions. Never
// reopen their server path on the client machine.
func (m *Model) sidebarImageProtocol() pets.ImageProtocol {
	if m == nil || m.rawOutput {
		return ""
	}
	support := m.petImageSupport()
	switch support.Protocol {
	case pets.ImageProtocolKitty, pets.ImageProtocolKittyLocalFile:
		return support.Protocol
	case pets.ImageProtocolSixel:
		if m.transcriptImageCell.Width > 0 && m.transcriptImageCell.Height > 0 {
			return support.Protocol
		}
	}
	return ""
}

func (m *Model) fitSidebarImage(tab *sidebarDocumentTab, width, height int) *transcript_image.TranscriptImage {
	rows := max(height-1, 0) // image label
	if height > 2 {
		rows--
	}
	if m.tmuxImageBase() != nil {
		rows = min(rows, 6)
	}
	return tab.View.Image.Fit(width, rows, m.sidebarImageProtocol(), m.transcriptImageCell)
}

func (m *Model) renderSidebarImage(rows []string, top, height, width int, tab *sidebarDocumentTab) {
	if height <= 0 {
		return
	}
	rows[top] = " " + codextui.TruncateToWidth(m.sidebarDocumentDimText(tab.View.Image.Label), width)
	if height > 1 && m.fitSidebarImage(tab, width, height) == nil {
		rows[top+1] = " " + codextui.TruncateToWidth("Image preview unavailable in this terminal", width)
		if height > 2 {
			rows[top+2] = " " + codextui.TruncateToWidth(tab.Path, width)
		}
	}
}

// sidebarImageOverlay is recomputed from the same body as the text frame, so
// tab close, resize, path view, loading and zoom release displaced graphics.
func (m *Model) sidebarImageOverlay() *transcriptImageOverlay {
	if !m.sidebarDocumentVisible() || m.sidebarImageProtocol() == "" {
		return nil
	}
	tab := m.sidebarDocumentActiveTab()
	if tab == nil || tab.Loading || tab.AwaitingOpen || tab.PathScroll != nil || tab.View.Err != "" || tab.View.Image == nil {
		return nil
	}
	left, width, _, ok := m.sidePaneLayout(m.width)
	if !ok {
		return nil
	}
	// Sliding columns crop the text; wait until the pane reaches its final width.
	if _, paint := m.sidePanePaintWidths(m.width, width, m.paneMotion.fraction); paint != width {
		return nil
	}
	height := m.sideWorkspaceHeight()
	if m.sideResize != nil && height > 1 {
		height--
	}
	footer := m.sidebarDocumentFooterRows(width, height, tab)
	top := 1
	body := max(height-top-len(footer), 0)
	if tab.Snapshot && body > 1 {
		top++
		body--
	}
	image := m.fitSidebarImage(tab, max(width-2, 1), body)
	if image == nil {
		return nil
	}
	x := 1 + (max(width-2, 1)-int(image.Columns))/2
	if left > 0 {
		x += left + sideWorkspaceDividerWidth
	}
	row := top + 1 + max(body-1-int(image.Rows), 0)/2
	frameRows := m.transcriptImageFrameRows
	if frameRows <= 0 {
		frameRows = m.height
	}
	row += m.height - frameRows + m.insetRenderOriginY
	return &transcriptImageOverlay{image: image, x: x, row: row}
}
