package doctor

import (
	"crypto/tls"
	"net"
	"net/url"
	"testing"
)

// Rust #58719 (route_aware_client_pool failure_class): a TLS error wrapped in
// another error layer still classifies as TlsError. Go's errors.As already
// unwraps url.Error and net.OpError chains, so this pins the behavior.
func TestIsTLSProbeErrorUnwrapsWrappedChainLikeRust(t *testing.T) {
	inner := &tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}
	wrapped := &url.Error{Op: "Get", URL: "https://example.com", Err: &net.OpError{Op: "dial", Net: "tcp", Err: inner}}
	if !isTLSProbeError(wrapped) {
		t.Fatalf("wrapped TLS error was not classified as TLS: %v", wrapped)
	}

	proxyAuth := &url.Error{Op: "Get", URL: "https://example.com", Err: &net.OpError{
		Op: "proxyconnect", Net: "tcp", Err: errString("proxy authentication required"),
	}}
	if isTLSProbeError(proxyAuth) {
		t.Fatalf("proxy authentication error was classified as TLS: %v", proxyAuth)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
