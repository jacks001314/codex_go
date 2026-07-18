package historycell

import (
	"fmt"
	"strings"

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
}

func NewFinalMessageSeparator(elapsedSeconds *int64, runtimeMetrics *RuntimeMetricsSummary) FinalMessageSeparator {
	return FinalMessageSeparator{
		ElapsedSeconds: cloneInt64PtrHistory(elapsedSeconds),
		RuntimeMetrics: cloneRuntimeMetricsSummary(runtimeMetrics),
	}
}

func (c FinalMessageSeparator) DisplayLines(width int) []string {
	width = max(width, 1)
	parts := c.labelParts()
	if len(parts) == 0 {
		return []string{strings.Repeat("\u2500", width)}
	}
	label := "\u2500 " + strings.Join(parts, " \u2022 ") + " \u2500"
	runes := []rune(label)
	if len(runes) >= width {
		return []string{string(runes[:width])}
	}
	return []string{label + strings.Repeat("\u2500", width-len(runes))}
}

func (c FinalMessageSeparator) RawLines() []string {
	parts := c.labelParts()
	if len(parts) == 0 {
		return nil
	}
	return []string{strings.Join(parts, " \u2022 ")}
}

func (c FinalMessageSeparator) labelParts() []string {
	parts := []string{}
	if c.ElapsedSeconds != nil && *c.ElapsedSeconds > 60 {
		parts = append(parts, "Worked for "+formatElapsedCompact(*c.ElapsedSeconds))
	}
	if c.RuntimeMetrics != nil {
		if label := RuntimeMetricsLabel(*c.RuntimeMetrics); label != "" {
			parts = append(parts, label)
		}
	}
	return parts
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
