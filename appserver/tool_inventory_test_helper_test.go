package appserver

import (
	"os"
	"testing"

	"codex_go/config"
)

// toolInfoConfigService enables features.tool_registry.turn_metadata_includes_tool_info
// so a turn's request metadata carries the model-visible tool inventory
// (Rust's collect_tool_namespaces_info gate).
func toolInfoConfigService(t *testing.T) *config.ConfigService {
	t.Helper()
	home := t.TempDir()
	enableToolInfoGate(t, home)
	return config.NewConfigService(home)
}

// enableToolInfoGate appends the tool-info gate to an existing test config home.
func enableToolInfoGate(t *testing.T, home string) {
	t.Helper()
	body, _ := os.ReadFile(config.ConfigPath(home))
	updated := string(body) + "\n[features.tool_registry]\nturn_metadata_includes_tool_info = true\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(updated), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
