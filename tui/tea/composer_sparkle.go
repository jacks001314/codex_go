package tea

import (
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	codextui "codex_go/tui"
)

// Rust parity: bottom_pane/chat_composer/sparkle.rs and sparkle_field.rs.
// A fresh Astra task gets one short, deterministic starfield in the empty
// composer. The field is subordinate to both tui.animations and
// tui.effects.starfield and never outlives its 15-second deadline.

type composerSparklePhase int

const (
	sparkleUnarmed composerSparklePhase = iota
	sparkleWaiting
	sparkleVisible
	sparkleFinished
)

const (
	sparkleFrameInterval = 150 * time.Millisecond
	sparkleIdleTimeout   = 15 * time.Second
	sparkleFadeDuration  = time.Second
)

var sparkleDots = []rune{'⠁', '⠂', '⠄', '⠈', '⠐', '⠠', '⡀', '⢀'}

type composerSparkleState struct {
	phase        composerSparklePhase
	fresh        bool
	draftStopped bool
	visibleSince time.Time
}

// isSparkleModel mirrors BottomPane::is_sparkle_model: any alphanumeric
// token equal to "astra" qualifies, including gpt-6-astra-wm.
func isSparkleModel(model string) bool {
	for _, part := range strings.FieldsFunc(model, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		if strings.EqualFold(part, "astra") {
			return true
		}
	}
	return false
}

func (m *Model) sparkleEffectsEnabled() bool {
	return m != nil && m.animationsEnabled && m.effects.Starfield
}

// markFreshAstraSparkle starts the opportunity for a newly created task.
func (m *Model) markFreshAstraSparkle() {
	if m == nil {
		return
	}
	m.sparkle = composerSparkleState{fresh: true}
	m.armAstraSparkle()
}

func (m *Model) armAstraSparkle() {
	if m == nil || !m.sparkle.fresh || m.sparkle.draftStopped {
		return
	}
	if m.sparkle.phase != sparkleUnarmed {
		return
	}
	if !m.sparkleEffectsEnabled() || !isSparkleModel(m.State.Model) || !m.composerIsEmpty() {
		return
	}
	m.sparkle.phase = sparkleWaiting
	m.sparkle.visibleSince = time.Time{}
}

// observeSparkleModel arms the flourish when a picker changes the model to an
// Astra model on a fresh task.
func (m *Model) observeSparkleModel() {
	if m == nil || m.State == nil {
		return
	}
	model := strings.TrimSpace(m.State.Model)
	m.sparkleObservedModel = model
	// A bootstrap thread can replace or reconfigure the model after the first
	// state reset. Re-establish the fresh opportunity whenever this thread has
	// no conversation turns yet; markFresh resets the phase only for a new task.
	if !m.sparkle.fresh && !m.hasConversationMessages() {
		m.sparkle.fresh = true
	}
	if m.sparkle.fresh {
		m.armAstraSparkle()
	}
}

// hasConversationMessages reports whether this thread has any user/assistant
// turns. Startup headers, tips and warnings do not make a thread non-fresh.
func (m *Model) hasConversationMessages() bool {
	if m == nil || m.State == nil {
		return false
	}
	for _, message := range m.State.Messages {
		if message.Role == codextui.RoleUser || message.Role == codextui.RoleAssistant {
			return true
		}
	}
	return false
}

func (m *Model) sparkleActive() bool {
	return m != nil && (m.sparkle.phase == sparkleWaiting || m.sparkle.phase == sparkleVisible)
}

func (m *Model) dismissAstraSparkle() {
	if m == nil {
		return
	}
	m.sparkle.draftStopped = true
	m.stopAstraSparkle()
}

func (m *Model) stopAstraSparkle() {
	if m != nil && m.sparkleActive() {
		m.sparkle.phase = sparkleFinished
	}
}

// prepareComposerSparkleKey mirrors Rust's before_sparkle_key: ordinary draft
// input ends the opportunity, while a leading slash command may still select
// Astra and navigation keys leave the flourish alone.
func (m *Model) prepareComposerSparkleKey(key bubbletea.KeyMsg) {
	if m == nil || !m.sparkleActive() {
		return
	}
	if key.Paste || key.Type == bubbletea.KeyRunes {
		text := string(key.Runes)
		canonical := m.composerText()
		if m.composerIsEmpty() && (text == "/" || text == "!") {
			return
		}
		if strings.HasPrefix(canonical, "/") || strings.HasPrefix(canonical, "!") {
			return
		}
		m.dismissAstraSparkle()
	}
}

// sparkleVisibility resolves the waiting/visible phases and returns the fade
// multiplier for the current frame.
func (m *Model) sparkleVisibility(now time.Time) float64 {
	if m == nil || !m.sparkleEffectsEnabled() {
		return 0
	}
	switch m.sparkle.phase {
	case sparkleWaiting:
		if !m.composerIsEmpty() {
			value := m.composerText()
			if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "!") {
				return 0
			}
			m.sparkle.phase = sparkleFinished
			return 0
		}
		m.sparkle.phase = sparkleVisible
		m.sparkle.visibleSince = now
	case sparkleVisible:
		if !m.composerIsEmpty() {
			value := m.composerText()
			if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "!") {
				return 0
			}
			m.sparkle.phase = sparkleFinished
			return 0
		}
	default:
		return 0
	}
	elapsed := now.Sub(m.sparkle.visibleSince)
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed >= sparkleIdleTimeout {
		m.sparkle.phase = sparkleFinished
		return 0
	}
	fadeStart := sparkleIdleTimeout - sparkleFadeDuration
	if elapsed <= fadeStart {
		return 1
	}
	return float64(sparkleIdleTimeout-elapsed) / float64(sparkleFadeDuration)
}

// applyComposerSparkle paints empty composer cells. The composer view is the
// styled textarea output, so the scanner copies SGR/OSC sequences verbatim and
// only replaces visible space cells with dim braille dots.
func (m *Model) applyComposerSparkle(content string, now time.Time) string {
	visibility := m.sparkleVisibility(now)
	if visibility <= 0 {
		return content
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	cursorRow, cursorColumn := m.composerCursorCell()
	placeholderWidth := 0
	if m.composerIsEmpty() {
		placeholderWidth = codextui.DisplayWidth(strings.TrimSpace(m.composer.Placeholder))
	}
	elapsed := now.Sub(m.sparkle.visibleSince).Seconds()
	for row := range lines {
		protected := map[int]bool{cursorColumn: row == cursorRow}
		if placeholderWidth > 0 {
			for column := 0; column <= placeholderWidth; column++ {
				protected[column] = true
			}
		}
		lines[row] = sparkleRenderLine(lines[row], row, elapsed, visibility, protected)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) composerCursorCell() (int, int) {
	row := m.composer.Line()
	info := m.composer.LineInfo()
	return row, info.CharOffset
}

func sparkleRenderLine(line string, row int, elapsed float64, visibility float64, protected map[int]bool) string {
	var out strings.Builder
	out.Grow(len(line))
	column := 0
	for i := 0; i < len(line); {
		if line[i] == 0x1b {
			end := sparkleSequenceEnd(line, i)
			out.WriteString(line[i:end])
			i = end
			continue
		}
		if line[i] == ' ' {
			if !protected[column] {
				if dot, ok := sparkleDotAt(row, column, elapsed, visibility); ok {
					out.WriteString("\x1b[2m")
					out.WriteRune(dot)
					out.WriteString("\x1b[22m")
					column++
					i++
					continue
				}
			}
			out.WriteByte(' ')
			column++
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		out.WriteString(line[i : i+size])
		column += max(runewidth.RuneWidth(r), 1)
		i += size
	}
	return out.String()
}

// sparkleSequenceEnd returns the end of one ANSI escape sequence. Composer
// output before hyperlink annotation contains SGR, plus OSC sequences emitted
// by the editor for bracketed text; both must be copied intact.
func sparkleSequenceEnd(line string, start int) int {
	i := start + 1
	if i >= len(line) {
		return len(line)
	}
	switch line[i] {
	case '[':
		i++
		for i < len(line) && (line[i] < 0x40 || line[i] > 0x7e) {
			i++
		}
		if i < len(line) {
			i++
		}
	case ']':
		i++
		for i < len(line) {
			if line[i] == 0x07 {
				return i + 1
			}
			if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '\\' {
				return i + 2
			}
			i++
		}
	default:
		i++
	}
	return i
}

func sparkleDotAt(row int, column int, elapsed float64, visibility float64) (rune, bool) {
	hash := uint64(row)*65537 + uint64(column)
	hash = (hash ^ (hash >> 16)) * 0x45d9f3b
	hash = (hash ^ (hash >> 16)) * 0x45d9f3b
	hash ^= hash >> 16
	if hash%5 != 0 {
		return 0, false
	}
	phase := math.Mod(elapsed/(4+float64(hash%31)/10)+float64(hash%997)/997, 1)
	brightness := math.Pow(math.Sin(phase*math.Pi), 12) * 0.55 * visibility
	if brightness < 0.04 {
		return 0, false
	}
	return sparkleDots[(hash/161)%uint64(len(sparkleDots))], true
}
