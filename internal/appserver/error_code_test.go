package appserver

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestJSONRPCErrorCodeConstantsMatchRust(t *testing.T) {
	if JSONRPCInvalidRequestErrorCode != -32600 {
		t.Fatalf("invalid request code = %d", JSONRPCInvalidRequestErrorCode)
	}
	if JSONRPCMethodNotFoundErrorCode != -32601 {
		t.Fatalf("method not found code = %d", JSONRPCMethodNotFoundErrorCode)
	}
	if JSONRPCInvalidParamsErrorCode != -32602 {
		t.Fatalf("invalid params code = %d", JSONRPCInvalidParamsErrorCode)
	}
	if JSONRPCInternalErrorCode != -32603 {
		t.Fatalf("internal error code = %d", JSONRPCInternalErrorCode)
	}
}

func TestRouterErrorCodeMappingMatchesRustStandardCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"method not found", methodNotFound("missing/method"), JSONRPCMethodNotFoundErrorCode},
		{"jsonrpc invalid request", jsonRPCInvalidRequest("Invalid request: bad params"), JSONRPCInvalidRequestErrorCode},
		{"invalid params", fmt.Errorf("%w: field is required", ErrInvalidRequest), JSONRPCInvalidParamsErrorCode},
		{"internal fallback", errors.New("boom"), JSONRPCInternalErrorCode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorCode(tc.err); got != tc.want {
				t.Fatalf("errorCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestRuntimeRouterErrorCodeMappingMatchesRustStandardCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"method not found", methodNotFound("missing/method"), JSONRPCMethodNotFoundErrorCode},
		{"jsonrpc invalid request", jsonRPCInvalidRequest("Invalid request: bad params"), JSONRPCInvalidRequestErrorCode},
		{"invalid params", fmt.Errorf("%w: field is required", ErrInvalidRequest), JSONRPCInvalidParamsErrorCode},
		{"internal fallback", errors.New("boom"), JSONRPCInternalErrorCode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeErrorCode(tc.err); got != tc.want {
				t.Fatalf("runtimeErrorCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestRuntimeRouterDecodeParamsErrorMatchesRustInvalidRequest(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	response := router.Handle(&Request{
		ID:     IntID(1),
		Method: MethodInitialize,
		Params: []byte(`{"clientInfo":"not-an-object"}`),
	})
	if response.Error == nil {
		t.Fatalf("expected error, got %+v", response)
	}
	if response.Error.Code != JSONRPCInvalidRequestErrorCode {
		t.Fatalf("error code = %d, want %d; error = %+v", response.Error.Code, JSONRPCInvalidRequestErrorCode, response.Error)
	}
	if !strings.HasPrefix(response.Error.Message, "Invalid request:") {
		t.Fatalf("error message = %q, want Rust Invalid request prefix", response.Error.Message)
	}
}
