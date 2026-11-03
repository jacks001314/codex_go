package app

import (
	"encoding/json"
	"testing"

	"codex_go/turn"
)

// Rust #57267 (codex-rs/tui/src/app_server_session.rs:1620-1634): turn/start
// fills params.permissions only inside the ThreadSettingsOwner::Client branch,
// so a server-owned (cloud) turn must not carry it. The positive control below
// proves Go's params type does serialize permissions, so the omission on the
// wire is a deliberate server-ownership decision rather than an artefact.
func TestCloudTurnStartOmitsPermissionsLikeRust(t *testing.T) {
	body := sendTurnStartAgainstFakeServer(t, ThreadParamsModeCloud)
	if raw, ok := body["permissions"]; ok {
		t.Fatalf("cloud turn/start sent permissions = %s", raw)
	}

	profile := "profile-1"
	raw, err := json.Marshal(turn.TurnStartParams{ThreadID: "thread-1", Permissions: &profile})
	if err != nil {
		t.Fatalf("marshal control params: %v", err)
	}
	var control map[string]json.RawMessage
	if err := json.Unmarshal(raw, &control); err != nil {
		t.Fatalf("unmarshal control params: %v", err)
	}
	if _, ok := control["permissions"]; !ok {
		t.Fatalf("control params lost the permissions field, so the cloud assertion is vacuous: %s", raw)
	}
}
