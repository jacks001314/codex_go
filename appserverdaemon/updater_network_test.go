package appserverdaemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/network"
)

func TestUpdaterHTTPClientEnforcesApplicationNetworkPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	requirements := `
[application.network]
enabled = true
[application.network.domains]
"allowed.example" = "allow"
"releases.openai.com" = "deny"
`
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, client, err := updaterNetworkPolicyAndClient(home)
	if err != nil {
		t.Fatalf("updaterNetworkPolicyAndClient() error = %v", err)
	}
	allowed, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://allowed.example/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Acquire(allowed.URL); err != nil {
		t.Fatalf("allowed destination was denied: %v", err)
	}
	for _, target := range []string{"https://releases.openai.com/", "https://unlisted.example/"} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Do(request); !errors.Is(err, network.ErrNetworkPolicyDestination) {
			t.Fatalf("client.Do(%s) error = %v, want destination denial", target, err)
		}
	}
}

func TestUpdaterNetworkConfigurationErrorIsFatalLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("not = [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := updaterHTTPClient(home); err == nil || !strings.Contains(err.Error(), "failed to load updater network configuration") {
		t.Fatalf("updaterHTTPClient() error = %v", err)
	}
	options := DefaultUpdateLoopOptions()
	if err := ConfigureUpdaterNetworkPolicy(options, home); err == nil {
		t.Fatal("ConfigureUpdaterNetworkPolicy() accepted invalid configuration")
	}
	migration := &MigrationOptions{}
	if err := ConfigureMigrationNetworkPolicy(migration, home); err == nil {
		t.Fatal("ConfigureMigrationNetworkPolicy() accepted invalid configuration")
	}
}
