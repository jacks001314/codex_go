package app

import (
	"os"
	"slices"
	"testing"

	"codex_go/cli"
	"codex_go/config"
)

func profilePtr(name string) *string {
	return &name
}

// TestInteractiveLaunchReasoningOverridesLikeRust covers Rust #50811
// (afb436df8b): the embedded TUI no longer forces reasoning summaries off for
// new threads; only explicit launch choices (generic `-c` overrides or a
// profile-scoped value) are forwarded, and a disabled value is forwarded too.
// Rust test:
// app/tests/new_session_tests.rs::replacement_uses_server_defaults_and_preserves_explicit_launch_settings.
func TestInteractiveLaunchReasoningOverridesLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	// Nothing configured: no override at all, so the server's and the model's
	// reasoning-summary defaults apply (the pre-#50811 code forced `none`).
	if got := interactiveLaunchReasoningOverrides(&cli.RootOptions{}); len(got) != 0 {
		t.Fatalf("default overrides = %#v, want none", got)
	}

	// A config-file value is not a launch choice.
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_reasoning_summary = \"detailed\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if got := interactiveLaunchReasoningOverrides(&cli.RootOptions{}); len(got) != 0 {
		t.Fatalf("config-file overrides = %#v, want none", got)
	}

	// Generic `-c` overrides are launch choices, in both directions.
	root := &cli.RootOptions{ConfigOverrides: []string{"model_reasoning_summary=concise", "features.concurrent_reasoning_summaries=true"}}
	got := interactiveLaunchReasoningOverrides(root)
	if !slices.Contains(got, "model_reasoning_summary=concise") {
		t.Fatalf("cli overrides = %#v, want the summary choice", got)
	}
	if !slices.Contains(got, "features.concurrent_reasoning_summaries=true") {
		t.Fatalf("cli overrides = %#v, want the concurrent summary choice", got)
	}
	root = &cli.RootOptions{ConfigOverrides: []string{"model_reasoning_summary=none"}}
	if got := interactiveLaunchReasoningOverrides(root); !slices.Contains(got, "model_reasoning_summary=none") {
		t.Fatalf("disabled cli overrides = %#v, want none preserved", got)
	}
}

// TestLaunchSettingForKeyMatchesRustIsLaunch covers the Rust #50811 `is_launch`
// predicate: the winning layer decides, SessionFlags and profile-scoped user
// values are launch choices, and dotted feature paths resolve too
// (config.HasLaunchSetting handles top-level keys only).
func TestLaunchSettingForKeyMatchesRustIsLaunch(t *testing.T) {
	userLayer := func(profile *string, values map[string]any) config.Layer {
		source := config.LayerSource{Type: config.LayerSourceUser}
		if profile != nil {
			source.Profile = profile
		}
		return config.Layer{Name: source, Config: values}
	}
	sessionFlags := config.Layer{
		Name:   config.LayerSource{Type: config.LayerSourceSessionFlags},
		Config: map[string]any{"model_reasoning_summary": "detailed"},
	}
	project := config.Layer{
		Name:   config.LayerSource{Type: config.LayerSourceProject},
		Config: map[string]any{"model_reasoning_summary": "concise"},
	}

	for _, testCase := range []struct {
		name       string
		layers     []config.Layer
		cliKeys    []string
		key        string
		wantLaunch bool
	}{
		{"absent key", []config.Layer{userLayer(nil, map[string]any{})}, nil, "model_reasoning_summary", false},
		{"plain user value", []config.Layer{userLayer(nil, map[string]any{"model_reasoning_summary": "detailed"})}, nil, "model_reasoning_summary", false},
		{"profile value", []config.Layer{userLayer(profilePtr("p"), map[string]any{"model_reasoning_summary": "detailed"})}, nil, "model_reasoning_summary", true},
		{"session flags layer", []config.Layer{sessionFlags}, nil, "model_reasoning_summary", true},
		{"cli override", nil, []string{"model_reasoning_summary"}, "model_reasoning_summary", true},
		{"project shadows profile", []config.Layer{userLayer(profilePtr("p"), map[string]any{"model_reasoning_summary": "detailed"}), project}, nil, "model_reasoning_summary", false},
		{"dotted feature in profile", []config.Layer{userLayer(profilePtr("p"), map[string]any{"features": map[string]any{"concurrent_reasoning_summaries": true}})}, nil, "features.concurrent_reasoning_summaries", true},
		{"dotted feature in plain user config", []config.Layer{userLayer(nil, map[string]any{"features": map[string]any{"concurrent_reasoning_summaries": true}})}, nil, "features.concurrent_reasoning_summaries", false},
		{"dotted cli override", nil, []string{"features.concurrent_reasoning_summaries"}, "features.concurrent_reasoning_summaries", true},
	} {
		if got := launchSettingForKey(testCase.layers, testCase.cliKeys, testCase.key); got != testCase.wantLaunch {
			t.Fatalf("%s: launchSettingForKey(%q) = %v, want %v", testCase.name, testCase.key, got, testCase.wantLaunch)
		}
	}

	// The nested resolver walks the dotted path only through tables.
	if _, ok := nestedConfigValue(map[string]any{"features": map[string]any{"concurrent_reasoning_summaries": true}}, "features.concurrent_reasoning_summaries"); !ok {
		t.Fatal("nestedConfigValue did not resolve the nested feature flag")
	}
	if _, ok := nestedConfigValue(map[string]any{"features": true}, "features.concurrent_reasoning_summaries"); ok {
		t.Fatal("nestedConfigValue resolved through a non-table value")
	}
}

// TestRemoteConfigValuesForwardProfileReasoningChoicesLikeRust covers the remote
// half of Rust #50811 (afb436df8b): both launch origins forward their
// reasoning-summary choices to the destination server, while a plain config-file
// value is left to the server. Rust test:
// app/tests/startup_defaults_tests.rs::fresh_startup_reads_destination_and_cleared_model_uses_catalog
// (it asserts no `model_reasoning_summary` reaches thread/start config for
// non-launch client settings).
func TestRemoteConfigValuesForwardProfileReasoningChoicesLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	// A plain config-file value is not a launch choice.
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_reasoning_summary = \"detailed\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	values, err := remoteConfigValues(&cli.RootOptions{}, cli.SharedOptions{})
	if err != nil {
		t.Fatalf("remoteConfigValues: %v", err)
	}
	if _, present := values["model_reasoning_summary"]; present {
		t.Fatalf("config-file value forwarded = %#v, want it left to the server", values)
	}

	// A selected profile supplies launch choices, including the nested feature.
	profilePath, err := config.ResolveProfileConfigPath(home, "work")
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if err := os.WriteFile(profilePath, []byte("model_reasoning_summary = \"concise\"\n[features]\nconcurrent_reasoning_summaries = true\n"), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	root := &cli.RootOptions{}
	root.Shared.Profile = "work"
	values, err = remoteConfigValues(root, cli.SharedOptions{})
	if err != nil {
		t.Fatalf("remoteConfigValues: %v", err)
	}
	if got, _ := values["model_reasoning_summary"].(string); got != "concise" {
		t.Fatalf("profile summary forwarded = %q, want concise (%#v)", got, values)
	}
	features, _ := values["features"].(map[string]any)
	if features == nil {
		t.Fatalf("profile feature not forwarded: %#v", values)
	}
	if concurrent, _ := features["concurrent_reasoning_summaries"].(bool); !concurrent {
		t.Fatalf("profile concurrent summaries forwarded = %#v, want true", features)
	}

	// The same launch resolution drives the embedded host.
	if got := interactiveLaunchReasoningOverrides(root); !slices.Contains(got, "model_reasoning_summary=concise") {
		t.Fatalf("embedded overrides = %#v, want the profile summary", got)
	}
}
