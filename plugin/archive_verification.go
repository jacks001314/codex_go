package plugin

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Rust parity: codex-rs/plugin-archive-verification (#63487/#63489).
//
// A SHA-256 digest alone cannot prove who produced an archive, so a release
// that carries signing metadata is verified against the Plugin Service release
// signing key before its bytes reach extraction. A release without signing
// metadata keeps the legacy behavior, while metadata that cannot be read fails
// the install instead of silently skipping the check.

const (
	// PluginArchiveSigningAlgorithm is the only archive signature algorithm the
	// Plugin Service publishes.
	PluginArchiveSigningAlgorithm = "RSA_PKCS1_SHA256"
	// PluginArchiveSigningKeyPurpose marks a key that may sign release archives.
	PluginArchiveSigningKeyPurpose = "PLUGIN_RELEASE_ARCHIVE_SIGNING"
	// PluginArchiveEvalSigningKeyPurpose marks a scoped key that may sign eval
	// plugin archives. It is only accepted from the eval_keys response list.
	PluginArchiveEvalSigningKeyPurpose = "EVAL_PLUGIN_RELEASE_ARCHIVE_SIGNING"
	// PluginArchiveMalformedMetadataCode is the failure category for signing
	// metadata that cannot be read.
	PluginArchiveMalformedMetadataCode = "malformed_metadata"
	// PluginArchiveSigningKeysUnavailableCode is the failure category for a key
	// request that cannot reach the Plugin Service.
	PluginArchiveSigningKeysUnavailableCode = "signing_keys_unavailable"
	// PluginArchiveInvalidSigningKeysResponseCode is the failure category for a
	// key response that is malformed or out of bounds.
	PluginArchiveInvalidSigningKeysResponseCode = "invalid_signing_keys_response"
	// PluginArchiveSigningKeysPath is the authenticated endpoint that lists the
	// Plugin Service signing keys.
	PluginArchiveSigningKeysPath = "/ps/plugins/signing-keys"

	maxPluginArchiveSignatureBase64Bytes     = 2048
	maxPluginArchiveKeyIDBytes               = 512
	maxPluginArchiveSigningKeysResponseBytes = 128 * 1024
	maxPluginArchiveSigningKeys              = 64
	maxPluginArchivePublicKeyBase64Bytes     = 16 * 1024
)

// PluginArchiveVerification is the outcome of checking a downloaded archive.
type PluginArchiveVerification string

const (
	// PluginArchiveVerified means the archive digest and detached signature
	// matched a release signing key.
	PluginArchiveVerified PluginArchiveVerification = "verified"
	// PluginArchiveNotRequired means the release carried no signing metadata.
	PluginArchiveNotRequired PluginArchiveVerification = "not_required"
)

// PluginArchiveVerificationError carries one bounded failure category that is
// safe to log or count; it never includes signature or key material.
type PluginArchiveVerificationError struct {
	code    string
	message string
}

func (e *PluginArchiveVerificationError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

// Code reports the failure category used in diagnostics and metrics.
func (e *PluginArchiveVerificationError) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}

// PluginArchiveFailureCode returns the bounded category of a verification
// failure, or an empty string for any other error.
func PluginArchiveFailureCode(err error) string {
	if typed, ok := err.(*PluginArchiveVerificationError); ok {
		return typed.Code()
	}
	return ""
}

func pluginArchiveFailure(code string, message string) *PluginArchiveVerificationError {
	return &PluginArchiveVerificationError{code: code, message: message}
}

var (
	errPluginArchiveMalformedMetadata = pluginArchiveFailure("malformed_metadata", "archive signature metadata is malformed")
	errPluginArchiveUnsupportedAlgo   = pluginArchiveFailure("unsupported_archive_algorithm", "archive signature algorithm is unsupported")
	errPluginArchiveDigestMismatch    = pluginArchiveFailure("digest_mismatch", "archive digest does not match its signed metadata")
	errPluginArchiveSigningKeyUnknown = pluginArchiveFailure("unknown_signing_key", "archive signing key is unknown")
	errPluginArchiveKeyAlgorithm      = pluginArchiveFailure("unsupported_key_algorithm", "archive signing key has an unsupported algorithm")
	errPluginArchiveInvalidPublicKey  = pluginArchiveFailure("invalid_public_key", "archive signing public key is invalid")
	errPluginArchiveSignatureMismatch = pluginArchiveFailure("signature_mismatch", "archive signature does not verify")
	errPluginArchiveKeysUnavailable   = pluginArchiveFailure("signing_keys_unavailable", "archive signing keys are unavailable")
	errPluginArchiveKeysInvalid       = pluginArchiveFailure("invalid_signing_keys_response", "archive signing-key response is invalid")
	errPluginArchiveKeyPurpose        = pluginArchiveFailure("unsupported_key_purpose", "archive signing key has an unsupported purpose")
)

// PluginArchiveSigning is the signing metadata a release publishes next to its
// bundle download URL. An absent or null value preserves legacy behavior; a
// value that exists but cannot be read fails at the download boundary.
type PluginArchiveSigning struct {
	Present   bool
	Malformed bool
	SHA256    string
	Signature string
	KeyID     string
	Algorithm string
}

// ParsePluginArchiveSigning reads the release's `signing` value. Malformed
// metadata is reported as Malformed instead of failing here so unchanged
// cached versions stay reusable (Rust #63487).
func ParsePluginArchiveSigning(raw json.RawMessage) PluginArchiveSigning {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return PluginArchiveSigning{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return PluginArchiveSigning{Malformed: true}
	}
	object, ok := value.(map[string]any)
	if !ok {
		return PluginArchiveSigning{Malformed: true}
	}
	fields := map[string]string{}
	for _, key := range []string{"sha256", "signature", "key_id", "algorithm"} {
		text, ok := object[key].(string)
		if !ok {
			return PluginArchiveSigning{Malformed: true}
		}
		fields[key] = text
	}
	return PluginArchiveSigning{
		Present:   true,
		SHA256:    fields["sha256"],
		Signature: fields["signature"],
		KeyID:     fields["key_id"],
		Algorithm: fields["algorithm"],
	}
}

// PluginArchiveSigningKey is one published Plugin Service signing key.
type PluginArchiveSigningKey struct {
	KeyID                  string `json:"key_id"`
	Purpose                string `json:"purpose"`
	Algorithm              string `json:"algorithm"`
	PublicKeySPKIDERBase64 string `json:"public_key_spki_der_base64"`
}

type pluginArchiveSigningKeysResponse struct {
	Keys            []PluginArchiveSigningKey `json:"keys"`
	EvalKeys        []PluginArchiveSigningKey `json:"eval_keys"`
	CacheTTLSeconds *uint64                   `json:"cache_ttl_seconds"`
}

// FetchPluginArchiveSigningKeys reads the authenticated signing-key list and
// validates its bounds. Older servers publish eval keys in `keys`, so both
// lists are accepted; a key with another purpose fails the fetch.
func FetchPluginArchiveSigningKeys(ctx context.Context, client HTTPDoer, keyURL string, headers http.Header) ([]PluginArchiveSigningKey, error) {
	keys, _, err := FetchPluginArchiveSigningKeysWithTTL(ctx, client, keyURL, headers)
	return keys, err
}

// FetchPluginArchiveSigningKeysWithTTL also reports the server-provided
// cache_ttl_seconds so a caller can reuse the listing (Rust #63490).
func FetchPluginArchiveSigningKeysWithTTL(ctx context.Context, client HTTPDoer, keyURL string, headers http.Header) ([]PluginArchiveSigningKey, uint64, error) {
	keyURL = strings.TrimSpace(keyURL)
	if client == nil || keyURL == "" {
		return nil, 0, errPluginArchiveKeysUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, keyURL, nil)
	if err != nil {
		return nil, 0, errPluginArchiveKeysUnavailable
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, errPluginArchiveKeysUnavailable
	}
	defer response.Body.Close()
	// The HTTP client can follow a redirect after dropping the original
	// credentials, so only the exact requested endpoint is accepted.
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, 0, errPluginArchiveKeysUnavailable
	}
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.String() != keyURL {
		return nil, 0, errPluginArchiveKeysUnavailable
	}
	if response.ContentLength > maxPluginArchiveSigningKeysResponseBytes {
		return nil, 0, errPluginArchiveKeysInvalid
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPluginArchiveSigningKeysResponseBytes+1))
	if err != nil {
		return nil, 0, errPluginArchiveKeysUnavailable
	}
	if len(body) > maxPluginArchiveSigningKeysResponseBytes {
		return nil, 0, errPluginArchiveKeysInvalid
	}
	var decoded pluginArchiveSigningKeysResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, 0, errPluginArchiveKeysInvalid
	}
	keys := make([]PluginArchiveSigningKey, 0, len(decoded.Keys)+len(decoded.EvalKeys))
	seen := map[string]bool{}
	// Rust #63528: keys is the marketplace/release list and eval_keys is the
	// eval-only scoped list. Legacy servers may still publish eval keys in
	// keys, but only with the marketplace purpose; an eval-purpose key there
	// is rejected instead of being silently accepted for every caller.
	for _, list := range []struct {
		keys    []PluginArchiveSigningKey
		purpose string
	}{
		{decoded.Keys, PluginArchiveSigningKeyPurpose},
		{decoded.EvalKeys, PluginArchiveEvalSigningKeyPurpose},
	} {
		for _, key := range list.keys {
			if strings.TrimSpace(key.KeyID) == "" || len(key.PublicKeySPKIDERBase64) > maxPluginArchivePublicKeyBase64Bytes || seen[key.KeyID] {
				return nil, 0, errPluginArchiveKeysInvalid
			}
			seen[key.KeyID] = true
			if key.Purpose != list.purpose {
				return nil, 0, errPluginArchiveKeyPurpose
			}
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 || len(keys) > maxPluginArchiveSigningKeys {
		return nil, 0, errPluginArchiveKeysInvalid
	}
	ttl := uint64(0)
	if decoded.CacheTTLSeconds != nil {
		ttl = *decoded.CacheTTLSeconds
	}
	return keys, ttl, nil
}

// VerifyPluginArchive checks the archive digest and detached signature. The
// bytes are returned unchanged so a caller can hand them straight to
// extraction once the call succeeds.
func VerifyPluginArchive(archive []byte, signing PluginArchiveSigning, keys []PluginArchiveSigningKey) ([]byte, PluginArchiveVerification, error) {
	if !signing.Present {
		if signing.Malformed {
			return nil, "", errPluginArchiveMalformedMetadata
		}
		return archive, PluginArchiveNotRequired, nil
	}
	if signing.Algorithm != PluginArchiveSigningAlgorithm {
		return nil, "", errPluginArchiveUnsupportedAlgo
	}
	var expected [32]byte
	decoded, err := hex.DecodeString(signing.SHA256)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != signing.SHA256 {
		return nil, "", errPluginArchiveMalformedMetadata
	}
	copy(expected[:], decoded)
	if len(signing.Signature) > maxPluginArchiveSignatureBase64Bytes ||
		signing.KeyID == "" ||
		len(signing.KeyID) > maxPluginArchiveKeyIDBytes {
		return nil, "", errPluginArchiveMalformedMetadata
	}
	signatureBytes, err := base64.StdEncoding.DecodeString(signing.Signature)
	if err != nil || len(signatureBytes) == 0 {
		return nil, "", errPluginArchiveMalformedMetadata
	}
	digest := sha256.Sum256(archive)
	if digest != expected {
		return nil, "", errPluginArchiveDigestMismatch
	}
	var key *PluginArchiveSigningKey
	for index := range keys {
		if keys[index].KeyID == signing.KeyID {
			key = &keys[index]
			break
		}
	}
	if key == nil {
		return nil, "", errPluginArchiveSigningKeyUnknown
	}
	if key.Algorithm != PluginArchiveSigningAlgorithm {
		return nil, "", errPluginArchiveKeyAlgorithm
	}
	publicKey, err := pluginArchivePublicKey(key.PublicKeySPKIDERBase64)
	if err != nil {
		return nil, "", errPluginArchiveInvalidPublicKey
	}
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signatureBytes); err != nil {
		return nil, "", errPluginArchiveSignatureMismatch
	}
	return archive, PluginArchiveVerified, nil
}

// pluginArchivePublicKey decodes the published SPKI DER RSA key in the size
// range the release signing algorithm accepts.
func pluginArchivePublicKey(encoded string) (*rsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("archive signing key is not RSA")
	}
	if bits := publicKey.N.BitLen(); bits < 2048 || bits > 8192 {
		return nil, fmt.Errorf("archive signing key size %d is unsupported", bits)
	}
	return publicKey, nil
}
