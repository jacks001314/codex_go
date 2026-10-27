package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func grantTestTokens(loginOwner string, refreshToken string) map[string]any {
	tokens := map[string]any{
		"oauth_provenance": map[string]any{"issuer": "https://auth.example", "client_id": "client-test"},
		"account_id":       "workspace-a",
		"chatgpt_user_id":  "user-a",
		"refresh_token":    refreshToken,
		"access_token":     "access-" + refreshToken,
	}
	if loginOwner != "" {
		tokens["login_owner_id"] = loginOwner
	}
	return tokens
}

func saveGrantTestAuth(t *testing.T, home string, tokens map[string]any) *AuthDotJSON {
	t.Helper()
	snapshot := &AuthDotJSON{AuthMode: "chatgpt", Tokens: tokens}
	if err := NewStore(home).Save(*snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return snapshot
}

func TestLeaseAccountOAuthGrantRequiresCompleteOAuthIdentityLikeRust(t *testing.T) {
	cachedValid := &AuthDotJSON{AuthMode: "chatgpt", Tokens: grantTestTokens("login-1", "refresh-1")}
	cases := []struct {
		name   string
		tokens map[string]any
		cached *AuthDotJSON
	}{
		{"missing provenance", map[string]any{"login_owner_id": "login-1", "account_id": "workspace-a", "chatgpt_user_id": "user-a", "refresh_token": "refresh-1"}, cachedValid},
		{"missing login owner", map[string]any{"oauth_provenance": map[string]any{"issuer": "https://auth.example", "client_id": "client-test"}, "account_id": "workspace-a", "chatgpt_user_id": "user-a", "refresh_token": "refresh-1"}, cachedValid},
		{"missing refresh token", map[string]any{"oauth_provenance": map[string]any{"issuer": "https://auth.example", "client_id": "client-test"}, "login_owner_id": "login-1", "account_id": "workspace-a", "chatgpt_user_id": "user-a"}, cachedValid},
		{"missing account identity", map[string]any{"oauth_provenance": map[string]any{"issuer": "https://auth.example", "client_id": "client-test"}, "login_owner_id": "login-1", "refresh_token": "refresh-1"}, cachedValid},
		{"cached owner differs", grantTestTokens("login-1", "refresh-1"), &AuthDotJSON{AuthMode: "chatgpt", Tokens: grantTestTokens("login-2", "refresh-1")}},
		{"cached mode differs", grantTestTokens("login-1", "refresh-1"), &AuthDotJSON{OpenAIAPIKey: "sk-test"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			saveGrantTestAuth(t, home, tc.tokens)
			lease, err := LeaseAccountOAuthGrant(context.Background(), &AccountOAuthGrantLeaseOptions{
				CodexHome: home,
				Cached:    func() *AuthDotJSON { return tc.cached },
			})
			if err != nil {
				t.Fatalf("LeaseAccountOAuthGrant() error = %v", err)
			}
			if lease != nil {
				t.Fatalf("lease = %+v, want no grant", lease)
			}
		})
	}

	home := t.TempDir()
	saved := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-1"))
	lease, err := LeaseAccountOAuthGrant(context.Background(), &AccountOAuthGrantLeaseOptions{
		CodexHome: home,
		Cached:    func() *AuthDotJSON { return saved },
	})
	if err != nil || lease == nil {
		t.Fatalf("valid grant lease = %+v, error = %v", lease, err)
	}
	if lease.Tokens()["refresh_token"] != "refresh-1" {
		t.Fatalf("lease tokens = %#v", lease.Tokens())
	}
	grant := lease.Release()
	current, err := grant.CheckCurrent()
	if err != nil || !current {
		t.Fatalf("released grant current = %v, error = %v", current, err)
	}
}

// TestAccountOAuthGrantRevisionIgnoresTokenRotationLikeRust mirrors #55859: a
// grant stays the same credential across access and ID token rotation, while a
// new refresh token or login is a different credential.
func TestAccountOAuthGrantRevisionIgnoresTokenRotationLikeRust(t *testing.T) {
	home := t.TempDir()
	saved := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-1"))
	lease, err := LeaseAccountOAuthGrant(context.Background(), &AccountOAuthGrantLeaseOptions{
		CodexHome: home,
		Cached:    func() *AuthDotJSON { return saved },
	})
	if err != nil || lease == nil {
		t.Fatalf("lease = %+v, error = %v", lease, err)
	}
	grant := lease.Release()

	rotated := grantTestTokens("login-1", "refresh-1")
	rotated["access_token"] = "access-rotated"
	rotated["id_token"] = "eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1c2VyIn0.signature"
	saveGrantTestAuth(t, home, rotated)
	current, ok, err := grant.CurrentTokens()
	if err != nil || !ok || current == nil {
		t.Fatalf("CurrentTokens() = %#v, %v, error = %v", current, ok, err)
	}
	if current["access_token"] != "access-rotated" {
		t.Fatalf("CurrentTokens() access token = %v", current["access_token"])
	}

	renewed := grantTestTokens("login-1", "refresh-2")
	if AccountOAuthGrantRevisionFromTokens(renewed) == AccountOAuthGrantRevisionFromTokens(grantTestTokens("login-1", "refresh-1")) {
		t.Fatal("a new refresh token must be a different credential revision")
	}
	if AccountOAuthGrantRevisionFromTokens(grantTestTokens("login-2", "refresh-1")) == AccountOAuthGrantRevisionFromTokens(grantTestTokens("login-1", "refresh-1")) {
		t.Fatal("a new login owner must be a different credential revision")
	}
	other := &AccountOAuthGrant{tokens: renewed}
	if grant.SameCredential(other) {
		t.Fatal("a renewed refresh token must not be the same credential")
	}
}

// Rust #55546: an identity-provider exchange holds the refresh lease, so an
// ordinary refresh must wait until it is released.
func TestAccountOAuthGrantLeaseBlocksRefreshUntilReleaseLikeRust(t *testing.T) {
	clearAuthEnvForRefreshTests(t)
	home := t.TempDir()
	cached := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-1"))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSONAuth(t, w, map[string]string{
			"access_token":  "new-access",
			"refresh_token": "refresh-2",
		})
	}))
	defer server.Close()
	t.Setenv(RefreshTokenURLEnvOverride, server.URL)

	lease, err := LeaseAccountOAuthGrant(context.Background(), &AccountOAuthGrantLeaseOptions{
		CodexHome: home,
		Cached:    func() *AuthDotJSON { return cached },
	})
	if err != nil || lease == nil {
		t.Fatalf("lease = %+v, error = %v", lease, err)
	}

	refreshDone := make(chan error, 1)
	go func() {
		_, refreshErr := RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{
			CodexHome:    home,
			Issuer:       "https://auth.example",
			ClientID:     "client-test",
			HTTPClient:   server.Client(),
			AuthSnapshot: cached,
		})
		refreshDone <- refreshErr
	}()
	select {
	case err := <-refreshDone:
		t.Fatalf("refresh finished while the lease was held: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if calls.Load() != 0 {
		t.Fatalf("refresh reached the authority while the lease was held")
	}
	lease.Release()
	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatalf("RefreshChatGPTTokens() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not resume after the lease was released")
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}
}

func TestAccountOAuthGrantRejectsRotatedReplacedOrMetadataLosingCredentialsLikeRust(t *testing.T) {
	home := t.TempDir()
	cached := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-1"))
	tracker := NewAuthChangeTracker(cached)
	options := &AccountOAuthGrantLeaseOptions{
		CodexHome:       home,
		Cached:          func() *AuthDotJSON { return cached },
		LoginGeneration: func() uint64 { return tracker.Snapshot().LoginGeneration },
	}
	lease, err := LeaseAccountOAuthGrant(context.Background(), options)
	if err != nil || lease == nil {
		t.Fatalf("lease = %+v, error = %v", lease, err)
	}
	grant := lease.Release()

	// Token rotation by another client invalidates the grant.
	saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-2"))
	current, err := grant.CheckCurrent()
	if err != nil || current {
		t.Fatalf("rotated grant current = %v, error = %v", current, err)
	}

	// A new login to the same account replaces the login owner.
	cached = saveGrantTestAuth(t, home, grantTestTokens("login-2", "refresh-2"))
	tracker.NoteAuth(cached)
	lease, err = LeaseAccountOAuthGrant(context.Background(), options)
	if err != nil || lease == nil {
		t.Fatalf("replacement lease = %+v, error = %v", lease, err)
	}
	grant = lease.Release()
	saveGrantTestAuth(t, home, grantTestTokens("login-3", "refresh-2"))
	current, err = grant.CheckCurrent()
	if err != nil || current {
		t.Fatalf("replaced grant current = %v, error = %v", current, err)
	}

	// Losing login metadata alone invalidates the grant while the owner stays.
	cached = saveGrantTestAuth(t, home, grantTestTokens("login-3", "refresh-2"))
	lease, err = LeaseAccountOAuthGrant(context.Background(), options)
	if err != nil || lease == nil {
		t.Fatalf("metadata lease = %+v, error = %v", lease, err)
	}
	grant = lease.Release()
	loginGeneration := tracker.Snapshot().LoginGeneration
	ownerGeneration := tracker.Snapshot().OwnerGeneration
	cached = saveGrantTestAuth(t, home, grantTestTokens("", "refresh-2"))
	state := tracker.NoteAuth(cached)
	if state.LoginGeneration == loginGeneration {
		t.Fatal("losing login metadata must advance the login generation")
	}
	if state.OwnerGeneration != ownerGeneration {
		t.Fatalf("losing login metadata changed the owner generation: %d -> %d", ownerGeneration, state.OwnerGeneration)
	}
	current, err = grant.CheckCurrent()
	if err != nil || current {
		t.Fatalf("metadata-losing grant current = %v, error = %v", current, err)
	}

	// Logout while a lease is held invalidates the grant.
	cached = saveGrantTestAuth(t, home, grantTestTokens("login-4", "refresh-4"))
	lease, err = LeaseAccountOAuthGrant(context.Background(), options)
	if err != nil || lease == nil {
		t.Fatalf("logout lease = %+v, error = %v", lease, err)
	}
	grant = lease.Release()
	if _, err := NewStore(home).Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	current, err = grant.CheckCurrent()
	if err != nil || current {
		t.Fatalf("logged-out grant current = %v, error = %v", current, err)
	}
}

// TestAccountLoginIsCurrentDetectsPersistedReplacementLikeRust mirrors #55680:
// a replacement persisted by another client is detected even while the cache
// still holds the previous login and the account owner is unchanged.
func TestAccountLoginIsCurrentDetectsPersistedReplacementLikeRust(t *testing.T) {
	home := t.TempDir()
	options := &StoreOptions{Mode: AuthCredentialsStoreFile}
	expected := saveGrantTestAuth(t, home, map[string]any{
		"login_owner_id":  "login-1",
		"account_id":      "workspace-a",
		"chatgpt_user_id": "user-a",
		"refresh_token":   "refresh-1",
	})
	cached := expected
	current, err := AccountLoginIsCurrent(home, options, expected, func() *AuthDotJSON { return cached })
	if err != nil || !current {
		t.Fatalf("current login = %v, error = %v", current, err)
	}

	// Another client replaces the login for the same account without this
	// process publishing the new snapshot to its cache.
	replacement := &AuthDotJSON{AuthMode: "chatgpt", Tokens: map[string]any{
		"login_owner_id":  "login-2",
		"account_id":      "workspace-a",
		"chatgpt_user_id": "user-a",
		"refresh_token":   "refresh-2",
	}}
	if err := NewStoreWithOptions(home, options).Save(*replacement); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	current, err = AccountLoginIsCurrent(home, options, expected, func() *AuthDotJSON { return cached })
	if err != nil || current {
		t.Fatalf("persisted replacement login = %v, error = %v", current, err)
	}

	// Even after the cache catches up, the captured login is no longer current.
	cached = replacement
	current, err = AccountLoginIsCurrent(home, options, expected, func() *AuthDotJSON { return cached })
	if err != nil || current {
		t.Fatalf("captured login after replacement = %v, error = %v", current, err)
	}

	// Non-ChatGPT auth compares the account and user identity instead.
	apiKey := &AuthDotJSON{OpenAIAPIKey: "sk-test", Tokens: map[string]any{
		"account_id":      "workspace-a",
		"chatgpt_user_id": "user-a",
	}}
	current, err = AccountLoginIsCurrent(home, options, apiKey, func() *AuthDotJSON { return apiKey })
	if err != nil || !current {
		t.Fatalf("api-key login = %v, error = %v", current, err)
	}
	otherUser := &AuthDotJSON{OpenAIAPIKey: "sk-test", Tokens: map[string]any{
		"account_id":      "workspace-a",
		"chatgpt_user_id": "user-b",
	}}
	current, err = AccountLoginIsCurrent(home, options, apiKey, func() *AuthDotJSON { return otherUser })
	if err != nil || current {
		t.Fatalf("changed api-key identity = %v, error = %v", current, err)
	}

	// A chatgpt login without owner metadata can never be current.
	anonymous := &AuthDotJSON{AuthMode: "chatgpt", Tokens: map[string]any{
		"account_id":      "workspace-a",
		"chatgpt_user_id": "user-a",
	}}
	current, err = AccountLoginIsCurrent(home, options, anonymous, func() *AuthDotJSON { return anonymous })
	if err != nil || current {
		t.Fatalf("login without owner = %v, error = %v", current, err)
	}
}

// TestAccountLoginIsCurrentReportsStorageFailureLikeRust mirrors #55680: an
// unavailable credential store is an error, not proof that the login moved.
func TestAccountLoginIsCurrentReportsStorageFailureLikeRust(t *testing.T) {
	withoutOSKeyring(t)
	home := t.TempDir()
	snapshot := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-1"))
	_, err := AccountLoginIsCurrent(home, &StoreOptions{Mode: AuthCredentialsStoreAuto}, snapshot, func() *AuthDotJSON { return snapshot })
	if err == nil || !strings.Contains(err.Error(), "Account credential storage is unavailable.") {
		t.Fatalf("AccountLoginIsCurrent() error = %v", err)
	}
}

func TestStoreLoadAccountGrantKeepsKeyringFailuresStrictLikeRust(t *testing.T) {
	withoutOSKeyring(t)
	home := t.TempDir()
	saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-1"))
	store := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreAuto})
	loaded, err := store.Load()
	if err != nil || loaded == nil || loaded.Tokens["refresh_token"] != "refresh-1" {
		t.Fatalf("ordinary Load() = %+v, error = %v", loaded, err)
	}
	if _, err := store.LoadAccountGrant(); err == nil || !strings.Contains(err.Error(), KeyringUnavailableError) {
		t.Fatalf("LoadAccountGrant() error = %v, want unavailable keyring", err)
	}
}

func TestPersistRefreshedChatGPTTokensGuardsAgainstConcurrentLoginLikeRust(t *testing.T) {
	clearAuthEnvForRefreshTests(t)
	home := t.TempDir()
	expected := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-1"))

	saveGrantTestAuth(t, home, grantTestTokens("login-2", "refresh-2"))
	err := persistRefreshedChatGPTTokens(home, expected, &ExchangedTokens{AccessToken: "new-access", RefreshToken: "refresh-3"}, nil)
	if err == nil || err.Error() != errAccountCredentialsChangedDuringRefresh.Error() {
		t.Fatalf("concurrent login persistence error = %v", err)
	}
	stored, loadErr := NewStore(home).Load()
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if stored.Tokens["login_owner_id"] != "login-2" || stored.Tokens["refresh_token"] != "refresh-2" {
		t.Fatalf("stored credential changed after a rejected refresh: %#v", stored.Tokens)
	}

	// An older client may drop login metadata while the refresh token matches.
	expected = saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-1"))
	saveGrantTestAuth(t, home, grantTestTokens("", "refresh-1"))
	if err := persistRefreshedChatGPTTokens(home, expected, &ExchangedTokens{AccessToken: "new-access", RefreshToken: "refresh-1"}, nil); err != nil {
		t.Fatalf("legacy metadata loss persistence error = %v", err)
	}
	stored, loadErr = NewStore(home).Load()
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if stored.Tokens["access_token"] != "new-access" || stored.Tokens["refresh_token"] != "refresh-1" {
		t.Fatalf("stored credential = %#v", stored.Tokens)
	}
	if _, ok := stored.Tokens["login_owner_id"]; ok {
		t.Fatalf("legacy metadata loss must not restore login metadata: %#v", stored.Tokens)
	}
	provenance, _ := stored.Tokens["oauth_provenance"].(map[string]any)
	if provenance["issuer"] != "https://auth.example" || provenance["client_id"] != "client-test" {
		t.Fatalf("provenance = %#v", provenance)
	}
}

// TestAutoStoreRefreshPersistenceMovesFallbackTokensToRecoveredKeyringLikeRust
// covers Rust #55646's keyring-recovery case: ordinary persistence may read the
// fallback record, writes prefer the keyring, and rotated tokens survive.
func TestAutoStoreRefreshPersistenceMovesFallbackTokensToRecoveredKeyringLikeRust(t *testing.T) {
	clearAuthEnvForRefreshTests(t)
	home := t.TempDir()
	keyring := NewKeyringStore(KeyringBackendDirect)
	t.Cleanup(func() { _, _ = keyring.Delete(authKeyringService, authStoreKey(home)) })
	options := &StoreOptions{Mode: AuthCredentialsStoreAuto, KeyringStore: keyring}

	expected := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-1"))
	fallback, err := NewStoreWithOptions(home, options).Load()
	if err != nil || fallback == nil || fallback.Tokens["refresh_token"] != "refresh-1" {
		t.Fatalf("auto fallback Load() = %+v, error = %v", fallback, err)
	}
	if err := persistRefreshedChatGPTTokens(home, expected, &ExchangedTokens{
		AccessToken:  "rotated-access",
		RefreshToken: "rotated-refresh",
	}, options); err != nil {
		t.Fatalf("persistRefreshedChatGPTTokens() error = %v", err)
	}
	refreshed, err := NewStoreWithOptions(home, options).Load()
	if err != nil || refreshed == nil {
		t.Fatalf("recovered Load() = %+v, error = %v", refreshed, err)
	}
	if refreshed.Tokens["access_token"] != "rotated-access" || refreshed.Tokens["refresh_token"] != "rotated-refresh" {
		t.Fatalf("rotated tokens = %#v", refreshed.Tokens)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("fallback auth.json survived the keyring write: %v", err)
	}
}

// TestPersistAgentIdentityRecordGuardsAgainstConcurrentLoginLikeRust covers
// Rust #55646: an older agent identity registration must not overwrite a login
// that replaced the credential while it was in flight.
func TestPersistAgentIdentityRecordGuardsAgainstConcurrentLoginLikeRust(t *testing.T) {
	home := t.TempDir()
	cached := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-1"))
	record := &AgentIdentityAuthRecord{AgentRuntimeID: "runtime-1", AgentPrivateKey: "key-1", AccountID: "workspace-a", ChatGPTUserID: "user-a"}

	saveGrantTestAuth(t, home, grantTestTokens("login-2", "refresh-2"))
	err := persistAgentIdentityRecord(NewStore(home), cached, record)
	if err == nil || err.Error() != "Account changed during agent identity registration." {
		t.Fatalf("concurrent login error = %v", err)
	}
	stored, loadErr := NewStore(home).Load()
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if stored.AgentIdentity != nil || stored.Tokens["login_owner_id"] != "login-2" {
		t.Fatalf("stored credential = %#v", stored)
	}

	cached = saveGrantTestAuth(t, home, grantTestTokens("login-2", "refresh-2"))
	if err := persistAgentIdentityRecord(NewStore(home), cached, record); err != nil {
		t.Fatalf("matching login persistence error = %v", err)
	}
	stored, loadErr = NewStore(home).Load()
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if stored.AgentIdentity == nil {
		t.Fatal("matching login did not persist the agent identity record")
	}

	// A legacy cached credential without login metadata skips the strict check.
	legacy := saveGrantTestAuth(t, home, grantTestTokens("", "refresh-2"))
	if err := persistAgentIdentityRecord(NewStore(home), legacy, record); err != nil {
		t.Fatalf("legacy persistence error = %v", err)
	}
}
