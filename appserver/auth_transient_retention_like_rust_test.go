package appserver

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/config"
)

type workloadStatusRoundTripper struct{ status int }

func (t workloadStatusRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.status,
		Status:     http.StatusText(t.status),
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("temporarily unavailable")),
	}, nil
}

func transientWorkloadOptions(status int) *auth.StoreOptions {
	return &auth.StoreOptions{WorkloadIdentity: &auth.WorkloadIdentityAuthOptions{
		HTTPClient: &http.Client{Transport: workloadStatusRoundTripper{status: status}},
	}}
}

// selectTransientWorkloadIdentity marks the process as using external auth and
// returns a unique assertion file so the shared exchange registry cannot serve
// a credential resolved by another test.
func selectTransientWorkloadIdentity(t *testing.T) {
	t.Helper()
	assertion := filepath.Join(t.TempDir(), "identity-token")
	if err := os.WriteFile(assertion, []byte("assertion-one"), 0o600); err != nil {
		t.Fatalf("write assertion: %v", err)
	}
	t.Setenv("OPENAI_FEDERATION_RULE_ID", "rule-transient-reload")
	t.Setenv("OPENAI_IDENTITY_TOKEN_FILE", assertion)
}

// Mirrors Rust #59491: a transient external auth failure keeps the cached
// identity without advancing the credential revisions, so a temporary provider
// outage does not look like a logout.
func TestRuntimeRouterKeepsCachedExternalAuthThroughTransientFailureLikeRust(t *testing.T) {
	selectTransientWorkloadIdentity(t)
	router := NewRuntimeRouter(RuntimeServices{})
	cached := reloadTestSnapshot("user-a", "workspace-a", "login-1", "cached-token")
	router.requireAccount().ApplyAuthSnapshot(cached)
	router.noteAuthChanged()
	before := router.authChangeTracker.Snapshot()

	resolved, err := router.resolveAuthWithLoginRestrictionsAndOptions(t.TempDir(), transientWorkloadOptions(http.StatusServiceUnavailable))
	if err != nil {
		t.Fatalf("resolveAuthWithLoginRestrictionsAndOptions() error = %v", err)
	}
	if resolved == nil || resolved.Auth.Tokens["access_token"] != "cached-token" {
		t.Fatalf("resolved auth = %#v, want the cached identity", resolved)
	}
	if got := router.requireAccount().AuthSnapshot(); got == nil || got.Tokens["access_token"] != "cached-token" {
		t.Fatalf("cached account = %#v, want the retained credential", got)
	}
	if after := router.authChangeTracker.Snapshot(); after != before {
		t.Fatalf("credential revisions advanced through a transient outage: %#v -> %#v", before, after)
	}

	// A cached credential the active workspace policy forbids is still cleared.
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("forced_chatgpt_workspace_id = \"workspace-other\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	restricted := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	restricted.requireAccount().ApplyAuthSnapshot(cached)
	if resolved, err := restricted.resolveAuthWithLoginRestrictionsAndOptions(t.TempDir(), transientWorkloadOptions(http.StatusServiceUnavailable)); err == nil || resolved != nil {
		t.Fatalf("restricted resolve = %#v, %v; want the disallowed credential dropped", resolved, err)
	}
}

// Mirrors Rust #59491: a forced workspace policy change advances the credential
// generation so a reload validated under the previous policy cannot publish.
func TestRuntimeRouterFencesReloadsWhenWorkspacePolicyChangesLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	router.noteForcedChatGPTWorkspaces([]string{"workspace-a"})
	generation := router.authChangeGeneration()
	router.noteForcedChatGPTWorkspaces([]string{"workspace-a"})
	if router.authChangeGeneration() != generation {
		t.Fatal("an unchanged workspace policy advanced the credential generation")
	}
	router.noteForcedChatGPTWorkspaces([]string{"workspace-b"})
	if router.authChangeGeneration() == generation {
		t.Fatal("a workspace policy change did not fence pending auth reloads")
	}
	if router.publishReloadedAuth(generation, reloadTestSnapshot("user-a", "workspace-a", "login-1", "fallback")) {
		t.Fatal("a reload validated against the previous policy was published")
	}
	if snapshot := router.requireAccount().AuthSnapshot(); snapshot != nil {
		t.Fatalf("stale fallback restored credentials: %#v", snapshot)
	}
}
