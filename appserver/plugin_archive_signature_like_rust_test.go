package appserver

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust parity: codex-rs/plugin-archive-verification (#63487/#63489). A signed
// remote plugin archive is verified against the Plugin Service release key
// before its bytes reach extraction; a tampered archive is rejected and an
// unsigned release keeps the legacy install path.

type pluginSignatureTestSigner struct {
	key    *rsa.PrivateKey
	keyID  string
	public string
}

func newPluginSignatureTestSigner(t *testing.T) *pluginSignatureTestSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return &pluginSignatureTestSigner{key: key, keyID: "release-key-1", public: base64.StdEncoding.EncodeToString(der)}
}

func (s *pluginSignatureTestSigner) signingJSON(t *testing.T, archive []byte) json.RawMessage {
	t.Helper()
	digest := sha256.Sum256(archive)
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15: %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"sha256":    hex.EncodeToString(digest[:]),
		"signature": base64.StdEncoding.EncodeToString(signature),
		"key_id":    s.keyID,
		"algorithm": "RSA_PKCS1_SHA256",
	})
	if err != nil {
		t.Fatalf("Marshal signing: %v", err)
	}
	return raw
}

func newPluginSigningKeysServer(t *testing.T, signer *pluginSignatureTestSigner) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ps/plugins/signing-keys" {
			http.NotFound(w, r)
			return
		}
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
	t.Cleanup(server.Close)
	return server
}

func TestRemoteInstalledPluginArchiveSignatureVerifiedBeforeExtractionLikeRust(t *testing.T) {
	t.Setenv("CODEX_TEST_ALLOW_HTTP_REMOTE_PLUGIN_BUNDLE_DOWNLOADS", "1")
	archive := remotePluginBundleForTest(t, "linear", "linear")
	signer := newPluginSignatureTestSigner(t)
	keysServer := newPluginSigningKeysServer(t, signer)

	bundleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer bundleServer.Close()

	newCandidate := func(version string, signing json.RawMessage) remoteInstalledPlugin {
		candidate := remoteInstalledPlugin{ID: "plugins~Plugin_linear", Name: "linear"}
		candidate.Release.Version = &version
		bundleURL := bundleServer.URL + "/bundle.tar.gz"
		candidate.Release.BundleDownloadURL = &bundleURL
		candidate.Release.Signing = signing
		return candidate
	}
	signing := remotePluginArchiveSigningContext{ServiceBaseURL: keysServer.URL, Headers: http.Header{}}

	home := t.TempDir()
	root, err := ensureRemoteInstalledPluginCache(context.Background(), bundleServer.Client(), home, remoteInstalledGlobalMarketplace, newCandidate("1.0.0", signer.signingJSON(t, archive)), signing)
	if err != nil {
		t.Fatalf("signed install error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex-plugin", "plugin.json")); err != nil {
		t.Fatalf("signed archive was not extracted: %v", err)
	}

	tamperedHome := t.TempDir()
	altered := append([]byte(nil), archive...)
	altered[0] ^= 1
	_, err = ensureRemoteInstalledPluginCache(context.Background(), bundleServer.Client(), tamperedHome, remoteInstalledGlobalMarketplace, newCandidate("1.0.0", signer.signingJSON(t, altered)), signing)
	if err == nil || !strings.Contains(err.Error(), "digest_mismatch") {
		t.Fatalf("tampered archive error = %v, want digest_mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(tamperedHome, "plugins", "cache", remoteInstalledGlobalMarketplace, "linear", "1.0.0")); statErr == nil {
		t.Fatal("tampered archive reached extraction")
	}

	unsignedHome := t.TempDir()
	unsignedRoot, err := ensureRemoteInstalledPluginCache(context.Background(), bundleServer.Client(), unsignedHome, remoteInstalledGlobalMarketplace, newCandidate("1.0.0", nil), remotePluginArchiveSigningContext{})
	if err != nil {
		t.Fatalf("unsigned install error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(unsignedRoot, ".codex-plugin", "plugin.json")); err != nil {
		t.Fatalf("unsigned archive was not extracted: %v", err)
	}

	malformedHome := t.TempDir()
	_, err = ensureRemoteInstalledPluginCache(context.Background(), bundleServer.Client(), malformedHome, remoteInstalledGlobalMarketplace, newCandidate("1.0.0", json.RawMessage(`{"sha256":"00"}`)), signing)
	if err == nil || !strings.Contains(err.Error(), "malformed_metadata") {
		t.Fatalf("malformed metadata error = %v, want malformed_metadata", err)
	}
}
