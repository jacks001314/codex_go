//go:build darwin && cgo

package appserver

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"codex_go/session"
)

// Provider selection must be testable without probing Touch ID, accessing the
// Keychain, or displaying authentication UI.
func TestDarwinUserVerificationNativeProviderSelection(t *testing.T) {
	home := t.TempDir()
	for name, selected := range map[string]UserVerificationProvider{
		"factory":              newUserVerificationPlatformProvider(),
		"nil service fallback": (&userVerificationService{}).platformProvider(),
		"default router":       NewDefaultRuntimeRouter(session.NewStore(home), home).services.UserVerificationProvider,
	} {
		t.Run(name, func(t *testing.T) {
			native, ok := selected.(*darwinUserVerificationProvider)
			if !ok {
				t.Fatalf("platform provider = %T, want *darwinUserVerificationProvider", selected)
			}
			if _, ok := native.backend.(cgoDarwinUserVerificationBackend); !ok {
				t.Fatalf("native backend = %T, want cgoDarwinUserVerificationBackend", native.backend)
			}
			if _, ok := selected.(userVerificationDeviceSupport); !ok {
				t.Fatalf("native provider does not implement DeviceSupported: %T", selected)
			}
		})
	}
}

// Readiness is a non-interactive probe, not an end-to-end Secure Enclave test.
// Report missing prerequisites explicitly instead of silently discarding errors.
func TestDarwinUserVerificationNativeBiometricReadiness(t *testing.T) {
	backend := cgoDarwinUserVerificationBackend{}
	supported := backend.DeviceSupported()
	err := backend.BiometricsAvailable()
	t.Logf("DeviceSupported=%t; BiometricsAvailable=%v", supported, err)
	if err != nil {
		t.Logf("native error kind=%s reason=%s message=%q", err.kind, err.reason, err.message)
	}
	if !supported || err != nil {
		t.Skipf("native biometric verification unavailable: DeviceSupported=%t; error=%+v; requires Touch ID hardware, enrolled biometrics, and an available local authentication session", supported, err)
	}
	t.Log("biometric readiness only; credential creation and interactive signing were not exercised")
}

// TestDarwinUserVerificationMissingCredentialIsReported covers the native
// verify path when the namespace has no Keychain item. Before the fix this
// aborted the process inside CoreFoundation (SIGILL from CFRelease(NULL)); it
// must instead surface a typed error without ever showing a prompt.
func TestDarwinUserVerificationMissingCredentialIsReported(t *testing.T) {
	backend := cgoDarwinUserVerificationBackend{}
	const label = "com.openai.codex.user-verification.plugin-service.v1.probe-missing"
	key, findErr := backend.FindKey(label)
	if findErr != nil {
		t.Fatalf("FindKey on a missing label = %+v, want an empty result", findErr)
	}
	if key != nil {
		t.Fatalf("FindKey on a missing label = %+v, want nil", key)
	}
	if _, _, err := backend.AuthorizeAndSign(label, []byte("probe"), "Probe", "", nil); err == nil {
		t.Fatal("AuthorizeAndSign without a credential must report a typed error")
	}
}

// TestDarwinUserVerificationLockMatchesRustLayout checks the cross-process
// lifecycle lock Rust places at
// ~/Library/Application Support/com.openai.codex/user-verification/<label>.lock,
// created 0700/0600 and never inside the Keychain.
func TestDarwinUserVerificationLockMatchesRustLayout(t *testing.T) {
	previous := darwinUserVerificationLockRootOverride
	darwinUserVerificationLockRootOverride = ""
	t.Cleanup(func() { darwinUserVerificationLockRootOverride = previous })
	directory, err := darwinUserVerificationLockDirectory()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	if want := filepath.Join(home, "Library", "Application Support", "com.openai.codex", "user-verification"); directory != want {
		t.Fatalf("lock directory = %q, want %q", directory, want)
	}

	// Exercise creation in a scratch root so the assertion never depends on the
	// real home directory's existing permissions.
	scratch := filepath.Join(t.TempDir(), "user-verification")
	darwinUserVerificationLockRootOverride = scratch
	provider := &darwinUserVerificationProvider{backend: &fakeDarwinUVBackend{}, namespace: "probe-label"}
	release, lockErr := provider.lock()
	if lockErr != nil {
		t.Fatalf("lock = %+v", lockErr)
	}
	release()
	info, statErr := os.Stat(scratch)
	if statErr != nil {
		t.Fatalf("stat %s: %v", scratch, statErr)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("lock directory mode = %o, want 0700", info.Mode().Perm())
	}
	lockFile, statErr := os.Stat(filepath.Join(scratch, "probe-label.lock"))
	if statErr != nil {
		t.Fatalf("stat lock file: %v", statErr)
	}
	if lockFile.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode = %o, want 0600", lockFile.Mode().Perm())
	}
}

// Rust enables the elicitation extension from the crate-level device_supported()
// probe, so an unbound RuntimeServices must still consult the platform instead
// of reporting no hardware support.
func TestDarwinUserVerificationDeviceSupportUsesPlatformProbe(t *testing.T) {
	previous := userVerificationPlatformDeviceProbe
	userVerificationPlatformDeviceProbe = func() bool { return true }
	t.Cleanup(func() { userVerificationPlatformDeviceProbe = previous })
	if !userVerificationDeviceSupported(nil) {
		t.Fatal("unbound RuntimeServices must fall back to the platform device probe")
	}
	if userVerificationDeviceSupported(UnsupportedUserVerificationProvider{}) {
		t.Fatal("an explicit unsupported provider must still report no hardware support")
	}
}

// TestDarwinUserVerificationKeyInfoMatchesSecurityFrameworkExport closes the
// loop the pure-Go derivation test cannot: the SEC1 point comes from
// SecKeyCopyExternalRepresentation, the same call the provider uses, and the
// SubjectPublicKeyInfo we build from it must be byte-identical to the DER Rust's
// p256 crate emits (fixed 26-byte P-256 header plus the 65-byte point).
func TestDarwinUserVerificationKeyInfoMatchesSecurityFrameworkExport(t *testing.T) {
	sec1, _, _, err := darwinUserVerificationTestSEC1()
	if err != nil {
		t.Fatalf("Security.framework export: %+v", err)
	}
	if len(sec1) != 65 || sec1[0] != 0x04 {
		t.Fatalf("exported point = %d bytes starting 0x%02x, want 65 starting 0x04", len(sec1), sec1[0])
	}
	curve := elliptic.P256()
	x, y := elliptic.Unmarshal(curve, sec1)
	if x == nil {
		t.Fatal("Security.framework export is not an uncompressed P-256 point")
	}
	wantSPKI, marshalErr := x509.MarshalPKIXPublicKey(&ecdsa.PublicKey{Curve: curve, X: x, Y: y})
	if marshalErr != nil {
		t.Fatalf("marshal SPKI: %v", marshalErr)
	}
	wantPrefix := []byte{
		0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01,
		0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00,
	}
	if len(wantSPKI) != len(wantPrefix)+65 || !bytes.Equal(wantSPKI[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("SPKI header = %x, want the fixed P-256 prefix", wantSPKI)
	}

	key, keyErr := darwinUserVerificationKeyFromSEC1(sec1)
	if keyErr != nil {
		t.Fatalf("derive key: %+v", keyErr)
	}
	if want := base64.RawURLEncoding.EncodeToString(wantSPKI); key.publicKey != want {
		t.Fatalf("publicKey = %q, want %q", key.publicKey, want)
	}
	digest := sha256.Sum256(wantSPKI)
	if want := base64.RawURLEncoding.EncodeToString(digest[:]); key.credentialID != want {
		t.Fatalf("credentialId = %q, want %q", key.credentialID, want)
	}
}

// Rust key_protection::validate rejects a software key even when its curve,
// size, and private-key class match. Use a real ephemeral Security.framework
// key to cover the missing kSecAttrTokenID branch without Secure Enclave.
func TestDarwinUserVerificationRejectsKeyWithoutSecureEnclaveToken(t *testing.T) {
	_, hasToken, protectedKey, err := darwinUserVerificationTestSEC1()
	if err != nil {
		t.Fatalf("create ephemeral software key: %v", err)
	}
	if hasToken {
		t.Fatal("software fixture unexpectedly contains kSecAttrTokenID")
	}
	if protectedKey {
		t.Fatal("native validator accepted a software key without kSecAttrTokenID")
	}
}
