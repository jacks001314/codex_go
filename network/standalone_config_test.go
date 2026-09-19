package network

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust `enables_mitm_for_limited_mode_and_hooks` (#46573).
func TestStandaloneProxyConfigEnablesMITMForLimitedModeAndHooks(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		mitm bool
	}{
		{name: "full mode", body: map[string]any{"enabled": true, "mode": "full"}},
		{name: "explicit mitm", body: map[string]any{"enabled": true, "mode": "full", "mitm": true}, mitm: true},
		{name: "limited mode", body: map[string]any{"enabled": true, "mode": "limited"}, mitm: true},
		{
			name: "configured hooks",
			body: map[string]any{"enabled": true, "mitm_hooks": []any{map[string]any{"host": "api.example.com"}}},
			mitm: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"network": testCase.body})
			if err != nil {
				t.Fatalf("marshal config: %v", err)
			}
			config, err := ParseStandaloneProxyConfig(data)
			if err != nil {
				t.Fatalf("ParseStandaloneProxyConfig error = %v", err)
			}
			if config.Network.MITM != testCase.mitm {
				t.Fatalf("MITM = %v, want %v", config.Network.MITM, testCase.mitm)
			}
			if !config.Network.Enabled {
				t.Fatal("enabled = false, want true")
			}
		})
	}
}

// Mirrors Rust `accepts_dynamic_domain_socket_and_hook_matcher_keys`.
func TestStandaloneProxyConfigAcceptsDynamicMatcherKeys(t *testing.T) {
	data := []byte(`{
		"network": {
			"enabled": true,
			"domains": {"api.example.com": "allow"},
			"unix_sockets": {"/tmp/example.sock": "allow"},
			"mitm_hooks": [{
				"host": "api.example.com",
				"match": {
					"query": {"scope": ["read"]},
					"headers": {"authorization": ["Bearer token"]}
				}
			}]
		}
	}`)
	config, err := ParseStandaloneProxyConfig(data)
	if err != nil {
		t.Fatalf("ParseStandaloneProxyConfig error = %v", err)
	}
	if allowed := config.Network.AllowedDomains(); len(allowed) != 1 || allowed[0] != "api.example.com" {
		t.Fatalf("allowed domains = %#v", allowed)
	}
	if sockets := config.Network.AllowUnixSockets(); len(sockets) != 1 || sockets[0] != "/tmp/example.sock" {
		t.Fatalf("allowed unix sockets = %#v", sockets)
	}
	if !config.Network.MITM || len(config.Network.MITMHooks) != 1 {
		t.Fatalf("hooks = %#v mitm=%v", config.Network.MITMHooks, config.Network.MITM)
	}
}

// Mirrors Rust `rejects_unknown_fields_at_every_mitm_hook_level`.
func TestStandaloneProxyConfigRejectsUnknownFieldsAtEveryLevel(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		unknown string
	}{
		{
			name:    "network",
			body:    `{"enabled": true, "unexpected_network": true}`,
			unknown: "unexpected_network",
		},
		{
			name:    "hook",
			body:    `{"enabled": true, "mitm_hooks": [{"host": "api.example.com", "unexpected_hook": true}]}`,
			unknown: "unexpected_hook",
		},
		{
			name:    "matcher",
			body:    `{"enabled": true, "mitm_hooks": [{"host": "api.example.com", "match": {"unexpected_matcher": true}}]}`,
			unknown: "unexpected_matcher",
		},
		{
			name:    "actions",
			body:    `{"enabled": true, "mitm_hooks": [{"host": "api.example.com", "actions": {"unexpected_action": true}}]}`,
			unknown: "unexpected_action",
		},
		{
			name:    "injected header",
			body:    `{"enabled": true, "mitm_hooks": [{"host": "api.example.com", "actions": {"inject_request_headers": [{"name": "authorization", "unexpected_header": true}]}}]}`,
			unknown: "unexpected_header",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			data := []byte(`{"network": ` + testCase.body + `}`)
			_, err := ParseStandaloneProxyConfig(data)
			if err == nil {
				t.Fatalf("unknown field %q was accepted", testCase.unknown)
			}
			if !strings.Contains(err.Error(), testCase.unknown) {
				t.Fatalf("error %q does not name %q", err, testCase.unknown)
			}
		})
	}
}

// Mirrors Rust `rejects_trailing_json_values`.
func TestStandaloneProxyConfigRejectsTrailingJSONValues(t *testing.T) {
	_, err := ParseStandaloneProxyConfig([]byte(`{"network":{"enabled":true}} {}`))
	if err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing values error = %v", err)
	}
}

func TestStandaloneProxyConfigRequiresEnabledAndBoundsSize(t *testing.T) {
	if _, err := ParseStandaloneProxyConfig([]byte(`{"network":{}}`)); err == nil || !strings.Contains(err.Error(), "network.enabled = true") {
		t.Fatalf("disabled config error = %v", err)
	}
	oversized := make([]byte, MaxStandaloneProxyConfigBytes+1)
	copy(oversized, `{"network":{"enabled":true}}`)
	if _, err := ParseStandaloneProxyConfig(oversized); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized config error = %v", err)
	}
}

// Mirrors Rust `wait_fails_fast_when_either_listener_fails` (#46573): a
// listener that fails surfaces through Wait instead of hanging.
func TestStandaloneProxyWaitFailsFastWhenListenerFails(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := StartStandaloneProxy(ctx, ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatalf("StartStandaloneProxy error = %v", err)
	}
	// Closing the HTTP listener makes Serve return, which must stop the other
	// listener and fail Wait promptly.
	if err := server.httpListener.Close(); err != nil {
		t.Fatalf("close http listener: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	select {
	case err := <-done:
		if err == nil || err.Error() != "http proxy listener failed" {
			t.Fatalf("Wait error = %v, want the HTTP listener failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not fail fast after a listener failure")
	}
	_ = server.Close()
}
