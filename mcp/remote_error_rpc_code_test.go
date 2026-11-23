package mcp

import "testing"

// MCPRemoteError exposes its numeric JSON-RPC code for span telemetry without
// handing the message or data to the telemetry layer (Rust #62833).
func TestMCPRemoteErrorRPCErrorCode(t *testing.T) {
	err := &MCPRemoteError{Code: -32042, Message: "tool denied", Data: []byte(`{"secret":"x"}`)}
	code, ok := err.RPCErrorCode()
	if !ok || code != -32042 {
		t.Fatalf("RPCErrorCode() = %d, %v", code, ok)
	}
	var nilErr *MCPRemoteError
	if _, ok := nilErr.RPCErrorCode(); ok {
		t.Fatal("nil error reported an RPC code")
	}
}
