package pets

func FrameAt(frames []string, index int) string {
	if len(frames) == 0 {
		return ""
	}
	if index < 0 {
		index = -index
	}
	return frames[index%len(frames)]
}
