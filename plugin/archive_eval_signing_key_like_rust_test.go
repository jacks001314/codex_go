package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Rust parity: codex-rs/plugin-archive-verification #63528. The release keys
// list and the eval-only scoped list have distinct purposes, and an eval key
// is still cached by the endpoint plus credential scope that fetched it.

func pluginArchiveTestKeyWithPurpose(signer *pluginArchiveTestSigner, purpose string) PluginArchiveSigningKey {
	key := signer.keys()[0]
	key.Purpose = purpose
	return key
}

func fetchPluginArchiveTestKeys(t *testing.T, body map[string]any) ([]PluginArchiveSigningKey, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode signing keys: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return FetchPluginArchiveSigningKeys(context.Background(), server.Client(), server.URL+PluginArchiveSigningKeysPath, http.Header{})
}

func TestPluginArchiveEvalSigningKeysAreScopedLikeRust(t *testing.T) {
	release := newPluginArchiveTestSigner(t)
	eval := newPluginArchiveTestSigner(t)
	eval.keyID = "eval-key-1"
	releaseKey := pluginArchiveTestKeyWithPurpose(release, PluginArchiveSigningKeyPurpose)
	evalKey := pluginArchiveTestKeyWithPurpose(eval, PluginArchiveEvalSigningKeyPurpose)

	keys, err := fetchPluginArchiveTestKeys(t, map[string]any{
		"keys":              []PluginArchiveSigningKey{releaseKey},
		"eval_keys":         []PluginArchiveSigningKey{evalKey},
		"cache_ttl_seconds": 600,
	})
	if err != nil {
		t.Fatalf("FetchPluginArchiveSigningKeys() error = %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("published keys = %d, want 2", len(keys))
	}
	var foundRelease, foundEval bool
	for _, key := range keys {
		switch key.KeyID {
		case releaseKey.KeyID:
			foundRelease = key.Purpose == PluginArchiveSigningKeyPurpose
		case evalKey.KeyID:
			foundEval = key.Purpose == PluginArchiveEvalSigningKeyPurpose
		}
	}
	if !foundRelease || !foundEval {
		t.Fatalf("published purposes = %#v, want release and eval scopes", keys)
	}

	archive := []byte("eval plugin archive bytes")
	signing := eval.signing(t, archive)
	if _, outcome, err := VerifyPluginArchive(archive, signing, keys); err != nil || outcome != PluginArchiveVerified {
		t.Fatalf("eval archive verify = (%q, %v), want verified", outcome, err)
	}

	// An eval-purpose key in the release list is not a legacy release key.
	if _, err := fetchPluginArchiveTestKeys(t, map[string]any{
		"keys": []PluginArchiveSigningKey{evalKey},
	}); PluginArchiveFailureCode(err) != "unsupported_key_purpose" {
		t.Fatalf("eval key in keys error = %v, want unsupported_key_purpose", err)
	}

	// A marketplace key in the eval-only list is likewise out of scope.
	if _, err := fetchPluginArchiveTestKeys(t, map[string]any{
		"eval_keys": []PluginArchiveSigningKey{releaseKey},
	}); PluginArchiveFailureCode(err) != "unsupported_key_purpose" {
		t.Fatalf("release key in eval_keys error = %v, want unsupported_key_purpose", err)
	}

	// Key IDs must be unique across both lists.
	duplicateEval := evalKey
	duplicateEval.KeyID = releaseKey.KeyID
	if _, err := fetchPluginArchiveTestKeys(t, map[string]any{
		"keys":      []PluginArchiveSigningKey{releaseKey},
		"eval_keys": []PluginArchiveSigningKey{duplicateEval},
	}); PluginArchiveFailureCode(err) != PluginArchiveInvalidSigningKeysResponseCode {
		t.Fatalf("duplicate scoped key error = %v, want invalid_signing_keys_response", err)
	}
}

func TestEvalSigningKeyCacheIsScopedToEndpointAccountAndTokenLikeRust(t *testing.T) {
	release := newPluginArchiveTestSigner(t)
	eval := newPluginArchiveTestSigner(t)
	eval.keyID = "eval-key-1"
	releaseKey := pluginArchiveTestKeyWithPurpose(release, PluginArchiveSigningKeyPurpose)
	evalKey := pluginArchiveTestKeyWithPurpose(eval, PluginArchiveEvalSigningKeyPurpose)

	response := func(w http.ResponseWriter, r *http.Request) {
		keys := []PluginArchiveSigningKey{releaseKey}
		evalKeys := []PluginArchiveSigningKey{}
		if r.Header.Get("Authorization") == "Bearer employee" && r.Header.Get("ChatGPT-Account-Id") == "employee" {
			evalKeys = append(evalKeys, evalKey)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"keys":              keys,
			"eval_keys":         evalKeys,
			"cache_ttl_seconds": 600,
		}); err != nil {
			t.Errorf("encode signing keys: %v", err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(response))
	t.Cleanup(server.Close)
	otherServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []PluginArchiveSigningKey{releaseKey}, "cache_ttl_seconds": 600})
	}))
	t.Cleanup(otherServer.Close)

	cache := &SigningKeyCache{}
	lookup := func(baseURL string, token string, account string) (PluginArchiveSigningKey, error) {
		headers := http.Header{}
		headers.Set("Authorization", "Bearer "+token)
		headers.Set("ChatGPT-Account-Id", account)
		return cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), baseURL, headers, evalKey.KeyID)
	}
	key, err := lookup(server.URL, "employee", "employee")
	if err != nil {
		t.Fatalf("employee lookup error = %v", err)
	}
	if key.KeyID != evalKey.KeyID || key.Purpose != PluginArchiveEvalSigningKeyPurpose {
		t.Fatalf("employee lookup = %#v, want scoped eval key", key)
	}
	if _, err := lookup(server.URL, "employee", "other"); PluginArchiveFailureCode(err) != "unknown_signing_key" {
		t.Fatalf("other account lookup error = %v, want unknown_signing_key", err)
	}
	if _, err := lookup(server.URL, "other", "employee"); PluginArchiveFailureCode(err) != "unknown_signing_key" {
		t.Fatalf("other token lookup error = %v, want unknown_signing_key", err)
	}
	if _, err := lookup(otherServer.URL, "employee", "employee"); PluginArchiveFailureCode(err) != "unknown_signing_key" {
		t.Fatalf("other endpoint lookup error = %v, want unknown_signing_key", err)
	}
}
