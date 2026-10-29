package tea

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// composerSurfaceSGR resolves the composer block fill from
// `style::user_message_style()`. The editable composer is the user-message
// shade blended toward the terminal's reported default background; when the
// terminal does not answer the default-color probe, Rust's
// `user_message_style_for(None)` leaves the block unstyled and so do we.
func composerSurfaceSGR() (string, bool) {
	terminalBG, ok := codextui.DefaultBackgroundRGB()
	if !ok {
		return "", false
	}
	level := codextui.DetectStdoutColorLevel()
	fill := codextui.UserMessageBackground(terminalBG, level)
	sgr := codextui.BackgroundSGR(fill, level)
	if sgr == "" {
		return "", false
	}
	return sgr, true
}

// paintComposerSurface fills every cell of the composer block with the shade,
// mirroring Rust's `Block::default().style(style).render(composer_rect, buf)`:
// ratatui paints the block rectangle first and then draws the composer's spans
// over it, so a span's own SGR reset must not punch a hole in the fill.
// Styling a whole string with a lipgloss background cannot express that - the
// prompt and placeholder emit resets mid-line - so the fill is re-applied after
// every reset and the line padded while it is still active.
func paintComposerSurface(content string, bgSGR string, width int) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = fillLineBackground(line, bgSGR, width)
	}
	return strings.Join(lines, "\n")
}

func fillLineBackground(line string, bgSGR string, width int) string {
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+bgSGR)
	line = strings.ReplaceAll(line, "\x1b[m", "\x1b[0m"+bgSGR)
	line = strings.ReplaceAll(line, "\x1b[49m", bgSGR)
	if pad := width - ansi.StringWidth(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return bgSGR + line + "\x1b[0m"
}
