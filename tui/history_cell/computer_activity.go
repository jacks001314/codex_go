package historycell

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/computer_activity.rs (#43576).
//
// Compact adjacent CUA calls without discarding their chronological transcript
// details. Only CUA calls enter this cell; other history items and turn
// boundaries end the group. Preview selection favors failures and images, but
// never changes the order of retained rows.

// ComputerActivityServer is the MCP server whose calls form computer activity
// (Rust McpInvocation::is_computer_activity).
const ComputerActivityServer = "cua_repl"

// IsComputerActivityServer reports whether an MCP server contributes computer
// activity.
func IsComputerActivityServer(server string) bool {
	return strings.TrimSpace(server) == ComputerActivityServer
}

// ComputerActivityCell groups adjacent computer calls into one history cell.
type ComputerActivityCell struct {
	Calls []McpToolCallCell
}

// Start appends a call once, keeping replay deduplication.
func (c *ComputerActivityCell) Start(call McpToolCallCell) {
	if c == nil {
		return
	}
	for index := range c.Calls {
		if c.Calls[index].CallID == call.CallID {
			return
		}
	}
	c.Calls = append(c.Calls, call)
}

// Complete records a call's result, adding it first when completion arrives
// without a start (Rust ComputerActivityCell::complete).
func (c *ComputerActivityCell) Complete(call McpToolCallCell, result McpToolResult) {
	if c == nil {
		return
	}
	c.Start(call)
	for index := range c.Calls {
		if c.Calls[index].CallID == call.CallID {
			c.Calls[index].Complete(result)
			return
		}
	}
}

// IsActive reports whether any grouped call is still running.
func (c ComputerActivityCell) IsActive() bool {
	for index := range c.Calls {
		if c.Calls[index].Result == nil {
			return true
		}
	}
	return false
}

// MarkFailed fails every still-running call (turn interruption/failure).
func (c *ComputerActivityCell) MarkFailed(message string) {
	if c == nil {
		return
	}
	for index := range c.Calls {
		if c.Calls[index].Result == nil {
			c.Calls[index].MarkFailed(message)
		}
	}
}

// Count returns the number of grouped calls.
func (c ComputerActivityCell) Count() int { return len(c.Calls) }

// DisplayLines renders the compact grouped view, mirroring Rust's
// display_lines.
func (c ComputerActivityCell) DisplayLines(width int) []string {
	if width == 0 || len(c.Calls) == 0 {
		return nil
	}
	active := -1
	failures := 0
	for index := range c.Calls {
		if c.Calls[index].Result == nil {
			active = index
			continue
		}
		if c.Calls[index].Result.IsError {
			failures++
		}
	}
	count := len(c.Calls)
	label := "Used computer"
	if active >= 0 {
		label = "Using computer"
	}
	unit := "actions"
	if count == 1 {
		unit = "action"
	}
	header := "\u2022 " + label + " \u00b7 " + strconv.Itoa(count) + " " + unit
	if failures > 0 {
		header += " \u00b7 " + strconv.Itoa(failures) + " failed"
	}
	lines := tui.AdaptiveWrapLine(header, tui.WrapOptions{
		Width:            max(width, 1),
		SubsequentIndent: "  ",
		BreakWords:       true,
	})

	selected := computerActivitySelectedRows(c, active)
	hidden := count - len(selected)
	for _, index := range selected {
		call := c.Calls[index]
		failed := call.Result != nil && call.Result.IsError
		summary := computerActivityRowSummary(call, width, failed)
		lines = append(lines, "  \u2502 "+previewComputerActivityText(summary, max(width-4, 0)))
	}
	if hidden > 0 && active < 0 {
		lines = append(lines, "  \u2502 "+previewComputerActivityText(
			strconv.Itoa(hidden)+" more \u00b7 ctrl+t", max(width-4, 0)))
	}
	return lines
}

// computerActivitySelectedRows mirrors Rust's preview selection: the active
// call alone while running, otherwise up to three rows chosen by failure,
// image presence, and recency, returned in chronological order.
func computerActivitySelectedRows(cell ComputerActivityCell, active int) []int {
	count := len(cell.Calls)
	if active >= 0 {
		return []int{active}
	}
	indices := make([]int, 0, count)
	for index := 0; index < count; index++ {
		indices = append(indices, index)
	}
	sort.SliceStable(indices, func(left int, right int) bool {
		leftCall := cell.Calls[indices[left]]
		rightCall := cell.Calls[indices[right]]
		leftKey := computerActivitySelectionKey(leftCall)
		rightKey := computerActivitySelectionKey(rightCall)
		if leftKey != rightKey {
			return leftKey > rightKey
		}
		return indices[left] > indices[right]
	})
	limit := 3
	if count > 3 {
		limit = 2
	}
	if len(indices) > limit {
		indices = indices[:limit]
	}
	sort.Ints(indices)
	return indices
}

// computerActivitySelectionKey packs the failure/image priority; larger values
// sort first.
func computerActivitySelectionKey(call McpToolCallCell) int {
	key := 0
	if call.Result != nil && call.Result.IsError {
		key += 2
	}
	if call.Result != nil && call.Result.HasImage {
		key++
	}
	return key
}

func computerActivityRowSummary(call McpToolCallCell, width int, failed bool) string {
	title := computerActivityTitle(call)
	if failed {
		errorText := computerActivityErrorPreview(call)
		switch {
		case errorText == "":
			return "Failed: " + title
		case width < 60:
			return "Failed: " + errorText
		default:
			return "Failed: " + previewComputerActivityText(title, width/3) + " \u2014 " + errorText
		}
	}
	if call.Result != nil && call.Result.HasImage {
		return "Captured screenshot \u00b7 " + title
	}
	return title
}

// computerActivityTitle mirrors Rust's argument-title extraction with the
// "Computer action" fallback.
func computerActivityTitle(call McpToolCallCell) string {
	arguments := strings.TrimSpace(call.Invocation.Arguments)
	if arguments != "" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(arguments), &parsed); err == nil {
			if title, ok := parsed["title"].(string); ok && strings.TrimSpace(title) != "" {
				return strings.TrimSpace(title)
			}
		}
	}
	return "Computer action"
}

// computerActivityErrorPreview mirrors Rust's error_preview: the error, else a
// "Script error:" content block, else the first non-empty block, reduced to the
// first non-empty line without the "Script error:" prefix.
func computerActivityErrorPreview(call McpToolCallCell) string {
	if call.Result == nil {
		return ""
	}
	text := strings.TrimSpace(call.Result.Error)
	if text == "" {
		fallback := ""
		for _, block := range call.Result.Content {
			if strings.HasPrefix(block, "Script error:") {
				fallback = block
				break
			}
			if fallback == "" && strings.TrimSpace(block) != "" {
				fallback = block
			}
		}
		text = fallback
	}
	text = strings.TrimPrefix(text, "Script error:")
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// previewComputerActivityText keeps a preview on one physical row, mirroring
// Rust's preview (whitespace-normalized, ellipsis when clipped).
func previewComputerActivityText(text string, width int) string {
	normalized := strings.Join(strings.Fields(text), " ")
	if width <= 0 {
		if normalized == "" {
			return ""
		}
		return ""
	}
	if tui.DisplayWidth(normalized) <= width {
		return normalized
	}
	clipped := tui.TruncateWithEllipsis(normalized, width)
	return clipped
}

// TranscriptLines renders every grouped call's full transcript detail.
func (c ComputerActivityCell) TranscriptLines(width int) []string {
	lines := []string{}
	for index := range c.Calls {
		lines = append(lines, c.Calls[index].TranscriptLines(width)...)
	}
	return lines
}

// RawLines renders the untruncated transcript used by raw scrollback mode.
func (c ComputerActivityCell) RawLines() []string {
	lines := []string{}
	for index := range c.Calls {
		lines = append(lines, c.Calls[index].RawLines()...)
	}
	return lines
}
