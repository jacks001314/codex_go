package model

import "testing"

// TestGatewayOAuthCredentialsStoreConfigLikeRust covers the provider config
// surface of Rust #58219: `gateway_oauth.credentials_store` accepts `keyring`
// and `file`, defaults to the keyring-backed store, and rejects other values.
// Rust represents the default as the `Keyring` variant and skips serializing
// it, so an explicit `keyring` is normalized to the empty default here.
func TestGatewayOAuthCredentialsStoreConfigLikeRust(t *testing.T) {
	base := map[string]any{
		"authorization_url": "https://issuer.example/authorize",
		"token_url":         "https://issuer.example/token",
		"client_id":         "client",
		"redirect_port":     int64(1455),
	}
	cases := []struct {
		name      string
		raw       any
		want      string
		wantError bool
	}{
		{name: "default", want: ""},
		{name: "keyring", raw: "keyring", want: ""},
		{name: "file", raw: "file", want: "file"},
		{name: "unknown", raw: "vault", wantError: true},
		{name: "not a string", raw: int64(1), wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]any{}
			for key, value := range base {
				values[key] = value
			}
			if tc.raw != nil {
				values["credentials_store"] = tc.raw
			}
			parsed, err := gatewayOAuthConfigFromConfig(values)
			if tc.wantError {
				if err == nil {
					t.Fatalf("gatewayOAuthConfigFromConfig() accepted %#v", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("gatewayOAuthConfigFromConfig() error = %v", err)
			}
			if parsed.CredentialsStore != tc.want {
				t.Fatalf("CredentialsStore = %q, want %q", parsed.CredentialsStore, tc.want)
			}
			if tc.want == "" {
				// The default store stays out of the serialized provider identity.
				reparsed, err := ProviderInfoFromConfig(map[string]any{
					"name":          "gateway",
					"base_url":      "https://gateway.example/v1",
					"gateway_oauth": values,
				})
				if err != nil {
					t.Fatalf("ProviderInfoFromConfig() error = %v", err)
				}
				if reparsed.GatewayOAuth == nil || reparsed.GatewayOAuth.CredentialsStore != "" {
					t.Fatalf("gateway config = %#v", reparsed.GatewayOAuth)
				}
			}
		})
	}
}
