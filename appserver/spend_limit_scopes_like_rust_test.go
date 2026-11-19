package appserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"codex_go/codexapi"
	"codex_go/state"
)

// TestAPISpendLimitScopesSurviveThreadReadLikeRust mirrors Rust #62329: each
// scoped spend limit is its own CodexErrorInfo variant, counts as a usage
// limit for goal and compaction stops, and survives `thread/read`.
func TestAPISpendLimitScopesSurviveThreadReadLikeRust(t *testing.T) {
	cases := []struct {
		kind     codexapi.APIErrorKind
		wantInfo string
		wantKind string
	}{
		{codexapi.ErrorAPIKeySpendLimitExceeded, `"apiKeySpendLimitExceeded"`, "api_key_spend_limit_exceeded"},
		{codexapi.ErrorProjectSpendLimitExceeded, `"projectSpendLimitExceeded"`, "project_spend_limit_exceeded"},
		{codexapi.ErrorOrganizationSpendLimitExceeded, `"organizationSpendLimitExceeded"`, "organization_spend_limit_exceeded"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			fields := turnAnalyticsErrorFieldsFromAPIError(&codexapi.APIError{
				Kind:    tc.kind,
				Status:  http.StatusTooManyRequests,
				Message: "spend limit",
			})
			encoded, err := json.Marshal(fields.TurnError)
			if err != nil {
				t.Fatalf("marshal codex error info: %v", err)
			}
			if string(encoded) != tc.wantInfo {
				t.Fatalf("codex error info = %s, want %s", encoded, tc.wantInfo)
			}
			if fields.CodexErrorKind == nil || *fields.CodexErrorKind != tc.wantKind {
				t.Fatalf("analytics kind = %#v, want %q", fields.CodexErrorKind, tc.wantKind)
			}
			if !codexErrorIsUsageLimited(fields.TurnError) {
				t.Fatalf("scoped spend limit must count as a usage limit")
			}

			errorJSON, err := json.Marshal(map[string]any{
				"message":           "Your API key has reached its spend limit.",
				"codexErrorInfo":    fields.TurnError,
				"additionalDetails": nil,
			})
			if err != nil {
				t.Fatalf("marshal stored turn error: %v", err)
			}
			read, err := stateThreadTurnToAPI(state.ThreadHistoryTurn{
				TurnID:    "turn-spend-limit",
				Status:    string(TurnStatusFailed),
				ErrorJSON: errorJSON,
			}, TurnItemsFull)
			if err != nil {
				t.Fatalf("stateThreadTurnToAPI: %v", err)
			}
			if read.Error == nil {
				t.Fatal("thread/read dropped the turn error")
			}
			readInfo, err := json.Marshal(read.Error.CodexErrorInfo)
			if err != nil {
				t.Fatalf("marshal read codex error info: %v", err)
			}
			if string(readInfo) != tc.wantInfo {
				t.Fatalf("thread/read codex error info = %s, want %s", readInfo, tc.wantInfo)
			}
		})
	}

	if !codexErrorIsUsageLimited("usageLimitExceeded") ||
		!codexErrorIsUsageLimited(map[string]any{"usageLimitExceeded": nil}) {
		t.Fatal("the generic usage limit must stay a usage limit")
	}
	if codexErrorIsUsageLimited("rateLimitExceeded") {
		t.Fatal("plain rate limits are not usage limits")
	}
}
