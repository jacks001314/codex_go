package telemetry

import (
	"unicode/utf8"

	"codex_go/protocol"
)

// Rust parity: codex-rs/otel/src/tool_result.rs (telemetry_preview) and
// codex_protocol::config_types::ToolResultLogConfig. The preview is what a
// `codex.tool_result` log record may carry: the tool output truncated to a byte
// budget with a truncation notice, independent of the model-visible output.

// ToolResultTruncationNotice mirrors Rust's TRUNCATION_NOTICE.
const ToolResultTruncationNotice = "[... telemetry preview truncated ...]"

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
