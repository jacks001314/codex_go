package tui

import (
	"io"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var selectedRowStyle = forcedColorRenderer().NewStyle().Foreground(lipgloss.Color("12")).Bold(true)

const SelectedRowMarker = "\u203a"

func forcedColorRenderer() *lipgloss.Renderer {
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.ANSI)
	return renderer
}

func RenderSelectedRow(line string) string {
	return selectedRowStyle.Render(line)
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
