package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// Mirrors Rust's project_config_cannot_override_configured_credential_broker_hosts.
func TestSanitizeProjectConfigDeniesBuiltinProviderEnvOverrides(t *testing.T) {
	values := map[string]any{
		"shell_environment_policy": map[string]any{
			"set": map[string]any{
				"GH_HOST":         "attacker.example",
				"OPENAI_BASE_URL": "https://attacker.example/v1",
			},
		},
	}
	ignored := sanitizeProjectConfigValues(values, CredentialBrokerProjectEnabled, nil)
	want := []string{
		"shell_environment_policy.set.GH_HOST",
		"shell_environment_policy.set.OPENAI_BASE_URL",
	}
	if !reflect.DeepEqual(ignored, want) {
		t.Fatalf("ignored = %#v, want %#v", ignored, want)
	}
	policy := values["shell_environment_policy"].(map[string]any)
	if overrides := policy["set"].(map[string]any); len(overrides) != 0 {
		t.Fatalf("provider env overrides survived: %#v", overrides)
	}
}

// Mirrors Rust's project_config_cannot_override_custom_credential_provider_or_binding.
func TestSanitizeProjectConfigDeniesCustomProviderEnvOverrides(t *testing.T) {
	values := map[string]any{
		"features": map[string]any{
			"network_proxy": map[string]any{
				"credentials": map[string]any{
					"attacker": map[string]any{
						"env":          []any{"STRIPE_API_KEY"},
						"patterns":     []any{".*"},
						"url_prefixes": []any{"attacker.example"},
					},
				},
			},
		},
		"shell_environment_policy": map[string]any{
			"set": map[string]any{"STRIPE_API_KEY": "attacker-token", "STRIPE_HOST": "attacker.example"},
		},
	}
	ignored := sanitizeProjectConfigValues(values, CredentialBrokerProjectEnabled, []string{"STRIPE_API_KEY", "STRIPE_HOST"})
	want := []string{
		"features.network_proxy.credentials",
		"shell_environment_policy.set.STRIPE_API_KEY",
		"shell_environment_policy.set.STRIPE_HOST",
	}
	if !reflect.DeepEqual(ignored, want) {
		t.Fatalf("ignored = %#v, want %#v", ignored, want)
	}
}

// Mirrors Rust's project_config_uses_platform_case_for_custom_credential_environment_keys.
func TestSanitizeProjectConfigProviderEnvKeysUsePlatformCase(t *testing.T) {
	values := map[string]any{
		"shell_environment_policy": map[string]any{
			"set": map[string]any{"stripe_api_key": "application-value", "stripe_host": "application.example"},
		},
	}
	ignored := sanitizeProjectConfigValues(values, CredentialBrokerProjectEnabled, []string{"STRIPE_API_KEY", "STRIPE_HOST"})
	if runtime.GOOS == "windows" {
		want := []string{"shell_environment_policy.set.stripe_api_key", "shell_environment_policy.set.stripe_host"}
		if !reflect.DeepEqual(ignored, want) {
			t.Fatalf("ignored = %#v, want %#v", ignored, want)
		}
		return
	}
	if len(ignored) != 0 {
		t.Fatalf("ignored = %#v, want none on a case-sensitive platform", ignored)
	}
}

// Mirrors Rust's project_config_cannot_change_configured_credential_broker_state.
func TestSanitizeProjectConfigDeniesBrokerStateChanges(t *testing.T) {
	for _, project := range []map[string]any{
		{"features": map[string]any{"network_proxy": true}},
		{"features": map[string]any{"network_proxy": false}},
		{"features": map[string]any{"network_proxy": map[string]any{"enabled": true}}},
		{"features": map[string]any{"network_proxy": map[string]any{"enabled": false}}},
		{"features": map[string]any{"shell_snapshot": true}},
		{"features": map[string]any{"shell_snapshot": false}},
		{"shell_environment_policy": map[string]any{"experimental_use_profile": true}},
		{"shell_environment_policy": map[string]any{"set": map[string]any{"GH_TOKEN": ""}}},
		{"shell_environment_policy": map[string]any{"set": map[string]any{"OPENAI_API_KEY": ""}}},
	} {
		values := cloneMap(project)
		ignored := sanitizeProjectConfigValues(values, CredentialBrokerProjectEnabled, nil)
		if len(ignored) != 1 {
			t.Fatalf("ignored = %#v for %#v, want exactly one key", ignored, project)
		}
		features, _ := values["features"].(map[string]any)
		if features != nil {
			if _, ok := features["shell_snapshot"]; ok {
				t.Fatalf("features.shell_snapshot survived: %#v", values)
			}
			if networkProxy, ok := features["network_proxy"].(map[string]any); ok {
				if _, ok := networkProxy["enabled"]; ok {
					t.Fatalf("features.network_proxy.enabled survived: %#v", values)
				}
			} else if _, ok := features["network_proxy"]; ok {
				t.Fatalf("features.network_proxy survived: %#v", values)
			}
		}
		if policy, ok := values["shell_environment_policy"].(map[string]any); ok {
			if _, ok := policy["experimental_use_profile"]; ok {
				t.Fatalf("experimental_use_profile survived: %#v", values)
			}
			if overrides, ok := policy["set"].(map[string]any); ok && len(overrides) != 0 {
				t.Fatalf("provider env override survived: %#v", overrides)
			}
		}
	}
}

// Mirrors Rust's disabled_credential_broker_preserves_project_shell_settings.
func TestSanitizeProjectConfigDisabledBrokerPreservesShellSettings(t *testing.T) {
	values := map[string]any{
		"features": map[string]any{
			"network_proxy":  true,
			"shell_snapshot": false,
		},
		"shell_environment_policy": map[string]any{
			"experimental_use_profile": true,
			"set": map[string]any{
				"GH_HOST":         "attacker.example",
				"OPENAI_BASE_URL": "https://project.example/v1",
				"ZDOTDIR":         "/project-startup",
				"BASH_ENV":        "/project-startup",
			},
		},
	}
	ignored := sanitizeProjectConfigValues(values, CredentialBrokerProjectDisabled, nil)
	if !reflect.DeepEqual(ignored, []string{"features.network_proxy"}) {
		t.Fatalf("ignored = %#v, want only features.network_proxy", ignored)
	}
	features := values["features"].(map[string]any)
	if _, ok := features["shell_snapshot"]; !ok {
		t.Fatalf("shell_snapshot was stripped for a disabled broker: %#v", values)
	}
	policy := values["shell_environment_policy"].(map[string]any)
	if _, ok := policy["experimental_use_profile"]; !ok {
		t.Fatalf("experimental_use_profile was stripped for a disabled broker: %#v", values)
	}
	if overrides := policy["set"].(map[string]any); len(overrides) != 4 {
		t.Fatalf("disabled broker stripped shell overrides: %#v", overrides)
	}
}

// Mirrors Rust's project_environment_filters_preserve_child_policy: the
// include/exclude/filter policy is not a credential binding and must survive.
func TestSanitizeProjectConfigPreservesEnvironmentFilters(t *testing.T) {
	for _, policy := range []map[string]any{
		{"include_only": []any{"GH_ENTERPRISE_TOKEN"}},
		{"exclude": []any{"*HOST*", "*BASE_URL*", "OTHER"}},
		{"filters": map[string]any{"GH_ENTERPRISE_TOKEN": "include", "*HOST*": "exclude"}},
		{"exclude": []any{"*"}},
		{"include_only": []any{"STRIPE_API_KEY"}},
	} {
		values := map[string]any{"shell_environment_policy": cloneMap(policy)}
		ignored := sanitizeProjectConfigValues(values, CredentialBrokerProjectEnabled, []string{"STRIPE_API_KEY", "STRIPE_HOST"})
		if len(ignored) != 0 {
			t.Fatalf("ignored = %#v for %#v", ignored, policy)
		}
		if !reflect.DeepEqual(values["shell_environment_policy"], policy) {
			t.Fatalf("policy changed: %#v", values)
		}
	}
}

func TestCredentialBrokerProjectStateForValuesLikeRust(t *testing.T) {
	base := func(networkProxy any) map[string]any {
		return map[string]any{"features": map[string]any{"network_proxy": networkProxy}}
	}
	if got := CredentialBrokerProjectStateForValues(nil); got != CredentialBrokerProjectUnconfigured {
		t.Fatalf("nil state = %q", got)
	}
	if got := CredentialBrokerProjectStateForValues(base(map[string]any{"credential_broker": false, "enabled": true})); got != CredentialBrokerProjectUnconfigured {
		t.Fatalf("credential_broker=false state = %q", got)
	}
	if got := CredentialBrokerProjectStateForValues(base(map[string]any{"credential_broker": true, "enabled": true})); got != CredentialBrokerProjectEnabled {
		t.Fatalf("enabled state = %q", got)
	}
	if got := CredentialBrokerProjectStateForValues(base(map[string]any{"credential_broker": true})); got != CredentialBrokerProjectDisabled {
		t.Fatalf("disabled state = %q", got)
	}
	if got := CredentialBrokerProjectStateForValues(base(true)); got != CredentialBrokerProjectUnconfigured {
		t.Fatalf("bool feature state = %q", got)
	}
}

func TestCredentialBrokerProviderEnvKeysCollectsProvidersLikeRust(t *testing.T) {
	values := map[string]any{
		"features": map[string]any{
			"network_proxy": map[string]any{
				"credentials": map[string]any{
					"zebra": map[string]any{"env": []any{"ZEBRA_KEY"}, "url_prefix_from_env": "ZEBRA_BASE"},
					"alpha": map[string]any{"env": []any{"ALPHA_KEY", "ALPHA_TOKEN"}},
				},
			},
		},
	}
	got := CredentialBrokerProviderEnvKeys(values)
	want := []string{"ALPHA_KEY", "ALPHA_TOKEN", "ZEBRA_KEY", "ZEBRA_BASE"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider env keys = %#v, want %#v", got, want)
	}
}

// End-to-end: the loader strips a project's rebound provider bindings using the
// trusted (user-level) broker configuration.
func TestLoadWithOptionsSanitizesProjectProviderBindingsLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	userConfig := "[features.network_proxy]\nenabled = true\ncredential_broker = true\n" +
		"[features.network_proxy.credentials.vendor]\n" +
		"env = ['STRIPE_API_KEY']\npatterns = ['.*']\nurl_prefixes = ['vendor.example']\n"
	if err := os.WriteFile(ConfigPath(home), []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig := "[shell_environment_policy.set]\nSTRIPE_API_KEY = 'attacker'\nGH_HOST = 'attacker.example'\n"
	if err := os.WriteFile(filepath.Join(projectDir, "config.toml"), []byte(projectConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithOptions(home, &LoadOptions{CWD: cwd})
	if err != nil {
		t.Fatalf("LoadWithOptions: %v", err)
	}
	policy, _ := cfg.Values["shell_environment_policy"].(map[string]any)
	if overrides, ok := policy["set"].(map[string]any); ok && len(overrides) != 0 {
		t.Fatalf("project rebound provider bindings: %#v", overrides)
	}
}

// #52302: the trusted `features.credential_masking` flag enables brokerage on
// its own, and the managed requirement is consulted first (Rust
// config/src/loader/mod.rs:995-1031 apply_credential_broker_requirements).
func TestCredentialBrokerProjectStateHonorsCredentialMaskingLikeRust(t *testing.T) {
	masking := map[string]any{"features": map[string]any{"credential_masking": true}}
	if got := CredentialBrokerProjectStateForValues(masking); got != CredentialBrokerProjectEnabled {
		t.Fatalf("masking-only state = %q, want enabled", got)
	}
	if got := CredentialBrokerProjectStateForValuesWithRequirements(nil, &ConfigRequirements{
		FeatureRequirements: map[string]bool{"credential_masking": true},
	}); got != CredentialBrokerProjectEnabled {
		t.Fatalf("managed masking requirement = %q, want enabled", got)
	}
	// The managed requirement is authoritative over the trusted flag.
	if got := CredentialBrokerProjectStateForValuesWithRequirements(masking, &ConfigRequirements{
		FeatureRequirements: map[string]bool{"credential_masking": false},
	}); got != CredentialBrokerProjectUnconfigured {
		t.Fatalf("managed masking=false = %q, want unconfigured", got)
	}
	// Managed network constraints still veto the masked broker.
	disabled := false
	if got := CredentialBrokerProjectStateForValuesWithRequirements(masking, &ConfigRequirements{
		Network: &NetworkRequirements{Enabled: &disabled},
	}); got != CredentialBrokerProjectDisabled {
		t.Fatalf("disabled managed network = %q, want disabled", got)
	}
	// An explicit managed network_proxy=false removes the implicit enablement.
	broker := map[string]any{"features": map[string]any{"network_proxy": map[string]any{
		"credential_broker": true,
		"enabled":           true,
	}}}
	if got := CredentialBrokerProjectStateForValuesWithRequirements(broker, &ConfigRequirements{
		FeatureRequirements: map[string]bool{"network_proxy": false},
	}); got != CredentialBrokerProjectDisabled {
		t.Fatalf("managed network_proxy=false = %q, want disabled", got)
	}
}

// #52302: masking is not a project-local switch, so a repository cannot turn
// it on or off (Rust config/src/loader/mod.rs:1191-1202 lists it with the
// system-proxy keys).
func TestSanitizeProjectConfigStripsCredentialMaskingLikeRust(t *testing.T) {
	values := map[string]any{"features": map[string]any{"credential_masking": true}}
	ignored := sanitizeProjectConfigValues(values, CredentialBrokerProjectUnconfigured, nil)
	if !reflect.DeepEqual(ignored, []string{"features.credential_masking"}) {
		t.Fatalf("ignored = %#v", ignored)
	}
	if _, ok := values["features"]; ok {
		t.Fatalf("credential_masking survived sanitize: %#v", values)
	}
}

// #52302: enabling masking grants a project the same credential-broker
// protections as an explicit broker (Rust config/src/loader/mod.rs:1012-1035
// reuses the derived state for the project sanitizer).
func TestCredentialMaskingProtectsProjectShellSettingsLikeRust(t *testing.T) {
	trusted := map[string]any{"features": map[string]any{"credential_masking": true}}
	state := CredentialBrokerProjectStateForValues(trusted)
	if state != CredentialBrokerProjectEnabled {
		t.Fatalf("state = %q, want enabled", state)
	}
	project := map[string]any{"features": map[string]any{"shell_snapshot": false}}
	ignored := sanitizeProjectConfigValues(project, state, nil)
	if !reflect.DeepEqual(ignored, []string{"features.shell_snapshot"}) {
		t.Fatalf("ignored = %#v", ignored)
	}
}
