package network

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestProxyManagedCAIsProcessLocalAndPrunesInactiveArtifactsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	directory := filepath.Join(home, ProxyManagedMITMCADir)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	staleCertificate, err := persistProxyManagedCACertificateInDirectory(directory, []byte("stale certificate"))
	if err != nil {
		t.Fatal(err)
	}
	staleBundle, err := PersistProxyManagedCATrustBundle(staleCertificate, "bundle with stale certificate")
	if err != nil {
		t.Fatal(err)
	}
	first, err := loadOrCreateProxyManagedCA()
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateProxyManagedCA()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseProxyManagedCA(first)
	defer releaseProxyManagedCA(second)
	if first != second || first.certificatePath == "" {
		t.Fatalf("managed CAs = %p/%p path=%q", first, second, first.certificatePath)
	}
	if _, err := os.Stat(staleCertificate); !os.IsNotExist(err) {
		t.Fatalf("stale certificate still exists: %v", err)
	}
	if _, err := os.Stat(staleBundle); !os.IsNotExist(err) {
		t.Fatalf("stale trust bundle still exists: %v", err)
	}
	if _, err := os.Stat(first.certificatePath); err != nil {
		t.Fatalf("active certificate missing: %v", err)
	}
}

func TestWriteAtomicCreateNewOrReuseHandlesConcurrentIdenticalWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.pem")
	contents := []byte("same contents")
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for index := 0; index < 8; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- writeAtomicCreateNewOrReuse(path, contents, 0o600)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent writer error = %v", err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(contents) {
		t.Fatalf("artifact = %q, err = %v", got, err)
	}
}
