package mcp

import (
	"errors"
	"testing"
)

func testToolRequestMetaRegistration() ResolvedServer {
	config := ServerConfig{
		URL:         "https://coordinator.example/mcp",
		Enabled:     true,
		HTTPHeaders: map[string]string{"x-route": "proxied"},
	}
	catalog := ResolveCatalog([]CatalogAction{{
		Name:   "coordinator",
		Source: CatalogSourceExtension,
		Config: config,
	}})
	return catalog.Servers["coordinator"]
}

// Mirrors Rust #58395: the hook adds its private value only for the exact
// server, tool, source and transport the host captured.
func TestMCPToolRequestMetaHookAddsValueForMatchingRegistrationLikeRust(t *testing.T) {
	registration := testToolRequestMetaRegistration()
	var providerThread, providerTurn, providerCall string
	hook := &MCPToolRequestMetaHook{
		ServerName: "coordinator",
		Source:     CatalogSourceExtension,
		Transport:  registration.Config,
		ToolName:   "submit",
		Key:        "x-codex-program",
		Provider: func(threadID string, turnID string, callID string) (any, error) {
			providerThread, providerTurn, providerCall = threadID, turnID, callID
			return "program-1", nil
		},
	}
	meta := map[string]any{"thread_id": "thread-a"}
	got, err := hook.Apply(meta, "coordinator", "submit", &registration, "thread-a", "turn-1", "call-1")
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	object, ok := got.(map[string]any)
	if !ok || object["x-codex-program"] != "program-1" {
		t.Fatalf("meta = %#v, want the provider value", got)
	}
	if providerThread != "thread-a" || providerTurn != "turn-1" || providerCall != "call-1" {
		t.Fatalf("provider args = %q/%q/%q", providerThread, providerTurn, providerCall)
	}
}

func TestMCPToolRequestMetaHookSkipsNonMatchingRegistrationLikeRust(t *testing.T) {
	registration := testToolRequestMetaRegistration()
	remote := registration
	remote.Config.EnvironmentID = "remote"
	sameNameOverride := registration
	sameNameOverride.Source = CatalogSourceConfig
	otherTransport := registration
	otherTransport.Config.Args = []string{"--other"}
	called := false
	hook := &MCPToolRequestMetaHook{
		ServerName: "coordinator",
		Source:     CatalogSourceExtension,
		Transport:  registration.Config,
		ToolName:   "submit",
		Key:        "x-codex-program",
		Provider: func(string, string, string) (any, error) {
			called = true
			return "program-1", nil
		},
	}
	tests := []struct {
		name         string
		serverName   string
		toolName     string
		registration *ResolvedServer
	}{
		{name: "other server", serverName: "other", toolName: "submit", registration: &registration},
		{name: "other tool", serverName: "coordinator", toolName: "read", registration: &registration},
		{name: "remote environment", serverName: "coordinator", toolName: "submit", registration: &remote},
		{name: "same name override", serverName: "coordinator", toolName: "submit", registration: &sameNameOverride},
		{name: "other transport", serverName: "coordinator", toolName: "submit", registration: &otherTransport},
		{name: "missing registration", serverName: "coordinator", toolName: "submit", registration: nil},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			meta := map[string]any{}
			got, err := hook.Apply(meta, testCase.serverName, testCase.toolName, testCase.registration, "thread-a", "turn-1", "call-1")
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if _, ok := got.(map[string]any)["x-codex-program"]; ok {
				t.Fatalf("meta = %#v, want no hook value", got)
			}
		})
	}
	if called {
		t.Fatal("provider ran for a non-matching registration")
	}
}

func TestMCPToolRequestMetaHookRejectsExistingKeyLikeRust(t *testing.T) {
	registration := testToolRequestMetaRegistration()
	hook := &MCPToolRequestMetaHook{
		ServerName: "coordinator",
		Source:     CatalogSourceExtension,
		Transport:  registration.Config,
		ToolName:   "submit",
		Key:        "x-codex-program",
		Provider:   func(string, string, string) (any, error) { return "program-1", nil },
	}
	_, err := hook.Apply(map[string]any{"x-codex-program": "host"}, "coordinator", "submit", &registration, "", "", "")
	if err == nil {
		t.Fatal("Apply() succeeded, want an existing-key error")
	}
}

func TestMCPToolRequestMetaHookRequiresMetadataObjectLikeRust(t *testing.T) {
	registration := testToolRequestMetaRegistration()
	hook := &MCPToolRequestMetaHook{
		ServerName: "coordinator",
		Source:     CatalogSourceExtension,
		Transport:  registration.Config,
		ToolName:   "submit",
		Key:        "x-codex-program",
		Provider:   func(string, string, string) (any, error) { return "program-1", nil },
	}
	if _, err := hook.Apply(nil, "coordinator", "submit", &registration, "", "", ""); err == nil {
		t.Fatal("Apply() succeeded without a metadata object")
	}
}

func TestMCPToolRequestMetaHookPropagatesProviderErrorLikeRust(t *testing.T) {
	registration := testToolRequestMetaRegistration()
	want := errors.New("provider unavailable")
	hook := &MCPToolRequestMetaHook{
		ServerName: "coordinator",
		Source:     CatalogSourceExtension,
		Transport:  registration.Config,
		ToolName:   "submit",
		Key:        "x-codex-program",
		Provider:   func(string, string, string) (any, error) { return nil, want },
	}
	meta := map[string]any{}
	if _, err := hook.Apply(meta, "coordinator", "submit", &registration, "", "", ""); !errors.Is(err, want) {
		t.Fatalf("Apply() error = %v, want the provider error", err)
	}
	if _, ok := meta["x-codex-program"]; ok {
		t.Fatal("failed provider still wrote the key")
	}
}
