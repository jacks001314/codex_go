package execserver

// Rendezvous connection diagnostics.
//
// Rust parity: codex-exec-server's remote/connection_diagnostics.rs (#51483).
// Failed Noise rendezvous connections produce correlated, credential-free
// diagnostics: every attempt carries its own request id (sent as
// `x-request-id`) and failures are reported as typed categories, I/O error
// kinds, HTTP status codes and allowlisted values of the rendezvous rejection
// header. The wire is untrusted, so unknown rejection reasons are dropped and
// response bodies are never inspected.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	// rendezvousRequestIDHeader correlates a client connection attempt with the
	// rendezvous server's logs (Rust remote.rs `connect_rendezvous`).
	rendezvousRequestIDHeader = "x-request-id"
	// rendezvousRejectionReasonHeader is written by the rendezvous server when
	// it rejects a registration. Only allowlisted values are reported.
	rendezvousRejectionReasonHeader = "x-codex-rendezvous-rejection-reason"
)

// rendezvousRejectionReasons mirrors Rust `rejection_reason`: the closed set of
// rejection codes the rendezvous server may report. Anything else (including
// response bodies that contain credentials) is ignored.
var rendezvousRejectionReasons = map[string]bool{
	"expired":                             true,
	"missing_role":                        true,
	"missing_expiry":                      true,
	"missing_signature":                   true,
	"missing_version":                     true,
	"missing_executor_registration_id":    true,
	"unexpected_executor_registration_id": true,
	"malformed_query":                     true,
	"invalid_role":                        true,
	"invalid_expiry":                      true,
	"invalid_signature":                   true,
	"invalid_secret":                      true,
	"extra_query_param":                   true,
	"pod_draining":                        true,
	"unknown_route":                       true,
	"wrong_rendezvous_cluster":            true,
	"route_unavailable":                   true,
}

// rendezvousConnectionFailure mirrors Rust `ConnectionFailure`: a
// credential-free description of a failed rendezvous connection.
type rendezvousConnectionFailure struct {
	ErrorKind       string
	IOErrorKind     string
	HTTPStatus      int
	RejectionReason string
}

// newRendezvousRequestID mirrors Rust `format!("req_{}", Uuid::new_v4().simple())`.
func newRendezvousRequestID() string {
	return "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// rendezvousDialOptions attaches the attempt's correlation id to the upgrade
// request. Embedding hosts' executor headers are added separately by the dialer.
func rendezvousDialOptions(requestID string) *websocket.DialOptions {
	headers := http.Header{}
	if trimmed := strings.TrimSpace(requestID); trimmed != "" {
		headers.Set(rendezvousRequestIDHeader, trimmed)
	}
	return &websocket.DialOptions{HTTPHeader: headers}
}

// rendezvousRejectionReason returns the header value only when it is one of the
// known rejection codes.
func rendezvousRejectionReason(raw string) string {
	value := strings.TrimSpace(raw)
	if rendezvousRejectionReasons[value] {
		return value
	}
	return ""
}

// classifyRendezvousFailure maps a failed dial onto the Rust categories. An
// HTTP handshake rejection is reported as `http` with the status code;
// transport failures are reported as `io` with the POSIX-style kind.
func classifyRendezvousFailure(err error, response *http.Response) rendezvousConnectionFailure {
	if response != nil {
		return rendezvousConnectionFailure{
			ErrorKind:       "http",
			HTTPStatus:      response.StatusCode,
			RejectionReason: rendezvousRejectionReason(response.Header.Get(rendezvousRejectionReasonHeader)),
		}
	}
	if err == nil {
		return rendezvousConnectionFailure{ErrorKind: "other"}
	}
	if isRendezvousConnectionClosed(err) {
		return rendezvousConnectionFailure{ErrorKind: "connection_closed"}
	}
	if isRendezvousTLSFailure(err) {
		return rendezvousConnectionFailure{ErrorKind: "tls"}
	}
	if kind := rendezvousIOErrorKind(err); kind != "" {
		return rendezvousConnectionFailure{ErrorKind: "io", IOErrorKind: kind}
	}
	if isRendezvousProtocolFailure(err) {
		return rendezvousConnectionFailure{ErrorKind: "protocol"}
	}
	return rendezvousConnectionFailure{ErrorKind: "other"}
}

// isRendezvousConnectionClosed mirrors Rust's Error::ConnectionClosed /
// AlreadyClosed: the peer or the local client finished the connection.
func isRendezvousConnectionClosed(err error) bool {
	var closeErr websocket.CloseError
	if errors.As(err, &closeErr) {
		return true
	}
	return errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.EOF) ||
		strings.Contains(err.Error(), "use of closed network connection")
}

func isRendezvousTLSFailure(err error) bool {
	var recordHeaderErr tls.RecordHeaderError
	if errors.As(err, &recordHeaderErr) {
		return true
	}
	var verificationErr *tls.CertificateVerificationError
	if errors.As(err, &verificationErr) {
		return true
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return true
	}
	return strings.Contains(err.Error(), "tls:")
}

// isRendezvousProtocolFailure covers WebSocket framing/handshake errors that
// are not HTTP status rejections (Rust Error::Protocol).
func isRendezvousProtocolFailure(err error) bool {
	message := err.Error()
	return strings.Contains(message, "failed to WebSocket dial") ||
		strings.Contains(message, "websocket:") ||
		strings.Contains(message, "bad handshake")
}

// rendezvousIOErrorKind mirrors Rust's std::io::ErrorKind mapping. Go reports
// transport failures as wrapped `syscall.Errno` values, so the same kinds are
// recovered with errors.Is.
func rendezvousIOErrorKind(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed_out"
	case errors.Is(err, context.Canceled):
		return "interrupted"
	case errors.Is(err, os.ErrDeadlineExceeded):
		return "timed_out"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.Is(err, syscall.ECONNABORTED):
		return "connection_aborted"
	case errors.Is(err, syscall.ENOTCONN):
		return "not_connected"
	case errors.Is(err, syscall.ENETDOWN):
		return "network_down"
	case errors.Is(err, syscall.ENETUNREACH):
		return "network_unreachable"
	case errors.Is(err, syscall.EHOSTUNREACH):
		return "host_unreachable"
	case errors.Is(err, syscall.EADDRNOTAVAIL):
		return "addr_not_available"
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM), errors.Is(err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, syscall.EPIPE):
		return "broken_pipe"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, syscall.EINTR):
		return "interrupted"
	case errors.Is(err, syscall.EAGAIN):
		return "would_block"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timed_out"
	}
	return ""
}
