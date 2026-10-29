package codexapi

import "testing"

// Mirrors the display strings Rust's `CodexErrorDetails` attaches to each error
// kind: the app-server reports `err.Error()` as the turn error message, so the
// user-facing copy must match Rust rather than a Go-only prefix.
func TestAPIErrorDisplayMatchesRustLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  *APIError
		want string
	}{
		{
			name: "context window",
			err:  &APIError{Kind: ErrorContextWindowExceeded},
			want: "Codex ran out of room in the model's context window. Start a new thread or clear earlier history before retrying.",
		},
		{
			name: "quota",
			err:  &APIError{Kind: ErrorQuotaExceeded},
			want: "Quota exceeded. Check your plan and billing details.",
		},
		{
			name: "usage not included",
			err:  &APIError{Kind: ErrorUsageNotIncluded},
			want: "To use Codex with your ChatGPT plan, upgrade to Plus: https://chatgpt.com/explore/plus.",
		},
		{
			name: "server overloaded",
			err:  &APIError{Kind: ErrorServerOverloaded},
			want: "Selected model is at capacity. Please try a different model.",
		},
		{
			// Rust #57452 distinguishes a policy-restricted capacity failure
			// from an ordinary overload even though both keep overload retry
			// advice.
			name: "policy capacity unavailable",
			err:  &APIError{Kind: ErrorPolicyCapacityUnavailable},
			want: "Policy-restricted capacity is unavailable. Please try again later.",
		},
		{
			name: "flex unavailable",
			err:  &APIError{Kind: ErrorFlexUnavailable},
			want: "Flex capacity unavailable.",
		},
		{
			name: "stream",
			err:  &APIError{Kind: ErrorStream, Message: "connection reset"},
			want: "stream disconnected before completion: connection reset",
		},
		{
			name: "connection failed",
			err:  &APIError{Kind: ErrorTransport, Message: "error sending request for url (http://example.com/)"},
			want: "Connection failed: error sending request for url (http://example.com/)",
		},
		{
			name: "retryable",
			err:  &APIError{Kind: ErrorRetryable, Message: "temporarily unavailable"},
			want: "stream disconnected before completion: temporarily unavailable",
		},
		{
			name: "rate limit exceeded",
			err:  &APIError{Kind: ErrorRateLimitExceeded, Message: "slow down"},
			want: "rate limit exceeded: slow down",
		},
		{
			name: "usage limit",
			err:  &APIError{Kind: ErrorRateLimit, Message: "You’ve hit your usage limit. Try again later."},
			want: "You’ve hit your usage limit. Try again later.",
		},
		{
			name: "invalid request",
			err:  &APIError{Kind: ErrorInvalidRequest, Message: "bad prompt"},
			want: "bad prompt",
		},
		{
			name: "invalid prompt",
			err:  &APIError{Kind: ErrorInvalidPrompt, Message: "bad prompt detail"},
			want: "bad prompt detail",
		},
		{
			name: "cyber policy",
			err:  &APIError{Kind: ErrorCyberPolicy, Message: "blocked for cyber risk"},
			want: "blocked for cyber risk",
		},
		{
			name: "bio policy",
			err:  &APIError{Kind: ErrorBioPolicy, Message: "blocked for bio risk"},
			want: "blocked for bio risk",
		},
		{
			name: "misalignment policy",
			err:  &APIError{Kind: ErrorMisalignmentPolicyViolation, Message: "blocked for misalignment"},
			want: "blocked for misalignment",
		},
		{
			name: "model not found",
			err:  &APIError{Kind: ErrorModelNotFound, Message: "Access to this deployment ended."},
			want: "Access to this deployment ended.",
		},
		{
			name: "model not found blank message",
			err:  &APIError{Kind: ErrorModelNotFound},
			want: "model_not_found",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.err.Error(); got != testCase.want {
				t.Fatalf("Error() = %q, want %q", got, testCase.want)
			}
		})
	}
}
