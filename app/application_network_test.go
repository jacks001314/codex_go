package app

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/network"
)

const cliHostNetworkRequirements = `
[application.network]
[application.network.domains]
"allowed.example" = "allow"
"denied.example" = "deny"
`

func writeCLIHostRequirements(t *testing.T, home string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(cliHostNetworkRequirements), 0o600); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
}

func cliHostURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return parsed
}

// Rust parity: EmbeddedNetworkPolicy::activate publishes the config's managed
// requirements into the policy the host binds to its transports.
func TestCLIHostPublishesApplicationNetworkPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	writeCLIHostRequirements(t, home)
	cfg, err := config.LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	policy := publishApplicationNetworkPolicy(cfg)
	if !policy.IsScoped() {
		t.Fatal("the published policy reports no scope")
	}
	if _, err := policy.Acquire(cliHostURL(t, "https://allowed.example/v1")); err != nil {
		t.Fatalf("allowed host error = %v", err)
	}
	for _, raw := range []string{"https://denied.example/v1", "https://unlisted.example/v1"} {
		if _, err := policy.Acquire(cliHostURL(t, raw)); !errors.Is(err, network.ErrNetworkPolicyDestination) {
			t.Fatalf("Acquire(%s) error = %v, want a destination denial", raw, err)
		}
	}

	// A host with no managed requirements still binds an unrestricted policy, so
	// every application destination stays reachable.
	plain := t.TempDir()
	plainConfig, err := config.LoadEffectiveWithOptions(plain, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions(plain) error = %v", err)
	}
	plainPolicy := publishApplicationNetworkPolicy(plainConfig)
	if _, err := plainPolicy.Acquire(cliHostURL(t, "https://anything.example/v1")); err != nil {
		t.Fatalf("unrestricted host error = %v", err)
	}
}

// The CLI host's MCP client binds the published policy, so a managed
// restriction rejects a denied destination before connecting.
func TestMCPCLIStoreBindsApplicationNetworkPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	writeCLIHostRequirements(t, home)
	store := newMCPCLIStore(home)
	if _, err := store.Load(nil); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if store.httpClient == nil {
		t.Fatal("the store kept no HTTP client")
	}
	if _, ok := store.httpClient.Transport.(*network.PolicyRoundTripper); !ok {
		t.Fatalf("client transport = %#v, want a policy round tripper", store.httpClient.Transport)
	}
	if _, err := store.httpClient.Do(&http.Request{URL: cliHostURL(t, "https://denied.example/mcp")}); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("denied MCP host error = %v", err)
	}
}
