package telemetry

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"codex_go/protocol"
)

// Rust parity: codex-rs/otel/src/tool_result.rs (telemetry_preview) and
// codex_protocol::config_types::ToolResultLogConfig. The preview is what a
// `codex.tool_result` log record may carry: the tool output truncated to a byte
// budget with a truncation notice, independent of the model-visible output.

// ToolResultTruncationNotice mirrors Rust's TRUNCATION_NOTICE.
const ToolResultTruncationNotice = "[... telemetry preview truncated ...]"

// ToolResultEventName mirrors the `event.name` every tool-result record carries.
const ToolResultEventName = "codex.tool_result"

// ToolResultPreview mirrors telemetry_preview's result: the text a log record
// carries and whether this telemetry limit removed text (not whether the tool
// result itself was lossless).
type ToolResultPreview struct {
	Text      string
	Truncated bool
}

// TelemetryPreview mirrors codex-otel's telemetry_preview: take the leading
// bytes at a char boundary, and when the budget removed text append the
// truncation notice on its own line.
func TelemetryPreview(content string, limits protocol.ToolResultLogConfig) ToolResultPreview {
	prefix := takeBytesAtCharBoundary(content, limits.MaxBytes)
	if len(prefix) == len(content) {
		return ToolResultPreview{Text: content}
	}
	separator := "\n"
	if prefix == "" || prefix[len(prefix)-1] == '\n' {
		separator = ""
	}
	return ToolResultPreview{
		Text:      prefix + separator + ToolResultTruncationNotice,
		Truncated: true,
	}
}

// takeBytesAtCharBoundary mirrors codex-utils-string's
// take_bytes_at_char_boundary: the longest prefix no longer than maxBytes that
// does not split a UTF-8 rune.
func takeBytesAtCharBoundary(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	lastOK := 0
	for index := 0; index < len(value); {
		_, size := utf8.DecodeRuneInString(value[index:])
		next := index + size
		if next > maxBytes {
			break
		}
		lastOK = next
		index = next
	}
	return value[:lastOK]
}

// toolResultSequence mirrors codex-otel's NEXT_SEQ: the result-recording order
// across all sessions in this process, independent of the rollout-trace writer
// and reset on process restart.
var toolResultSequence uint64

// nextToolResultSequence mirrors codex-otel's next_tool_result_seq.
func nextToolResultSequence() uint64 {
	return atomic.AddUint64(&toolResultSequence, 1)
}

// resetToolResultSequence restores the process-global sequence between tests.
func resetToolResultSequence() {
	atomic.StoreUint64(&toolResultSequence, 0)
}

// ToolResultEvent mirrors codex-otel's ToolResultEvent: one completed tool call.
type ToolResultEvent struct {
	// ToolName is Rust's ToolName.name (the un-namespaced name).
	ToolName string
	// ToolNamespace is Rust's ToolName.namespace; empty means the default
	// function namespace.
	ToolNamespace string
	CallID        string
	Arguments     string
	// MCPServer and MCPServerOrigin are the executor's trace-only tags; an empty
	// server marks a built-in tool.
	MCPServer       string
	MCPServerOrigin string
	Duration        time.Duration
	Success         bool
	Output          string
}

// EmitToolResult mirrors codex-otel's emit_tool_result: the diagnostic log
// record and the trace-safe event that describe a completed tool call, without
// changing the tool-call metrics.
func EmitToolResult(ctx context.Context, telemetry *SessionTelemetry, limits protocol.ToolResultLogConfig, event ToolResultEvent) {
	if telemetry == nil {
		return
	}
	namespace := strings.TrimSpace(event.ToolNamespace)
	if namespace == "" {
		namespace = protocol.DefaultFunctionNamespace
	}
	preview := TelemetryPreview(event.Output, limits)
	telemetry.LogAndTraceEvent(ctx, ToolResultEventName,
		map[string]string{
			"tool_result_seq":  strconv.FormatUint(nextToolResultSequence(), 10),
			"tool_name":        event.ToolName,
			"tool_namespace":   namespace,
			"call_id":          event.CallID,
			"duration_ms":      strconv.FormatInt(event.Duration.Milliseconds(), 10),
			"success":          strconv.FormatBool(event.Success),
			"output_truncated": strconv.FormatBool(preview.Truncated),
		},
		map[string]string{
			"agent_name":        telemetry.Metadata.AgentName,
			"arguments":         event.Arguments,
			"output":            preview.Text,
			"mcp_server":        event.MCPServer,
			"mcp_server_origin": event.MCPServerOrigin,
		},
		map[string]string{
			"arguments_length":  strconv.Itoa(len(event.Arguments)),
			"output_length":     strconv.Itoa(len(event.Output)),
			"output_line_count": strconv.Itoa(lineCount(event.Output)),
			"tool_origin":       toolOrigin(event.MCPServer),
			"mcp_tool":          strconv.FormatBool(event.MCPServer != ""),
		})
}

// toolOrigin mirrors codex-otel's tool_origin field.
func toolOrigin(mcpServer string) string {
	if mcpServer == "" {
		return "builtin"
	}
	return "mcp"
}

// lineCount mirrors Rust's `output.lines().count()`: lines are terminated by
// '\n', and a trailing newline does not open another line.
func lineCount(value string) int {
	if value == "" {
		return 0
	}
	count := strings.Count(value, "\n")
	if !strings.HasSuffix(value, "\n") {
		count++
	}
	return count
}
