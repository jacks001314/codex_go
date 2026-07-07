package network

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCAEnvFromMapAndStartupValues(t *testing.T) {
	env := map[string]string{
		"SSL_CERT_FILE":       "/tmp/certs.pem",
		ProxySSLCertDirEnvKey: "/tmp/certs",
		"NODE_EXTRA_CA_CERTS": "",
		"IGNORED":             "value",
	}
	got := ProxyCAEnvFromMap(env)
	if got["SSL_CERT_FILE"] != "/tmp/certs.pem" || got[ProxySSLCertDirEnvKey] != "/tmp/certs" {
		t.Fatalf("ca env = %#v", got)
	}
	startup := ProxyStartupCAFileEnvValues(env)
	if startup["SSL_CERT_FILE"] != "/tmp/certs.pem" {
		t.Fatalf("startup = %#v", startup)
	}
	if _, ok := startup["NODE_EXTRA_CA_CERTS"]; ok {
		t.Fatalf("empty startup value should be skipped")
	}
}

func TestPersistManagedCATrustBundleAndHashValidation(t *testing.T) {
	dir := t.TempDir()
	managedCAPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(managedCAPath, []byte("managed ca\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := PersistProxyManagedCATrustBundle(managedCAPath, "trusted roots")
	if err != nil {
		t.Fatal(err)
	}
	if !IsProxyGeneratedTrustBundlePath(path, dir) {
		t.Fatalf("expected generated bundle path")
	}
	if err := os.WriteFile(path, []byte("tampered roots"), 0o600); err != nil {
		t.Fatal(err)
	}
	if IsProxyGeneratedTrustBundlePath(path, dir) {
		t.Fatalf("tampered generated bundle should be rejected")
	}
}

func TestCurrentGeneratedTrustBundlePathDetectsEmbeddedManagedCA(t *testing.T) {
	dir := t.TempDir()
	managedCAPath := filepath.Join(dir, "ca.pem")
	bundlePath := filepath.Join(dir, "ca-bundle-parent.pem")
	if err := os.WriteFile(managedCAPath, []byte("managed ca\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundlePath, []byte("roots\nmanaged ca\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsCurrentProxyGeneratedTrustBundlePath(bundlePath, managedCAPath) {
		t.Fatalf("expected inherited bundle to be current")
	}
	if err := os.WriteFile(bundlePath, []byte("stale roots\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if IsCurrentProxyGeneratedTrustBundlePath(bundlePath, managedCAPath) {
		t.Fatalf("stale inherited bundle should be rejected")
	}
}

func TestBuildManagedCATrustBundleAppendsStartupAndManagedCA(t *testing.T) {
	dir := t.TempDir()
	managedCAPath := filepath.Join(dir, "ca.pem")
	startupPath := filepath.Join(dir, "startup.pem")
	certDir := filepath.Join(dir, "certs")
	if err := os.Mkdir(certDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedCAPath, []byte("managed ca\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(startupPath, []byte("startup ca\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "dir.pem"), []byte("dir ca\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := BuildProxyManagedCATrustBundle(managedCAPath, map[string]string{"SSL_CERT_FILE": startupPath}, certDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"startup ca\n", "dir ca\n", "managed ca\n"} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("bundle missing %q: %q", want, bundle)
		}
	}
}

func TestPushCertificatePEM(t *testing.T) {
	var builder strings.Builder
	PushProxyCertificatePEM(&builder, []byte("abc"))
	got := builder.String()
	if !strings.Contains(got, "-----BEGIN CERTIFICATE-----") || !strings.Contains(got, "YWJj") {
		t.Fatalf("pem = %q", got)
	}
}

func TestGeneratedManagedCAArtifactPathRejectsBadHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fmt.Sprintf("ca-bundle-%064x.pem", 1))
	if err := os.WriteFile(path, []byte("bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if IsProxyGeneratedManagedCAArtifactPath(path, dir, ProxyManagedMITMCATrustBundlePrefix) {
		t.Fatalf("bad hash should be rejected")
	}
	sum := sha256.Sum256([]byte("bundle"))
	good := filepath.Join(dir, fmt.Sprintf("ca-bundle-%x.pem", sum[:]))
	if err := os.WriteFile(good, []byte("bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsProxyGeneratedManagedCAArtifactPath(good, dir, ProxyManagedMITMCATrustBundlePrefix) {
		t.Fatalf("good hash should be accepted")
	}
}
