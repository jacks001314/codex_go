package features

import "testing"

func TestKnownFeature(t *testing.T) {
	if !Known("unified_exec") {
		t.Fatal("unified_exec should be known")
	}
	if !Known("experimental_use_unified_exec_tool") {
		t.Fatal("legacy experimental_use_unified_exec_tool should be known")
	}
	if Known("does_not_exist") {
		t.Fatal("does_not_exist should not be known")
	}
	if err := Validate("does_not_exist"); err == nil || err.Error() != "Unknown feature flag: does_not_exist" {
		t.Fatalf("Validate unknown error = %v", err)
	}
}

func TestSorted(t *testing.T) {
	sorted := Sorted()
	if len(sorted) != len(Registry) {
		t.Fatalf("len(Sorted) = %d, want %d", len(sorted), len(Registry))
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].Key > sorted[i].Key {
			t.Fatalf("features not sorted at %d: %q > %q", i, sorted[i-1].Key, sorted[i].Key)
		}
	}
}

func TestDefaultsIncludesStableShellTool(t *testing.T) {
	defaults := Defaults()
	if !defaults["shell_tool"] {
		t.Fatal("shell_tool default = false, want true")
	}
	if !defaults["in_app_updates"] {
		t.Fatal("in_app_updates default = false, want true")
	}
	if defaults["recommended_plugins"] {
		t.Fatal("recommended_plugins default = true, want false")
	}
	if !defaults["code_mode_host"] {
		t.Fatal("code_mode_host default = false, want true")
	}
	for _, spec := range Registry {
		if spec.Key == "code_mode_host" && spec.Stage != StageStable {
			t.Fatalf("code_mode_host stage = %q, want %q", spec.Stage, StageStable)
		}
	}
}

func TestModelClientBetaFeaturesHeader(t *testing.T) {
	header := ModelClientBetaFeaturesHeader(map[string]bool{"memories": true, "shell_tool": true})
	if header != "memories,remote_compaction_v2" {
		t.Fatalf("header = %q", header)
	}

	header = ModelClientBetaFeaturesHeader(map[string]bool{"remote_compaction_v2": false})
	if header != "" {
		t.Fatalf("header with remote compaction disabled = %q", header)
	}
}

func TestResolveSettingsCanonicalizesLegacyAliases(t *testing.T) {
	settings, usages := ResolveSettings(map[string]any{
		"codex_hooks":           false,
		"web_search":            true,
		"web_search_request":    false,
		"use_legacy_landlock":   true,
		"remote_control":        true,
		"code_mode_only":        true,
		"current_time_reminder": map[string]any{"enabled": true},
	})
	if settings["hooks"] {
		t.Fatalf("hooks = true, want false from codex_hooks alias: %#v", settings)
	}
	if settings["web_search_request"] {
		t.Fatalf("web_search_request = true, want false from canonical override: %#v", settings)
	}
	if !settings["use_legacy_landlock"] {
		t.Fatalf("use_legacy_landlock = false, want true: %#v", settings)
	}
	if settings["remote_control"] {
		t.Fatalf("remote_control should be ignored: %#v", settings)
	}
	if !settings["code_mode"] || !settings["code_mode_only"] {
		t.Fatalf("code mode dependency not normalized: %#v", settings)
	}
	if !settings["current_time_reminder"] {
		t.Fatalf("structured enabled feature missing: %#v", settings)
	}
	want := []LegacyFeatureUsage{
		{Alias: "codex_hooks", Feature: "hooks"},
		{Alias: "features.use_legacy_landlock", Feature: "use_legacy_landlock"},
		{Alias: "features.web_search_request", Feature: "web_search_request"},
		{Alias: "web_search", Feature: "web_search_request"},
	}
	if len(usages) != len(want) {
		t.Fatalf("usages = %#v, want %#v", usages, want)
	}
	for i := range want {
		if usages[i] != want[i] {
			t.Fatalf("usage %d = %#v, want %#v (all %#v)", i, usages[i], want[i], usages)
		}
	}
}

func TestNoticeForLegacyFeatureUsageMatchesRust(t *testing.T) {
	tests := []struct {
		name  string
		usage LegacyFeatureUsage
		want  LegacyFeatureNotice
	}{
		{
			name:  "web search request",
			usage: LegacyFeatureUsage{Alias: "features.web_search_request", Feature: "web_search_request"},
			want: LegacyFeatureNotice{
				Summary: "`[features].web_search_request` is deprecated because web search is enabled by default.",
				Details: "Set `web_search` to `\"live\"`, `\"indexed\"`, `\"cached\"`, or `\"disabled\"` at the top level (or under a profile) in config.toml if you want to override it.",
			},
		},
		{
			name:  "legacy landlock",
			usage: LegacyFeatureUsage{Alias: "features.use_legacy_landlock", Feature: "use_legacy_landlock"},
			want: LegacyFeatureNotice{
				Summary: "`[features].use_legacy_landlock` is deprecated and will be removed soon.",
				Details: "Remove this setting to stop opting into the legacy Linux sandbox behavior.",
			},
		},
		{
			name:  "renamed feature",
			usage: LegacyFeatureUsage{Alias: "codex_hooks", Feature: "hooks"},
			want: LegacyFeatureNotice{
				Summary: "`[features].codex_hooks` is deprecated. Use `[features].hooks` instead.",
				Details: "Enable it with `--enable hooks` or `[features].hooks` in config.toml. See https://developers.openai.com/codex/config-basic#feature-flags for details.",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NoticeForLegacyFeatureUsage(test.usage); got != test.want {
				t.Fatalf("notice = %#v, want %#v", got, test.want)
			}
			if got := test.want.Message(); got != test.want.Summary+" ("+test.want.Details+")" {
				t.Fatalf("message = %q", got)
			}
		})
	}
}
