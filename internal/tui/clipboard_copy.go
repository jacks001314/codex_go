package tui

func ClipboardCopySequence(text string, tmux bool) (string, error) {
	return OSC52Sequence(text, tmux)
}
