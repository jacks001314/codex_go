package tui

func ASCIIAnimationFrame(frames []string, tick int) string {
	if len(frames) == 0 {
		return ""
	}
	if tick < 0 {
		tick = -tick
	}
	return frames[tick%len(frames)]
}
