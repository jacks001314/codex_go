package execserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/coder/websocket"
)

// Mirrors Rust `only_known_http_rejection_headers_are_reported`: a known legacy
// body must not override a header or stand in for a missing one, and unknown
// header values are dropped.
func TestOnlyKnownRendezvousRejectionHeadersAreReported(t *testing.T) {
	cases := []struct {
		header   string
		present  bool
		expected string
	}{
		{header: "expired", present: true, expected: "expired"},
		{header: "invalid_signature", present: true, expected: "invalid_signature"},
		{header: "pod_draining", present: true, expected: "pod_draining"},
		{header: "expired secret=credential", present: true, expected: ""},
		{header: "private-proxy-hostname", present: true, expected: ""},
		{present: false, expected: ""},
	}
	for _, tc := range cases {
		response := &http.Response{StatusCode: 401, Header: http.Header{}}
		if tc.present {
			response.Header.Set(rendezvousRejectionReasonHeader, tc.header)
		}
		failure := classifyRendezvousFailure(errors.New("handshake rejected"), response)
		if failure.ErrorKind != "http" || failure.HTTPStatus != 401 {
			t.Fatalf("header %q: failure = %#v", tc.header, failure)
		}
		if failure.RejectionReason != tc.expected {
			t.Fatalf("header %q: rejection reason = %q, want %q", tc.header, failure.RejectionReason, tc.expected)
		}
	}
}

// Mirrors Rust's typed I/O failure coverage: transport failures report the
// category and the POSIX-style kind, never the raw error text.
func TestRendezvousIOFailuresAreTyped(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	if failure := classifyRendezvousFailure(refused, nil); failure.ErrorKind != "io" || failure.IOErrorKind != "connection_refused" {
		t.Fatalf("refused failure = %#v", failure)
	}
	timeout := &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	if failure := classifyRendezvousFailure(timeout, nil); failure.ErrorKind != "io" || failure.IOErrorKind != "timed_out" {
		t.Fatalf("timeout failure = %#v", failure)
	}
	eof := classifyRendezvousFailure(io.ErrUnexpectedEOF, nil)
	if eof.ErrorKind != "io" || eof.IOErrorKind != "unexpected_eof" {
		t.Fatalf("unexpected EOF failure = %#v", eof)
	}
	closed := classifyRendezvousFailure(net.ErrClosed, nil)
	if closed.ErrorKind != "connection_closed" {
		t.Fatalf("closed failure = %#v", closed)
	}
	other := classifyRendezvousFailure(errors.New("something else"), nil)
	if other.ErrorKind != "other" || other.IOErrorKind != "" {
		t.Fatalf("untyped failure = %#v", other)
	}
}

func TestRendezvousRequestIDIsCorrelatedAndFormatStable(t *testing.T) {
	first := newRendezvousRequestID()
	second := newRendezvousRequestID()
	if first == second {
		t.Fatalf("request ids must differ per attempt: %q", first)
	}
	if !strings.HasPrefix(first, "req_") || len(first) != len("req_")+32 {
		t.Fatalf("request id = %q, want req_<32 hex chars>", first)
	}
	for _, char := range strings.TrimPrefix(first, "req_") {
		if !strings.ContainsRune("0123456789abcdef", char) {
			t.Fatalf("request id = %q, want lowercase hex", first)
		}
	}
	if header := rendezvousDialOptions(first).HTTPHeader.Get(rendezvousRequestIDHeader); header != first {
		t.Fatalf("dial header = %q, want %q", header, first)
	}
}

// Mirrors Rust's rejected-handshake test: the attempt carries its request id on
// the wire and the failure preserves the HTTP status plus the known rejection
// reason without inspecting the response body.
func TestRejectedRendezvousHandshakeReportsStatusAndReason(t *testing.T) {
	var seenRequestID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRequestID = r.Header.Get(rendezvousRequestIDHeader)
		w.Header().Set(rendezvousRejectionReasonHeader, "expired")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("expired secret=credential"))
	}))
	defer server.Close()

	requestID := newRendezvousRequestID()
	ctx, cancel := context.WithTimeout(context.Background(), 10_000_000_000)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), rendezvousDialOptions(requestID))
	if conn != nil {
		_ = conn.CloseNow()
	}
	if err == nil {
		t.Fatal("dial unexpectedly succeeded")
	}
	if seenRequestID != requestID {
		t.Fatalf("server saw request id %q, want %q", seenRequestID, requestID)
	}
	if response == nil {
		t.Fatalf("handshake failure lost the HTTP response: %v", err)
	}
	failure := classifyRendezvousFailure(err, response)
	if failure.ErrorKind != "http" || failure.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("failure = %#v", failure)
	}
	if failure.RejectionReason != "expired" {
		t.Fatalf("rejection reason = %q, want expired", failure.RejectionReason)
	}
}
