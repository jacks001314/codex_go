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
//
// Scope note (documented architecture difference): Rust strict config also
// rejects serde *type* errors with precedence over unknown-field errors
// (strict_config_tests.rs type_errors_take_precedence_over_ignored_fields,
// e.g. model_context_window = "wide" -> `invalid type: string "wide", expected
// i64`). Go's config surface is a lenient TOML map plus typed getters, so a
// wrong-typed value does not fail loading (it falls back to the getter
// default). That boundary is not part of this shared-fixture contract; only
// the accept/reject semantics of the field surface are.
func TestRustConfigParseSamplesRunInGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "config", "src", "config_toml.rs"))
	if err != nil {
		t.Fatalf("ReadFile(config_toml.rs) error = %v", err)
	}
	strictSource, err := os.ReadFile(filepath.Join(root, "config", "src", "strict_config_tests.rs"))
	if err != nil {
		t.Fatalf("ReadFile(strict_config_tests.rs) error = %v", err)
	}

	cases := []struct {
		id          string
		rustTest    string   // #[test] fn name in config/src/config_toml.rs
		sourceFile  string   // optional Rust source file containing the test (default config_toml.rs)
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
		{
			id:         "strict_config_rejects_unknown_feature_key",
			rustTest:   "strict_config_rejects_unknown_feature_key",
			sourceFile: "strict_config_tests.rs",
			toml:       "[features]\nfoo = true",
			wantAccept: false,
			wantMessage: []string{
				"unknown configuration field `features.foo`",
			},
		},
		{
			id:         "strict_config_rejects_unknown_profile_feature_key",
			rustTest:   "strict_config_rejects_unknown_profile_feature_key",
			sourceFile: "strict_config_tests.rs",
			toml:       "[profiles.work.features]\nfoo = true",
			wantAccept: false,
			wantMessage: []string{
				"unknown configuration field `profiles.work.features.foo`",
			},
		},
		{
			id:         "strict_config_accepts_tool_registry_config",
			rustTest:   "strict_config_accepts_tool_registry_config",
			sourceFile: "strict_config_tests.rs",
			toml:       "[features.tool_registry]\nerror_on_tool_collisions = true",
			wantAccept: true,
		},
		{
			id:         "strict_config_accepts_tool_registry_config_profile",
			rustTest:   "strict_config_accepts_tool_registry_config",
			sourceFile: "strict_config_tests.rs",
			toml:       "[profiles.work.features.tool_registry]\nerror_on_tool_collisions = true",
			wantAccept: true,
		},
		{
			id:         "strict_config_accepts_opaque_desktop_keys",
			rustTest:   "strict_config_accepts_opaque_desktop_keys",
			sourceFile: "strict_config_tests.rs",
			toml:       "[desktop]\nappearanceTheme = \"dark\"\n[desktop.workspace]\ncollapsed = true",
			wantAccept: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			rustSource := string(source)
			rustSourceLabel := "config/src/config_toml.rs"
			if tc.sourceFile != "" {
				rustSource = string(strictSource)
				rustSourceLabel = "config/src/strict_config_tests.rs"
			}
			if !strings.Contains(rustSource, "fn "+tc.rustTest+"()") {
				t.Fatalf("Rust test fn %s no longer exists in %s; re-sync the shared fixture", tc.rustTest, rustSourceLabel)
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
