package tui

import "fmt"

// Rust parity: codex-rs/tui/src/token_usage.rs.

const baselineTokens int64 = 12000

type TokenUsage struct {
	InputTokens           int64
	CachedInputTokens     int64
	OutputTokens          int64
	ReasoningOutputTokens int64
	TotalTokens           int64
}

func (u TokenUsage) IsZero() bool {
	return u.TotalTokens == 0
}

func (u TokenUsage) CachedInput() int64 {
	if u.CachedInputTokens < 0 {
		return 0
	}
	return u.CachedInputTokens
}

func (u TokenUsage) NonCachedInput() int64 {
	value := u.InputTokens - u.CachedInput()
	if value < 0 {
		return 0
	}
	return value
}

func (u TokenUsage) BlendedTotal() int64 {
	output := u.OutputTokens
	if output < 0 {
		output = 0
	}
	return u.NonCachedInput() + output
}

func (u TokenUsage) TokensInContextWindow() int64 {
	return u.TotalTokens
}

func (u TokenUsage) PercentOfContextWindowRemaining(contextWindow int64) int64 {
	if contextWindow <= baselineTokens {
		return 0
	}
	effective := contextWindow - baselineTokens
	used := u.TokensInContextWindow() - baselineTokens
	if used < 0 {
		used = 0
	}
	remaining := effective - used
	if remaining < 0 {
		remaining = 0
	}
	percent := float64(remaining) / float64(effective) * 100
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return int64(percent + 0.5)
}

func (u TokenUsage) String() string {
	cached := ""
	if u.CachedInput() > 0 {
		cached = fmt.Sprintf(" (+ %s cached)", FormatInt(u.CachedInput()))
	}
	reasoning := ""
	if u.ReasoningOutputTokens > 0 {
		reasoning = fmt.Sprintf(" (reasoning %s)", FormatInt(u.ReasoningOutputTokens))
	}
	return fmt.Sprintf(
		"Token usage: total=%s input=%s%s output=%s%s",
		FormatInt(u.BlendedTotal()),
		FormatInt(u.NonCachedInput()),
		cached,
		FormatInt(u.OutputTokens),
		reasoning,
	)
}

func FormatInt(value int64) string {
	if value < 0 {
		return "-" + FormatInt(-value)
	}
	text := fmt.Sprintf("%d", value)
	if len(text) <= 3 {
		return text
	}
	first := len(text) % 3
	if first == 0 {
		first = 3
	}
	out := text[:first]
	for i := first; i < len(text); i += 3 {
		out += "," + text[i:i+3]
	}
	return out
}
