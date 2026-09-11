package network

import "testing"

// TestConfiguredCredentialBrokerVirtualizesAndInjectsLikeRust is the
// end-to-end check for the #44056 runtime half: a configured provider's env var
// is replaced with a pattern-matching dummy, and the proxy substitutes the real
// value only for authorized destinations (scheme/host/port/path).
func TestConfiguredCredentialBrokerVirtualizesAndInjectsLikeRust(t *testing.T) {
	realValue := "vk-0123456789abcdef"
	provider := ConfiguredCredentialProvider("vendor", CredentialProviderConfig{
		Env:         []string{"VENDOR_TOKEN"},
		Patterns:    []string{"vk-[a-z0-9]{16}"},
		URLPrefixes: []string{"https://api.vendor.example/v1"},
		Auth:        []CredentialAuthMethod{CredentialAuthBearer},
	})
	if provider == nil {
		t.Fatal("ConfiguredCredentialProvider returned nil")
	}
	broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{provider})

	env := map[string]string{"VENDOR_TOKEN": realValue}
	broker.VirtualizeChildEnv(env)
	dummy := env["VENDOR_TOKEN"]
	if dummy == "" || dummy == realValue {
		t.Fatalf("virtualized env = %q", dummy)
	}
	if !broker.HostRequiresMITM("api.vendor.example") {
		t.Fatal("configured destination did not require MITM")
	}
	if broker.HostRequiresMITM("unrelated.example") {
		t.Fatal("unrelated host should not require MITM")
	}

	authorized := map[string][]string{"Authorization": {"Bearer " + dummy}}
	broker.InjectRequestHeadersForDestination("https", "api.vendor.example", 443, "/v1/accounts", authorized)
	if got := authorized["Authorization"]; len(got) != 1 || got[0] != "Bearer "+realValue {
		t.Fatalf("authorized injection = %#v", authorized)
	}

	cases := []struct {
		name   string
		scheme string
		host   string
		port   uint16
		path   string
	}{
		{name: "wrong path", scheme: "https", host: "api.vendor.example", port: 443, path: "/v2/accounts"},
		{name: "wrong port", scheme: "https", host: "api.vendor.example", port: 8443, path: "/v1/accounts"},
		{name: "wrong scheme", scheme: "http", host: "api.vendor.example", port: 80, path: "/v1/accounts"},
		{name: "wrong host", scheme: "https", host: "other.example", port: 443, path: "/v1/accounts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string][]string{"Authorization": {"Bearer " + dummy}}
			broker.InjectRequestHeadersForDestination(tc.scheme, tc.host, tc.port, tc.path, headers)
			if got := headers["Authorization"]; len(got) != 1 || got[0] != "Bearer "+dummy {
				t.Fatalf("unauthorized injection = %#v", headers)
			}
		})
	}
}
