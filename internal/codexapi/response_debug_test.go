package codexapi

import (
	"net/http"
	"testing"
)

func TestExtractResponseDebugContext(t *testing.T) {
	headers := http.Header{}
	headers.Set(OAIRequestIDHeader, "req-auth")
	headers.Set(CFRayHeader, "ray-auth")
	headers.Set(AuthErrorHeader, "missing_authorization_header")
	headers.Set(XErrorJSONHeader, "eyJlcnJvciI6eyJjb2RlIjoidG9rZW5fZXhwaXJlZCJ9fQ==")
	context := ExtractResponseDebugContext(&ResponseTransportError{Kind: ResponseTransportHTTP, Status: 401, Headers: headers})
	if context.RequestID == nil || *context.RequestID != "req-auth" {
		t.Fatalf("RequestID = %+v", context.RequestID)
	}
	if context.AuthErrorCode == nil || *context.AuthErrorCode != "token_expired" {
		t.Fatalf("AuthErrorCode = %+v", context.AuthErrorCode)
	}
}

func TestTelemetryMessagesOmitHTTPBody(t *testing.T) {
	transport := &ResponseTransportError{Kind: ResponseTransportHTTP, Status: 401, Message: "secret body"}
	if got := TelemetryResponseTransportErrorMessage(transport); got != "http 401" {
		t.Fatalf("TelemetryResponseTransportErrorMessage() = %q", got)
	}
	api := &ResponseAPIError{Kind: ResponseAPITransport, Transport: transport}
	if got := TelemetryResponseAPIErrorMessage(api); got != "http 401" {
		t.Fatalf("TelemetryResponseAPIErrorMessage() = %q", got)
	}
}
