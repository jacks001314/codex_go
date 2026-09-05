package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMCPErrAuthenticationRequired(t *testing.T) {
	if mcpErrAuthenticationRequired(nil) {
		t.Fatal("nil error reported as authentication failure")
	}
	if !mcpErrAuthenticationRequired(&mcpHTTPStatusError{StatusCode: 401}) {
		t.Fatal("401 not reported as authentication failure")
	}
	if mcpErrAuthenticationRequired(&mcpHTTPStatusError{StatusCode: 500}) {
		t.Fatal("500 reported as authentication failure")
	}
	if !mcpErrAuthenticationRequired(fmtWrap(errors.New("wrapped"), &mcpHTTPStatusError{StatusCode: 407})) {
		t.Fatal("wrapped 407 not reported as authentication failure")
	}
}

func TestMCPInitAuthErrorDisplayMatchesRust(t *testing.T) {
	localOAuth := &ServerConfig{URL: "https://example.test/mcp", EnvironmentID: DefaultMCPServerEnvironmentID, Auth: ServerAuthOAuth}
	if display, ok := mcpInitAuthErrorDisplay("docs", localOAuth, MCPAuthOAuth, &mcpHTTPStatusError{StatusCode: 401}); !ok ||
		!strings.Contains(display, "requires OAuth reauthentication") ||
		!strings.Contains(display, "Run `codex mcp login docs`.") {
		t.Fatalf("local OAuth display = %q, ok=%v", display, ok)
	}

	local := &ServerConfig{URL: "https://example.test/mcp", EnvironmentID: DefaultMCPServerEnvironmentID}
	if display, ok := mcpInitAuthErrorDisplay("docs", local, MCPAuthNotLoggedIn, &mcpHTTPStatusError{StatusCode: 401}); !ok ||
		!strings.Contains(display, "is not logged in") {
		t.Fatalf("local display = %q, ok=%v", display, ok)
	}

	remote := &ServerConfig{URL: "https://example.test/mcp", EnvironmentID: "customer-executor"}
	if display, ok := mcpInitAuthErrorDisplay("docs", remote, MCPAuthOAuth, &mcpHTTPStatusError{StatusCode: 401}); !ok ||
		!strings.Contains(display, "Use your client's MCP OAuth sign-in flow.") {
		t.Fatalf("remote display = %q, ok=%v", display, ok)
	}

	if display, ok := mcpInitAuthErrorDisplay("docs", local, MCPAuthOAuth, errors.New("inventory down")); ok || display != "" {
		t.Fatalf("non-auth error display = %q, ok=%v", display, ok)
	}
	if display, ok := mcpInitAuthErrorDisplay("docs", nil, MCPAuthNotLoggedIn, &mcpHTTPStatusError{StatusCode: 401}); !ok ||
		!strings.Contains(display, "Run `codex mcp login docs`.") {
		t.Fatalf("nil-config local display = %q, ok=%v", display, ok)
	}
}

func TestMCPServerStatusFailureReasonWire(t *testing.T) {
	reason := "reauthenticationRequired"
	status := MCPServerStatus{Name: "docs", FailureReason: &reason}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal status error = %v", err)
	}
	if !strings.Contains(string(raw), `"failureReason":"reauthenticationRequired"`) {
		t.Fatalf("status JSON = %s", raw)
	}
	cloned := cloneMCPServerStatus(status)
	if cloned.FailureReason == nil || *cloned.FailureReason != "reauthenticationRequired" {
		t.Fatalf("cloned FailureReason = %#v", cloned.FailureReason)
	}
	plain, err := json.Marshal(MCPServerStatus{Name: "docs"})
	if err != nil || strings.Contains(string(plain), "failureReason") {
		t.Fatalf("plain status JSON should omit failureReason: %s, %v", plain, err)
	}
	if cloned := cloneMCPServerStatus(MCPServerStatus{Name: "docs"}); cloned.FailureReason != nil {
		t.Fatalf("cloned nil FailureReason = %#v", cloned.FailureReason)
	}
}

func fmtWrap(err error, status *mcpHTTPStatusError) error {
	return errors.Join(err, status)
}

func TestMCPHTTPStatusErrorCombinedWWWAuthenticate(t *testing.T) {
	err := &mcpHTTPStatusError{StatusCode: 401, WWWAuthenticate: []string{`Bearer realm="a"`, `Basic realm="b"`}}
	if got := err.CombinedWWWAuthenticate(); got != `Bearer realm="a", Basic realm="b"` {
		t.Fatalf("CombinedWWWAuthenticate = %q", got)
	}
	if got := (&mcpHTTPStatusError{StatusCode: 401}).CombinedWWWAuthenticate(); got != "" {
		t.Fatalf("empty challenges = %q", got)
	}
}

func TestMCPAuthenticationChallengeToolOutput(t *testing.T) {
	output, ok := mcpAuthenticationChallengeToolOutput(&mcpHTTPStatusError{StatusCode: 401, WWWAuthenticate: []string{`Bearer realm="a"`}})
	if !ok || output == nil || output.Success || output.Data["mcp/www_authenticate"] != `Bearer realm="a"` {
		t.Fatalf("challenge output = %#v, ok=%v", output, ok)
	}
	if _, ok := mcpAuthenticationChallengeToolOutput(&mcpHTTPStatusError{StatusCode: 500}); ok {
		t.Fatal("non-401 should not produce a challenge tool error")
	}
	if _, ok := mcpAuthenticationChallengeToolOutput(&mcpHTTPStatusError{StatusCode: 401}); ok {
		t.Fatal("401 without WWW-Authenticate should not produce a challenge tool error")
	}
}
