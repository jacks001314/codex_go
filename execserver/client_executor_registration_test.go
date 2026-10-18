package execserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Rust #46555: renewal may replace the registration while keeping the same
// executor Noise identity and the same session.
func TestClientRecoveryRenewsExecutorRegistrationLikeRust(t *testing.T) {
	executorKey := RemotePublicKey{Suite: noiseChannelSuite, X25519PublicKey: "executor-key"}
	replacement := newHangingEnvironmentInfoConnection()
	client := newNetworkPolicyTestClient(nil)
	client.sessionID = "session-1"
	client.executorRegistrationID = "registration-1"
	client.executorPublicKey = &executorKey
	client.open = func(_ context.Context, sessionID string, _ func(string, json.RawMessage) error) (clientConnection, *InitializeResponse, clientConnectMetadata, error) {
		return replacement, &InitializeResponse{SessionID: sessionID}, clientConnectMetadata{
			executorRegistrationID: "registration-2",
			executorPublicKey:      &executorKey,
		}, nil
	}
	defer client.Close()

	if err := client.recoverConnection(context.Background()); err != nil {
		t.Fatalf("recoverConnection() error = %v", err)
	}
	if registrationID, ok := client.CachedExecutorRegistrationID(); !ok || registrationID != "registration-2" {
		t.Fatalf("cached registration = %q, %v, want registration-2", registrationID, ok)
	}
	if sessionID := client.SessionID(); sessionID != "session-1" {
		t.Fatalf("session ID after renewal = %q, want session-1", sessionID)
	}
}

// A stale registry lookup must not resume the session against a different
// executor Noise identity, and it must not publish that registration.
func TestClientRecoveryRejectsChangedExecutorNoiseKeyLikeRust(t *testing.T) {
	originalKey := RemotePublicKey{Suite: noiseChannelSuite, X25519PublicKey: "original-executor"}
	changedKey := RemotePublicKey{Suite: noiseChannelSuite, X25519PublicKey: "changed-executor"}
	rejected := newHangingEnvironmentInfoConnection()
	client := newNetworkPolicyTestClient(nil)
	client.sessionID = "session-1"
	client.executorRegistrationID = "registration-1"
	client.executorPublicKey = &originalKey
	client.open = func(_ context.Context, sessionID string, _ func(string, json.RawMessage) error) (clientConnection, *InitializeResponse, clientConnectMetadata, error) {
		return rejected, &InitializeResponse{SessionID: sessionID}, clientConnectMetadata{
			executorRegistrationID: "registration-2",
			executorPublicKey:      &changedKey,
		}, nil
	}
	defer client.Close()

	err := client.recoverConnection(context.Background())
	if err == nil || !strings.Contains(err.Error(), "executor key changed during session recovery") {
		t.Fatalf("recoverConnection() error = %v, want changed executor key", err)
	}
	if registrationID, ok := client.CachedExecutorRegistrationID(); !ok || registrationID != "registration-1" {
		t.Fatalf("cached registration after rejection = %q, %v, want registration-1", registrationID, ok)
	}
	select {
	case <-rejected.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("rejected recovery connection was not closed")
	}
}

func TestClientCachedExecutorRegistrationIDWithoutNoiseSessionLikeRust(t *testing.T) {
	client := newNetworkPolicyTestClient(nil)
	if registrationID, ok := client.CachedExecutorRegistrationID(); ok || registrationID != "" {
		t.Fatalf("cached registration = %q, %v, want absent", registrationID, ok)
	}
}
