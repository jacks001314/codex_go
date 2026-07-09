package tui

type KeyHint struct {
	Key   string
	Label string
}

func KeyHintText(h KeyHint) string {
	if h.Key == "" {
		return h.Label
	}
	return h.Key + " " + h.Label
}
