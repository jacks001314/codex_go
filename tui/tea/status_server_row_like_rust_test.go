package tea

import (
	"strings"
	"testing"
)

// Rust #46905: the TUI fills the /status Server row from the app-server
// target, not from the model provider or daemon menu state.
func TestModelStatusServerRowLikeRust(t *testing.T) {
	local := NewModel(nil, Options{LocalDaemonSession: true, DaemonVersion: "0.153.0", ServerAddress: "unix:///tmp/control.sock"})
	card := local.State.RenderStatusCardWidth(80)
	if !strings.Contains(card, "Server:") || !strings.Contains(card, "Local background server") {
		t.Fatalf("local daemon status card = %q", card)
	}
	remote := NewModel(nil, Options{RemoteAppServer: true, ServerAddress: "ws://127.0.0.1:4500/", ServerVersion: "v1.2.3"})
	remoteCard := remote.State.RenderStatusCardWidth(80)
	if !strings.Contains(remoteCard, "Server:") || !strings.Contains(remoteCard, "ws://127.0.0.1:4500/ (v1.2.3)") {
		t.Fatalf("remote status card = %q", remoteCard)
	}
	embedded := NewModel(nil, Options{})
	if strings.Contains(embedded.State.RenderStatusCardWidth(80), "Server:") {
		t.Fatalf("embedded status card has a Server row: %q", embedded.State.RenderStatusCardWidth(80))
	}
}
