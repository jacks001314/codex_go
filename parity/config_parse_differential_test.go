package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

// TestRustConfigParseSamplesRunInGo is the djalign dynamic-layer method-1
// shared-fixture differential for config parsing: every sample input below is
// taken from the Rust config_toml.rs test module (single source of truth),
// Go parses the same TOML input, and the accept/reject semantics must match
// the Rust test assertions exactly.
//
// The Rust side is pinned by name: the test first verifies the referenced
// #[test] fn still exists in config/src/config_toml.rs, so upstream removal or
// renames break the contract instead of silently drifting.
func TestRustConfigParseSamplesRunInGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "config", "src", "config_toml.rs"))
	if err != nil {
		t.Fatalf("ReadFile(config_toml.rs) error = %v", err)
	}

	cases := []struct {
		id          string
		rustTest    string   // #[test] fn name in config/src/config_toml.rs
		toml        string   // shared input
		wantAccept  bool     // Rust serde outcome (accept vs reject)
		wantMessage []string // substrings the Rust assertion checks on reject
		check       func(t *testing.T, cfg *config.Config)
	}{
		{
			id:         "forced_chatgpt_workspace_id_accepts_single_string",
			rustTest:   "forced_chatgpt_workspace_id_accepts_single_string",
			toml:       "forced_chatgpt_workspace_id = \"123e4567-e89b-42d3-a456-426614174000\"",
			wantAccept: true,
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				got := cfg.ForcedChatGPTWorkspaceIDs()
				want := []string{"123e4567-e89b-42d3-a456-426614174000"}
				if len(got) != 1 || got[0] != want[0] {
					t.Fatalf("single string ForcedChatGPTWorkspaceIDs = %v, want %v", got, want)
				}
			},
		},
		{
			id:         "forced_chatgpt_workspace_id_accepts_string_list",
			rustTest:   "forced_chatgpt_workspace_id_accepts_string_list",
			toml:       "forced_chatgpt_workspace_id = [\"123e4567-e89b-42d3-a456-426614174000\", \"123e4567-e89b-42d3-a456-426614174001\"]",
			wantAccept: true,
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				got := cfg.ForcedChatGPTWorkspaceIDs()
				want := []string{"123e4567-e89b-42d3-a456-426614174000", "123e4567-e89b-42d3-a456-426614174001"}
				if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
					t.Fatalf("string list ForcedChatGPTWorkspaceIDs = %v, want %v", got, want)
				}
			},
		},
		{
			id:         "forced_chatgpt_workspace_id_rejects_comma_separated_string",
			rustTest:   "forced_chatgpt_workspace_id_rejects_comma_separated_string",
			toml:       "forced_chatgpt_workspace_id = \"123e4567-e89b-42d3-a456-426614174000,123e4567-e89b-42d3-a456-426614174001\"",
			wantAccept: false,
			wantMessage: []string{
				"TOML list of strings",
				"comma-separated strings are not supported",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			if !strings.Contains(string(source), "fn "+tc.rustTest+"()") {
				t.Fatalf("Rust test fn %s no longer exists in config/src/config_toml.rs; re-sync the shared fixture", tc.rustTest)
			}
			dir := t.TempDir()
			body := "model = \"gpt-5\"\n" + tc.toml + "\n"
			if err := os.WriteFile(config.ConfigPath(dir), []byte(body), 0o600); err != nil {
				t.Fatalf("WriteFile config returned error: %v", err)
			}
			cfg, err := config.LoadEffectiveWithOptions(dir, &config.EffectiveOptions{StrictConfig: true})
			if tc.wantAccept {
				if err != nil {
					t.Fatalf("Rust accepts this input; Go strict config rejected it: %v", err)
				}
				if tc.check != nil {
					tc.check(t, cfg)
				}
				return
			}
			if err == nil {
				t.Fatal("Rust rejects this input; Go strict config accepted it")
			}
			for _, want := range tc.wantMessage {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("Go error %q missing Rust-asserted substring %q", err, want)
				}
			}
		})
	}
}
