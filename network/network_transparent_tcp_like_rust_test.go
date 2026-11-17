package network

import (
	"strings"
	"testing"
)

// TestProxySettingsParsesTransparentTCPLikeRust pins Rust #61851: the
// transparent_tcp switch is part of the network proxy table, the standalone
// config, and the prepared sandbox context.
func TestProxySettingsParsesTransparentTCPLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	if err := applyProxySettingsTable(&settings, map[string]any{
		"enabled":         true,
		"enable_socks5":   true,
		"transparent_tcp": true,
	}, "features.network_proxy"); err != nil {
		t.Fatalf("applyProxySettingsTable() error = %v", err)
	}
	if !settings.TransparentTCP {
		t.Fatal("transparent_tcp was not parsed")
	}
	prepared := PrepareProxyManagedNetwork(nil, MustProxyTCPAddr("127.0.0.1", 3128), MustProxyTCPAddr("127.0.0.1", 1080), true, settings)
	if !prepared.SandboxContext.TransparentTCP {
		t.Fatal("prepared sandbox context did not carry transparent_tcp")
	}
	if !prepared.SandboxContextSnapshot().TransparentTCP {
		t.Fatal("sandbox context snapshot dropped transparent_tcp")
	}

	standalone, err := ParseStandaloneProxyConfig([]byte(`{"network":{"enabled":true,"transparent_tcp":true}}`))
	if err != nil {
		t.Fatalf("ParseStandaloneProxyConfig() error = %v", err)
	}
	if !standalone.Network.TransparentTCP {
		t.Fatal("standalone config did not parse transparent_tcp")
	}
}

// TestProxyRequirementsOverrideTransparentTCPLikeRust pins the managed
// requirements override from Rust #61851 network_proxy_spec requirements.
func TestProxyRequirementsOverrideTransparentTCPLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.TransparentTCP = true
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, &ProxyRequirements{TransparentTCP: boolPtr(false)}, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	if spec.Config().Network.TransparentTCP {
		t.Fatal("managed requirements did not disable transparent_tcp")
	}

	settings = DefaultProxySettings()
	settings.Enabled = true
	spec, err = NewProxySpec(ProxyConfig{Network: settings}, &ProxyRequirements{TransparentTCP: boolPtr(true)}, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	if !spec.Config().Network.TransparentTCP {
		t.Fatal("managed requirements did not enable transparent_tcp")
	}
}

// TestTransparentTCPRequiresManagedSOCKS5LikeRust pins the Rust #61851
// build_config_state guard.
func TestTransparentTCPRequiresManagedSOCKS5LikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.TransparentTCP = true
	settings.EnableSocks5 = false
	_, err := NewProxySpec(ProxyConfig{Network: settings}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "transparent_tcp requires an enabled managed SOCKS5 proxy") {
		t.Fatalf("NewProxySpec() error = %v, want the managed SOCKS5 requirement", err)
	}

	settings.EnableSocks5 = true
	settings.Enabled = false
	_, err = NewProxySpec(ProxyConfig{Network: settings}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "transparent_tcp requires an enabled managed SOCKS5 proxy") {
		t.Fatalf("NewProxySpec() error = %v, want the managed SOCKS5 requirement", err)
	}
}

// TestEnvironmentNetworkPolicyTransparentTCPLikeRust pins the attachment
// policy OR from Rust #61851 environment_policy.
func TestEnvironmentNetworkPolicyTransparentTCPLikeRust(t *testing.T) {
	policy := EnvironmentNetworkPolicyFromConfig(&Config{Enabled: true, TransparentTCP: true}, false)
	if !policy.TransparentTCP {
		t.Fatal("policy did not inherit transparent_tcp")
	}
	config := &Config{}
	policy.ApplyTo(config)
	if !config.TransparentTCP {
		t.Fatal("attachment policy did not enable transparent_tcp")
	}
	config = &Config{TransparentTCP: true}
	(&EnvironmentNetworkPolicy{}).ApplyTo(config)
	if !config.TransparentTCP {
		t.Fatal("empty attachment policy disabled controller capture")
	}
}
