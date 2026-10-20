package appserver

// Thread sample protocol types (Rust #55339). A sample runs one model request on
// a private ephemeral fork so clients can inspect a hypothetical response
// without changing the source thread.

type ThreadSampleParams struct {
	ThreadID       string  `json:"threadId"`
	ExpectedTurnID string  `json:"expectedTurnId"`
	Prompt         string  `json:"prompt"`
	Effort         *string `json:"effort,omitempty"`
	TurnTrigger    *string `json:"turnTrigger,omitempty"`
}

type ThreadSampleResponse struct {
	SourceTurnID      string  `json:"sourceTurnId"`
	ResponseID        string  `json:"responseId"`
	Text              *string `json:"text"`
	InputTokens       *int64  `json:"inputTokens"`
	CachedInputTokens *int64  `json:"cachedInputTokens"`
	OutputTokens      *int64  `json:"outputTokens"`
}

type ThreadSampleCancelParams struct {
	RequestID RequestID `json:"requestId"`
}

type ThreadSampleCancelResponse struct{}
