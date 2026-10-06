package appserver

// Thread prediction protocol types (Rust #49480). The request is defined but
// intentionally unimplemented — the app-server answers it with a
// method-not-found error — while the updated notification is routed to its
// thread by clients.

// ThreadPredictionRequestParams mirrors Rust ThreadPredictionRequestParams.
type ThreadPredictionRequestParams struct {
	ThreadID     string `json:"threadId"`
	SourceTurnID string `json:"sourceTurnId"`
}

// ThreadPredictionRequestResponse mirrors Rust ThreadPredictionRequestResponse.
type ThreadPredictionRequestResponse struct{}

// ThreadPredictionResult mirrors Rust ThreadPredictionResult: a `completed`
// result may carry optional text, and a `failed` result carries none.
type ThreadPredictionResult struct {
	Type string  `json:"type"`
	Text *string `json:"text,omitempty"`
}

// ThreadPredictionUpdatedNotification mirrors Rust
// ThreadPredictionUpdatedNotification.
type ThreadPredictionUpdatedNotification struct {
	ThreadID     string                 `json:"threadId"`
	SourceTurnID string                 `json:"sourceTurnId"`
	Result       ThreadPredictionResult `json:"result"`
}
