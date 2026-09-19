package status

import (
	"strings"
	"testing"
)

// Mirrors Rust remote_connection_tests::server_version_notice_uses_client_release_policy
// (#43622, #46673): an older released service reads "older than", while a local
// client and another release line read "different from".
func TestServerVersionNoticeUsesClientReleasePolicyLikeRust(t *testing.T) {
	older := "0.152.1"
	if message, ok := ServerVersionNoticeMessage("0.153.0", &older); !ok ||
		message != "A background Codex service is running v0.152.1, older than your Codex CLI v0.153.0." {
		t.Fatalf("older notice = %q ok=%v", message, ok)
	}
	same := "0.153.0"
	if _, ok := ServerVersionNoticeMessage("0.153.0", &same); ok {
		t.Fatal("equal versions must not warn")
	}
	// A source build client reports a mismatch instead of staying silent.
	if message, ok := ServerVersionNoticeMessage("0.0.0", &older); !ok ||
		message != "A background Codex service is running v0.152.1, different from your Codex CLI v0.0.0." {
		t.Fatalf("local client notice = %q ok=%v", message, ok)
	}
	if _, ok := ServerVersionNoticeMessage("0.153.0", nil); ok {
		t.Fatal("missing server version must not warn")
	}
	source := "0.0.0"
	if _, ok := ServerVersionNoticeMessage("0.153.0", &source); ok {
		t.Fatal("a source server build must not warn a released client")
	}

	// Prerelease clients order within their release line and report another line
	// as different.
	previousAlpha := "0.153.0-alpha.9"
	if message, ok := ServerVersionNoticeMessage("0.153.0-alpha.10", &previousAlpha); !ok ||
		message != "A background Codex service is running v0.153.0-alpha.9, older than your Codex CLI v0.153.0-alpha.10." {
		t.Fatalf("prerelease older notice = %q ok=%v", message, ok)
	}
	for _, server := range []string{"0.153.0-alpha.10", "0.153.0-alpha.11", "0.153.0"} {
		if message, ok := ServerVersionNoticeMessage("0.153.0-alpha.10", &server); ok {
			t.Fatalf("server %q must not warn the prerelease client: %q", server, message)
		}
	}
	otherLine := "0.156.0"
	if message, ok := ServerVersionNoticeMessage("0.155.0-alpha.12", &otherLine); !ok ||
		message != "A background Codex service is running v0.156.0, different from your Codex CLI v0.155.0-alpha.12." {
		t.Fatalf("other release line notice = %q ok=%v", message, ok)
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

	// A local (source build) client now produces a mismatch notice, and it dedups
	// through the same key rules while keeping the update guidance.
	localNotice, localKey, ok := PendingServerVersionNotice(RemoteConnectionUnixSocket, "/tmp/codex.sock", "", "0.0.0", client, true, "")
	if !ok || localNotice == nil || !localNotice.OfferUpdate ||
		!strings.Contains(localNotice.Message, "different from") {
		t.Fatalf("local mismatch notice = %#v ok=%v", localNotice, ok)
	}
	if _, _, ok := PendingServerVersionNotice(RemoteConnectionUnixSocket, "/tmp/codex.sock", "", "0.0.0", client, true, localKey); ok {
		t.Fatal("the local mismatch notice must dedup by its key")
	}
}
