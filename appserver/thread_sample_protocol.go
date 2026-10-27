package appserver

// Thread sample protocol types (Rust #55339). A sample runs one model request on
// a private ephemeral fork so clients can inspect a hypothetical response
// without changing the source thread.

type ThreadSampleParams struct {
	ThreadID       string  `json:"threadId"`
	ExpectedTurnID string  `json:"expectedTurnId"`
	Prompt         string  `json:"prompt"`
	Effort         *string `json:"effort,omitempty"`
	// ThreadSource overrides the ephemeral fork's thread source (#56364).
	// ThreadSource is a wire string: "user", "subagent", "guardian_review",
	// "memory_consolidation", or a feature name such as
	// "composer_predictions". The default is "thread_sample".
	ThreadSource *string `json:"threadSource,omitempty"`
	TurnTrigger  *string `json:"turnTrigger,omitempty"`
	// ResponsesAPIClientMetadata enriches the sample turn's Responses API
	// turn metadata (#56031). Reserved keys such as turn_trigger cannot
	// override the explicit request fields.
	ResponsesAPIClientMetadata map[string]string `json:"responsesapiClientMetadata,omitempty"`
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
