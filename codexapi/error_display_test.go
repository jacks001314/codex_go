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
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.err.Error(); got != testCase.want {
				t.Fatalf("Error() = %q, want %q", got, testCase.want)
			}
		})
	}
}
