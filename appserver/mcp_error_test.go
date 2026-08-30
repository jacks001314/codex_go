package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"codex_go/mcp"
)

func TestMCPRemoteErrorSurfacesStructuredProtocolErrorLikeRust(t *testing.T) {
	remote := &mcp.MCPRemoteError{Code: -32000, Message: "custom mcp failure", Data: json.RawMessage(`{"detail":"x"}`)}
	if got := errorCode(remote); got != -32000 {
		t.Fatalf("errorCode = %d, want -32000", got)
	}
	if got := jsonRPCErrorMessage(remote); got != "custom mcp failure" {
		t.Fatalf("jsonRPCErrorMessage = %q, want custom mcp failure", got)
	}
	if data := jsonRPCErrorData(remote); data == nil || data["type"] != "mcp_remote_error" || data["code"] != int64(-32000) {
		t.Fatalf("jsonRPCErrorData = %#v", data)
	}

	// A wrapped MCP error is also detected.
	wrapped := fmt.Errorf("context: %w", remote)
	if got := errorCode(wrapped); got != -32000 {
		t.Fatalf("wrapped errorCode = %d, want -32000", got)
	}
	if got := jsonRPCErrorMessage(wrapped); got != "custom mcp failure" {
		t.Fatalf("wrapped jsonRPCErrorMessage = %q, want custom mcp failure", got)
	}

	// Non-MCP errors keep the generic internal code and message.
	if got := errorCode(errors.New("boom")); got != JSONRPCInternalErrorCode {
		t.Fatalf("generic errorCode = %d, want %d", got, JSONRPCInternalErrorCode)
	}
	if got := jsonRPCErrorMessage(errors.New("boom")); got != "boom" {
		t.Fatalf("generic message = %q, want boom", got)
	}
}
