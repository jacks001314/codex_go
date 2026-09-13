package protocol

// Rust parity: codex_protocol::config_types::ToolResultLogConfig. This bounds
// the tool output carried by a `codex.tool_result` log record; it does not
// affect the model-visible output.

// DefaultToolResultLogMaxBytes mirrors ToolResultLogConfig::default().max_bytes.
const DefaultToolResultLogMaxBytes = 2 * 1024

// ToolResultLogConfig is the maximum UTF-8 bytes of tool output included in a
// log record before the truncation notice.
type ToolResultLogConfig struct {
	MaxBytes int
}

// DefaultToolResultLogConfig mirrors ToolResultLogConfig::default.
func DefaultToolResultLogConfig() ToolResultLogConfig {
	return ToolResultLogConfig{MaxBytes: DefaultToolResultLogMaxBytes}
}
