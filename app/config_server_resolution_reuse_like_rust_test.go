package app

import (
	"context"
	"sync/atomic"
	"testing"

	"codex_go/config"
)

func configServerReuseConfig(respectSystemProxy bool, systemProxyFallback *bool) *config.Config {
	features := map[string]any{"respect_system_proxy": respectSystemProxy}
	if systemProxyFallback != nil {
		features["system_proxy_fallback"] = *systemProxyFallback
	}
	return &config.Config{Values: map[string]any{"features": features}}
}

// Rust #63473: the resolved config-server settings are reused across sign-in,
// startup and managed-policy loading. A cached result only matches the same
// server, network policy and proxy settings, and onboarding drops it so the
// server is rediscovered.
func TestConfigServerResolutionReuseLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := discoveredConfigServer(t, &requests, false)
	defer server.Close()
	forgetConfigServerOAuthResolution(server.URL)

	cfg := configServerReuseConfig(false, nil)
	policy := publishApplicationNetworkPolicy(cfg)
	resolved, err := resolveConfigServerOAuthForConfig(context.Background(), &config.ConfigServer{BaseURL: server.URL}, server.Client(), &policy, cfg)
	if err != nil {
		t.Fatalf("first resolve error = %v", err)
	}
	if resolved == nil || resolved.OAuth == nil {
		t.Fatalf("resolved = %#v, want discovered OAuth", resolved)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("first resolve discovery requests = %d, want 1", got)
	}

	if _, err := resolveConfigServerOAuthForConfig(context.Background(), &config.ConfigServer{BaseURL: server.URL}, server.Client(), &policy, cfg); err != nil {
		t.Fatalf("second resolve error = %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("reused resolve discovery requests = %d, want 1", got)
	}

	systemProxyConfig := configServerReuseConfig(true, nil)
	systemProxyPolicy := publishApplicationNetworkPolicy(systemProxyConfig)
	if _, err := resolveConfigServerOAuthForConfig(context.Background(), &config.ConfigServer{BaseURL: server.URL}, server.Client(), &systemProxyPolicy, systemProxyConfig); err != nil {
		t.Fatalf("system-proxy resolve error = %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("system-proxy change discovery requests = %d, want 2", got)
	}

	disabled := false
	fallbackConfig := configServerReuseConfig(true, &disabled)
	fallbackPolicy := publishApplicationNetworkPolicy(fallbackConfig)
	if _, err := resolveConfigServerOAuthForConfig(context.Background(), &config.ConfigServer{BaseURL: server.URL}, server.Client(), &fallbackPolicy, fallbackConfig); err != nil {
		t.Fatalf("fallback resolve error = %v", err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("system-proxy fallback change discovery requests = %d, want 3", got)
	}

	forgetConfigServerOAuthResolution(server.URL)
	if _, err := resolveConfigServerOAuthForConfig(context.Background(), &config.ConfigServer{BaseURL: server.URL}, server.Client(), &policy, cfg); err != nil {
		t.Fatalf("rediscover error = %v", err)
	}
	if got := requests.Load(); got != 4 {
		t.Fatalf("rediscovery requests after onboarding = %d, want 4", got)
	}
}
