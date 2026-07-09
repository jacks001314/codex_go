package render

func TrimLineToWidth(line string, width int) string {
	runes := []rune(line)
	if width <= 0 || len(runes) <= width {
		return line
	}
	return string(runes[:width])
}
