package apps

import "testing"

func boolPolicyPtr(value bool) *bool { return &value }

func appsConfigFromTOMLShape(values map[string]any) *AppsConfig {
	return AppsConfigFromValues(map[string]any{"apps": values})
}

// TestAppsConfigParsesToolPolicyLikeRust pins the parsing of the app policy
// surface Rust's AppsConfigToml accepts.
func TestAppsConfigParsesToolPolicyLikeRust(t *testing.T) {
	config := appsConfigFromTOMLShape(map[string]any{
		"_default": map[string]any{
			"enabled":                     true,
			"destructive_enabled":         false,
			"open_world_enabled":          false,
			"default_tools_approval_mode": "writes",
			"approvals_reviewer":          "auto_review",
		},
		"calendar": map[string]any{
			"enabled":                     true,
			"default_tools_enabled":       false,
			"destructive_enabled":         true,
			"open_world_enabled":          true,
			"default_tools_approval_mode": "prompt",
			"tools": map[string]any{
				"events/create": map[string]any{"enabled": true, "approval_mode": "approve"},
			},
			"links": map[string]any{
				"link-1": map[string]any{"default_tools_approval_mode": "prompt"},
			},
		},
	})
	if config == nil {
		t.Fatal("AppsConfigFromValues returned nil")
	}
	if config.Default == nil || !config.Default.Enabled || config.Default.DestructiveEnabled || config.Default.OpenWorldEnabled {
		t.Fatalf("defaults = %#v", config.Default)
	}
	if config.Default.ApprovalsReviewer == nil || *config.Default.ApprovalsReviewer != "auto_review" {
		t.Fatalf("defaults reviewer = %#v", config.Default.ApprovalsReviewer)
	}
	app, ok := config.Apps["calendar"]
	if !ok {
		t.Fatalf("calendar missing from %#v", config.Apps)
	}
	if app.Enabled == nil || !*app.Enabled || app.DefaultToolsEnabled == nil || *app.DefaultToolsEnabled {
		t.Fatalf("calendar enablement = %#v", app)
	}
	if app.DestructiveEnabled == nil || !*app.DestructiveEnabled || app.OpenWorldEnabled == nil || !*app.OpenWorldEnabled {
		t.Fatalf("calendar hints = %#v", app)
	}
	tool, ok := app.Tools["events/create"]
	if !ok || tool.Enabled == nil || !*tool.Enabled || tool.ApprovalMode == nil || *tool.ApprovalMode != AppToolApprovalApprove {
		t.Fatalf("calendar tool = %#v", app.Tools)
	}
	link, ok := app.Links.Links["link-1"]
	if !ok || link.DefaultToolsApprovalMode == nil || *link.DefaultToolsApprovalMode != AppToolApprovalPrompt {
		t.Fatalf("calendar link = %#v", app.Links)
	}
}

// TestAppIsEnabledUsesDefaultsAndPerAppOverridesLikeRust mirrors Rust
// app_enablement_uses_defaults_and_per_app_overrides.
func TestAppIsEnabledUsesDefaultsAndPerAppOverridesLikeRust(t *testing.T) {
	config := appsConfigFromTOMLShape(map[string]any{
		"_default": map[string]any{"enabled": false},
		"calendar": map[string]any{"enabled": true},
	})
	tests := []struct {
		name        string
		connectorID string
		want        bool
	}{
		{name: "per-app override", connectorID: "calendar", want: true},
		{name: "inherits default", connectorID: "drive", want: false},
		{name: "no connector", connectorID: "", want: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := AppIsEnabled(config, testCase.connectorID); got != testCase.want {
				t.Fatalf("AppIsEnabled(%q) = %v, want %v", testCase.connectorID, got, testCase.want)
			}
		})
	}
	if AppIsEnabled(nil, "calendar") != true {
		t.Fatal("an absent apps config must leave apps enabled")
	}
}

// TestApplyAppEnabledStateOnlyTouchesConfiguredAppsLikeRust mirrors Rust
// AppToolPolicyEvaluator::apply_app_enabled_state.
func TestApplyAppEnabledStateOnlyTouchesConfiguredAppsLikeRust(t *testing.T) {
	config := appsConfigFromTOMLShape(map[string]any{
		"_default": map[string]any{"enabled": false},
		"calendar": map[string]any{"enabled": true},
	})
	entries := []AppEntry{
		{ID: "calendar", IsEnabled: false, Enabled: false},
		{ID: "drive", IsEnabled: true, Enabled: true},
	}
	out := NewAppToolPolicyEvaluator(config).ApplyAppEnabledState(entries)
	if !out[0].IsEnabled || !out[0].Enabled || !out[0].EnabledExplicit {
		t.Fatalf("calendar = %#v", out[0])
	}
	if out[1].IsEnabled || out[1].Enabled || !out[1].EnabledExplicit {
		t.Fatalf("drive = %#v", out[1])
	}
}

// TestAppToolPolicyPrecedenceLikeRust mirrors Rust
// evaluator_reuses_one_snapshot_across_tools for the user-config half (the
// managed requirement override is not modeled in Go yet).
func TestAppToolPolicyPrecedenceLikeRust(t *testing.T) {
	config := appsConfigFromTOMLShape(map[string]any{
		"calendar": map[string]any{
			"default_tools_enabled": false,
			"tools": map[string]any{
				"events/create": map[string]any{"enabled": true, "approval_mode": "prompt"},
			},
		},
	})
	evaluator := NewAppToolPolicyEvaluator(config)
	tests := []struct {
		name  string
		input AppToolPolicyInput
		want  AppToolPolicy
	}{
		{
			name:  "per-tool override",
			input: AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/create"},
			want:  AppToolPolicy{Enabled: true, Approval: AppToolApprovalPrompt},
		},
		{
			name:  "app default disables other tools",
			input: AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/list"},
			want:  AppToolPolicy{Enabled: false, Approval: AppToolApprovalAuto},
		},
		{
			name:  "tool title fallback",
			input: AppToolPolicyInput{ConnectorID: "calendar", ToolName: "calendar_events/create", ToolTitle: "events/create"},
			want:  AppToolPolicy{Enabled: true, Approval: AppToolApprovalPrompt},
		},
		{
			name:  "unconfigured app",
			input: AppToolPolicyInput{ConnectorID: "drive", ToolName: "files/read"},
			want:  AppToolPolicy{Enabled: true, Approval: AppToolApprovalAuto},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := evaluator.Policy(testCase.input); got != testCase.want {
				t.Fatalf("Policy() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestAppToolPolicyHintDefaultsLikeRust mirrors Rust
// evaluator_uses_global_defaults_for_destructive_hints,
// evaluator_defaults_missing_destructive_hint_to_true and
// evaluator_defaults_missing_open_world_hint_to_true.
func TestAppToolPolicyHintDefaultsLikeRust(t *testing.T) {
	destructiveOff := appsConfigFromTOMLShape(map[string]any{
		"_default": map[string]any{"destructive_enabled": false},
	})
	openWorldOff := appsConfigFromTOMLShape(map[string]any{
		"_default": map[string]any{"open_world_enabled": false},
	})
	tests := []struct {
		name    string
		config  *AppsConfig
		input   AppToolPolicyInput
		enabled bool
	}{
		{
			name:    "destructive hint set",
			config:  destructiveOff,
			input:   AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/create", DestructiveHint: boolPolicyPtr(true)},
			enabled: false,
		},
		{
			name:    "missing destructive hint defaults true",
			config:  destructiveOff,
			input:   AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/create", OpenWorldHint: boolPolicyPtr(false)},
			enabled: false,
		},
		{
			name:    "missing open world hint defaults true",
			config:  openWorldOff,
			input:   AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/create", DestructiveHint: boolPolicyPtr(false)},
			enabled: false,
		},
		{
			name:    "safe hints stay enabled",
			config:  openWorldOff,
			input:   AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/create", DestructiveHint: boolPolicyPtr(false), OpenWorldHint: boolPolicyPtr(false)},
			enabled: true,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := NewAppToolPolicyEvaluator(testCase.config).Policy(testCase.input)
			if got.Enabled != testCase.enabled {
				t.Fatalf("Enabled = %v, want %v (%#v)", got.Enabled, testCase.enabled, got)
			}
		})
	}
}

// TestAppToolApprovalPrecedenceLikeRust covers the link/app/default chain.
func TestAppToolApprovalPrecedenceLikeRust(t *testing.T) {
	config := appsConfigFromTOMLShape(map[string]any{
		"_default": map[string]any{"default_tools_approval_mode": "writes"},
		"calendar": map[string]any{
			"default_tools_approval_mode": "prompt",
			"links": map[string]any{
				"link-1": map[string]any{"default_tools_approval_mode": "approve"},
			},
		},
		"drive": map[string]any{},
	})
	evaluator := NewAppToolPolicyEvaluator(config)
	tests := []struct {
		name  string
		input AppToolPolicyInput
		want  AppToolApproval
	}{
		{
			name:  "link default wins over app default",
			input: AppToolPolicyInput{ConnectorID: "calendar", LinkID: "link-1", ToolName: "events/list"},
			want:  AppToolApprovalApprove,
		},
		{
			name:  "app default wins over global default",
			input: AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/list"},
			want:  AppToolApprovalPrompt,
		},
		{
			name:  "global default for connector without settings",
			input: AppToolPolicyInput{ConnectorID: "drive", ToolName: "files/read"},
			want:  AppToolApprovalWrites,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := evaluator.Policy(testCase.input); got.Approval != testCase.want {
				t.Fatalf("Approval = %q, want %q", got.Approval, testCase.want)
			}
		})
	}

	// An app entry that exists but omits the mode still only consults the
	// global default when the app is explicitly configured.
	noApps := appsConfigFromTOMLShape(map[string]any{
		"_default": map[string]any{"default_tools_approval_mode": "writes"},
	})
	policy := NewAppToolPolicyEvaluator(noApps).Policy(AppToolPolicyInput{ConnectorID: "", ToolName: "events/list"})
	if policy.Approval != AppToolApprovalAuto {
		t.Fatalf("connector-less approval = %q, want auto", policy.Approval)
	}
}

// TestAppToolPolicyToolEnablementWinsLikeRust covers the per-tool `enabled`
// override and the default_tools_enabled fallback.
func TestAppToolPolicyToolEnablementWinsLikeRust(t *testing.T) {
	config := appsConfigFromTOMLShape(map[string]any{
		"calendar": map[string]any{
			"default_tools_enabled": true,
			"tools": map[string]any{
				"events/create": map[string]any{"enabled": false},
			},
		},
	})
	evaluator := NewAppToolPolicyEvaluator(config)
	if policy := evaluator.Policy(AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/create"}); policy.Enabled {
		t.Fatalf("per-tool disable ignored: %#v", policy)
	}
	if policy := evaluator.Policy(AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/list"}); !policy.Enabled {
		t.Fatalf("default_tools_enabled ignored: %#v", policy)
	}

	disabledApp := appsConfigFromTOMLShape(map[string]any{
		"calendar": map[string]any{
			"enabled":               false,
			"default_tools_enabled": true,
			"tools":                 map[string]any{"events/create": map[string]any{"enabled": true}},
		},
	})
	if policy := NewAppToolPolicyEvaluator(disabledApp).Policy(AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/create"}); policy.Enabled {
		t.Fatalf("disabled app still exposed a tool: %#v", policy)
	}
	if NewAppToolPolicyEvaluator(nil).Policy(AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/create"}) != DefaultAppToolPolicy() {
		t.Fatal("a nil config must resolve to the default policy")
	}
	if NewAppToolPolicyEvaluator(disabledApp).Policy(AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/create"}) == (AppToolPolicy{}) {
		t.Fatal("approval must still resolve for a disabled app")
	}
}
