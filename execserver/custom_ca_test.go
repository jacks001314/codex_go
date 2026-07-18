package execserver

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPRequestHonorsCodexCustomCALikeRust(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("trusted"))
	}))
	defer server.Close()
	certificate := server.Certificate()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	if err := os.WriteFile(caPath, pemData, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("CODEX_CA_CERTIFICATE", caPath)
	t.Setenv("SSL_CERT_FILE", "")

	response, err := doHTTPRequest(context.Background(), &HTTPRequestParams{Method: http.MethodGet, URL: server.URL})
	if err != nil {
		t.Fatalf("doHTTPRequest() error = %v", err)
	}
	if response.Status != http.StatusOK || response.BodyBase64 != "dHJ1c3RlZA==" {
		t.Fatalf("doHTTPRequest() response = %#v", response)
	}
}

func TestHTTPRequestCustomCAUsesRustPrecedenceAndErrorHint(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.pem")
	t.Setenv("CODEX_CA_CERTIFICATE", missing)
	t.Setenv("SSL_CERT_FILE", filepath.Join(t.TempDir(), "fallback.pem"))
	_, err := newExecServerHTTPClient()
	if err == nil || !strings.Contains(err.Error(), "selected by CODEX_CA_CERTIFICATE") || !strings.Contains(err.Error(), customCAHint) {
		t.Fatalf("newExecServerHTTPClient() error = %v", err)
	}
}
