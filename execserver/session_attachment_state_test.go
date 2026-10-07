package execserver

import (
	"context"
	"testing"
	"time"
)

func testConnectionContext(connectionID string) context.Context {
	return context.WithValue(context.Background(), connectionProtocolStateContextKey{}, &connectionProtocolState{
		connectionID: connectionID,
		detached:     make(chan struct{}),
	})
}

func assertAttached(t *testing.T, entry *serverSessionEntry, connectionID string) {
	t.Helper()
	if entry.connectionID != connectionID {
		t.Fatalf("connectionID = %q, want %q", entry.connectionID, connectionID)
	}
	if entry.detachedConnectionID != "" {
		t.Fatalf("detachedConnectionID = %q while attached, want empty", entry.detachedConnectionID)
	}
	if !entry.detachedExpiresAt.IsZero() {
		t.Fatalf("detachedExpiresAt = %v while attached, want the zero time", entry.detachedExpiresAt)
	}
}

func assertDetached(t *testing.T, entry *serverSessionEntry, connectionID string) {
	t.Helper()
	if entry.connectionID != "" {
		t.Fatalf("connectionID = %q while detached, want empty", entry.connectionID)
	}
	if entry.detachedConnectionID != connectionID {
		t.Fatalf("detachedConnectionID = %q, want %q", entry.detachedConnectionID, connectionID)
	}
	if entry.detachedExpiresAt.IsZero() {
		t.Fatal("detachedExpiresAt is the zero time while detached, want a deadline")
	}
}

// Rust #49286 (65c3f40bef) replaces the three optional fields of AttachmentState
// with Attached/Detached variants so that inconsistent combinations cannot be
// represented. Go keeps a connection id plus a detach (id, deadline) pair in
// separate fields, so the invariant has to hold by construction at both write
// sites; this test pins it: attaching clears the detach pair, detaching sets
// both, and resuming from another connection clears both again.
func TestSessionAttachmentStateInvariantsLikeRust(t *testing.T) {
	server := &Server{sessions: map[string]*serverSessionEntry{}, detachedSessionTTL: 30 * time.Second}
	attachCtx := testConnectionContext("conn-1")

	entry, err := server.attachSession(attachCtx, nil)
	if err != nil || entry == nil {
		t.Fatalf("attachSession() = %v, %v", entry, err)
	}
	assertAttached(t, entry, "conn-1")

	server.detachConnection(attachCtx)
	assertDetached(t, entry, "conn-1")

	resumeID := entry.id
	resumeCtx := testConnectionContext("conn-2")
	resumed, err := server.attachSession(resumeCtx, &resumeID)
	if err != nil || resumed != entry {
		t.Fatalf("attachSession(resume) = %v, %v", resumed, err)
	}
	assertAttached(t, entry, "conn-2")
}

// A detached session stays resumable until its deadline passes; Rust's Detached
// variant carries exactly this (connection_id, expires_at) pair.
func TestDetachedSessionExpiryClearsAttachmentStateLikeRust(t *testing.T) {
	server := &Server{sessions: map[string]*serverSessionEntry{}, detachedSessionTTL: 5 * time.Millisecond}
	ctx := testConnectionContext("conn-1")
	entry, err := server.attachSession(ctx, nil)
	if err != nil || entry == nil {
		t.Fatalf("attachSession() = %v, %v", entry, err)
	}
	server.detachConnection(ctx)

	time.Sleep(20 * time.Millisecond)
	server.expireDetachedSession(entry.id, "conn-1")

	server.registryMu.Lock()
	_, present := server.sessions[entry.id]
	server.registryMu.Unlock()
	if present {
		t.Fatal("expired detached session is still registered")
	}

	resumeID := entry.id
	if _, err := server.attachSession(testConnectionContext("conn-2"), &resumeID); err == nil {
		t.Fatal("resuming an expired session id succeeded, want an unknown-session error")
	}
}
