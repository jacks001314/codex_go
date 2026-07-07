package markdown

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
)

const defaultWidth = 80

func Render(text string, width int) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if width <= 0 {
		width = defaultWidth
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(styles.NoTTYStyle),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	out, err := renderer.Render(text)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}
