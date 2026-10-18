package codemode

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRemoteSessionNotFoundRetiresBindingLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		err        error
		wantRetire bool
	}{
		{name: "missing session", err: status.Error(codes.NotFound, "unknown session"), wantRetire: true},
		{name: "invalid argument", err: status.Error(codes.InvalidArgument, "bad request")},
		{name: "ordinary error", err: errors.New("transport failed")},
		{name: "success"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			connection := &remoteConnection{}
			session := &remoteSession{openedOn: connection}
			session.invalidateSessionOnNotFound(connection, testCase.err)
			if retired := session.openedOn == nil; retired != testCase.wantRetire {
				t.Fatalf("retired = %t, want %t", retired, testCase.wantRetire)
			}
		})
	}
}

// Mirrors Rust #52825: a call created before a runtime replacement must report
// the explicit reset error instead of executing against the new generation.
func TestRemoteSessionStaleGenerationReportsResetLikeRust(t *testing.T) {
	session := &remoteSession{openedOn: &remoteConnection{}, generation: 2}
	if err := session.ensureGeneration(1, true); err == nil || err.Error() != execRuntimeResetError {
		t.Fatalf("stale generation error = %v", err)
	}
	// Calls created before the first binding join the first opened generation.
	if err := session.ensureGeneration(0, false); err != nil {
		t.Fatalf("unbound generation error = %v", err)
	}
}

// Mirrors Rust #52825: terminate only uses the existing binding and must not
// reopen a runtime, which could hide the reset from the next request.
func TestRemoteSessionTerminateDoesNotOpenLikeRust(t *testing.T) {
	session := &remoteSession{}
	if _, err := session.existingConnection(); err == nil {
		t.Fatal("existingConnection() opened a new binding")
	}
	session.closed = true
	session.openedOn = &remoteConnection{}
	if _, err := session.existingConnection(); err == nil {
		t.Fatal("existingConnection() accepted a closed session")
	}
}
