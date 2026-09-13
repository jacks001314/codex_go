package appserver

import (
	"testing"

	"codex_go/config"
)

func managedNetworkOpenAIHostValues(baseURL any) map[string]any {
	values := map[string]any{
		"features": map[string]any{"network_proxy": map[string]any{
			"enabled":       true,
			"proxy_url":     "http://127.0.0.1:0",
			"enable_socks5": false,
		}},
	}
	if baseURL != nil {
		values["openai_base_url"] = baseURL
	}
	return values
}

// The trusted configured openai_base_url becomes the credential broker's
// configured OpenAI host (Rust
// set_credential_broker_openai_base_url(cfg.openai_base_url)).
func TestManagedNetworkBindsConfiguredOpenAIHostLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	router := &RuntimeRouter{services: RuntimeServices{
		Config:     config.NewConfigService(home),
		DefaultCWD: cwd,
	}}

	proxyConfig, shouldStart, err := router.buildManagedNetworkProxyConfigForCWD(
		managedNetworkOpenAIHostValues("https://gateway.example/v1"), cwd)
	if err != nil || !shouldStart {
		t.Fatalf("build error = %v shouldStart = %v", err, shouldStart)
	}
	if got := proxyConfig.Network.CredentialBrokerOpenAIHost; got != "gateway.example" {
		t.Fatalf("configured OpenAI host = %q", got)
	}

	// An untrusted (plaintext) base URL contributes no configured host, matching
	// trusted_credential_broker_host returning None.
	proxyConfig, _, err = router.buildManagedNetworkProxyConfigForCWD(
		managedNetworkOpenAIHostValues("http://gateway.example/v1"), cwd)
	if err != nil {
		t.Fatalf("untrusted build error = %v", err)
	}
	if got := proxyConfig.Network.CredentialBrokerOpenAIHost; got != "" {
		t.Fatalf("untrusted configured OpenAI host = %q", got)
	}

	// Without the key nothing is derived.
	proxyConfig, _, err = router.buildManagedNetworkProxyConfigForCWD(managedNetworkOpenAIHostValues(nil), cwd)
	if err != nil {
		t.Fatalf("absent build error = %v", err)
	}
	if got := proxyConfig.Network.CredentialBrokerOpenAIHost; got != "" {
		t.Fatalf("absent configured OpenAI host = %q", got)
	}
}

// The host is only derived when the proxy is active for the effective profile
// (Rust sets it inside the feature-enabled branch); a disabled proxy keeps the
// default, empty setting.
func TestManagedNetworkConfiguredOpenAIHostRequiresEnabledProxy(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	router := &RuntimeRouter{services: RuntimeServices{
		Config:     config.NewConfigService(home),
		DefaultCWD: cwd,
	}}
	values := managedNetworkOpenAIHostValues("https://gateway.example/v1")
	values["features"] = map[string]any{"network_proxy": map[string]any{"enabled": false}}
	proxyConfig, shouldStart, err := router.buildManagedNetworkProxyConfigForCWD(values, cwd)
	if err != nil {
		t.Fatalf("build error = %v", err)
	}
	if shouldStart {
		t.Fatal("a disabled proxy should not start")
	}
	if got := proxyConfig.Network.CredentialBrokerOpenAIHost; got != "" {
		t.Fatalf("disabled hosted proxy set the configured OpenAI host = %q", got)
	}
}
