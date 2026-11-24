package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"codex_go/plugin"
)

// Rust #63490/#63491: one owner reuses the Plugin Service key listing for every
// signed archive it materializes, while another owner with its own cache
// fetches again.
func TestRemoteInstalledPluginKeyCacheReuseLikeRust(t *testing.T) {
	t.Setenv("CODEX_TEST_ALLOW_HTTP_REMOTE_PLUGIN_BUNDLE_DOWNLOADS", "1")
	signer := newPluginSignatureTestSigner(t)
	var keyRequests atomic.Int32
	keysServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyRequests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"key_id":                     signer.keyID,
				"purpose":                    "PLUGIN_RELEASE_ARCHIVE_SIGNING",
				"algorithm":                  "RSA_PKCS1_SHA256",
				"public_key_spki_der_base64": signer.public,
			}},
			"cache_ttl_seconds": 600,
		})
	}))
	defer keysServer.Close()

	archives := map[string][]byte{
		"linear": remotePluginBundleForTest(t, "linear", "linear"),
		"slack":  remotePluginBundleForTest(t, "slack", "slack"),
	}
	bundleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		archive, ok := archives[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer bundleServer.Close()

	signing := remotePluginArchiveSigningContext{
		ServiceBaseURL: keysServer.URL,
		Headers:        http.Header{"Authorization": {"Bearer test-token"}},
	}
	install := func(cache *plugin.SigningKeyCache, name string) {
		t.Helper()
		version := "1.0.0"
		bundleURL := bundleServer.URL + "/bundle.tar.gz?name=" + name
		candidate := remoteInstalledPlugin{ID: "plugins~Plugin_" + name, Name: name}
		candidate.Release.Version = &version
		candidate.Release.BundleDownloadURL = &bundleURL
		candidate.Release.Signing = signer.signingJSON(t, archives[name])
		if _, err := ensureRemoteInstalledPluginCacheWithKeys(context.Background(), bundleServer.Client(), t.TempDir(), remoteInstalledGlobalMarketplace, candidate, signing, cache); err != nil {
			t.Fatalf("install %s: %v", name, err)
		}
	}

	owner := &plugin.SigningKeyCache{}
	install(owner, "linear")
	install(owner, "slack")
	if got := keyRequests.Load(); got != 1 {
		t.Fatalf("key requests for one owner = %d, want one shared listing", got)
	}

	install(&plugin.SigningKeyCache{}, "linear")
	if got := keyRequests.Load(); got != 2 {
		t.Fatalf("key requests after a second owner = %d, want its own fetch", got)
	}
}
