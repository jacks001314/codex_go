package historycell

import (
	"fmt"
	"strings"
	"time"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/separators.rs.

type RuntimeMetricCountDuration struct {
	Count      int64
	DurationMS int64
}

type RuntimeMetricsSummary struct {
	ToolCalls                       RuntimeMetricCountDuration
	APICalls                        RuntimeMetricCountDuration
	WebSocketCalls                  RuntimeMetricCountDuration
	StreamingEvents                 RuntimeMetricCountDuration
	WebSocketEvents                 RuntimeMetricCountDuration
	ResponsesAPIOverheadMS          int64
	ResponsesAPIInferenceTimeMS     int64
	ResponsesAPIEngineIAPITTFTMS    int64
	ResponsesAPIEngineServiceTTFTMS int64
	ResponsesAPIEngineIAPITBTMS     int64
	ResponsesAPIEngineServiceTBTMS  int64
}

type FinalMessageSeparator struct {
	ElapsedSeconds *int64
	RuntimeMetrics *RuntimeMetricsSummary
	// CompletedAt is the local completion time, when the turn reported one. Rust
	// #43558 shows "done <time>" after the final answer; times use a twelve-hour
	// clock, other local days add the date, and other years add the year.
	CompletedAt *time.Time
	// DisplayDate fixes "today" at construction so crossing midnight cannot
	// invalidate cached heights.
	DisplayDate time.Time
}

func NewFinalMessageSeparator(elapsedSeconds *int64, runtimeMetrics *RuntimeMetricsSummary) FinalMessageSeparator {
	return FinalMessageSeparator{
		ElapsedSeconds: cloneInt64PtrHistory(elapsedSeconds),
		RuntimeMetrics: cloneRuntimeMetricsSummary(runtimeMetrics),
		DisplayDate:    separatorDate(time.Now()),
	}
}

// WithCompletedAt attaches the turn's local completion time (Rust #43558).
func (c FinalMessageSeparator) WithCompletedAt(completedAt time.Time) FinalMessageSeparator {
	c.CompletedAt = &completedAt
	return c
}

// WithRuntimeMetrics attaches runtime metrics to the completion metadata.
func (c FinalMessageSeparator) WithRuntimeMetrics(runtimeMetrics *RuntimeMetricsSummary) FinalMessageSeparator {
	c.RuntimeMetrics = cloneRuntimeMetricsSummary(runtimeMetrics)
	return c
}

// Label returns the joined completion metadata, or "" when there is none. The
// separator occupies no transcript rows in that case.
func (c FinalMessageSeparator) Label() string {
	parts := c.labelParts()
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " \u00b7 ")
}

func (c FinalMessageSeparator) DisplayLines(width int) []string {
	if width <= 0 {
		return nil
	}
	label := c.Label()
	if label == "" {
		return nil
	}
	indent := ""
	if width > 2 {
		indent = "  "
	}
	return wrapSeparatorLabel(label, width, indent)
}

func (c FinalMessageSeparator) RawLines() []string {
	if label := c.Label(); label != "" {
		return []string{label}
	}
	return nil
}

func (c FinalMessageSeparator) labelParts() []string {
	parts := []string{}
	if c.ElapsedSeconds != nil && *c.ElapsedSeconds > 60 {
		parts = append(parts, "Worked for "+formatElapsedFull(*c.ElapsedSeconds))
	}
	if c.CompletedAt != nil {
		parts = append(parts, "done "+formatCompletionTime(*c.CompletedAt, c.displayDate()))
	}
	if c.RuntimeMetrics != nil {
		if label := RuntimeMetricsLabel(*c.RuntimeMetrics); label != "" {
			parts = append(parts, label)
		}
	}
	return parts
}

func (c FinalMessageSeparator) displayDate() time.Time {
	if c.DisplayDate.IsZero() {
		return separatorDate(time.Now())
	}
	return c.DisplayDate
}

func separatorDate(value time.Time) time.Time {
	local := value.Local()
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
}

// formatCompletionTime mirrors Rust's timestamp formatting: twelve-hour clock,
// with the date for other local days and the year for other years.
func formatCompletionTime(completedAt time.Time, today time.Time) string {
	local := completedAt.Local()
	switch {
	case sameSeparatorDate(local, today):
		return local.Format("3:04 PM")
	case local.Year() == today.Year():
		return local.Format("Jan 2 at 3:04 PM")
	default:
		return local.Format("Jan 2, 2006 at 3:04 PM")
	}
}

func sameSeparatorDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// formatElapsedFull mirrors Rust's elapsed formatting: every nonzero unit down
// to seconds ("1h 5m 3s", "5m 3s", "45s"). Callers only show it above 60s.
func formatElapsedFull(seconds int64) string {
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	switch {
	case hours > 0:
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, secs)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// wrapSeparatorLabel word-wraps the label to width, applying the indent to
// every line (Rust uses textwrap with initial/subsequent indents).
func wrapSeparatorLabel(label string, width int, indent string) []string {
	indentWidth := len([]rune(indent))
	contentWidth := width - indentWidth
	if contentWidth < 1 {
		indent = ""
		indentWidth = 0
		contentWidth = width
	}
	words := strings.Fields(label)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, 1)
	current := ""
	currentWidth := 0
	for _, word := range words {
		wordWidth := len([]rune(word))
		if current == "" {
			current = word
			currentWidth = wordWidth
			continue
		}
		if currentWidth+1+wordWidth <= contentWidth {
			current += " " + word
			currentWidth += 1 + wordWidth
			continue
		}
		lines = append(lines, indent+current)
		current = word
		currentWidth = wordWidth
	}
	lines = append(lines, indent+current)
	// An individual word wider than the content width still occupies its own
	// line; Rust's textwrap behaves the same way for unbreakable words.
	return lines
}

func RuntimeMetricsLabel(summary RuntimeMetricsSummary) string {
	parts := []string{}
	if summary.ToolCalls.Count > 0 {
		parts = append(parts, fmt.Sprintf("Local tools: %s %s (%s)", tui.FormatInt(summary.ToolCalls.Count), pluralizeHistory(summary.ToolCalls.Count, "call", "calls"), formatDurationMS(summary.ToolCalls.DurationMS)))
	}
	if summary.APICalls.Count > 0 {
		parts = append(parts, fmt.Sprintf("Inference: %s %s (%s)", tui.FormatInt(summary.APICalls.Count), pluralizeHistory(summary.APICalls.Count, "call", "calls"), formatDurationMS(summary.APICalls.DurationMS)))
	}
	if summary.WebSocketCalls.Count > 0 {
		parts = append(parts, fmt.Sprintf("WebSocket: %s events send (%s)", tui.FormatInt(summary.WebSocketCalls.Count), formatDurationMS(summary.WebSocketCalls.DurationMS)))
	}
	if summary.StreamingEvents.Count > 0 {
		streamLabel := pluralizeHistory(summary.StreamingEvents.Count, "Stream", "Streams")
		eventLabel := pluralizeHistory(summary.StreamingEvents.Count, "event", "events")
		parts = append(parts, fmt.Sprintf("%s: %s %s (%s)", streamLabel, tui.FormatInt(summary.StreamingEvents.Count), eventLabel, formatDurationMS(summary.StreamingEvents.DurationMS)))
	}
	if summary.WebSocketEvents.Count > 0 {
		parts = append(parts, fmt.Sprintf("%s events received (%s)", tui.FormatInt(summary.WebSocketEvents.Count), formatDurationMS(summary.WebSocketEvents.DurationMS)))
	}
	if summary.ResponsesAPIOverheadMS > 0 {
		parts = append(parts, "Responses API overhead: "+formatDurationMS(summary.ResponsesAPIOverheadMS))
	}
	if summary.ResponsesAPIInferenceTimeMS > 0 {
		parts = append(parts, "Responses API inference: "+formatDurationMS(summary.ResponsesAPIInferenceTimeMS))
	}
	ttftParts := []string{}
	if summary.ResponsesAPIEngineIAPITTFTMS > 0 {
		ttftParts = append(ttftParts, formatDurationMS(summary.ResponsesAPIEngineIAPITTFTMS)+" (iapi)")
	}
	if summary.ResponsesAPIEngineServiceTTFTMS > 0 {
		ttftParts = append(ttftParts, formatDurationMS(summary.ResponsesAPIEngineServiceTTFTMS)+" (service)")
	}
	if len(ttftParts) > 0 {
		parts = append(parts, "TTFT: "+strings.Join(ttftParts, " "))
	}
	tbtParts := []string{}
	if summary.ResponsesAPIEngineIAPITBTMS > 0 {
		tbtParts = append(tbtParts, formatDurationMS(summary.ResponsesAPIEngineIAPITBTMS)+" (iapi)")
	}
	if summary.ResponsesAPIEngineServiceTBTMS > 0 {
		tbtParts = append(tbtParts, formatDurationMS(summary.ResponsesAPIEngineServiceTBTMS)+" (service)")
	}
	if len(tbtParts) > 0 {
		parts = append(parts, "TBT: "+strings.Join(tbtParts, " "))
	}
	return strings.Join(parts, " \u2022 ")
}

func formatDurationMS(durationMS int64) string {
	if durationMS >= 1000 {
		return fmt.Sprintf("%.1fs", float64(durationMS)/1000.0)
	}
	return tui.FormatInt(durationMS) + "ms"
}

func formatElapsedCompact(seconds int64) string {
	if seconds < 60 {
		return tui.FormatInt(seconds) + "s"
	}
	minutes := seconds / 60
	remaining := seconds % 60
	if minutes < 60 {
		if remaining == 0 {
			return tui.FormatInt(minutes) + "m"
		}
		return fmt.Sprintf("%sm %ss", tui.FormatInt(minutes), tui.FormatInt(remaining))
	}
	hours := minutes / 60
	minutes = minutes % 60
	if minutes == 0 {
		return tui.FormatInt(hours) + "h"
	}
	return fmt.Sprintf("%sh %sm", tui.FormatInt(hours), tui.FormatInt(minutes))
}

func pluralizeHistory(count int64, singular string, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func cloneInt64PtrHistory(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneRuntimeMetricsSummary(value *RuntimeMetricsSummary) *RuntimeMetricsSummary {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
