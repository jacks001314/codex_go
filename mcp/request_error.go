package mcp

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
)

// requestError wraps an MCP request failure with the bounded caller-side
// category Rust reports through `rmcp_client::request_error_code` (#54094). The
// wrapped error keeps its model-facing text; only telemetry reads the code.
type requestError struct {
	code string
	err  error
}

func (e *requestError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *requestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// RequestErrorCode implements tool.RequestErrorCoder.
func (e *requestError) RequestErrorCode() string {
	if e == nil {
		return ""
	}
	return e.code
}

// mcpRequestErrorCode classifies a request failure without parsing error text.
// It mirrors Rust's ServiceError match: only known transport/protocol failures
// get a code, everything else stays uncoded.
func mcpRequestErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var remote *MCPRemoteError
	if errors.As(err, &remote) {
		return "protocol_error"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return "unexpected_response"
	}
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return "transport_closed"
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "request_timeout"
		}
		return "transport_error"
	}
	return ""
}

// withMCPRequestErrorCode attaches the bounded category when one is known.
func withMCPRequestErrorCode(err error) error {
	if err == nil {
		return nil
	}
	var existing *requestError
	if errors.As(err, &existing) {
		return err
	}
	code := strings.TrimSpace(mcpRequestErrorCode(err))
	if code == "" {
		return err
	}
	return &requestError{code: code, err: err}
}
