package tui

import (
	"io"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

const SelectedRowMarker = "\u203a"

func forcedColorRenderer() *lipgloss.Renderer {
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.ANSI)
	return renderer
}

func RenderSelectedRow(line string) string {
	// Rust renders the selected row with the contrast-aware selection style
	// (style::selection_style): the shared ChatGPT-blue fill with a readable
	// foreground, or the bold reversed terminal defaults on an unknown palette.
	if sgr := SelectionSGR(DetectStdoutColorLevel()); sgr != "" {
		return sgr + line + "\x1b[0m"
	}
	return line
}

func ForcedColorStyle() lipgloss.Style {
	return forcedColorRenderer().NewStyle()
}

func SelectionPrefix(selected bool) string {
	if selected {
		return SelectedRowMarker + " "
	}
	return "  "
}

func NumberedSelectionPrefix(index int, selected bool) string {
	return SelectionPrefix(selected) + FormatInt(int64(index+1)) + ". "
}
