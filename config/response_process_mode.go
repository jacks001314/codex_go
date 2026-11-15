package config

// ResponseProcessMode mirrors Rust's config_toml::ResponseProcessMode
// (#61577): whether model responses are handled as they stream or buffered
// until the completed response has been settled.
type ResponseProcessMode string

const (
	// ResponseProcessModeStreaming processes items as they arrive, so a tool
	// can start before its response is complete. This is the default.
	ResponseProcessModeStreaming ResponseProcessMode = "streaming"
	// ResponseProcessModeAfterCompleteResponse releases buffered items after
	// the response completes, synchronous post-hooks finish, and budget
	// charges are posted.
	ResponseProcessModeAfterCompleteResponse ResponseProcessMode = "after_complete_response"
)

// ResponseProcessMode resolves `responses.process_mode`. An absent table or
// field keeps Rust's streaming default; invalid values were rejected while
// loading config.toml.
func (c *Config) ResponseProcessMode() ResponseProcessMode {
	if c != nil {
		if responses, ok := c.Values["responses"].(map[string]any); ok {
			if mode, ok := responses["process_mode"].(string); ok {
				switch ResponseProcessMode(mode) {
				case ResponseProcessModeAfterCompleteResponse:
					return ResponseProcessModeAfterCompleteResponse
				case ResponseProcessModeStreaming:
					return ResponseProcessModeStreaming
				}
			}
		}
	}
	return ResponseProcessModeStreaming
}
