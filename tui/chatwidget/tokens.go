package chatwidget

type TokenUsageSnapshot struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

func (s TokenUsageSnapshot) Total() int64 {
	if s.TotalTokens > 0 {
		return s.TotalTokens
	}
	return s.InputTokens + s.OutputTokens
}

func (s TokenUsageSnapshot) CompactTotal() string {
	return FormatTokensCompact(s.Total())
}
