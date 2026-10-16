package app

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/codexuds"
)

// Mirrors Rust app-server-transport::verified_local_daemon_socket (#54421): a
// socket is the local daemon's only once its private rendezvous directory
// validates. Rust's own test binds the control socket, asserts the verified
// endpoint, then binds directly at the advertised path (an SSH/socat forward)
// and asserts it is not verified.
func TestVerifiedLocalDaemonSocketRequiresPrivateDirectoryLikeRust(t *testing.T) {
	root, err := os.MkdirTemp("", "r90a-uds")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	defer os.RemoveAll(root)
	// Mirror codexuds's own coverage: the daemon owns a dedicated directory
	// beneath a state root, not the state root itself.
	directory := filepath.Join(root, "app-server-daemon")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	socketPath := filepath.Join(directory, "app-server-control.sock")
	if runtime.GOOS == "windows" {
		// Windows validates the rendezvous directory's DACL, so an unprotected
		// directory is not the daemon's.
		if got, ok := appserver.VerifiedLocalDaemonSocket(socketPath); ok {
			t.Fatalf("VerifiedLocalDaemonSocket(%q) = %q/true, want an unverified socket", socketPath, got)
		}
	}
	if err := codexuds.PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatalf("PreparePrivateSocketDirectory() error = %v", err)
	}
	got, ok := appserver.VerifiedLocalDaemonSocket(socketPath)
	if !ok || filepath.Base(got) != "app-server-control.sock" {
		t.Fatalf("VerifiedLocalDaemonSocket(%q) = %q/%v, want the verified socket path", socketPath, got, ok)
	}
	if got, ok := appserver.VerifiedLocalDaemonSocket(""); ok {
		t.Fatalf("VerifiedLocalDaemonSocket(\"\") = %q/true, want unverified", got)
	}
}

// Mirrors Rust #54421's verified explicit attachment: only the default daemon
// socket that validates as the daemon's private endpoint keeps local semantics.
func TestInteractiveRemoteEndpointIsLocalRequiresVerifiedDefaultDaemonSocket(t *testing.T) {
	home := privateTempHome(t)
	t.Setenv("CODEX_HOME", home)
	defaultSocket := defaultLocalDaemonSocketPath()
	if !strings.HasSuffix(filepath.ToSlash(defaultSocket), "/app-server-control/app-server-control.sock") {
		t.Fatalf("default daemon socket = %q, want the control socket under the codex home", defaultSocket)
	}
	if interactiveRemoteEndpointIsLocal(nil) {
		t.Fatal("a nil endpoint must not classify as local")
	}
	if interactiveRemoteEndpointIsLocal(appserverdaemon.NewUnixSocketEndpoint(filepath.Join(home, "elsewhere.sock"))) {
		t.Fatal("an explicit non-default unix socket must not classify as local")
	}
	// Rust app_server_target_for_launch: loopback websockets stay local.
	if !interactiveRemoteEndpointIsLocal(appserverdaemon.NewWebSocketEndpoint("ws://127.0.0.1:4500/rpc", nil)) {
		t.Fatal("a loopback websocket must stay local")
	}
	if interactiveRemoteEndpointIsLocal(appserverdaemon.NewUnixSocketEndpoint(defaultSocket)) {
		t.Fatal("the default socket needs a private directory before it is local")
	}
	if err := os.MkdirAll(filepath.Dir(defaultSocket), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(defaultSocket), err)
	}
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(defaultSocket)); err != nil {
		t.Fatalf("PreparePrivateSocketDirectory() error = %v", err)
	}
	if _, verified := appserver.VerifiedLocalDaemonSocket(defaultSocket); !verified {
		t.Fatalf("VerifiedLocalDaemonSocket(%q) is unverified after preparing the socket directory", defaultSocket)
	}
	if !interactiveRemoteEndpointIsLocal(appserverdaemon.NewUnixSocketEndpoint(defaultSocket)) {
		t.Fatal("the verified default daemon socket must classify as local")
	}
}

// Mirrors Rust startup_orchestration.rs (#54421): an explicit attachment to the
// default daemon socket requires the server to be present instead of being
// reclassified as remote, and it is replaced by the verified endpoint when the
// endpoint validates.
func TestInteractiveExplicitDefaultDaemonSocketRequiresPresenceLikeRust(t *testing.T) {
	home := privateTempHome(t)
	t.Setenv("CODEX_HOME", home)
	// The interactive launch refuses TERM=dumb before it resolves the remote.
	t.Setenv("TERM", "xterm-256color")
	defaultSocket := defaultLocalDaemonSocketPath()

	// Rust startup_orchestration.rs returns this message through the launch's
	// fatal path, so Go reports it on stderr with the silent exit code.
	stderr := bytes.Buffer{}
	err := runInteractive(context.Background(), &cli.RootOptions{Remote: "unix://"}, strings.NewReader(""), io.Discard, &stderr)
	if err == nil {
		t.Fatal("interactive --remote unix:// unexpectedly succeeded")
	}
	if !strings.Contains(stderr.String(), ExplicitLocalDaemonSocketUnavailableError) {
		t.Fatalf("stderr = %q, want %q (error = %v)", stderr.String(), ExplicitLocalDaemonSocketUnavailableError, err)
	}

	if socketPath, requested := explicitDefaultDaemonSocketPath(appserverdaemon.NewUnixSocketEndpoint(filepath.Join(home, "other.sock"))); requested || socketPath != "" {
		t.Fatalf("explicitDefaultDaemonSocketPath(other socket) = %q/%v, want unrequested", socketPath, requested)
	}
	if socketPath, requested := explicitDefaultDaemonSocketPath(appserverdaemon.NewUnixSocketEndpoint(defaultSocket)); !requested || socketPath != defaultSocket {
		t.Fatalf("explicitDefaultDaemonSocketPath(default) = %q/%v, want the default socket", socketPath, requested)
	}
	if socketPath, requested := explicitDefaultDaemonSocketPath(appserverdaemon.NewWebSocketEndpoint("ws://127.0.0.1:4500/rpc", nil)); requested || socketPath != "" {
		t.Fatalf("explicitDefaultDaemonSocketPath(websocket) = %q/%v, want unrequested", socketPath, requested)
	}

	// The presence probe is a real filesystem check that also works for a bound
	// AF_UNIX socket on this platform.
	if err := os.MkdirAll(filepath.Dir(defaultSocket), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(defaultSocket), err)
	}
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(defaultSocket)); err != nil {
		t.Fatalf("PreparePrivateSocketDirectory() error = %v", err)
	}
	listener, listenErr := net.Listen("unix", defaultSocket)
	if listenErr != nil {
		t.Fatalf("net.Listen(unix, %q) error = %v", defaultSocket, listenErr)
	}
	defer listener.Close()
	if _, statErr := os.Stat(defaultSocket); statErr != nil {
		t.Fatalf("os.Stat(bound socket) error = %v", statErr)
	}
	// Rust rejects a directly bound socket only in its Unix arm (the advertised
	// path must be a symlink to the protected socket). The Windows arm validates
	// the rendezvous directory, so a socket inside the prepared directory stays
	// verified and the launch proceeds through the peer check at connect time.
	if runtime.GOOS == "windows" {
		if _, verified := verifiedLocalDaemonEndpoint(appserverdaemon.NewUnixSocketEndpoint(defaultSocket)); !verified {
			t.Fatal("a socket inside the prepared rendezvous directory stays verified on windows")
		}
	}
}

// privateTempHome returns a codex home beneath a plain temp root. codexuds
// prepares the private rendezvous directory below the state root, so the checks
// run against a directory the daemon itself would create.
func privateTempHome(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "r90a-home")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

// Mirrors Rust app_server_target_for_launch_uses_local_daemon_for_default_socket
// (tui/src/lib.rs:3205-3231, `allow_embedded_fallback: !explicit`) plus
// startup_orchestration.rs:548-552 (`implicit_connection` is required before the
// shared server is started): the default daemon socket is a local daemon whether
// it is named explicitly or discovered implicitly, but only an implicit launch
// may start the shared server or fall back to embedded mode. An explicit
// attachment keeps the remote app-server path.
func TestExplicitAttachmentNeverFallsBackToEmbeddedLikeRust(t *testing.T) {
	originalFeature := daemonAutoStartFeature
	originalStart := daemonAutoStartStart
	t.Cleanup(func() {
		daemonAutoStartFeature = originalFeature
		daemonAutoStartStart = originalStart
	})

	home := privateTempHome(t)
	t.Setenv("CODEX_HOME", home)
	// The interactive launch refuses TERM=dumb before it resolves the remote.
	t.Setenv("TERM", "xterm-256color")

	started := 0
	daemonAutoStartFeature = func(*cli.RootOptions) bool { return true }
	daemonAutoStartStart = func(*cli.RootOptions) (string, error) {
		started++
		return "/tmp/auto-started.sock", nil
	}

	defaultSocket := defaultLocalDaemonSocketPath()
	if err := os.MkdirAll(filepath.Dir(defaultSocket), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(defaultSocket), err)
	}
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(defaultSocket)); err != nil {
		t.Fatalf("PreparePrivateSocketDirectory() error = %v", err)
	}
	listener, listenErr := net.Listen("unix", defaultSocket)
	if listenErr != nil {
		t.Fatalf("net.Listen(unix, %q) error = %v", defaultSocket, listenErr)
	}
	defer listener.Close()

	// `--remote unix://` names the default control socket explicitly, so the
	// launch must neither start the shared server nor fall back to embedded.
	stderr := bytes.Buffer{}
	err := runInteractive(context.Background(), &cli.RootOptions{Remote: "unix://"}, strings.NewReader(""), io.Discard, &stderr)
	if started != 0 {
		t.Fatalf("an explicit attachment started the shared background server %d times", started)
	}
	if err == nil || !strings.Contains(err.Error(), "interactive remote app-server TUI requires a real terminal") {
		t.Fatalf("explicit attachment error = %v, want the remote app-server path instead of an embedded fallback", err)
	}

	// The implicit launch discovers the same socket and may start the server.
	started = 0
	stderr.Reset()
	err = runInteractive(context.Background(), &cli.RootOptions{}, strings.NewReader(""), io.Discard, &stderr)
	if started != 1 {
		t.Fatalf("an implicit launch started the shared background server %d times, want 1", started)
	}
	if err == nil || !strings.Contains(err.Error(), "interactive remote app-server TUI requires a real terminal") {
		t.Fatalf("implicit launch error = %v, want the remote app-server path", err)
	}
}
