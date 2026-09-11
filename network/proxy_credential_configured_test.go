package network

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func configuredTestProvider(t *testing.T, config CredentialProviderConfig) *ProxyCredentialProvider {
	t.Helper()
	provider := ConfiguredCredentialProvider("vendor", config)
	if provider == nil {
		t.Fatal("ConfiguredCredentialProvider returned nil")
	}
	return provider
}

// TestConfiguredCredentialProviderBuildsRuntimeProvider mirrors Rust
// ConfiguredCredentialProvider: pattern-matching dummies, destination host
// binding, and the supported auth translations.
func TestConfiguredCredentialProviderBuildsRuntimeProvider(t *testing.T) {
	config := CredentialProviderConfig{
		Env:         []string{"VENDOR_TOKEN"},
		Patterns:    []string{"vk-[a-z0-9]{16}"},
		URLPrefixes: []string{"https://api.vendor.example/v1"},
		Auth:        []CredentialAuthMethod{CredentialAuthBearer},
	}
	provider := configuredTestProvider(t, config)
	if !reflect.DeepEqual(provider.ContextEnvVars, []string{"VENDOR_TOKEN"}) {
		t.Fatalf("context env vars = %#v", provider.ContextEnvVars)
	}
	if len(provider.Destinations) != 1 || provider.Destinations[0].Host != "api.vendor.example" || provider.Destinations[0].PathPrefix != "/v1" {
		t.Fatalf("destinations = %#v", provider.Destinations)
	}
	if binding, ok := provider.Sources[0].HostBinding(map[string]string{}); !ok || !reflect.DeepEqual(binding.ExactHosts, []string{"api.vendor.example"}) {
		t.Fatalf("host binding = %#v/%v", binding, ok)
	}
	dummy := provider.DummyValue("vk-0123456789abcdef")
	if dummy == "" || dummy == "vk-0123456789abcdef" || len(dummy) != len("vk-0123456789abcdef") {
		t.Fatalf("dummy = %q", dummy)
	}
	if matches, err := credentialPatternMatches("vk-[a-z0-9]{16}", dummy); err != nil || !matches {
		t.Fatalf("dummy %q does not match pattern: %v/%v", dummy, matches, err)
	}
	if value, ok := provider.RequestHeaderValue("vk-abc"); !ok || value != "Bearer vk-abc" {
		t.Fatalf("bearer header value = %q/%v", value, ok)
	}
	headers := map[string][]string{}
	provider.InsertHeader(headers, "Bearer vk-dummy")
	if got := headers["Authorization"]; len(got) != 1 || got[0] != "Bearer vk-dummy" {
		t.Fatalf("inserted headers = %#v", headers)
	}
	if value, ok := provider.RequestHeader(map[string][]string{"AUTHORIZATION": {"Bearer vk-dummy"}}); !ok || value != "Bearer vk-dummy" {
		t.Fatalf("request header = %q/%v", value, ok)
	}
}

func TestConfiguredCredentialProviderAuthTranslationsLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		config CredentialProviderConfig
		want   string
	}{
		{
			name:   "token",
			config: CredentialProviderConfig{Env: []string{"V"}, Patterns: []string{"v-[a-z]+"}, URLPrefixes: []string{"api.vendor.example"}, Auth: []CredentialAuthMethod{CredentialAuthToken}},
			want:   "token abc",
		},
		{
			name:   "basic",
			config: CredentialProviderConfig{Env: []string{"V"}, Patterns: []string{"v-[a-z]+"}, URLPrefixes: []string{"api.vendor.example"}, Auth: []CredentialAuthMethod{CredentialAuthBasic}},
			want:   "Basic " + base64.StdEncoding.EncodeToString([]byte("abc")),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := configuredTestProvider(t, tc.config)
			value, ok := provider.RequestHeaderValue("abc")
			if !ok || value != tc.want {
				t.Fatalf("header value = %q/%v, want %q", value, ok, tc.want)
			}
		})
	}

	headerName := "X-Vendor-Token"
	prefix := "Token "
	config := CredentialProviderConfig{
		Env:         []string{"V"},
		Patterns:    []string{"v-[a-z]+"},
		URLPrefixes: []string{"api.vendor.example"},
		Auth:        []CredentialAuthMethod{CredentialAuthHeader},
		Header:      &headerName,
		Prefix:      &prefix,
	}
	provider := configuredTestProvider(t, config)
	if value, ok := provider.RequestHeaderValue("abc"); !ok || value != "Token abc" {
		t.Fatalf("custom header value = %q/%v", value, ok)
	}
	headers := map[string][]string{}
	provider.InsertHeader(headers, "Token abc")
	if got := headers["X-Vendor-Token"]; len(got) != 1 || got[0] != "Token abc" {
		t.Fatalf("custom headers = %#v", headers)
	}
	if _, ok := provider.RequestHeaderValue("bad\nvalue"); ok {
		t.Fatal("invalid header value should be rejected")
	}
}

// TestConfiguredCredentialProviderDynamicDestination mirrors Rust
// dynamic_destination / host_binding: url_prefix_from_env contributes an
// additional authorized host at env virtualization time.
func TestConfiguredCredentialProviderDynamicDestination(t *testing.T) {
	envKey := "VENDOR_API_BASE"
	config := CredentialProviderConfig{
		Env:              []string{"VENDOR_TOKEN"},
		Patterns:         []string{"v-[a-z]+"},
		URLPrefixes:      []string{"https://api.vendor.example"},
		URLPrefixFromEnv: &envKey,
		Auth:             []CredentialAuthMethod{CredentialAuthBearer},
	}
	provider := configuredTestProvider(t, config)
	binding, ok := provider.Sources[0].HostBinding(map[string]string{envKey: "tenant.vendor.example"})
	if !ok {
		t.Fatal("dynamic host binding missing")
	}
	if !strings.Contains(strings.Join(binding.ExactHosts, ","), "tenant.vendor.example") {
		t.Fatalf("dynamic hosts = %#v", binding.ExactHosts)
	}
}
