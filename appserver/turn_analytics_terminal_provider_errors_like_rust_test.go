package appserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"codex_go/codexapi"
)

// TestTurnAnalyticsClassifiesTerminalProviderRejectionsLikeRust mirrors Rust
// #60475/#60476/#60479: workspace access denials and access-program rejections
// report CodexErrorInfo::HttpConnectionFailed with their HTTP status, while
// content-policy refusals report CodexErrorInfo::InvalidPrompt (the app-server
// v2 `other` surface in Go).
func TestTurnAnalyticsClassifiesTerminalProviderRejectionsLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		kind       codexapi.APIErrorKind
		status     int
		wantInfo   string
		wantKind   string
		wantStatus int
	}{
		{name: "workspace access denied http", kind: codexapi.ErrorWorkspaceAccessDenied, status: http.StatusForbidden, wantInfo: `{"httpConnectionFailed":{"httpStatusCode":403}}`, wantKind: "workspace_access_denied", wantStatus: http.StatusForbidden},
		{name: "workspace access denied streamed", kind: codexapi.ErrorWorkspaceAccessDenied, status: 0, wantInfo: `{"httpConnectionFailed":{"httpStatusCode":null}}`, wantKind: "workspace_access_denied", wantStatus: 0},
		{name: "access program not enabled http", kind: codexapi.ErrorAccessProgramNotEnabled, status: http.StatusForbidden, wantInfo: `{"httpConnectionFailed":{"httpStatusCode":403}}`, wantKind: "access_program_not_enabled", wantStatus: http.StatusForbidden},
		{name: "content policy", kind: codexapi.ErrorContentPolicyViolation, status: http.StatusBadRequest, wantInfo: `"other"`, wantKind: "content_policy_violation", wantStatus: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fields := turnAnalyticsErrorFieldsFromAPIError(&codexapi.APIError{Kind: tc.kind, Status: tc.status, Message: "denied"})
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
			if tc.wantStatus == 0 {
				if fields.HTTPStatusCode != nil {
					t.Fatalf("analytics status = %d, want none", *fields.HTTPStatusCode)
				}
				return
			}
			if fields.HTTPStatusCode == nil || int(*fields.HTTPStatusCode) != tc.wantStatus {
				t.Fatalf("analytics status = %#v, want %d", fields.HTTPStatusCode, tc.wantStatus)
			}
		})
	}
}
