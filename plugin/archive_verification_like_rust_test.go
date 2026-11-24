package plugin

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
	"testing"
)

// Rust parity: codex-rs/plugin-archive-verification (#63487). A release that
// publishes signing metadata is verified against the Plugin Service release
// key; an unsigned release keeps working, and every failure reports a bounded
// category.

type pluginArchiveTestSigner struct {
	key    *rsa.PrivateKey
	keyID  string
	public string
}

func newPluginArchiveTestSigner(t *testing.T) *pluginArchiveTestSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return &pluginArchiveTestSigner{key: key, keyID: "release-key-1", public: base64.StdEncoding.EncodeToString(der)}
}

func (s *pluginArchiveTestSigner) signing(t *testing.T, archive []byte) PluginArchiveSigning {
	t.Helper()
	digest := sha256.Sum256(archive)
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15: %v", err)
	}
	return PluginArchiveSigning{
		Present:   true,
		SHA256:    hex.EncodeToString(digest[:]),
		Signature: base64.StdEncoding.EncodeToString(signature),
		KeyID:     s.keyID,
		Algorithm: PluginArchiveSigningAlgorithm,
	}
}

func (s *pluginArchiveTestSigner) keys() []PluginArchiveSigningKey {
	return []PluginArchiveSigningKey{{
		KeyID:                  s.keyID,
		Purpose:                PluginArchiveSigningKeyPurpose,
		Algorithm:              PluginArchiveSigningAlgorithm,
		PublicKeySPKIDERBase64: s.public,
	}}
}

func TestVerifyPluginArchiveLikeRust(t *testing.T) {
	signer := newPluginArchiveTestSigner(t)
	archive := []byte("plugin archive bytes")
	signing := signer.signing(t, archive)

	if _, outcome, err := VerifyPluginArchive(archive, signing, signer.keys()); err != nil || outcome != PluginArchiveVerified {
		t.Fatalf("verify = (%q, %v), want verified", outcome, err)
	}
	if _, outcome, err := VerifyPluginArchive(archive, PluginArchiveSigning{}, nil); err != nil || outcome != PluginArchiveNotRequired {
		t.Fatalf("unsigned verify = (%q, %v), want not_required", outcome, err)
	}
	if _, _, err := VerifyPluginArchive(archive, PluginArchiveSigning{Malformed: true}, nil); PluginArchiveFailureCode(err) != PluginArchiveMalformedMetadataCode {
		t.Fatalf("malformed metadata error = %v", err)
	}

	tampered := append([]byte(nil), archive...)
	tampered[0] ^= 1
	if _, _, err := VerifyPluginArchive(tampered, signing, signer.keys()); PluginArchiveFailureCode(err) != "digest_mismatch" {
		t.Fatalf("tampered archive error = %v", err)
	}

	// A signature over different bytes keeps the digest but fails the key check.
	otherSigning := signer.signing(t, []byte("different bytes"))
	otherSigning.SHA256 = signing.SHA256
	if _, _, err := VerifyPluginArchive(archive, otherSigning, signer.keys()); PluginArchiveFailureCode(err) != "signature_mismatch" {
		t.Fatalf("signature mismatch error = %v", err)
	}

	unsupported := signing
	unsupported.Algorithm = "RSA_PKCS1_SHA1"
	if _, _, err := VerifyPluginArchive(archive, unsupported, signer.keys()); PluginArchiveFailureCode(err) != "unsupported_archive_algorithm" {
		t.Fatalf("unsupported algorithm error = %v", err)
	}

	unknown := signing
	unknown.KeyID = "missing-key"
	if _, _, err := VerifyPluginArchive(archive, unknown, signer.keys()); PluginArchiveFailureCode(err) != "unknown_signing_key" {
		t.Fatalf("unknown key error = %v", err)
	}

	uppercase := signing
	uppercase.SHA256 = string([]byte("ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"))
	if _, _, err := VerifyPluginArchive(archive, uppercase, signer.keys()); PluginArchiveFailureCode(err) != PluginArchiveMalformedMetadataCode {
		t.Fatalf("uppercase digest error = %v", err)
	}
}

func TestParsePluginArchiveSigningLikeRust(t *testing.T) {
	if signing := ParsePluginArchiveSigning(nil); signing.Present || signing.Malformed {
		t.Fatalf("nil metadata = %#v", signing)
	}
	if signing := ParsePluginArchiveSigning(json.RawMessage("null")); signing.Present || signing.Malformed {
		t.Fatalf("null metadata = %#v", signing)
	}
	if signing := ParsePluginArchiveSigning(json.RawMessage(`"signed"`)); !signing.Malformed {
		t.Fatalf("string metadata = %#v", signing)
	}
	if signing := ParsePluginArchiveSigning(json.RawMessage(`{"sha256":"00"}`)); !signing.Malformed {
		t.Fatalf("incomplete metadata = %#v", signing)
	}
	signing := ParsePluginArchiveSigning(json.RawMessage(`{"sha256":"00","signature":"AA==","key_id":"k","algorithm":"RSA_PKCS1_SHA256"}`))
	if !signing.Present || signing.Malformed || signing.KeyID != "k" || signing.Algorithm != PluginArchiveSigningAlgorithm {
		t.Fatalf("signed metadata = %#v", signing)
	}
}

func TestFetchPluginArchiveSigningKeysLikeRust(t *testing.T) {
	signer := newPluginArchiveTestSigner(t)
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc(PluginArchiveSigningKeysPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys":              signer.keys(),
			"cache_ttl_seconds": 600,
		})
	})
	headers := http.Header{}
	headers.Set("Authorization", "Bearer token")
	keys, err := FetchPluginArchiveSigningKeys(context.Background(), server.Client(), server.URL+PluginArchiveSigningKeysPath, headers)
	if err != nil || len(keys) != 1 || keys[0].KeyID != signer.keyID {
		t.Fatalf("keys = %#v, %v", keys, err)
	}

	wrongPurpose := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"key_id": "k", "purpose": "OTHER", "algorithm": PluginArchiveSigningAlgorithm,
			"public_key_spki_der_base64": signer.public,
		}}})
	}))
	defer wrongPurpose.Close()
	if _, err := FetchPluginArchiveSigningKeys(context.Background(), wrongPurpose.Client(), wrongPurpose.URL+PluginArchiveSigningKeysPath, headers); PluginArchiveFailureCode(err) != "unsupported_key_purpose" {
		t.Fatalf("wrong purpose error = %v", err)
	}

	duplicate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := signer.keys()[0]
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []PluginArchiveSigningKey{key, key}})
	}))
	defer duplicate.Close()
	if _, err := FetchPluginArchiveSigningKeys(context.Background(), duplicate.Client(), duplicate.URL+PluginArchiveSigningKeysPath, headers); PluginArchiveFailureCode(err) != PluginArchiveInvalidSigningKeysResponseCode {
		t.Fatalf("duplicate key error = %v", err)
	}

	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": signer.keys()})
	}))
	defer redirectTarget.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+PluginArchiveSigningKeysPath, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	if _, err := FetchPluginArchiveSigningKeys(context.Background(), redirector.Client(), redirector.URL+PluginArchiveSigningKeysPath, headers); PluginArchiveFailureCode(err) != PluginArchiveSigningKeysUnavailableCode {
		t.Fatalf("redirect error = %v", err)
	}
}
