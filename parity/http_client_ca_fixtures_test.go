package parity

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"codex_go/network"
)

// TestRustHTTPClientCAFixturesLoadInGo is the shared-fixture double-run for the
// http-client TLS fixtures: Go's x509 stack must accept the same CA material
// Rust's custom_ca loads, including the OpenSSL TRUSTED CERTIFICATE (X509_AUX)
// variant that the standard library rejects without trimming.
func TestRustHTTPClientCAFixturesLoadInGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	fixtures := filepath.Join(root, "http-client", "tests", "fixtures")

	ca := parseFixtureCert(t, fixtures, "test-ca.pem")
	intermediate := parseFixtureCert(t, fixtures, "test-intermediate.pem")
	if !ca.IsCA || !intermediate.IsCA {
		t.Fatalf("fixture CAs: ca.IsCA=%v intermediate.IsCA=%v, want both true", ca.IsCA, intermediate.IsCA)
	}

	trustedData, err := os.ReadFile(filepath.Join(fixtures, "test-ca-trusted.pem"))
	if err != nil {
		t.Fatalf("ReadFile(test-ca-trusted.pem): %v", err)
	}

	// The standard library rejects the X509_AUX variant, proving the
	// normalization adds coverage rather than relying on the stdlib.
	if x509.NewCertPool().AppendCertsFromPEM(trustedData) {
		t.Fatal("stdlib AppendCertsFromPEM unexpectedly accepted X509_AUX data")
	}

	pool := x509.NewCertPool()
	if !network.AppendCertsFromPEMNormalized(pool, trustedData) {
		t.Fatal("AppendCertsFromPEMNormalized rejected the trusted-cert fixture")
	}
	subjects := pool.Subjects()
	if len(subjects) != 1 {
		t.Fatalf("pool subjects = %d, want 1", len(subjects))
	}
	if string(subjects[0]) != string(ca.RawSubject) {
		t.Fatal("trusted cert subject differs from test-ca")
	}
}

func parseFixtureCert(t *testing.T, fixturesDir, name string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixturesDir, name))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("%s: no PEM block", name)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate(%s): %v", name, err)
	}
	return cert
}
