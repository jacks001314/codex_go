package config

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"codex_go/network"
)

func embeddedPolicyURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return parsed
}

// Rust parity: EmbeddedNetworkPolicy::activate/bind_config keep every config a
// host produces on the same live read handle, so a reload or failure reaches
// transports that were built before it.
func TestEmbeddedNetworkPolicySharesOneLiveGenerationLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(`
[application.network]
[application.network.domains]
"allowed.example" = "allow"
`), 0o600); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
	restricted, err := LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions(restricted) error = %v", err)
	}
	unrestrictedHome := t.TempDir()
	unrestricted, err := LoadEffectiveWithOptions(unrestrictedHome, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions(unrestricted) error = %v", err)
	}

	owner := NewEmbeddedNetworkPolicy()
	if err := owner.LoadLocal(home); err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if _, err := owner.Local().Acquire(embeddedPolicyURL(t, "https://allowed.example/v1")); err != nil {
		t.Fatalf("local allowed host error = %v", err)
	}
	if _, err := owner.Local().Acquire(embeddedPolicyURL(t, "https://denied.example/v1")); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("local denied host error = %v", err)
	}

	effective := owner.Effective()
	if _, err := effective.Acquire(embeddedPolicyURL(t, "https://allowed.example/v1")); !errors.Is(err, network.ErrNetworkPolicyUnavailable) {
		t.Fatalf("unactivated effective policy error = %v, want unavailable", err)
	}
	owner.Activate(restricted)
	if restricted.ApplicationNetworkPolicyOwner != owner {
		t.Fatal("Activate did not bind the host owner to the config")
	}
	if _, err := effective.Acquire(embeddedPolicyURL(t, "https://allowed.example/v1")); err != nil {
		t.Fatalf("activated allowed host error = %v", err)
	}
	if _, err := effective.Acquire(embeddedPolicyURL(t, "https://denied.example/v1")); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("activated denied host error = %v", err)
	}

	// A rebuilt config binds the same handle without publishing a new policy.
	rebuilt, err := LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions(rebuilt) error = %v", err)
	}
	owner.BindConfig(rebuilt)
	if rebuilt.ApplicationNetworkPolicyOwner != owner {
		t.Fatal("BindConfig did not carry the host owner forward")
	}
	if _, err := rebuilt.NetworkPolicy().Acquire(embeddedPolicyURL(t, "https://denied.example/v1")); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("rebuilt denied host error = %v", err)
	}

	// Activating a config without restrictions updates every earlier handle,
	// including the one the first config still carries.
	owner.Activate(unrestricted)
	for name, policy := range map[string]network.NetworkPolicy{
		"effective": effective,
		"first":     restricted.NetworkPolicy(),
		"rebuilt":   rebuilt.NetworkPolicy(),
	} {
		if _, err := policy.Acquire(embeddedPolicyURL(t, "https://anything.example/v1")); err != nil {
			t.Fatalf("%s policy after unrestricted reload error = %v", name, err)
		}
	}

	// A failed reload withdraws the policy; a later successful one recovers it.
	owner.Unavailable()
	for name, policy := range map[string]network.NetworkPolicy{
		"effective": effective,
		"first":     restricted.NetworkPolicy(),
		"rebuilt":   rebuilt.NetworkPolicy(),
	} {
		if _, err := policy.Acquire(embeddedPolicyURL(t, "https://anything.example/v1")); !errors.Is(err, network.ErrNetworkPolicyUnavailable) {
			t.Fatalf("%s policy after failed reload error = %v, want unavailable", name, err)
		}
	}
	owner.Activate(restricted)
	if _, err := restricted.NetworkPolicy().Acquire(embeddedPolicyURL(t, "https://allowed.example/v1")); err != nil {
		t.Fatalf("recovered policy error = %v", err)
	}
}

// BindApplicationNetworkPolicy delegates to the host owner instead of creating
// a private controller per config load.
func TestBindApplicationNetworkPolicyReusesHostOwnerLikeRust(t *testing.T) {
	owner := NewEmbeddedNetworkPolicy()
	cfg := &Config{ApplicationNetworkPolicyOwner: owner}
	first := cfg.BindApplicationNetworkPolicy()
	second := cfg.BindApplicationNetworkPolicy()
	if first == nil || first != second {
		t.Fatalf("host-owned controllers = %p/%p, want the same controller", first, second)
	}
	if first != owner.EffectiveController() {
		t.Fatal("BindApplicationNetworkPolicy did not return the host owner's controller")
	}
}
