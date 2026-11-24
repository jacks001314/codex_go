package network

import (
	"context"
	"strings"
	"testing"
)

// Rust #60046 NetworkProxy::validate_config_update: an in-place reload may
// replace the policy, but the settings that decided which listeners the running
// proxy bound must match so the process never silently changes endpoints.
func TestValidateProxyConfigUpdateKeepsRunningListenersLikeRust(t *testing.T) {
	base := DefaultProxySettings()
	base.Enabled = true
	base.ProxyURL = "http://127.0.0.1:0"
	base.EnableSocks5 = true
	base.SocksURL = "socks5://127.0.0.1:0"

	if err := validateProxyConfigUpdate(base, base); err != nil {
		t.Fatalf("unchanged settings rejected: %v", err)
	}

	policyOnly := base
	policyOnly.Mode = ProxyModeLimited
	policyOnly.SetAllowedDomains([]string{"example.com"})
	policyOnly.ReviewDomains = []string{"review.example.com"}
	if err := validateProxyConfigUpdate(base, policyOnly); err != nil {
		t.Fatalf("policy-only reload rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*ProxySettings)
		want   string
	}{
		{name: "enabled", mutate: func(settings *ProxySettings) { settings.Enabled = false }, want: "network.enabled"},
		{name: "proxy url", mutate: func(settings *ProxySettings) { settings.ProxyURL = "http://127.0.0.1:1" }, want: "network.proxy_url"},
		{name: "socks url", mutate: func(settings *ProxySettings) { settings.SocksURL = "socks5://127.0.0.1:1" }, want: "network.socks_url"},
		{name: "socks", mutate: func(settings *ProxySettings) { settings.EnableSocks5 = false }, want: "network.enable_socks5"},
		{name: "socks udp", mutate: func(settings *ProxySettings) { settings.EnableSocks5UDP = false }, want: "network.enable_socks5_udp"},
	}
	for _, testCase := range cases {
		next := base
		testCase.mutate(&next)
		err := validateProxyConfigUpdate(base, next)
		if err == nil {
			t.Fatalf("%s change accepted, want a running-proxy error", testCase.name)
		}
		if !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s error = %q, want it to name %s", testCase.name, err, testCase.want)
		}
	}
}

// The running server routes its reloads through the same check, so a listener
// change is rejected before any policy state is replaced.
func TestProxyServerReloadRejectsListenerChangesLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.EnableSocks5 = false
	settings.AllowLocalBinding = true
	settings.SetAllowedDomains([]string{"example.com"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()

	disabled := settings
	disabled.Enabled = false
	if err := prepared.ReloadConfig(ProxyConfig{Network: disabled}); err == nil || !strings.Contains(err.Error(), "network.enabled") {
		t.Fatalf("reload with a changed enabled flag = %v, want the running-proxy error", err)
	}

	policyOnly := settings
	policyOnly.SetAllowedDomains([]string{"example.com", "other.example"})
	if err := prepared.ReloadConfig(ProxyConfig{Network: policyOnly}); err != nil {
		t.Fatalf("policy-only reload failed: %v", err)
	}
}
