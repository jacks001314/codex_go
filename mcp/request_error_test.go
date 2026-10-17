package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
)

// TestMCPRequestErrorCodeLikeRust mirrors Rust #54094
// (`rmcp_client::request_error_code`): typed request failures map to bounded
// codes, unknown failures stay uncoded, and the wrapper leaves the message and
// error chain untouched.
func TestMCPRequestErrorCodeLikeRust(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"deadline", context.DeadlineExceeded, "request_timeout"},
		{"cancelled", context.Canceled, "cancelled"},
		{"remote", &MCPRemoteError{Method: "tools/call", Code: -32603, Message: "boom"}, "protocol_error"},
		{"unexpected eof", io.ErrUnexpectedEOF, "unexpected_response"},
		{"eof", io.EOF, "unexpected_response"},
		{"closed pipe", io.ErrClosedPipe, "transport_closed"},
		{"net closed", net.ErrClosed, "transport_closed"},
		{"unknown", errors.New("something else"), ""},
		{"nil", nil, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := mcpRequestErrorCode(test.err); got != test.want {
				t.Fatalf("mcpRequestErrorCode(%v) = %q, want %q", test.err, got, test.want)
			}
			wrapped := withMCPRequestErrorCode(test.err)
			if test.want == "" {
				if wrapped != test.err {
					t.Fatalf("uncoded error was wrapped: %v", wrapped)
				}
				return
			}
			if got := mcpRequestErrorCode(wrapped); got != test.want {
				t.Fatalf("wrapped code = %q, want %q", got, test.want)
			}
			var coder interface{ RequestErrorCode() string }
			if !errors.As(wrapped, &coder) || coder.RequestErrorCode() != test.want {
				t.Fatalf("wrapped error does not expose code %q: %v", test.want, wrapped)
			}
			if test.err != nil && wrapped.Error() != test.err.Error() {
				t.Fatalf("wrapped message = %q, want %q", wrapped.Error(), test.err.Error())
			}
			if test.err != nil && !errors.Is(wrapped, test.err) {
				t.Fatalf("wrapped error no longer matches the original chain: %v", wrapped)
			}
		})
	}
}

// TestMCPRequestErrorCodeKeepsContextWrappedTimeouts follows Rust's typed-error
// downcast requirement through Go's wrapping (`%w`): a deadline wrapped in
// context is still classified.
func TestMCPRequestErrorCodeKeepsContextWrappedTimeouts(t *testing.T) {
	err := fmt.Errorf("context: %w", context.DeadlineExceeded)
	if got := mcpRequestErrorCode(err); got != "request_timeout" {
		t.Fatalf("wrapped deadline code = %q", got)
	}
}
