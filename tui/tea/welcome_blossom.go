package tea

import (
	"fmt"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	welcome "codex_go/tui/welcome"
	"codex_go/utils"
)

// Rust parity: empty_state_animation.rs render_first_screen. Fresh fullscreen
// conversations show a settled, dim blossom in unused space; clicking replays
// one spin before it fades back to the settled pose.

const (
	blossomColumns       = welcome.MaxColumns
	blossomStageRows     = 21
	blossomMinRows       = 14
	blossomSettledPhase  = 0.5
	blossomStaticOpacity = 0.18
	blossomLoopSeconds   = 7.2
	blossomSpinSeconds   = 10.8
	blossomReplaySeconds = blossomSpinSeconds - blossomSettledPhase*blossomLoopSeconds
	blossomFadeSeconds   = 0.4
)

type welcomeBlossomRect struct {
	X int
	Y int
	W int
	H int
}

type welcomeBlossomState struct {
	renderer        *welcome.Renderer
	replayStartedAt time.Time
	rect            welcomeBlossomRect
}

func newWelcomeBlossomState() welcomeBlossomState {
	return welcomeBlossomState{renderer: welcome.NewRenderer()}
}

func (m *Model) welcomeBlossomEligible() bool {
	if m == nil {
		return false
	}
	if !m.fullscreenTranscript || !m.animationsEnabled || !m.effects.Welcome {
		return false
	}
	if !m.composerIsEmpty() || m.hasConversationMessages() {
		return false
	}
	if m.modal != nil || m.overlay != nil || m.warningsView != nil || m.readOnlyThread ||
		m.editorActive || m.windowsSandboxSetupActive || m.vimSearchMode ||
		m.slashPopup.Active || m.skillPopup.Active || m.mentionPopup != nil {
		return false
	}
	return m.activeSide == nil
}

func (m *Model) blossomOpacity(now time.Time) (float64, bool) {
	state := &m.welcomeBlossom
	if state.replayStartedAt.IsZero() {
		return blossomStaticOpacity, false
	}
	elapsed := now.Sub(state.replayStartedAt).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed >= blossomReplaySeconds+blossomFadeSeconds {
		state.replayStartedAt = time.Time{}
		return blossomStaticOpacity, false
	}
	if elapsed <= blossomReplaySeconds {
		return blendBlossomOpacity(blossomStaticOpacity, 1, elapsed/blossomFadeSeconds), true
	}
	return blendBlossomOpacity(1, blossomStaticOpacity, (elapsed-blossomReplaySeconds)/blossomFadeSeconds), true
}

func (m *Model) blossomPhase(now time.Time) float64 {
	state := &m.welcomeBlossom
	if state.replayStartedAt.IsZero() {
		return blossomSettledPhase
	}
	elapsed := now.Sub(state.replayStartedAt).Seconds()
	phase := blossomSettledPhase + elapsed/blossomLoopSeconds
	return phase - float64(int(phase))
}

func blendBlossomOpacity(from, to, progress float64) float64 {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	return from + (to-from)*progress
}

// insertWelcomeBlossom adds a centered stage immediately above the composer.
// The section consumes the unused vertical space so the composer stays at the
// bottom; if the stage cannot fit, nothing is inserted.
func (m *Model) insertWelcomeBlossom(sections []string, composerIndex int) []string {
	if m == nil || composerIndex < 0 || !m.welcomeBlossomEligible() {
		m.welcomeBlossom.rect = welcomeBlossomRect{}
		return sections
	}
	width := min(max(m.width-4, 0), blossomColumns)
	height := width * blossomStageRows / blossomColumns
	if width < 20 || height < blossomMinRows || height > m.height {
		m.welcomeBlossom.rect = welcomeBlossomRect{}
		return sections
	}
	// The transcript viewport pads itself to the available height. Reclaim its
	// trailing blank rows for the centered stage instead of adding height that
	// would push the composer below the screen.
	transcriptIndex := composerIndex - 1
	if transcriptIndex < 0 {
		m.welcomeBlossom.rect = welcomeBlossomRect{}
		return sections
	}
	transcriptLines := strings.Split(sections[transcriptIndex], "\n")
	kept := len(transcriptLines)
	for kept > 0 && strings.TrimSpace(utils.StripANSI(transcriptLines[kept-1])) == "" {
		kept--
	}
	freed := len(transcriptLines) - kept
	if freed < height {
		m.welcomeBlossom.rect = welcomeBlossomRect{}
		return sections
	}
	topPad := (freed - height) / 2
	bottomPad := freed - height - topPad
	now := m.currentTime()
	opacity, _ := m.blossomOpacity(now)
	phase := m.blossomPhase(now)
	frame := m.welcomeBlossom.renderer.Frame(width, height, phase, welcome.DarkLighting())
	leftPad := max((m.width-width)/2, 0)
	lines := make([]string, 0, freed)
	blank := strings.Repeat(" ", max(m.width, 0))
	for i := 0; i < topPad; i++ {
		lines = append(lines, blank)
	}
	for row := 0; row < height; row++ {
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", leftPad))
		lastColor := ""
		for column := 0; column < width; column++ {
			cell := frame[row*width+column]
			if cell.Dots == 0 {
				b.WriteByte(' ')
				continue
			}
			r, g, bl := blendBlossomColor(cell.R, cell.G, cell.B, opacity)
			color := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, bl)
			if color != lastColor {
				b.WriteString(color)
				lastColor = color
			}
			b.WriteRune(rune(0x2800 + int(cell.Dots)))
		}
		if lastColor != "" {
			b.WriteString("\x1b[0m")
		}
		lines = append(lines, b.String())
	}
	for i := 0; i < bottomPad; i++ {
		lines = append(lines, blank)
	}
	trimmed := append([]string(nil), sections...)
	trimmed[transcriptIndex] = strings.Join(transcriptLines[:kept], "\n")
	before := 0
	for _, section := range trimmed[:composerIndex] {
		before += strings.Count(section, "\n") + 1
	}
	m.welcomeBlossom.rect = welcomeBlossomRect{
		X: leftPad,
		Y: before + topPad,
		W: width,
		H: height,
	}
	inserted := append([]string(nil), trimmed[:composerIndex]...)
	inserted = append(inserted, strings.Join(lines, "\n"))
	inserted = append(inserted, trimmed[composerIndex:]...)
	return inserted
}

func blendBlossomColor(r, g, b uint8, opacity float64) (uint8, uint8, uint8) {
	background := [3]float64{16, 16, 16}
	values := [3]uint8{}
	for i, channel := range []uint8{r, g, b} {
		values[i] = uint8(mathRound(background[i] + (float64(channel)-background[i])*opacity))
	}
	return values[0], values[1], values[2]
}

func mathRound(value float64) float64 {
	if value < 0 {
		return 0
	}
	return float64(int(value + 0.5))
}

// handleWelcomeBlossomClick replays the blossom when the stage itself is
// clicked. Unrelated gestures keep their existing owner.
func (m *Model) handleWelcomeBlossomClick(msg bubbletea.MouseMsg) bool {
	if m == nil || msg.Action != bubbletea.MouseActionPress || msg.Button != bubbletea.MouseButtonLeft {
		return false
	}
	rect := m.welcomeBlossom.rect
	if rect.W <= 0 || rect.H <= 0 {
		return false
	}
	if msg.X < rect.X || msg.X >= rect.X+rect.W || msg.Y < rect.Y || msg.Y >= rect.Y+rect.H {
		return false
	}
	if !m.welcomeBlossomEligible() {
		return false
	}
	m.welcomeBlossom.replayStartedAt = m.currentTime()
	return true
}
