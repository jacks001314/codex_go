package appserver

import (
	"errors"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/network"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #49432: an authenticated-owner change revokes the application network
// policy's account-owned content access, while the embedded host's local,
// endpoint-restricted bootstrap policy stays usable for login and discovery.
func TestRuntimeRouterAuthOwnerChangeRevokesApplicationNetworkPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, `
[application.network]
[application.network.domains]
"allowed.example" = "allow"
"denied.example" = "deny"
`)
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(home)),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
	})
	defer router.Close()

	snapshot := func(user string, token string) *auth.AuthDotJSON {
		return &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens: map[string]any{
				"access_token":    token,
				"chatgpt_user_id": user,
				"account_id":      "workspace-" + user,
			},
		}
	}
	router.requireAccount().ApplyAuthSnapshot(snapshot("owner-a", "token-1"))
	router.noteAuthChanged()

	contentURL := appServerTestURL(t, "https://allowed.example/backend-api/wham/config")
	appPolicy, composed := router.refreshApplicationNetworkPolicy()
	if !composed.IsRestricted() {
		t.Fatalf("composed application policy = %#v, want the managed restrictions", composed)
	}
	content := appPolicy.ForCurrentAccount()
	permit, err := content.Acquire(contentURL)
	if err != nil {
		t.Fatalf("account content acquire before the owner change: %v", err)
	}

	// The bootstrap discovery policy is the separate local controller.
	bootstrapURL := appServerTestURL(t, "https://allowed.example/backend-api/wham/config")
	localPolicy, localComposed := router.refreshLocalApplicationNetworkPolicy()
	if !localComposed.IsRestricted() {
		t.Fatalf("local bootstrap policy = %#v, want the home's restrictions", localComposed)
	}
	localBootstrap := localPolicy.ForCurrentAccount()
	bootstrapPermit, err := localBootstrap.Acquire(bootstrapURL)
	if err != nil {
		t.Fatalf("bootstrap acquire before the owner change: %v", err)
	}

	// A same-owner token refresh keeps the account and its permits.
	router.requireAccount().ApplyAuthSnapshot(snapshot("owner-a", "token-2"))
	router.noteAuthChanged()
	if err := permit.Check(); err != nil {
		t.Fatalf("same-owner refresh revoked the account content permit: %v", err)
	}
	if _, err := content.Acquire(contentURL); err != nil {
		t.Fatalf("same-owner refresh revoked account content access: %v", err)
	}

	// An account/workspace change revokes what the previous owner was granted.
	router.requireAccount().ApplyAuthSnapshot(snapshot("owner-b", "token-3"))
	router.noteAuthChanged()
	if err := permit.Check(); !errors.Is(err, network.ErrNetworkPolicyRevoked) {
		t.Fatalf("permit after the owner change = %v, want %v", err, network.ErrNetworkPolicyRevoked)
	}
	if _, err := content.Acquire(contentURL); !errors.Is(err, network.ErrNetworkPolicyRevoked) {
		t.Fatalf("account content acquire after the owner change = %v, want %v", err, network.ErrNetworkPolicyRevoked)
	}

	// Bootstrap discovery survives the owner change: it is bound to the local
	// endpoint-restricted policy, not the revoked account-owned one.
	if err := bootstrapPermit.Check(); err != nil {
		t.Fatalf("bootstrap permit revoked by the owner change: %v", err)
	}
	if _, err := localBootstrap.Acquire(bootstrapURL); err != nil {
		t.Fatalf("bootstrap discovery after the owner change: %v", err)
	}
	// The local policy still denies a destination the home does not allow.
	if _, err := localBootstrap.Acquire(appServerTestURL(t, "https://denied.example/backend-api/wham/config")); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("local bootstrap acquire of a denied host = %v, want %v", err, network.ErrNetworkPolicyDestination)
	}
}
