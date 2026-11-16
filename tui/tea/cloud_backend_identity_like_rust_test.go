package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// Rust #61742: backend identity outlives the live connection, so labels keep
// their marker after a disconnect.
func TestCloudBackendIdentitySurvivesDisconnectLikeRust(t *testing.T) {
	cloud := codextui.NewBackendID()
	model := NewModel(codextui.NewState(nil), Options{
		BackendID:           cloud,
		ServerOwnedSettings: true,
		Width:               80,
		Height:              24,
	})
	if !model.isCloudBackend(cloud) {
		t.Fatal("cloud launch backend was not recorded")
	}
	if !model.noteThreadBackend("thread-cloud", cloud) {
		t.Fatal("thread ownership was not recorded")
	}
	if backendState := model.agentsOverviewBackendState(cloud); backendState != nil {
		backendState.disconnected = true
	}
	if !model.threadLabelCloud("thread-cloud", codextui.BackendID{}) {
		t.Fatal("cloud label lost its marker after disconnect")
	}
}
