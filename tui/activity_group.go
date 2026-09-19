package tui

import "strings"

// Rust parity: codex-rs/tui/src/history_cell/activity_group.rs (#46565).
//
// Activity groups keep ordered calls plus transcript-only reasoning. Tool cells
// own call matching and compact previews; reasoning is attached after the calls
// that already started, so it keeps its position among them as they complete
// without changing the compact display.

// ActivityReasoning is one transcript-only reasoning block attached to an
// activity group, paired with the number of calls that preceded it.
type ActivityReasoning struct {
	// AfterCalls is the number of calls that preceded the block.
	AfterCalls int
	// ItemID identifies the reasoning item. Attaching the same item twice (a
	// replayed or re-delivered completion) is a no-op so the block never renders
	// twice inside one group.
	ItemID string
	// Content is the reasoning summary the expanded transcript renders;
	// RawContent is the raw chain-of-thought variant the transcript uses when
	// show_raw_agent_reasoning is enabled.
	Content    string
	RawContent string
}

// ActivityGroup holds ordered calls and transcript-only reasoning. Reasoning
// renders only in the expanded (rich) transcript: compact previews and raw
// output stay reasoning-free.
type ActivityGroup[T any] struct {
	Calls     []T
	Reasoning []ActivityReasoning
}

// PushReasoning attaches a reasoning block after the calls currently grouped.
// It reports false when the same item is already attached.
func (g *ActivityGroup[T]) PushReasoning(itemID string, content string, rawContent string) bool {
	if g == nil {
		return false
	}
	itemID = strings.TrimSpace(itemID)
	for index := range g.Reasoning {
		if itemID != "" && g.Reasoning[index].ItemID == itemID {
			return false
		}
	}
	g.Reasoning = append(g.Reasoning, ActivityReasoning{
		AfterCalls: len(g.Calls),
		ItemID:     itemID,
		Content:    content,
		RawContent: rawContent,
	})
	return true
}

// TranscriptLines renders the calls in order and, in rich mode, interleaves each
// reasoning block after the calls that preceded it (Rust
// ActivityGroup::transcript_lines). renderCall returns one call's lines;
// renderReasoning returns one block's lines and is only called in rich mode.
func (g *ActivityGroup[T]) TranscriptLines(
	width int,
	rich bool,
	renderCall func(index int, call T) []string,
	renderReasoning func(reasoning ActivityReasoning, width int) []string,
) []string {
	if g == nil {
		return nil
	}
	lines := make([]string, 0, len(g.Calls)*3)
	next := 0
	for index := range g.Calls {
		if renderCall != nil {
			lines = append(lines, renderCall(index, g.Calls[index])...)
		}
		if !rich || renderReasoning == nil {
			continue
		}
		for next < len(g.Reasoning) && g.Reasoning[next].AfterCalls == index+1 {
			block := renderReasoning(g.Reasoning[next], width)
			next++
			if len(block) == 0 {
				continue
			}
			lines = append(lines, "")
			lines = append(lines, block...)
		}
	}
	return lines
}
