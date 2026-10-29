package network

import (
	"strings"
	"testing"
)

// TestStaticProxyConfigValidatesNativePlatformPolicyLikeRust pins Rust #46578:
// the standalone proxy builds its config state with Platform::native(), so the
// socket allowlist is validated for the host platform and the credential broker
// still requires MITM before any listener starts.
func TestStaticProxyConfigValidatesNativePlatformPolicyLikeRust(t *testing.T) {
	relative, err := FromStaticProxyConfigBytes([]byte(`{"network":{"enabled":true,"allow_unix_sockets":["relative.sock"]}}`))
	if err != nil {
		t.Fatalf("parse relative socket path: %v", err)
	}
	if err := relative.Validate(); err == nil || !strings.Contains(err.Error(), "network.allow_unix_sockets[0]") {
		t.Fatalf("relative socket path error = %v, want an allow_unix_sockets path rejection", err)
	}

	absolute, err := FromStaticProxyConfigBytes([]byte(`{"network":{"enabled":true,"allow_unix_sockets":["/var/run/codex.sock"]}}`))
	if err != nil {
		t.Fatalf("parse absolute socket path: %v", err)
	}
	if err := absolute.Validate(); err != nil {
		t.Fatalf("absolute socket path rejected: %v", err)
	}

	broker, err := FromStaticProxyConfigBytes([]byte(`{"network":{"enabled":true,"credential_broker":true}}`))
	if err != nil {
		t.Fatalf("parse credential broker: %v", err)
	}
	if err := broker.Validate(); err == nil || !strings.Contains(err.Error(), "network.credential_broker requires network.mitm = true") {
		t.Fatalf("credential broker without MITM error = %v", err)
	}
}
