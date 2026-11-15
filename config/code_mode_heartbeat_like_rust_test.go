package config

import (
	"strings"
	"testing"
)

// Rust #61385 adds the top-level `[code_mode] heartbeat_interval_s` u32.
// Go's gRPC code-mode path uses the framed Transport RPC, so the setting only
// needs to load with Rust's shape and stay available to hosts.
func TestCodeModeHeartbeatIntervalLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{
		"code_mode": map[string]any{"heartbeat_interval_s": int64(30)},
	}}
	if got := cfg.CodeModeHeartbeatIntervalSeconds(); got != 30 {
		t.Fatalf("heartbeat interval = %d, want 30", got)
	}
	if got := (&Config{Values: map[string]any{}}).CodeModeHeartbeatIntervalSeconds(); got != 0 {
		t.Fatalf("missing heartbeat interval = %d, want 0", got)
	}

	valid := map[string]any{"code_mode": map[string]any{"heartbeat_interval_s": int64(30)}}
	if err := ValidateConfigValues(valid); err != nil {
		t.Fatalf("ValidateConfigValues(valid) = %v", err)
	}
	if err := validateKnownTopLevelConfigFields(valid); err != nil {
		t.Fatalf("strict config rejected [code_mode]: %v", err)
	}

	unknown := map[string]any{"code_mode": map[string]any{"future": true}}
	if err := validateKnownTopLevelConfigFields(unknown); err == nil || !strings.Contains(err.Error(), "code_mode.future") {
		t.Fatalf("strict config error = %v, want code_mode.future", err)
	}

	negative := map[string]any{"code_mode": map[string]any{"heartbeat_interval_s": int64(-1)}}
	if err := ValidateConfigValues(negative); err == nil {
		t.Fatal("serde shape validation accepted a negative heartbeat interval")
	}
}
