package tui

func RenderMarkdownPlain(text string, width int) []string {
	return WrapLines([]string{text}, WrapOptions{Width: width, BreakWords: true})
}
