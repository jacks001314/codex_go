//go:build darwin

package network

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeDarwinRootBundleLoadsCertificates(t *testing.T) {
	bundle, err := loadProxyPlatformRootBundle()
	if err != nil {
		t.Fatalf("loadProxyPlatformRootBundle: %v", err)
	}
	pool := x509.NewCertPool()
	if bundle == "" || !pool.AppendCertsFromPEM([]byte(bundle)) {
		t.Fatal("native macOS root bundle contains no parseable certificates")
	}
}

func TestNativeDarwinUnixSocketRoundTrip(t *testing.T) {
	if !proxyUnixSocketSupported() {
		t.Fatal("Darwin proxy must report Unix socket support")
	}
	dir, err := os.MkdirTemp("", "codex-uds-")
	if err != nil {
		t.Fatalf("create short Unix socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "proxy.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on Unix socket: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/native" {
			http.NotFound(writer, request)
			return
		}
		_, _ = io.WriteString(writer, "DARWIN_UDS_OK")
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		_ = listener.Close()
	})

	request, err := http.NewRequest(http.MethodGet, "http://unix-socket/native", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := proxyUnixSocketRoundTrip(request, socketPath)
	if err != nil {
		t.Fatalf("proxyUnixSocketRoundTrip: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "DARWIN_UDS_OK" {
		t.Fatalf("response = %d %q", response.StatusCode, body)
	}
}
