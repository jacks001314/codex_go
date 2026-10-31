package mcp

import "testing"

// TestMCPOAuthCallbackIDMatchesRustURLSerialization pins the hash input to the
// url crate's serialization: lowercase scheme and host, no default port, the "/"
// root path and no fragment. The 0.166 binary produces exactly these IDs for
// `codex mcp add --url ... --oauth-client-id c`.
func TestMCPOAuthCallbackIDMatchesRustURLSerialization(t *testing.T) {
	for _, test := range []struct {
		serverURL string
		want      string
	}{
		{"https://a", "q5hkBE_CdBkI"},
		{"https://A.Example", "vv3kmKRbbIII"},
		{"https://a:443/mcp", "CwkrpmlhFpeS"},
		{"http://a:80/mcp", "1oX_iem5GZNa"},
		{"https://mcp.example.com/mcp?tenant=one", "XuuuHAzzHOni"},
		{"https://a#fragment", "q5hkBE_CdBkI"},
	} {
		got, err := MCPOAuthCallbackID(test.serverURL)
		if err != nil {
			t.Fatalf("MCPOAuthCallbackID(%q) error = %v", test.serverURL, err)
		}
		if got != test.want {
			t.Fatalf("MCPOAuthCallbackID(%q) = %q, want %q", test.serverURL, got, test.want)
		}
	}
}
