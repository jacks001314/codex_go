package historycell

import "codex_go/context"

// NewDelegatedPromptCell mirrors the Rust replay/transcript cell for a delegated
// tool output (#41046): a dim `• ` prefix followed by the source task
// attribution and the delegated prompt, with two-space continuation lines.
func NewDelegatedPromptCell(sourceThreadID string, prompt string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell(context.DelegatedToolOutputNotice(sourceThreadID, prompt), "• ", "  ")
}
