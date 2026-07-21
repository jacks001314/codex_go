package mcp

import (
	"reflect"
	"testing"
)

func TestConnectorAuthFailureFromToolResultParsesAuthFailure(t *testing.T) {
	result := authFailureResult()
	failure := ConnectorAuthFailureFromToolResult(
		result,
		"connector_calendar",
		"Google Calendar",
		"https://chatgpt.com/apps/google-calendar/connector_calendar",
	)
	if failure == nil {
		t.Fatal("auth failure should be parsed")
	}
	if failure.ConnectorID != "connector_calendar" || failure.ConnectorName != "Google Calendar" {
		t.Fatalf("connector identity = %#v", failure)
	}
	if failure.InstallURL != "https://chatgpt.com/apps/google-calendar/connector_calendar" {
		t.Fatalf("install url = %q", failure.InstallURL)
	}
	if failure.AuthReason != "reauthentication_required" {
		t.Fatalf("auth reason = %q", failure.AuthReason)
	}
	if failure.LinkID != "link_123" || failure.ErrorCode != "UNAUTHORIZED" || failure.ErrorAction != "TRIGGER_REAUTHENTICATION" {
		t.Fatalf("failure details = %#v", failure)
	}
	if failure.ErrorHTTPStatusCode == nil || *failure.ErrorHTTPStatusCode != 401 {
		t.Fatalf("http status = %#v", failure.ErrorHTTPStatusCode)
	}
}

func TestConnectorAuthFailureRejectsNonError(t *testing.T) {
	result := authFailureResult()
	notError := false
	result.IsError = &notError
	failure := ConnectorAuthFailureFromToolResult(result, "connector_calendar", "Google Calendar", "https://example.com")
	if failure != nil {
		t.Fatal("non-error result should not parse as auth failure")
	}
}

func TestConnectorAuthFailureRejectsMissingOrMismatchedConnectorID(t *testing.T) {
	result := authFailureResult()
	if failure := ConnectorAuthFailureFromToolResult(result, "", "Google Calendar", "https://example.com"); failure != nil {
		t.Fatal("missing connector id should yield nil")
	}
	if failure := ConnectorAuthFailureFromToolResult(result, "connector_drive", "Google Drive", "https://example.com"); failure != nil {
		t.Fatal("mismatched connector id should yield nil")
	}
}

func TestConnectorAuthFailureRejectsMissingInstallURL(t *testing.T) {
	result := authFailureResult()
	if failure := ConnectorAuthFailureFromToolResult(result, "connector_calendar", "Google Calendar", ""); failure != nil {
		t.Fatal("missing install url should yield nil")
	}
}

func TestBuildAuthElicitationPlan(t *testing.T) {
	result := authFailureResult()
	plan := BuildAuthElicitationPlan(
		"call_123",
		result,
		"connector_calendar",
		"Google Calendar",
		"https://chatgpt.com/apps/google-calendar/connector_calendar",
	)
	if plan == nil {
		t.Fatal("auth elicitation plan should be built")
	}
	if plan.AuthFailure.ConnectorName != "Google Calendar" {
		t.Fatalf("auth failure connector = %q", plan.AuthFailure.ConnectorName)
	}
	if plan.Elicitation.ElicitationID != "codex_apps_auth_call_123" {
		t.Fatalf("elicitation id = %q", plan.Elicitation.ElicitationID)
	}
	if plan.Elicitation.URL != "https://chatgpt.com/apps/google-calendar/connector_calendar" {
		t.Fatalf("elicitation url = %q", plan.Elicitation.URL)
	}
	expectedMessage := "Reconnect Google Calendar on ChatGPT to restore access for this request."
	if plan.Elicitation.Message != expectedMessage {
		t.Fatalf("elicitation message = %q", plan.Elicitation.Message)
	}
}

func TestBuildAuthElicitationPlanReturnsNilOnNonError(t *testing.T) {
	result := authFailureResult()
	notError := false
	result.IsError = &notError
	plan := BuildAuthElicitationPlan("call_123", result, "connector_calendar", "Google Calendar", "https://example.com")
	if plan != nil {
		t.Fatal("plan should be nil for non-error result")
	}
}

func TestAuthElicitationMessages(t *testing.T) {
	tests := []struct {
		reason   string
		contains string
	}{
		{"oauth_upgrade_required", "grant the permissions"},
		{"reauthentication_required", "restore access"},
		{"missing_link", "Sign in to"},
		{"", "Sign in to"},
		{"unknown_reason", "Sign in to"},
	}
	for _, test := range tests {
		failure := &CodexAppsConnectorAuthFailure{
			ConnectorID:   "test",
			ConnectorName: "TestApp",
			InstallURL:    "https://example.com",
			AuthReason:    test.reason,
		}
		msg := authElicitationMessage(failure)
		if msg == "" || !containsString(msg, test.contains) {
			t.Fatalf("authElicitationMessage(reason=%q) = %q, want contains %q", test.reason, msg, test.contains)
		}
	}
}

func TestAuthElicitationID(t *testing.T) {
	if id := AuthElicitationID("call_123"); id != "codex_apps_auth_call_123" {
		t.Fatalf("AuthElicitationID = %q", id)
	}
	if id := AuthElicitationID(" call_456 "); id != "codex_apps_auth_call_456" {
		t.Fatalf("AuthElicitationID(trimmed) = %q", id)
	}
}

func TestAuthElicitationCompletedResult(t *testing.T) {
	failure := &CodexAppsConnectorAuthFailure{
		ConnectorID:   "test",
		ConnectorName: "TestApp",
		InstallURL:    "https://example.com",
	}
	result := AuthElicitationCompletedResult(failure, nil)
	if result == nil || result.IsError == nil || !*result.IsError {
		t.Fatal("completed result should be an error")
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatal("completed result should have text content")
	}
}

func TestAuthElicitationMetaRoundTrip(t *testing.T) {
	failure := &CodexAppsConnectorAuthFailure{
		ConnectorID:   "connector_calendar",
		ConnectorName: "Google Calendar",
		InstallURL:    "https://chatgpt.com/apps/google-calendar/connector_calendar",
		AuthReason:    "reauthentication_required",
		LinkID:        "link_123",
		ErrorCode:     "UNAUTHORIZED",
		ErrorAction:   "TRIGGER_REAUTHENTICATION",
	}
	status401 := int64(401)
	failure.ErrorHTTPStatusCode = &status401

	elicitation := BuildAuthElicitation("call_123", failure)
	if elicitation == nil {
		t.Fatal("elicitation should not be nil")
	}
	if elicitation.ElicitationID != "codex_apps_auth_call_123" {
		t.Fatalf("elicitation id = %q", elicitation.ElicitationID)
	}

	meta, ok := elicitation.Meta.(map[string]any)
	if !ok {
		t.Fatal("elicitation meta should be a map")
	}
	codexApps, ok := meta[MCPToolCodexAppsMetaKey].(map[string]any)
	if !ok {
		t.Fatal("meta should contain _codex_apps")
	}
	authFailureMeta, ok := codexApps[connectorAuthFailureMetaKey].(map[string]any)
	if !ok {
		t.Fatal("_codex_apps should contain connector_auth_failure")
	}
	if !reflect.DeepEqual(authFailureMeta, authFailureMetaMap(failure)) {
		t.Fatalf("auth failure meta mismatch: got=%#v", authFailureMeta)
	}
}

func TestConnectorAuthFailureFromToolResultWithNilResult(t *testing.T) {
	if failure := ConnectorAuthFailureFromToolResult(nil, "connector_calendar", "Google Calendar", "https://example.com"); failure != nil {
		t.Fatal("nil result should yield nil")
	}
}

func containsString(s string, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && searchString(s, substr))
}

func searchString(s string, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
