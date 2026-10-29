package execserver

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"codex_go/network"
)

// TestCustomCAClientKeepsPlatformRootsIsolatedLikeRust mirrors Rust #54056
// (`http-client/src/native_roots.rs` consumed as `(*load()).clone()` in
// `custom_ca.rs`): the custom CA bundle is layered onto a private copy of the
// platform anchors, so the client trusts the bundle while the process-wide
// platform pool stays untouched.
func TestCustomCAClientKeepsPlatformRootsIsolatedLikeRust(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("trusted"))
	}))
	defer server.Close()
	certificate := server.Certificate()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("CODEX_CA_CERTIFICATE", caPath)
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", "")

	client, err := newExecServerHTTPClient()
	if err != nil {
		t.Fatalf("newExecServerHTTPClient() error = %v", err)
	}
	transport := network.BaseTransport(client.Transport)
	if transport == nil || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
		t.Fatalf("client transport = %#v, want TLS roots carrying the custom CA", client.Transport)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: transport.TLSClientConfig.RootCAs}); err != nil {
		t.Fatalf("client roots do not trust the configured custom CA: %v", err)
	}

	systemRoots, err := x509.SystemCertPool()
	if err != nil || systemRoots == nil {
		t.Skipf("no platform pool on this host: %v", err)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: systemRoots}); err == nil {
		t.Fatal("the custom CA leaked into the process-wide platform pool")
	}
}
