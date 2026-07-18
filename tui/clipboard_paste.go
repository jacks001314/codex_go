package tui

func ClipboardPastePath(value string) (string, bool) {
	return NormalizePastedPath(value)
}
