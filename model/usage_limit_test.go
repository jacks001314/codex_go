package model

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"codex_go/codexapi"
)

// Mirrors Rust's `UsageLimitReachedError` Display: the copy the user sees is
// rebuilt from the limit name, the reached type, the promo message and the plan
// type - never from the response body's message.
func TestUsageLimitReachedMessageLikeRust(t *testing.T) {
	now := time.Date(2026, 9, 26, 15, 30, 0, 0, time.Local)
	previousNow := usageLimitRetryNow
	usageLimitRetryNow = func() time.Time { return now }
	defer func() { usageLimitRetryNow = previousNow }()

	sameDay := time.Date(2026, 9, 26, 15, 5, 0, 0, time.Local)
	otherDay := time.Date(2026, 10, 5, 9, 7, 0, 0, time.Local)

	for _, testCase := range []struct {
		name     string
		evidence usageLimitEvidence
		want     string
	}{
		{
			name:     "limit name",
			evidence: usageLimitEvidence{LimitName: "gpt-5-codex"},
			want:     "You’ve hit your usage limit for gpt-5-codex. Switch to another model now, or try again later.",
		},
		{
			name:     "limit name with a reset",
			evidence: usageLimitEvidence{LimitName: "gpt-5-codex", ResetsAt: &sameDay},
			want:     "You’ve hit your usage limit for gpt-5-codex. Switch to another model now, or try again at 3:05 PM.",
		},
		{
			name:     "codex limit name is the ordinary copy",
			evidence: usageLimitEvidence{LimitName: "codex"},
			want:     "You’ve hit your usage limit. Try again later.",
		},
		{
			name:     "reserve limit name is the ordinary copy",
			evidence: usageLimitEvidence{LimitName: "GPT-Reserve"},
			want:     "You’ve hit your usage limit. Try again later.",
		},
		{
			name:     "workspace owner credits",
			evidence: usageLimitEvidence{RateLimitReachedType: "workspace_owner_credits_depleted"},
			want:     "Your workspace is out of credits. Add credits to continue.",
		},
		{
			name:     "workspace member credits",
			evidence: usageLimitEvidence{RateLimitReachedType: "workspace_member_credits_depleted"},
			want:     "Your workspace is out of credits. Ask your workspace owner to refill in order to continue.",
		},
		{
			name:     "workspace owner usage limit",
			evidence: usageLimitEvidence{RateLimitReachedType: "workspace_owner_usage_limit_reached"},
			want:     "You hit your spend cap set in your workspace. Increase your spend cap to continue.",
		},
		{
			name:     "workspace member usage limit",
			evidence: usageLimitEvidence{RateLimitReachedType: "workspace_member_usage_limit_reached"},
			want:     "You hit your spend cap set by the owner of your workspace. Ask an owner to increase your spend cap to continue.",
		},
		{
			name:     "generic reached type keeps the plan copy",
			evidence: usageLimitEvidence{RateLimitReachedType: "rate_limit_reached", PlanType: "plus"},
			want:     "You’ve hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again later.",
		},
		{
			name:     "promo message",
			evidence: usageLimitEvidence{PromoMessage: "Get 2x usage for $10"},
			want:     "You’ve hit your usage limit. Get 2x usage for $10, or try again later.",
		},
		{
			name:     "promo message with a reset",
			evidence: usageLimitEvidence{PromoMessage: "Get 2x usage for $10", ResetsAt: &otherDay},
			want:     "You’ve hit your usage limit. Get 2x usage for $10, or try again at Oct 5th, 2026 9:07 AM.",
		},
		{
			name:     "plus",
			evidence: usageLimitEvidence{PlanType: "plus"},
			want:     "You’ve hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again later.",
		},
		{
			name:     "team",
			evidence: usageLimitEvidence{PlanType: "team"},
			want:     "You’ve hit your usage limit. To get more access now, send a request to your admin or try again later.",
		},
		{
			name:     "business",
			evidence: usageLimitEvidence{PlanType: "business"},
			want:     "You’ve hit your usage limit. To get more access now, send a request to your admin or try again later.",
		},
		{
			name:     "free",
			evidence: usageLimitEvidence{PlanType: "free"},
			want:     "You’ve hit your usage limit. Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus), or try again later.",
		},
		{
			name:     "go",
			evidence: usageLimitEvidence{PlanType: "go"},
			want:     "You’ve hit your usage limit. Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus), or try again later.",
		},
		{
			name:     "pro",
			evidence: usageLimitEvidence{PlanType: "pro", ResetsAt: &sameDay},
			want:     "You’ve hit your usage limit. Visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again at 3:05 PM.",
		},
		{
			name:     "promax",
			evidence: usageLimitEvidence{PlanType: "promax"},
			want:     "You’ve hit your usage limit. Visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again later.",
		},
		{
			name:     "enterprise",
			evidence: usageLimitEvidence{PlanType: "enterprise", ResetsAt: &sameDay},
			want:     "You’ve hit your usage limit. Try again at 3:05 PM.",
		},
		{
			name:     "education alias",
			evidence: usageLimitEvidence{PlanType: "education"},
			want:     "You’ve hit your usage limit. Try again later.",
		},
		{
			name:     "unknown plan spelling",
			evidence: usageLimitEvidence{PlanType: "PLUS"},
			want:     "You’ve hit your usage limit. Try again later.",
		},
		{
			name:     "absent plan",
			evidence: usageLimitEvidence{},
			want:     "You’ve hit your usage limit. Try again later.",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := usageLimitReachedMessage(testCase.evidence); got != testCase.want {
				t.Fatalf("usageLimitReachedMessage() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// The other-day branch mirrors Rust's `%b %-d{suffix}, %Y %-I:%M %p` format,
// including the ordinal suffix.
func TestUsageLimitRetryTimestampFallsBackToTheDateLikeRust(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	previousNow := usageLimitRetryNow
	usageLimitRetryNow = func() time.Time { return now }
	defer func() { usageLimitRetryNow = previousNow }()

	for _, testCase := range []struct {
		day  int
		want string
	}{
		{day: 1, want: "Oct 1st, 2026 9:07 AM"},
		{day: 2, want: "Oct 2nd, 2026 9:07 AM"},
		{day: 3, want: "Oct 3rd, 2026 9:07 AM"},
		{day: 11, want: "Oct 11th, 2026 9:07 AM"},
		{day: 12, want: "Oct 12th, 2026 9:07 AM"},
		{day: 13, want: "Oct 13th, 2026 9:07 AM"},
		{day: 21, want: "Oct 21st, 2026 9:07 AM"},
		{day: 22, want: "Oct 22nd, 2026 9:07 AM"},
	} {
		resetsAt := time.Date(2026, 10, testCase.day, 9, 7, 0, 0, time.Local)
		got := usageLimitReachedMessage(usageLimitEvidence{PlanType: "enterprise", ResetsAt: &resetsAt})
		if want := "You’ve hit your usage limit. Try again at " + testCase.want + "."; got != want {
			t.Fatalf("day %d message = %q, want %q", testCase.day, got, want)
		}
	}
}

// Mirrors Rust's api_bridge usage-limit branch: the evidence comes from the
// body's `plan_type`/`resets_at`/`limit_window_minutes` and from the response
// headers, and the message is the composed recovery copy.
func TestResponsesHTTPErrorCarriesUsageLimitEvidenceLikeRust(t *testing.T) {
	resetsAt := time.Date(2026, 9, 26, 15, 5, 0, 0, time.UTC)
	now := resetsAt.Add(time.Hour)
	previousNow := usageLimitRetryNow
	usageLimitRetryNow = func() time.Time { return now }
	defer func() { usageLimitRetryNow = previousNow }()

	headers := http.Header{}
	headers.Set("x-codex-active-limit", "gpt-5-codex")
	headers.Set("x-gpt-5-codex-limit-name", "gpt-5-codex")
	headers.Set("x-codex-promo-message", "Get 2x usage for $10")
	headers.Set("x-codex-rate-limit-reached-type", "workspace_member_usage_limit_reached")
	body := fmt.Sprintf(`{"error":{"type":"usage_limit_reached","message":"server copy","plan_type":"promar","resets_at":%d,"limit_window_minutes":300}}`, resetsAt.Unix())

	err := responsesHTTPError("OpenAI", http.StatusTooManyRequests, headers, []byte(body))
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorRateLimit {
		t.Fatalf("usage limit error = %#v", err)
	}
	// The active limit's name takes precedence over the reached type (Rust's
	// Display checks `rate_limits.limit_name` first), and the reset instant uses
	// the local clock formatting.
	if want := "You’ve hit your usage limit for gpt-5-codex. Switch to another model now, or try again at " +
		formatUsageLimitRetryTimestamp(resetsAt) + "."; apiErr.Message != want {
		t.Fatalf("message = %q, want %q", apiErr.Message, want)
	}
	// The app-server reports `err.Error()` as the turn error message, and Rust's
	// `CodexErr::UsageLimitReached` display is the usage-limit copy alone.
	if apiErr.Error() != apiErr.Message {
		t.Fatalf("displayed error = %q, want the copy %q", apiErr.Error(), apiErr.Message)
	}
	if apiErr.UsageLimitPlanType == nil || *apiErr.UsageLimitPlanType != "promar" {
		t.Fatalf("plan type = %v", apiErr.UsageLimitPlanType)
	}
	if apiErr.UsageLimitResetsAt == nil || !apiErr.UsageLimitResetsAt.Equal(resetsAt) {
		t.Fatalf("resets at = %v, want %v", apiErr.UsageLimitResetsAt, resetsAt)
	}
	if apiErr.UsageLimitWindowMinutes == nil || *apiErr.UsageLimitWindowMinutes != 300 {
		t.Fatalf("window = %v", apiErr.UsageLimitWindowMinutes)
	}
	if apiErr.UsageLimitLimitName == nil || *apiErr.UsageLimitLimitName != "gpt-5-codex" {
		t.Fatalf("limit name = %v", apiErr.UsageLimitLimitName)
	}
	if apiErr.UsageLimitPromoMessage == nil || *apiErr.UsageLimitPromoMessage != "Get 2x usage for $10" {
		t.Fatalf("promo = %v", apiErr.UsageLimitPromoMessage)
	}
	if apiErr.UsageLimitRateLimitReachedType == nil || *apiErr.UsageLimitRateLimitReachedType != "workspace_member_usage_limit_reached" {
		t.Fatalf("reached type = %v", apiErr.UsageLimitRateLimitReachedType)
	}
	details := apiErr.Details()
	if details.UsageLimitPlanType == nil || *details.UsageLimitPlanType != "promar" ||
		details.UsageLimitResetsAt == nil || !details.UsageLimitResetsAt.Equal(resetsAt) ||
		details.UsageLimitLimitName == nil || *details.UsageLimitLimitName != "gpt-5-codex" {
		t.Fatalf("details = %#v", details)
	}

	// Without an active limit name the reached type selects the workspace copy.
	reached := http.Header{}
	reached.Set("x-codex-rate-limit-reached-type", "workspace_member_usage_limit_reached")
	reachedErr := responsesHTTPError("OpenAI", http.StatusTooManyRequests, reached,
		[]byte(`{"error":{"type":"usage_limit_reached","plan_type":"plus"}}`))
	if !errors.As(reachedErr, &apiErr) {
		t.Fatalf("reached-type error = %#v", reachedErr)
	}
	if apiErr.Message != "You hit your spend cap set by the owner of your workspace. Ask an owner to increase your spend cap to continue." {
		t.Fatalf("reached-type message = %q", apiErr.Message)
	}

	// A well-formed response without the optional evidence still composes the
	// ordinary copy and leaves the evidence unset.
	plain := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{},
		[]byte(`{"error":{"type":"usage_limit_reached","message":"server copy"}}`))
	if !errors.As(plain, &apiErr) || apiErr.Message != "You’ve hit your usage limit. Try again later." {
		t.Fatalf("plain usage limit = %#v", plain)
	}
	if apiErr.UsageLimitPlanType != nil || apiErr.UsageLimitResetsAt != nil || apiErr.UsageLimitLimitName != nil ||
		apiErr.UsageLimitPromoMessage != nil || apiErr.UsageLimitRateLimitReachedType != nil {
		t.Fatalf("plain usage limit carried evidence: %#v", apiErr)
	}
}

// Rust only classifies the usage limit inside the strict body parse, so a body
// whose declared fields are malformed falls through to the generic retry
// classification instead of becoming a usage-limit failure.
func TestResponsesHTTPErrorFallsThroughForMalformedUsageLimitBodyLikeRust(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"usage_limit_reached","plan_type":42}}`,
		`{"error":{"type":"usage_limit_reached","resets_at":"soon"}}`,
		`{"error":{"type":"usage_limit_reached","resets_at":1.5}}`,
		`{"error":{"type":"usage_limit_reached","code":42}}`,
		`{"error":{"type":"usage_limit_reached","type":42}}`,
		`{"error":[]}`,
		`{"error":null}`,
		`not json`,
	} {
		err := responsesHTTPError("OpenAI", http.StatusTooManyRequests, http.Header{}, []byte(body))
		var apiErr *codexapi.APIError
		if errors.As(err, &apiErr) && apiErr.Kind == codexapi.ErrorRateLimit {
			t.Fatalf("body %s was classified as a usage limit: %#v", body, apiErr)
		}
		if _, ok := err.(*ResponsesAPIError); !ok {
			t.Fatalf("body %s error = %#v, want the generic retry classification", body, err)
		}
	}
}

// The wrapped-WebSocket path maps the whole event through the same classifier, so
// the evidence and the copy match the HTTP path.
func TestWebsocketUsageLimitErrorCarriesEvidenceLikeRust(t *testing.T) {
	event := map[string]any{
		"type":   "error",
		"status": float64(429),
		"error": map[string]any{
			"type":                 "usage_limit_reached",
			"message":              "server copy",
			"plan_type":            "plus",
			"limit_window_minutes": float64(10080),
		},
		"headers": map[string]any{
			"x-codex-promo-message": "Get 2x usage for $10",
		},
	}
	err, ok := websocketUsageLimitError(event)
	if !ok {
		t.Fatal("the wrapped websocket usage limit was not classified")
	}
	if err.UsageLimitPlanType == nil || *err.UsageLimitPlanType != "plus" {
		t.Fatalf("plan type = %v", err.UsageLimitPlanType)
	}
	if err.UsageLimitWindowMinutes == nil || *err.UsageLimitWindowMinutes != 10080 {
		t.Fatalf("window = %v", err.UsageLimitWindowMinutes)
	}
	if err.UsageLimitPromoMessage == nil || *err.UsageLimitPromoMessage != "Get 2x usage for $10" {
		t.Fatalf("promo = %v", err.UsageLimitPromoMessage)
	}
	if err.Message != "You’ve hit your usage limit. Get 2x usage for $10, or try again later." {
		t.Fatalf("message = %q", err.Message)
	}

	// A malformed error object is not classified at all.
	if _, ok := websocketUsageLimitError(map[string]any{
		"type":   "error",
		"status": float64(429),
		"error":  map[string]any{"type": "usage_limit_reached", "plan_type": float64(42)},
	}); ok {
		t.Fatal("a malformed usage-limit event was classified")
	}
}
