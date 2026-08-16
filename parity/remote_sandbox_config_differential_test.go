package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

// TestRustRemoteSandboxConfigSamplesRunInGo is the djalign dynamic-layer
// method-1 shared-fixture differential for remote_sandbox_config: the samples
// below mirror the Rust config_requirements.rs test module (single source of
// truth) for hostname-pattern-scoped sandbox mode requirements. Go parses the
// same TOML input with the same hostname and must produce the same
// allowed_sandbox_modes outcome as Rust apply_remote_sandbox_config.
//
// The Rust side is pinned by name: the test first verifies the referenced
// #[test] fn still exists in config/src/config_requirements.rs, so upstream
// removal or renames break the contract instead of silently drifting.
func TestRustRemoteSandboxConfigSamplesRunInGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "config", "src", "config_requirements.rs"))
	if err != nil {
		t.Fatalf("ReadFile(config_requirements.rs) error = %v", err)
	}

	cases := []struct {
		id         string
		rustTest   string
		values     map[string]any
		hostname   string
		wantModes  []sandbox.SandboxMode
		wantReject string // substring the Rust assertion checks on reject
	}{
		{
			id:       "deserialize_remote_sandbox_config_accepts_patterns_and_modes",
			rustTest: "deserialize_remote_sandbox_config_requires_hostname_patterns_list",
			values: map[string]any{
				"remote_sandbox_config": []any{
					map[string]any{
						"hostname_patterns":     []any{"*.org", "runner-??.ci"},
						"allowed_sandbox_modes": []any{"read-only", "workspace-write"},
					},
				},
			},
			hostname:  "build.org",
			wantModes: []sandbox.SandboxMode{sandbox.SandboxReadOnly, sandbox.SandboxWorkspaceWrite},
		},
		{
			id:       "remote_sandbox_config_rejects_string_hostname_patterns",
			rustTest: "deserialize_remote_sandbox_config_requires_hostname_patterns_list",
			values: map[string]any{
				"remote_sandbox_config": []any{
					map[string]any{
						"hostname_patterns":     "*.org",
						"allowed_sandbox_modes": []any{"read-only"},
					},
				},
			},
			wantReject: "hostname_patterns list",
		},
		{
			id:       "remote_sandbox_config_first_match_overrides_top_level",
			rustTest: "remote_sandbox_config_first_match_overrides_top_level",
			values: map[string]any{
				"allowed_sandbox_modes": []any{"read-only"},
				"remote_sandbox_config": []any{
					map[string]any{
						"hostname_patterns":     []any{"build-*.example.com"},
						"allowed_sandbox_modes": []any{"read-only", "workspace-write"},
					},
					map[string]any{
						"hostname_patterns":     []any{"build-01.example.com"},
						"allowed_sandbox_modes": []any{"read-only", "danger-full-access"},
					},
				},
			},
			hostname:  "BUILD-01.EXAMPLE.COM.",
			wantModes: []sandbox.SandboxMode{sandbox.SandboxReadOnly, sandbox.SandboxWorkspaceWrite},
		},
		{
			id:       "remote_sandbox_config_non_match_preserves_top_level",
			rustTest: "remote_sandbox_config_non_match_preserves_top_level",
			values: map[string]any{
				"allowed_sandbox_modes": []any{"read-only"},
				"remote_sandbox_config": []any{
					map[string]any{
						"hostname_patterns":     []any{"build-*.example.com"},
						"allowed_sandbox_modes": []any{"read-only", "workspace-write"},
					},
				},
			},
			hostname:  "laptop.example.com",
			wantModes: []sandbox.SandboxMode{sandbox.SandboxReadOnly},
		},
	}

	// The fourth Rust sample,
	// remote_sandbox_config_does_not_override_higher_precedence_sandbox_modes,
	// exercises layer merge (merge_unset_fields) rather than single-layer
	// apply; its behavior is pinned in the config package test
	// (TestRemoteSandboxConfigDoesNotOverrideHigherPrecedenceModes) which has
	// access to mergeConfigRequirements. Pin the fn name here so upstream
	// removal or renames still break the contract.
	for _, rustTest := range []string{
		"remote_sandbox_config_does_not_override_higher_precedence_sandbox_modes",
	} {
		if !strings.Contains(string(source), "fn "+rustTest+"()") {
			t.Fatalf("Rust test fn %s no longer exists in config/src/config_requirements.rs; re-sync the shared fixture", rustTest)
		}
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			if !strings.Contains(string(source), "fn "+tc.rustTest+"()") {
				t.Fatalf("Rust test fn %s no longer exists in config/src/config_requirements.rs; re-sync the shared fixture", tc.rustTest)
			}
			cfg, err := config.ConfigRequirementsFromMapWithHostname(tc.values, tc.hostname)
			if tc.wantReject != "" {
				if err == nil {
					t.Fatal("Rust rejects this input; Go accepted it")
				}
				if !strings.Contains(err.Error(), tc.wantReject) {
					t.Fatalf("Go error %q missing Rust-asserted substring %q", err, tc.wantReject)
				}
				return
			}
			if err != nil {
				t.Fatalf("Rust accepts this input; Go rejected it: %v", err)
			}
			if cfg == nil {
				t.Fatal("Go produced nil requirements for accepted input")
			}
			if len(cfg.AllowedSandboxModes) != len(tc.wantModes) {
				t.Fatalf("AllowedSandboxModes = %v, want %v", cfg.AllowedSandboxModes, tc.wantModes)
			}
			for i := range tc.wantModes {
				if cfg.AllowedSandboxModes[i] != tc.wantModes[i] {
					t.Fatalf("AllowedSandboxModes = %v, want %v", cfg.AllowedSandboxModes, tc.wantModes)
				}
			}
		})
	}
}
