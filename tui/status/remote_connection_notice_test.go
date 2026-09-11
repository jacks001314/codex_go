package status

import "testing"

// Mirrors Rust remote_connection_tests::server_version_notice_only_for_older_official_server
// (#43622).
func TestServerVersionNoticeOnlyForOlderOfficialServer(t *testing.T) {
	server := "0.152.1"
	message, ok := ServerVersionNoticeMessage("0.153.0", &server)
	if !ok || message != "A background Codex service is running v0.152.1, older than your Codex CLI v0.153.0." {
		t.Fatalf("notice = %q ok=%v", message, ok)
	}
	same := "0.153.0"
	if _, ok := ServerVersionNoticeMessage("0.153.0", &same); ok {
		t.Fatal("equal versions must not warn")
	}
	source := "0.0.0"
	if _, ok := ServerVersionNoticeMessage("0.0.0", &server); ok {
		t.Fatal("source builds must not warn")
	}
	if _, ok := ServerVersionNoticeMessage("0.153.0", nil); ok {
		t.Fatal("missing server version must not warn")
	}
	if _, ok := ServerVersionNoticeMessage("0.153.0", &source); ok {
		t.Fatal("source server build must not warn")
	}
}

// Mirrors Rust update_command_is_only_suggested_for_implicit_local_daemon and
// notice_key_identifies_socket_and_server_home_across_connection_modes.
func TestPendingServerVersionNoticeDedupAndServiceIdentity(t *testing.T) {
	const (
		client = "0.153.0"
		server = "0.152.1"
	)
	notice, key, ok := PendingServerVersionNotice(RemoteConnectionUnixSocket, "/tmp/codex.sock", "", client, server, true, "")
	if !ok || notice == nil || !notice.OfferUpdate {
		t.Fatalf("local notice = %#v ok=%v", notice, ok)
	}
	if _, _, ok := PendingServerVersionNotice(RemoteConnectionUnixSocket, "/tmp/codex.sock", "", client, server, true, key); ok {
		t.Fatal("same notice key must be suppressed")
	}
	remote, remoteKey, ok := PendingServerVersionNotice(RemoteConnectionWebSocket, "wss://example.invalid/rpc?workspace=ws-1", "", client, server, false, "")
	if !ok || remote == nil || remote.OfferUpdate {
		t.Fatalf("remote notice = %#v ok=%v", remote, ok)
	}
	// Credentials and non-routing query parameters do not change the identity.
	_, sameRemoteKey, ok := PendingServerVersionNotice(RemoteConnectionWebSocket, "wss://user:pass@example.invalid/rpc?workspace=ws-1&token=secret#frag", "", client, server, false, "")
	if !ok || sameRemoteKey != remoteKey {
		t.Fatalf("sanitized key = %q, want %q", sameRemoteKey, remoteKey)
	}
	// A different routing workspace is a different service identity.
	_, otherKey, ok := PendingServerVersionNotice(RemoteConnectionWebSocket, "wss://example.invalid/rpc?workspace=ws-2", "", client, server, false, "")
	if !ok || otherKey == remoteKey {
		t.Fatalf("distinct routing workspace key = %q equal to %q", otherKey, remoteKey)
	}
	// The server home is part of the identity.
	_, otherHome, ok := PendingServerVersionNotice(RemoteConnectionUnixSocket, "/tmp/codex.sock", "/home/a", client, server, true, "")
	if !ok || otherHome == key {
		t.Fatalf("distinct server home key = %q equal to %q", otherHome, key)
	}
}
