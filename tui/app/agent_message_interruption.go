package app

import "strings"

// Rust parity: codex-rs/tui/src/app/agent_message_consolidation.rs (#59700).

// InterruptedItemKey identifies the assistant reply a flushed streaming fragment
// belongs to, as `turnID:itemID`.
func InterruptedItemKey(turnID string, itemID string) string {
	turnID = strings.TrimSpace(turnID)
	itemID = strings.TrimSpace(itemID)
	if turnID == "" || itemID == "" {
		return ""
	}
	return turnID + ":" + itemID
}

// ConsolidateInterruptedAgentMessage replaces a reply's flushed fragments with the
// completed message at the reply's original position. Intervening tool cells keep
// their order, and duplicate fragments for the same item are dropped.
func ConsolidateInterruptedAgentMessage(cells []TranscriptCell, itemKey string, source string, cwd string) AgentMessageConsolidationResult {
	out := append([]TranscriptCell(nil), cells...)
	itemKey = strings.TrimSpace(itemKey)
	if itemKey == "" {
		return AgentMessageConsolidationResult{Cells: out, ConsolidationItem: itemKey}
	}
	first := -1
	kept := make([]TranscriptCell, 0, len(out))
	for _, cell := range out {
		belongs := cell.InterruptedItem == itemKey &&
			(cell.Kind == TranscriptCellAgentMessage || cell.Kind == TranscriptCellAgentMarkdown)
		if !belongs {
			kept = append(kept, cell)
			continue
		}
		if first < 0 {
			first = len(kept)
		}
	}
	if first < 0 {
		return AgentMessageConsolidationResult{Cells: out, ConsolidationItem: itemKey}
	}
	consolidated := TranscriptCell{
		Kind:            TranscriptCellAgentMarkdown,
		Lines:           []string{source},
		Source:          source,
		CWD:             cwd,
		InterruptedItem: itemKey,
	}
	next := make([]TranscriptCell, 0, len(kept)+1)
	next = append(next, kept[:first]...)
	next = append(next, consolidated)
	next = append(next, kept[first:]...)
	return AgentMessageConsolidationResult{
		Cells:             next,
		Consolidated:      true,
		Start:             first,
		End:               first + 1,
		ConsolidationItem: itemKey,
	}
}
