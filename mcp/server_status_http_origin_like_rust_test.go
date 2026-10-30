package mcp

import (
	"encoding/json"
	"testing"
)

// Mirrors Rust #47159: McpServerStatus carries the effective endpoint's HTTP(S)
// origin, excluding credentials, path, query and fragment; stdio and non-HTTP
// transports serialize null.
func TestMCPServerStatusHTTPOriginLikeRust(t *testing.T) {
	service := NewMCPService(nil)
	for name, config := range map[string]*ServerConfig{
		"default-port":  {URL: "https://user:secret@Example.COM:443/mcp?token=1#frag", Enabled: true},
		"explicit-port": {URL: "http://127.0.0.1:8080/mcp?q=1", Enabled: true},
		"stdio":         {Command: "docs-mcp", Args: []string{"--stdio"}, Enabled: true},
		"non-http":      {URL: "ftp://example.com/mcp", Enabled: true},
		"invalid":       {URL: "://not-a-url", Enabled: true},
	} {
		service.SetServerConfig(name, config)
	}
	response := service.ListStatus(nil)
	origins := map[string]*string{}
	for _, status := range response.Data {
		origins[status.Name] = status.HTTPOrigin
	}
	wants := map[string]*string{
		"default-port":  stringPointerForMCPTest("https://example.com"),
		"explicit-port": stringPointerForMCPTest("http://127.0.0.1:8080"),
		"stdio":         nil,
		"non-http":      nil,
		"invalid":       nil,
	}
	for name, want := range wants {
		got := origins[name]
		if want == nil {
			if got != nil {
				t.Fatalf("%s httpOrigin = %q, want null", name, *got)
			}
			continue
		}
		if got == nil || *got != *want {
			t.Fatalf("%s httpOrigin = %v, want %q", name, got, *want)
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, status := range payload.Data {
		value, ok := status["httpOrigin"]
		if !ok {
			t.Fatalf("status %v omitted httpOrigin", status["name"])
		}
		if status["name"] == "stdio" && value != nil {
			t.Fatalf("stdio httpOrigin = %v, want null", value)
		}
	}
}

func stringPointerForMCPTest(value string) *string { return &value }
