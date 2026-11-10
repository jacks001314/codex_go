package model

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"

	"codex_go/codexapi"
)

// TestWorkspaceAccessDeniedIsTerminalLikeRust mirrors Rust #60475/#60480: a 403
// naming the workspace rejection is typed, keeps the provider message, and is
// never retried.
func TestWorkspaceAccessDeniedIsTerminalLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		header string
		body   string
	}{
		{name: "error code", body: `{"error":{"code":"invalid_workspace_selected","message":"workspace not allowed"}}`},
		{name: "detail code", body: `{"detail":{"code":"codex_workspace_access_denied"},"error":{"message":"denied"}}`},
		{name: "identity header", header: base64.StdEncoding.EncodeToString([]byte(`{"error":{"code":"codex_workspace_access_denied"}}`)), body: `{"error":{"message":"denied"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{}
			if tc.header != "" {
				headers.Set("x-error-json", tc.header)
			}
			err := responsesHTTPError("openai", http.StatusForbidden, headers, []byte(tc.body))
			var apiErr *codexapi.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("responsesHTTPError() = %T %v, want *codexapi.APIError", err, err)
			}
			if apiErr.Kind != codexapi.ErrorWorkspaceAccessDenied {
				t.Fatalf("kind = %q, want %q", apiErr.Kind, codexapi.ErrorWorkspaceAccessDenied)
			}
			if apiErr.Status != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", apiErr.Status)
			}
			if isRetryableResponsesStreamError(err) {
				t.Fatalf("workspace access denial must be terminal")
			}
		})
	}
}

// TestWorkspaceAccessDeniedCodePrecedenceLikeRust mirrors Rust #60475: the body
// code wins over the identity header, and /detail/code wins over /error/code.
func TestWorkspaceAccessDeniedCodePrecedenceLikeRust(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-error-json", base64.StdEncoding.EncodeToString([]byte(`{"error":{"code":"invalid_workspace_selected"}}`)))
	err := responsesHTTPError("openai", http.StatusForbidden, headers, []byte(`{"detail":{"code":"some_other_code"},"error":{"code":"access_program_not_enabled"}}`))
	var responseErr *ResponsesAPIError
	if !errors.As(err, &responseErr) {
		t.Fatalf("body code must win over the identity header, got %T %v", err, err)
	}
	bodyErr := responsesHTTPError("openai", http.StatusForbidden, http.Header{}, []byte(`{"error":{"code":"access_program_not_enabled"}}`))
	var apiErr *codexapi.APIError
	if !errors.As(bodyErr, &apiErr) || apiErr.Kind != codexapi.ErrorAccessProgramNotEnabled {
		t.Fatalf("error.code must classify without a detail code, got %v", bodyErr)
	}
}

// TestAccessProgramNotEnabledIsTerminalLikeRust mirrors Rust #60476.
func TestAccessProgramNotEnabledIsTerminalLikeRust(t *testing.T) {
	err := responsesHTTPError("openai", http.StatusForbidden, http.Header{}, []byte(`{"error":{"code":"access_program_not_enabled","message":"daybreak not enabled"}}`))
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorAccessProgramNotEnabled {
		t.Fatalf("403 access_program_not_enabled = %v, want ErrorAccessProgramNotEnabled", err)
	}
	if !strings.Contains(apiErr.Error(), "daybreak not enabled") {
		t.Fatalf("display = %q, want the provider message", apiErr.Error())
	}
	if isRetryableResponsesStreamError(err) {
		t.Fatalf("access program rejection must be terminal")
	}
}

// TestUnclassifiedForbiddenKeepsRetryPolicyLikeRust mirrors Rust #60475: other
// 403 responses keep their existing (non-typed) handling.
func TestUnclassifiedForbiddenKeepsRetryPolicyLikeRust(t *testing.T) {
	err := responsesHTTPError("openai", http.StatusForbidden, http.Header{}, []byte(`{"error":{"code":"something_else","message":"nope"}}`))
	var apiErr *codexapi.APIError
	if errors.As(err, &apiErr) && apiErr.Kind == codexapi.ErrorWorkspaceAccessDenied {
		t.Fatalf("unrecognized 403 code must not be typed as a workspace denial")
	}
	var responseErr *ResponsesAPIError
	if !errors.As(err, &responseErr) {
		t.Fatalf("responsesHTTPError() = %T, want *ResponsesAPIError", err)
	}
}

// TestStreamedTerminalProviderCodesLikeRust mirrors Rust #60479/#60481/#60482:
// the statusless streamed forms are typed, and a 503 keeps the retryable path.
func TestStreamedTerminalProviderCodesLikeRust(t *testing.T) {
	body := func(code, message string) *responsesAgentAPIErrorBody {
		return &responsesAgentAPIErrorBody{Code: code, Message: message}
	}
	cases := []struct {
		name     string
		code     string
		status   int
		wantKind codexapi.APIErrorKind
		wantText string
	}{
		{name: "content policy streamed", code: "content_policy_violation", status: 0, wantKind: codexapi.ErrorContentPolicyViolation, wantText: ContentPolicyFallbackMessage},
		{name: "content policy 400", code: "content_policy_violation", status: http.StatusBadRequest, wantKind: codexapi.ErrorContentPolicyViolation, wantText: "blocked"},
		{name: "invalid access program streamed", code: "invalid_access_program", status: 0, wantKind: codexapi.ErrorInvalidRequest, wantText: AccessProgramUnsupportedFallbackMessage},
		{name: "unsupported access program streamed", code: "unsupported_access_program", status: 0, wantKind: codexapi.ErrorInvalidRequest, wantText: AccessProgramUnsupportedFallbackMessage},
		{name: "access program streamed", code: "access_program_not_enabled", status: 0, wantKind: codexapi.ErrorAccessProgramNotEnabled, wantText: AccessProgramNotEnabledFallbackMessage},
		{name: "context length streamed", code: "context_length_exceeded", status: 0, wantKind: codexapi.ErrorContextWindowExceeded},
		{name: "context length 400", code: "context_length_exceeded", status: http.StatusBadRequest, wantKind: codexapi.ErrorContextWindowExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message := ""
			if tc.status == http.StatusBadRequest {
				message = "blocked"
			}
			got := terminalProviderError(body(tc.code, message), tc.status)
			if got == nil {
				t.Fatalf("terminalProviderError(%q, %d) = nil", tc.code, tc.status)
			}
			if got.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if tc.wantText != "" && got.Message != tc.wantText {
				t.Fatalf("message = %q, want %q", got.Message, tc.wantText)
			}
		})
	}
	if got := terminalProviderError(body("context_length_exceeded", ""), http.StatusServiceUnavailable); got != nil {
		t.Fatalf("503 context length must stay retryable, got %#v", got)
	}
	if got := terminalProviderError(body("content_policy_violation", ""), http.StatusInternalServerError); got != nil {
		t.Fatalf("500 content policy must not be typed, got %#v", got)
	}
	if got := terminalProviderError(body("access_program_not_enabled", ""), http.StatusBadRequest); got != nil {
		t.Fatalf("400 access program must keep the HTTP bridge path, got %#v", got)
	}
}
