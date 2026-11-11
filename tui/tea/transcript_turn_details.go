package tea

import (
	"strings"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Turn details in the full-screen transcript mirror Rust #60554/#60556
// (transcript_view/turn_details.rs): consecutive tool activity of one turn is
// summarized on a single disclosure row, and the detailed cells stay available
// when the row is expanded. Collection, search and the raw view keep the
// original cells; only the collapsed presentation is rewritten here.

// transcriptToolActivity reports whether a message is a tool invocation the
// summary may absorb. Rust summarizes cells that report activity IDs and a
// tool-call label; notices, reasoning blocks and generated summaries stay
// untouched.
func transcriptToolActivity(message codextui.Message) bool {
	if message.Role != codextui.RoleHistory {
		return false
	}
	// Rust #60555: restored tool cells (MCP, dynamic, patch, image, agent)
	// report no activity ids, so the producer marks them as tool activity
	// and the summary label alone identifies the row.
	if len(message.ActivityIDs) == 0 && !message.ToolActivity {
		return false
	}
	if message.TranscriptOnly || message.StartupTip || message.Recap || message.TurnDetailSummary {
		return false
	}
	return strings.TrimSpace(message.Text) != "" || strings.TrimSpace(message.TranscriptText) != ""
}

// transcriptToolActivityLabel returns the output-free invocation label Rust's
// tool_call_labels() reports: the cell's first line, never its output.
func transcriptToolActivityLabel(message codextui.Message) string {
	text := message.Text
	if strings.TrimSpace(text) == "" {
		text = message.TranscriptText
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		label := strings.TrimSpace(utils.StripANSI(line))
		if label == "" {
			continue
		}
		return sanitizeTurnDetailLabel(label)
	}
	return ""
}

// sanitizeTurnDetailLabel keeps a label on one printable line, matching Rust's
// control-character replacement in TurnRow::layout.
func sanitizeTurnDetailLabel(label string) string {
	var builder strings.Builder
	for _, r := range label {
		if r < 0x20 || r == 0x7f {
			builder.WriteByte(' ')
			continue
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String())
}

// turnDetailExplored reports whether a label describes reading or searching
// rather than running a command (Rust's explored counter).
func turnDetailExplored(label string) bool {
	lower := strings.ToLower(label)
	for _, prefix := range []string{"read", "search", "list", "explored", "reading", "searching", "listing"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// TurnDetailSummaryLabel mirrors Rust's ToolGroup::label(): multiple calls
// collapse into counts ("Ran 3 commands", "Explored files · ran 1 command"),
// while a single call keeps its own label.
func TurnDetailSummaryLabel(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	if len(labels) == 1 {
		return labels[0]
	}
	explored, ran := 0, 0
	for _, label := range labels {
		if turnDetailExplored(label) {
			explored++
			continue
		}
		ran++
	}
	if explored+ran == len(labels) {
		noun := "commands"
		if ran == 1 {
			noun = "command"
		}
		switch {
		case ran == 0:
			return "Explored files"
		case explored == 0:
			return "Ran " + itoaTurnDetail(ran) + " " + noun
		default:
			return "Explored files · ran " + itoaTurnDetail(ran) + " " + noun
		}
	}
	last := labels[len(labels)-1]
	return itoaTurnDetail(len(labels)) + " tool calls · " + last
}

func itoaTurnDetail(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

// turnDetailCompletes reports whether the turn owning the activity run finished
// before the next prompt. Rust #60557 only folds completed turns: the live tail
// keeps its activity until the turn settles.
func turnDetailCompletes(messages []codextui.Message, from int) bool {
	for index := from; index < len(messages); index++ {
		message := messages[index]
		switch message.Role {
		case codextui.RoleUser:
			return false
		case codextui.RoleAssistant:
			phase := strings.ToLower(strings.TrimSpace(message.Phase))
			if phase == "" || phase == "final" {
				return true
			}
		}
	}
	return false
}

// CollapseTurnDetails rewrites a conversation for the full-screen transcript so
// each completed turn's consecutive tool activity becomes one disclosure row
// (Rust #60554/#60556/#60557). The returned slice keeps the original message
// positions: absorbed cells become empty placeholders that render no rows, so
// per-message expansion state and highlight ranges stay aligned with the source
// conversation. Expanded groups keep their original cells unchanged.
//
// It returns nil when nothing collapses, so the caller can keep its normal
// rendering path.
func CollapseTurnDetails(messages []codextui.Message, expanded func(index int) bool) []codextui.Message {
	if len(messages) == 0 {
		return nil
	}
	collapsed := make([]codextui.Message, len(messages))
	copy(collapsed, messages)
	changed := false
	for index := 0; index < len(messages); index++ {
		if !transcriptToolActivity(messages[index]) {
			continue
		}
		end := index
		for end+1 < len(messages) && transcriptToolActivity(messages[end+1]) {
			end++
		}
		groupExpanded := false
		if expanded != nil {
			for member := index; member <= end; member++ {
				if expanded(member) {
					groupExpanded = true
					break
				}
			}
		}
		if groupExpanded || !turnDetailCompletes(messages, end+1) {
			index = end
			continue
		}
		labels := make([]string, 0, end-index+1)
		ids := make([]string, 0, end-index+1)
		for member := index; member <= end; member++ {
			if label := transcriptToolActivityLabel(messages[member]); label != "" {
				labels = append(labels, label)
			}
			ids = append(ids, messages[member].ActivityIDs...)
		}
		summary := messages[index]
		summary.Text = "\u25b8 " + TurnDetailSummaryLabel(labels)
		summary.RawText = ""
		summary.TranscriptText = ""
		summary.ActivityIDs = ids
		if len(ids) == 0 {
			// Rust #60555 gives every summary a stable disclosure identity even
			// when the cells it stands for report none (Rust work:{key}).
			summary.ActivityIDs = []string{"summary:" + itoaTurnDetail(index)}
		}
		summary.TurnDetailSummary = true
		collapsed[index] = summary
		for member := index + 1; member <= end; member++ {
			collapsed[member] = codextui.Message{}
		}
		changed = true
		index = end
	}
	if !changed {
		return nil
	}
	return collapsed
}
