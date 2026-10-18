//go:build windows

package win

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"codex_go/sandbox/windowssandbox"
)

func TestSetupPayloadFromEnvironmentTransportLikeRust(t *testing.T) {
	payload := setupPayload{
		Version:         windowssandbox.SetupVersion,
		CodexHome:       t.TempDir(),
		CommandCWD:      t.TempDir(),
		DenyReadPaths:   []string{`C:\workspace\nested\secret.env`},
		OfflineUsername: "offline",
		OnlineUsername:  "online",
		Mode:            setupModeFull,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	env := map[string]string{}
	if err := windowssandbox.EncodeSandboxLaunchPayload(encoded, env); err != nil {
		t.Fatal(err)
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
	parsed, err := payloadFromArgs([]string{windowssandbox.SandboxLaunchPayloadArg})
	if err != nil {
		t.Fatalf("payloadFromArgs(environment) error = %v", err)
	}
	if parsed.CodexHome != payload.CodexHome || len(parsed.DenyReadPaths) != 1 || parsed.Mode != setupModeFull {
		t.Fatalf("parsed payload = %#v", parsed)
	}
}
