package execserver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"codex_go/network"
)

// TestExecServerRuntimeReadablePathsLikeRust mirrors Rust #60663: the execution
// host grants restricted reads to its own helper executable and the absolute
// CA bundle selected by the child environment, while relative paths are ignored.
func TestExecServerRuntimeReadablePathsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	directory := filepath.Join(home, network.ProxyManagedMITMCADir)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	caCertificate := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(caCertificate, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	caBundle, err := network.PersistProxyManagedCATrustBundle(caCertificate, "bundle")
	if err != nil {
		t.Fatal(err)
	}
	paths := execServerRuntimeReadablePaths(&ExecParams{ManagedNetwork: &ManagedNetworkSandboxContext{}, Env: map[string]string{
		"SSL_CERT_FILE":                      caBundle,
		"DENO_CERT":                          caBundle,
		"CLOUDSDK_CORE_CUSTOM_CA_CERTS_FILE": caBundle,
		"CODEX_CA_CERTIFICATE":               "relative-ca.pem",
	}})
	seen := map[string]bool{}
	for _, path := range paths {
		seen[path] = true
	}
	if !seen[filepath.Clean(caBundle)] {
		t.Fatalf("runtime readable paths = %#v, want CA bundle %q", paths, caBundle)
	}
	ignored := execServerRuntimeReadablePaths(&ExecParams{ManagedNetwork: &ManagedNetworkSandboxContext{}, Env: map[string]string{
		"SSL_CERT_FILE": "/tmp/command-ca.pem",
	}})
	for _, path := range ignored {
		if path == filepath.Clean("/tmp/command-ca.pem") {
			t.Fatalf("command-scoped CA override was granted: %#v", ignored)
		}
	}
	unmanaged := execServerRuntimeReadablePaths(&ExecParams{Env: map[string]string{"SSL_CERT_FILE": caBundle}})
	for _, path := range unmanaged {
		if path == filepath.Clean(caBundle) {
			t.Fatalf("CA bundle granted without managed network: %#v", unmanaged)
		}
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			t.Fatalf("runtime readable path is not absolute: %q", path)
		}
	}
	if runtime.GOOS == "linux" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if absolute, absErr := filepath.Abs(executable); absErr == nil && !seen[filepath.Clean(absolute)] {
			t.Fatalf("runtime readable paths = %#v, want helper executable %q", paths, absolute)
		}
	}

	duplicate := execServerRuntimeReadablePaths(&ExecParams{ManagedNetwork: &ManagedNetworkSandboxContext{}, Env: map[string]string{
		"SSL_CERT_FILE":        caBundle,
		"CODEX_CA_CERTIFICATE": caBundle,
	}})
	count := 0
	for _, path := range duplicate {
		if path == filepath.Clean(caBundle) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate CA bundle granted %d times: %#v", count, duplicate)
	}
}

// TestGeneratedChildOnlyCAOverridesStayOutOfSnapshotsLikeRust mirrors Rust
// #61349: generated defaults are regenerated for each launch while a login
// shell's explicit value remains part of the snapshot.
func TestGeneratedChildOnlyCAOverridesStayOutOfSnapshotsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	directory := filepath.Join(home, network.ProxyManagedMITMCADir)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	certificate := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(certificate, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := network.PersistProxyManagedCATrustBundle(certificate, "bundle")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range network.ProxyChildOnlyCAEnvKeys {
		if !generatedChildOnlyCAEnvOverride(key, bundle) {
			t.Fatalf("%s generated default should be filtered", key)
		}
		if generatedChildOnlyCAEnvOverride(key, "/login/shell/ca.pem") {
			t.Fatalf("%s login-shell value should be captured", key)
		}
	}
	if generatedChildOnlyCAEnvOverride("SSL_CERT_FILE", bundle) {
		t.Fatalf("trust-root keys are not child-only overrides")
	}
}
