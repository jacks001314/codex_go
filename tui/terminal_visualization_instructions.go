package tui

import "strings"

// Rust parity: codex-rs/tui/src/terminal_visualization_instructions.rs.

const TerminalVisualizationInstructionsText = `- This surface is a terminal. When the formatting rules require a visual, include one in the final answer using compact ASCII diagrams, trees, timelines, or tables.
- Use tables for exact mappings or comparisons rather than collapsing known mappings into prose.
- Use trees for hierarchy or one-to-many relationships, and diagrams or timelines for sequence, change, or state transferred between records across event order.
- Use only ASCII characters in visuals.`

func TerminalVisualizationInstructions() string {
	return TerminalVisualizationInstructionsText
}

func WithTerminalVisualizationInstructions(enabled bool, developerInstructions string, controlInstructions *string) *string {
	if !enabled {
		if controlInstructions == nil {
			return nil
		}
		return stringPtr(*controlInstructions)
	}
	existing := developerInstructions
	if controlInstructions != nil {
		existing = *controlInstructions
	}
	if strings.TrimSpace(existing) != "" {
		return stringPtr(existing + "\n\n" + TerminalVisualizationInstructionsText)
	}
	return stringPtr(TerminalVisualizationInstructionsText)
}
