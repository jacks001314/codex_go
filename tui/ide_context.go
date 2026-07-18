package tui

type IDEContextSummary struct {
	Connected bool
	Selection string
	OpenTabs  []string
}

func (s IDEContextSummary) HasPromptContext() bool {
	return s.Selection != "" || len(s.OpenTabs) > 0
}
