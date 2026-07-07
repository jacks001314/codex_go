package network

import (
	"net/http"
	"testing"
)

func TestBlockedMessagesAndHeaders(t *testing.T) {
	cases := map[string]struct {
		header  string
		message string
	}{
		ProxyReasonNotAllowed:       {"blocked-by-allowlist", "Domain not in allowlist."},
		ProxyReasonNotAllowedLocal:  {"blocked-by-allowlist", "Sandbox policy blocks local/private network addresses."},
		ProxyReasonDenied:           {"blocked-by-denylist", "Domain denied by the sandbox policy."},
		ProxyReasonMethodNotAllowed: {"blocked-by-method-policy", "Method not allowed in limited mode."},
		ProxyReasonMITMHookDenied:   {"blocked-by-mitm-hook", "HTTPS request denied by MITM hook policy."},
		ProxyReasonMITMRequired:     {"blocked-by-mitm-required", "MITM required for limited HTTPS."},
		ProxyReasonProxyDisabled:    {"blocked-by-policy", "network proxy is disabled"},
	}
	for reason, want := range cases {
		if got := ProxyBlockedHeaderValue(reason); got != want.header {
			t.Fatalf("ProxyBlockedHeaderValue(%q) = %q", reason, got)
		}
		if got := ProxyBlockedMessage(reason); got != want.message {
			t.Fatalf("ProxyBlockedMessage(%q) = %q", reason, got)
		}
	}
	response := ProxyBlockedTextResponse(ProxyReasonDenied)
	if response.Status != http.StatusForbidden || response.Headers["x-proxy-error"] != "blocked-by-denylist" {
		t.Fatalf("blocked response = %#v", response)
	}
}

func TestProxyJSONResponse(t *testing.T) {
	response := ProxyJSONResponse(map[string]string{"ok": "yes"})
	if response.Status != http.StatusOK || response.Headers["content-type"] != "application/json" {
		t.Fatalf("json response = %#v", response)
	}
	if response.Body != `{"ok":"yes"}` {
		t.Fatalf("json body = %q", response.Body)
	}
}

func TestBlockedMessageWithPolicy(t *testing.T) {
	details := ProxyPolicyDecisionDetails{
		Decision: ProxyPolicyDecisionAsk,
		Reason:   ProxyReasonNotAllowed,
		Source:   ProxyDecisionSourceDecider,
		Protocol: ProxyProtocolHTTPSConnect,
		Host:     "api.example.com",
		Port:     443,
	}
	if got := ProxyBlockedMessageWithPolicy(ProxyReasonNotAllowed, details); got != "Domain not in allowlist." {
		t.Fatalf("message = %q", got)
	}
}
