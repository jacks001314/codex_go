package tea

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust parity: app/footer_selection.rs + rendered_selection.rs. Go keeps text
// selection terminal-owned inside the transcript, but status and footer rows
// are painted by the model and therefore support app-owned selection.

type renderedRegionKind int

const (
	renderedRegionStatus renderedRegionKind = iota
	renderedRegionFooter
	renderedRegionWorkingTip
	renderedRegionCompletionTip
	renderedRegionCount
)

type renderedRegion struct {
	kind         renderedRegionKind
	sectionIndex int
	sectionRow   int
	x            int
	y            int
	w            int
	h            int
	text         []string
	hash         string
}

type renderedSelectionState struct {
	active       bool
	region       renderedRegionKind
	anchorX      int
	anchorY      int
	endX         int
	endY         int
	snapshot     []string
	snapshotHash string
	regions      [renderedRegionCount]renderedRegion
}

func (s *renderedSelectionState) clear() {
	if s == nil {
		return
	}
	s.active = false
	s.region = 0
	s.snapshot = nil
	s.snapshotHash = ""
}

func renderedRegionHash(text []string) string {
	sum := sha256.Sum256([]byte(strings.Join(text, "\n")))
	return hex.EncodeToString(sum[:])
}

// captureRenderedSelectionRegions records the painted status and footer rows
// for the next gesture. A changed region invalidates a selection in that
// region, while unrelated regions stay live.
func (m *Model) captureRenderedSelectionRegions(sections []string, footerIndex int) {
	if m == nil || len(sections) == 0 {
		return
	}
	if !m.fullscreenTranscript || m.rawOutput {
		m.renderedSelection.clear()
		for i := range m.renderedSelection.regions {
			m.renderedSelection.regions[i] = renderedRegion{}
		}
		return
	}
	statusText := renderedRegionLines(sections[0])
	m.captureRenderedRegion(renderedRegionStatus, 0, 0, 0, 0, m.width, len(statusText), statusText)
	if footerIndex >= 0 && footerIndex < len(sections) {
		footerText := renderedRegionLines(sections[footerIndex])
		y := 0
		for _, section := range sections[:footerIndex] {
			y += sectionHeight(section)
		}
		m.captureRenderedRegion(renderedRegionFooter, footerIndex, 0, 0, y, m.width, len(footerText), footerText)
	}
}

func (m *Model) captureRenderedRegion(kind renderedRegionKind, sectionIndex, sectionRow, x, y, width, height int, text []string) {
	region := &m.renderedSelection.regions[kind]
	hash := renderedRegionHash(text)
	if m.renderedSelection.active && m.renderedSelection.region == kind {
		if region.hash != hash ||
			region.x != x ||
			region.w != width ||
			region.h != height {
			m.renderedSelection.clear()
		} else {
			// Read-only selections survive unrelated rows being inserted above
			// them (for example copy feedback), following the same cells.
			deltaY := (y - region.y) - (sectionRow - region.sectionRow)
			if deltaY != 0 {
				m.renderedSelection.anchorY += deltaY
				m.renderedSelection.endY += deltaY
			}
		}
	}
	*region = renderedRegion{
		kind:         kind,
		sectionIndex: sectionIndex,
		sectionRow:   sectionRow,
		x:            x,
		y:            y,
		w:            width,
		h:            height,
		text:         text,
		hash:         hash,
	}
}

// captureTurnTipSelectionRegions registers the painted working and completion
// tip rows for the same read-only selection path as status and footer text
// (Rust #53152).
func (m *Model) captureTurnTipSelectionRegions(sections []string, workingIndex int) {
	if m == nil {
		return
	}
	if !m.fullscreenTranscript || m.rawOutput {
		m.clearRenderedRegion(renderedRegionWorkingTip)
		m.clearRenderedRegion(renderedRegionCompletionTip)
		return
	}
	m.captureTurnTipSelectionRegion(renderedRegionWorkingTip, sections, workingIndex)
	m.captureTurnTipSelectionRegion(renderedRegionCompletionTip, sections, 1)
}

func (m *Model) captureTurnTipSelectionRegion(kind renderedRegionKind, sections []string, sectionIndex int) {
	if sectionIndex < 0 || sectionIndex >= len(sections) {
		m.clearRenderedRegion(kind)
		return
	}
	lines := renderedRegionLines(sections[sectionIndex])
	row := -1
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.Contains(lines[index], "\u2514 ") && strings.Contains(lines[index], "Tip:") {
			row = index
			break
		}
	}
	if row < 0 {
		m.clearRenderedRegion(kind)
		return
	}
	y := sectionTop(sections, sectionIndex) + row
	m.captureRenderedRegion(kind, sectionIndex, row, 0, y, m.width, 1, []string{lines[row]})
}

func sectionTop(sections []string, index int) int {
	y := 0
	for sectionIndex := 0; sectionIndex < index && sectionIndex < len(sections); sectionIndex++ {
		y += sectionHeight(sections[sectionIndex])
	}
	return y
}

// clearRenderedRegion invalidates a selection whose painted surface vanished.
func (m *Model) clearRenderedRegion(kind renderedRegionKind) {
	if m == nil {
		return
	}
	if m.renderedSelection.active && m.renderedSelection.region == kind {
		m.renderedSelection.clear()
	}
	m.renderedSelection.regions[kind] = renderedRegion{}
}

func renderedRegionLines(section string) []string {
	plain := utils.StripANSI(strings.ReplaceAll(section, "\r\n", "\n"))
	lines := strings.Split(plain, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func sectionHeight(section string) int {
	if section == "" {
		return 1
	}
	return strings.Count(section, "\n") + 1
}

func (m *Model) renderedSelectionRegionAt(x, y int) (renderedRegion, bool) {
	if m == nil {
		return renderedRegion{}, false
	}
	for _, region := range m.renderedSelection.regions {
		if region.w <= 0 || region.h <= 0 {
			continue
		}
		if x >= region.x && x < region.x+region.w && y >= region.y && y < region.y+region.h {
			return region, true
		}
	}
	return renderedRegion{}, false
}

// handleRenderedSelectionMouse captures a drag over registered passive rows.
// It returns true when the gesture belongs to the selection.
func (m *Model) handleRenderedSelectionMouse(msg bubbletea.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || !m.fullscreenTranscript || m.rawOutput ||
		msg.Button != bubbletea.MouseButtonLeft && msg.Action != bubbletea.MouseActionMotion {
		return false, nil
	}
	state := &m.renderedSelection
	if msg.Button == bubbletea.MouseButtonWheelUp || msg.Button == bubbletea.MouseButtonWheelDown {
		if state.active {
			state.clear()
			return true, nil
		}
		return false, nil
	}
	switch msg.Action {
	case bubbletea.MouseActionPress:
		region, ok := m.renderedSelectionRegionAt(msg.X, msg.Y)
		if !ok {
			if state.active {
				state.clear()
				return true, nil
			}
			return false, nil
		}
		state.active = true
		state.region = region.kind
		state.anchorX, state.anchorY = msg.X, msg.Y
		state.endX, state.endY = msg.X, msg.Y
		state.snapshot = append([]string(nil), region.text...)
		state.snapshotHash = region.hash
		return true, nil
	case bubbletea.MouseActionMotion:
		if !state.active {
			return false, nil
		}
		region := state.regions[state.region]
		state.endX = clampModalInt(msg.X, region.x, region.x+max(region.w-1, 0))
		state.endY = clampModalInt(msg.Y, region.y, region.y+max(region.h-1, 0))
		return true, nil
	case bubbletea.MouseActionRelease:
		if !state.active {
			return false, nil
		}
		region := state.regions[state.region]
		state.endX = clampModalInt(msg.X, region.x, region.x+max(region.w-1, 0))
		state.endY = clampModalInt(msg.Y, region.y, region.y+max(region.h-1, 0))
		if state.anchorX == state.endX && state.anchorY == state.endY {
			state.clear()
			return true, nil
		}
		if m.copyOnSelectEnabled() {
			cmd := m.copyRenderedSelection()
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

func (m *Model) copyOnSelectEnabled() bool {
	if m == nil {
		return false
	}
	return normalizeCopyOnSelectMode(m.copyOnSelectMode) != "never"
}

func (m *Model) copyRenderedSelection() bubbletea.Cmd {
	text := m.renderedSelectionText()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return m.clipboardWriteCmd("footer selection", text, false, false)
}

func (m *Model) renderedSelectionText() string {
	if m == nil || !m.renderedSelection.active {
		return ""
	}
	state := &m.renderedSelection
	region := state.regions[state.region]
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

func sliceDisplayCells(line string, from, to int) string {
	if from < 0 {
		from = 0
	}
	if to <= from {
		return ""
	}
	var out strings.Builder
	column := 0
	for i := 0; i < len(line); {
		r, size := utf8.DecodeRuneInString(line[i:])
		width := max(runewidth.RuneWidth(r), 1)
		next := column + width
		if next > from && column < to {
			out.WriteString(line[i : i+size])
		}
		column = next
		i += size
		if column >= to {
			break
		}
	}
	return out.String()
}

// applyRenderedSelection restyles selected status/footer cells with reverse
// video before the frame is joined.
func (m *Model) applyRenderedSelection(sections []string) []string {
	if m == nil || !m.renderedSelection.active {
		return sections
	}
	state := &m.renderedSelection
	region := state.regions[state.region]
	index := region.sectionIndex
	if index < 0 || index >= len(sections) {
		return sections
	}
	if region.hash != state.snapshotHash {
		state.clear()
		return sections
	}
	lines := strings.Split(sections[index], "\n")
	startY := region.sectionRow + state.anchorY - region.y
	endY := region.sectionRow + state.endY - region.y
	startX, endX := state.anchorX-region.x, state.endX-region.x
	if startY > endY || startY == endY && startX > endX {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}
	for row := max(startY, 0); row <= min(endY, len(lines)-1); row++ {
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
	updated[index] = strings.Join(lines, "\n")
	return updated
}

func highlightDisplayCells(line string, from, to int) string {
	if to <= from {
		return line
	}
	var out strings.Builder
	column := 0
	highlighted := false
	for i := 0; i < len(line); {
		if line[i] == 0x1b {
			end := sparkleSequenceEnd(line, i)
			out.WriteString(line[i:end])
			i = end
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		width := max(runewidth.RuneWidth(r), 1)
		selected := column+width > from && column < to
		if selected != highlighted {
			if selected {
				out.WriteString("\x1b[7m")
			} else {
				out.WriteString("\x1b[27m")
			}
			highlighted = selected
		}
		out.WriteString(line[i : i+size])
		column += width
		i += size
	}
	if highlighted {
		out.WriteString("\x1b[27m")
	}
	return out.String()
}
