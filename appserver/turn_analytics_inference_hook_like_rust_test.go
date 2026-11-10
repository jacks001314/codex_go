package appserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"codex_go/codexapi"
)

// Rust #60511: a workspace inference-hook denial is a distinct CodexErrorInfo
// variant, keeps its own analytics kind, reports no HTTP status, and survives
// the turn-error wire shape used by thread/read.
func TestTurnAnalyticsExposesInferenceHookDeniedLikeRust(t *testing.T) {
	fields := turnAnalyticsErrorFieldsFromAPIError(&codexapi.APIError{
		Kind:    codexapi.ErrorInferenceHookDenied,
		Status:  http.StatusForbidden,
		Message: "Blocked by the workspace hook.",
	})
	encoded, err := json.Marshal(fields.TurnError)
	if err != nil {
		t.Fatalf("marshal codex error info: %v", err)
	}
	if string(encoded) != `"inferenceHookDenied"` {
		t.Fatalf("codex error info = %s, want %q", encoded, "inferenceHookDenied")
	}
	if fields.CodexErrorKind == nil || *fields.CodexErrorKind != "inference_hook_denied" {
		t.Fatalf("analytics kind = %#v, want inference_hook_denied", fields.CodexErrorKind)
	}
	if fields.HTTPStatusCode != nil {
		t.Fatalf("analytics status = %d, want none", *fields.HTTPStatusCode)
	}

	turnError, err := json.Marshal(TurnError{Message: "Blocked by the workspace hook.", CodexErrorInfo: fields.TurnError})
	if err != nil {
		t.Fatalf("marshal turn error: %v", err)
	}
	var decoded struct {
		Message        string          `json:"message"`
		CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
	}
	if err := json.Unmarshal(turnError, &decoded); err != nil {
		t.Fatalf("unmarshal turn error: %v", err)
	}
	if decoded.Message != "Blocked by the workspace hook." || string(decoded.CodexErrorInfo) != `"inferenceHookDenied"` {
		t.Fatalf("turn error wire shape = %s", turnError)
	}
}
