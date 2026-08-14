package mcp

import "testing"

// TestRuntimeConfigOAuthCallbackPortParsesPerServerMatchesRust mirrors Rust
// #38448: a per-server OAuth callback port can be configured either at the
// server top level or nested under oauth.
func TestRuntimeConfigOAuthCallbackPortParsesPerServerMatchesRust(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]any
		want   uint16
	}{
		{
			name:   "top-level",
			values: map[string]any{"oauth_callback_port": float64(43210)},
			want:   43210,
		},
		{
			name:   "nested",
			values: map[string]any{"oauth": map[string]any{"callback_port": float64(54321)}},
			want:   54321,
		},
		{
			name:   "camel-case",
			values: map[string]any{"oauthCallbackPort": float64(12345)},
			want:   12345,
		},
		{
			name:   "missing",
			values: map[string]any{},
			want:   0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runtimeConfigOAuthCallbackPort(test.values); got != test.want {
				t.Fatalf("runtimeConfigOAuthCallbackPort(%#v) = %d, want %d", test.values, got, test.want)
			}
		})
	}
}
