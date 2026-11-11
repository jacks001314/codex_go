package tea

import (
	"strings"

	mousemsg "codex_go/tui/mousemsg"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// transcriptMouseSelection mirrors renderedSelectionState for the fullscreen
// transcript. The owned screen captures mouse input, so the terminal cannot
// provide native drag selection over the conversation; this state selects the
// painted rows instead. Inline mode keeps selection terminal-owned.
type transcriptMouseSelection struct {
	active       bool
	anchorX      int
	anchorY      int
	endX         int
	endY         int
	snapshot     []string
	snapshotHash string
}

type transcriptMouseSelectionRegion struct {
	x    int
	y    int
	w    int
	h    int
	text []string
	hash string
}

func (s *transcriptMouseSelection) clear() {
	if s == nil {
		return
	}
	s.active = false
	s.anchorX = 0
	s.anchorY = 0
	s.endX = 0
	s.endY = 0
	s.snapshot = nil
	s.snapshotHash = ""
}

// captureTranscriptSelectionRegion records the painted transcript rows for the
// next gesture. The painted section is used instead of the raw transcript rows
// so widths, pinned headers, and padding match what the user sees. A changed
// transcript invalidates the selection; rows inserted above it shift the
// recorded anchors so existing selections keep following their cells.
func (m *Model) captureTranscriptSelectionRegion(sections []string, transcriptIndex int) {
	if m == nil {
		return
	}
	if !m.fullscreenTranscript || m.rawOutput || transcriptIndex < 0 || transcriptIndex >= len(sections) {
		m.transcriptSelection.clear()
		m.transcriptSelectionRegion = transcriptMouseSelectionRegion{}
		return
	}
	text := renderedRegionLines(sections[transcriptIndex])
	if len(text) == 0 {
		m.transcriptSelection.clear()
		m.transcriptSelectionRegion = transcriptMouseSelectionRegion{}
		return
	}
	width := max(m.width, 1)
	height := len(text)
	y := sectionTop(sections, transcriptIndex)
	hash := renderedRegionHash(text)
	if m.transcriptSelection.active {
		region := m.transcriptSelectionRegion
		if region.hash != hash || region.w != width || region.h != height {
			m.transcriptSelection.clear()
		} else if deltaY := y - region.y; deltaY != 0 {
			m.transcriptSelection.anchorY += deltaY
			m.transcriptSelection.endY += deltaY
		}
	}
	m.transcriptSelectionRegion = transcriptMouseSelectionRegion{
		x:    0,
		y:    y,
		w:    width,
		h:    height,
		text: text,
		hash: hash,
	}
}

func (m *Model) transcriptSelectionRegionAt(x, y int) (transcriptMouseSelectionRegion, bool) {
	if m == nil {
		return transcriptMouseSelectionRegion{}, false
	}
	region := m.transcriptSelectionRegion
	if region.w <= 0 || region.h <= 0 {
		return transcriptMouseSelectionRegion{}, false
	}
	if x >= region.x && x < region.x+region.w && y >= region.y && y < region.y+region.h {
		return region, true
	}
	return transcriptMouseSelectionRegion{}, false
}

// handleTranscriptSelectionMouse captures a drag over the visible transcript.
// It returns true when the gesture belongs to the selection, so the mouse
// press does not fall through to overlay or wheel handling.
func (m *Model) handleTranscriptSelectionMouse(msg mousemsg.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || !m.fullscreenTranscript || m.rawOutput {
		return false, nil
	}
	state := &m.transcriptSelection
	if msg.Button == mousemsg.MouseButtonWheelUp || msg.Button == mousemsg.MouseButtonWheelDown {
		// Rust clears the painted selection on ScrollUp/ScrollDown and lets the
		// same wheel scroll the transcript.
		state.clear()
		return false, nil
	}
	if msg.Button != mousemsg.MouseButtonLeft && msg.Action != mousemsg.MouseActionMotion {
		return false, nil
	}
	switch msg.Action {
	case mousemsg.MouseActionPress:
		if msg.Button != mousemsg.MouseButtonLeft {
			return false, nil
		}
		region, ok := m.transcriptSelectionRegionAt(msg.X, msg.Y)
		if !ok {
			if state.active {
				state.clear()
			}
			return false, nil
		}
		state.active = true
		state.anchorX, state.anchorY = msg.X, msg.Y
		state.endX, state.endY = msg.X, msg.Y
		state.snapshot = append([]string(nil), region.text...)
		state.snapshotHash = region.hash
		return true, nil
	case mousemsg.MouseActionMotion:
		if !state.active {
			return false, nil
		}
		m.extendTranscriptSelection(msg.X, msg.Y)
		return true, nil
	case mousemsg.MouseActionRelease:
		if msg.Button != mousemsg.MouseButtonLeft || !state.active {
			return false, nil
		}
		m.extendTranscriptSelection(msg.X, msg.Y)
		if state.anchorX == state.endX && state.anchorY == state.endY {
			state.clear()
			return true, nil
		}
		if m.copyOnSelectEnabled() {
			cmd := m.copyTranscriptSelection(true)
			if m.copyOnSelectClear {
				state.clear()
			}
			return true, cmd
		}
		return true, nil
	default:
		return false, nil
	}
}

func (m *Model) extendTranscriptSelection(x int, y int) {
	if m == nil || !m.transcriptSelection.active {
		return
	}
	region := m.transcriptSelectionRegion
	height := len(m.transcriptSelection.snapshot)
	if height == 0 {
		height = region.h
	}
	m.transcriptSelection.endX = clampModalInt(x, region.x, region.x+max(region.w-1, 0))
	m.transcriptSelection.endY = clampModalInt(y, region.y, region.y+max(height-1, 0))
}

func (m *Model) copyTranscriptSelection(primary bool) bubbletea.Cmd {
	text := m.transcriptSelectionText()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	clipboardCmd := m.clipboardWriteCmd("transcript selection", text, false, false)
	if !primary {
		return clipboardCmd
	}
	return bubbletea.Batch(clipboardCmd, m.publishPrimarySelectionCmd(text))
}

func (m *Model) transcriptSelectionText() string {
	if m == nil || !m.transcriptSelection.active {
		return ""
	}
	state := &m.transcriptSelection
	region := m.transcriptSelectionRegion
	startX, startY := state.anchorX-region.x, state.anchorY-region.y
	endX, endY := state.endX-region.x, state.endY-region.y
	if startY > endY || startY == endY && startX > endX {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}
	if startY < 0 {
		startY = 0
	}
	if endY >= len(state.snapshot) {
		endY = len(state.snapshot) - 1
	}
	if endY < startY {
		return ""
	}
	lines := make([]string, 0, endY-startY+1)
	for row := startY; row <= endY; row++ {
		line := state.snapshot[row]
		from, to := 0, codextui.DisplayWidth(line)
		if row == startY {
			from = startX
		}
		if row == endY {
			to = endX + 1
		}
		lines = append(lines, strings.TrimRight(sliceDisplayCells(line, from, to), " "))
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// applyTranscriptSelection restyles the selected transcript cells with reverse
// video before the frame is joined. Only the frame that captured the selection
// is highlighted, so stale snapshots never paint over different content.
func (m *Model) applyTranscriptSelection(sections []string, transcriptIndex int) []string {
	if m == nil || !m.transcriptSelection.active {
		return sections
	}
	if transcriptIndex < 0 || transcriptIndex >= len(sections) {
		return sections
	}
	state := &m.transcriptSelection
	region := m.transcriptSelectionRegion
	if region.w <= 0 || region.h <= 0 || region.hash != state.snapshotHash {
		state.clear()
		return sections
	}
	lines := strings.Split(sections[transcriptIndex], "\n")
	startX, startY := state.anchorX-region.x, state.anchorY-region.y
	endX, endY := state.endX-region.x, state.endY-region.y
	if startY > endY || startY == endY && startX > endX {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}
	startY = max(startY, 0)
	endY = min(endY, len(lines)-1)
	if endY < startY {
		return sections
	}
	for row := startY; row <= endY; row++ {
		from, to := 0, codextui.DisplayWidth(utils.StripANSI(lines[row]))
		if row == startY {
			from = startX
		}
		if row == endY {
			to = endX + 1
		}
		lines[row] = highlightDisplayCells(lines[row], from, to)
	}
	updated := append([]string(nil), sections...)
	updated[transcriptIndex] = strings.Join(lines, "\n")
	return updated
}
