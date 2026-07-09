package tui

type MarkdownStreamState struct {
	Buffer string
}

func (s *MarkdownStreamState) Push(delta string) {
	if s != nil {
		s.Buffer += delta
	}
}
