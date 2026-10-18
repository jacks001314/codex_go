package appserver

import (
	"errors"
	"testing"

	"codex_go/session"
)

func TestServerDrainingAdmissionErrorExposesReasonLikeRust(t *testing.T) {
	manager := NewThreadManager(NewThreadStatusManager())
	manager.BeginShutdown()

	err := manager.ReserveTurn("thread-1")
	if err == nil {
		t.Fatal("ReserveTurn() succeeded while the server was draining")
	}
	if !errors.Is(err, ErrServerDraining) || !errors.Is(err, session.ErrConflict) {
		t.Fatalf("draining error identity = %v", err)
	}
	if got := err.Error(); got != "Server is draining; retry after reconnecting" {
		t.Fatalf("draining error = %q", got)
	}
	if got := runtimeErrorCode(err); got != JSONRPCInvalidRequestErrorCode {
		t.Fatalf("draining code = %d, want %d", got, JSONRPCInvalidRequestErrorCode)
	}
	data := jsonRPCErrorData(err)
	if data["reason"] != "serverShuttingDown" {
		t.Fatalf("draining data = %#v", data)
	}

	cancelled := false
	if err := manager.RegisterTurn("thread-2", "turn-2", func() { cancelled = true }, 0, nil); !errors.Is(err, ErrServerDraining) || !cancelled {
		t.Fatalf("RegisterTurn() draining error = %v, cancelled = %t", err, cancelled)
	}

	response := ErrorResponse(StringID("request-1"), runtimeErrorCode(err), err.Error(), data)
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Data["reason"] != "serverShuttingDown" {
		t.Fatalf("JSON-RPC draining response = %#v", response)
	}
}
