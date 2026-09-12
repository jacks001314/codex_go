package apps

import "testing"

func requirementApproval(mode AppToolApproval) *AppToolApproval { return &mode }

func requirementEnabled(value bool) *bool { return &value }

// TestAppsRequirementsFromMapLikeRust covers Rust AppsRequirementsToml parsing.
func TestAppsRequirementsFromMapLikeRust(t *testing.T) {
	requirements, err := AppsRequirementsFromMap(map[string]any{
		"calendar": map[string]any{
			"enabled": false,
			"tools": map[string]any{
				"events/create": map[string]any{
					"approval_mode":           "approve",
					"analytics_result_source": map[string]any{"format": "detailed_message_search_v1", "type": "mcp_tool_result"},
				},
				"events/list": map[string]any{"approval_mode": "prompt"},
			},
		},
	})
	if err != nil {
		t.Fatalf("AppsRequirementsFromMap() error = %v", err)
	}
	calendar, ok := requirements["calendar"]
	if !ok || calendar.Enabled == nil || *calendar.Enabled {
		t.Fatalf("calendar requirement = %#v", calendar)
	}
	create := calendar.Tools["events/create"]
	if create.ApprovalMode == nil || *create.ApprovalMode != AppToolApprovalApprove {
		t.Fatalf("events/create approval = %#v", create)
	}
	if create.AnalyticsResultSource == nil ||
		create.AnalyticsResultSource.Format != "detailed_message_search_v1" ||
		create.AnalyticsResultSource.SourceType != "mcp_tool_result" {
		t.Fatalf("events/create analytics = %#v", create.AnalyticsResultSource)
	}
	if list := calendar.Tools["events/list"]; list.ApprovalMode == nil || *list.ApprovalMode != AppToolApprovalPrompt {
		t.Fatalf("events/list approval = %#v", list)
	}
	if empty, err := AppsRequirementsFromMap(nil); err != nil || empty != nil {
		t.Fatalf("nil requirements = %#v, %v", empty, err)
	}
	if _, err := AppsRequirementsFromMap(map[string]any{"calendar": "nope"}); err == nil {
		t.Fatal("a non-table app requirement must be rejected")
	}
}

// TestMergeAppRequirementsDescendingLikeRust mirrors Rust's
// merge_app_requirements_descending_* vectors.
func TestMergeAppRequirementsDescendingLikeRust(t *testing.T) {
	t.Run("unions distinct apps", func(t *testing.T) {
		merged := MergeAppRequirementsDescending(
			AppsRequirements{"connector_high": {Enabled: requirementEnabled(false)}},
			AppsRequirements{"connector_low": {Enabled: requirementEnabled(true)}},
		)
		if len(merged) != 2 {
			t.Fatalf("merged = %#v", merged)
		}
		if high := merged["connector_high"]; high.Enabled == nil || *high.Enabled {
			t.Fatalf("connector_high = %#v", high)
		}
		if low := merged["connector_low"]; low.Enabled == nil || !*low.Enabled {
			t.Fatalf("connector_low = %#v", low)
		}
	})

	t.Run("prefers false from lower precedence", func(t *testing.T) {
		merged := MergeAppRequirementsDescending(
			AppsRequirements{"connector_123123": {Enabled: requirementEnabled(true)}},
			AppsRequirements{"connector_123123": {Enabled: requirementEnabled(false)}},
		)
		if enabled := merged["connector_123123"].Enabled; enabled == nil || *enabled {
			t.Fatalf("merged = %#v", merged)
		}
	})

	t.Run("keeps higher true when lower is unset", func(t *testing.T) {
		merged := MergeAppRequirementsDescending(
			AppsRequirements{"connector_123123": {Enabled: requirementEnabled(true)}},
			AppsRequirements{"connector_123123": {}},
		)
		if enabled := merged["connector_123123"].Enabled; enabled == nil || !*enabled {
			t.Fatalf("merged = %#v", merged)
		}
	})

	t.Run("uses lower value when higher is missing", func(t *testing.T) {
		merged := MergeAppRequirementsDescending(
			nil,
			AppsRequirements{"connector_123123": {Enabled: requirementEnabled(true)}},
		)
		if enabled := merged["connector_123123"].Enabled; enabled == nil || !*enabled {
			t.Fatalf("merged = %#v", merged)
		}
	})

	t.Run("keeps the higher tool approval and fills the rest", func(t *testing.T) {
		merged := MergeAppRequirementsDescending(
			AppsRequirements{"calendar": {Tools: map[string]AppToolRequirement{
				"events/create": {ApprovalMode: requirementApproval(AppToolApprovalApprove)},
			}}},
			AppsRequirements{"calendar": {Tools: map[string]AppToolRequirement{
				"events/create": {ApprovalMode: requirementApproval(AppToolApprovalPrompt)},
				"events/list":   {ApprovalMode: requirementApproval(AppToolApprovalPrompt)},
			}}},
		)
		tools := merged["calendar"].Tools
		if mode := tools["events/create"].ApprovalMode; mode == nil || *mode != AppToolApprovalApprove {
			t.Fatalf("higher tool approval lost: %#v", tools)
		}
		if mode := tools["events/list"].ApprovalMode; mode == nil || *mode != AppToolApprovalPrompt {
			t.Fatalf("lower tool approval not adopted: %#v", tools)
		}
	})
}

// TestAppToolPolicyHonorsManagedRequirementsLikeRust mirrors Rust
// evaluator_reuses_one_snapshot_across_tools.
func TestAppToolPolicyHonorsManagedRequirementsLikeRust(t *testing.T) {
	config := appsConfigFromTOMLShape(map[string]any{
		"calendar": map[string]any{
			"default_tools_enabled": false,
			"tools": map[string]any{
				"events/create": map[string]any{"enabled": true, "approval_mode": "prompt"},
			},
		},
	})
	requirements := AppsRequirements{"calendar": {Tools: map[string]AppToolRequirement{
		"events/create": {ApprovalMode: requirementApproval(AppToolApprovalApprove)},
	}}}
	evaluator := NewAppToolPolicyEvaluatorWithRequirements(config, requirements)
	tests := []struct {
		name  string
		input AppToolPolicyInput
		want  AppToolPolicy
	}{
		{
			name:  "managed approval wins",
			input: AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/create"},
			want:  AppToolPolicy{Enabled: true, Approval: AppToolApprovalApprove},
		},
		{
			name:  "app default still disables",
			input: AppToolPolicyInput{ConnectorID: "calendar", ToolName: "events/list"},
			want:  AppToolPolicy{Enabled: false, Approval: AppToolApprovalAuto},
		},
		{
			name:  "title alias bypasses the managed tool key",
			input: AppToolPolicyInput{ConnectorID: "calendar", ToolName: "calendar_events/create", ToolTitle: "events/create"},
			want:  AppToolPolicy{Enabled: true, Approval: AppToolApprovalPrompt},
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

// TestAppToolPolicyRequirementsDisableAppLikeRust covers Rust's
// apply_requirements_apps_constraints (a managed enabled = false).
func TestAppToolPolicyRequirementsDisableAppLikeRust(t *testing.T) {
	requirements := AppsRequirements{"drive": {Enabled: requirementEnabled(false)}}
	evaluator := NewAppToolPolicyEvaluatorWithRequirements(nil, requirements)
	if evaluator.AppEnabled("drive") {
		t.Fatal("managed disablement was ignored")
	}
	if policy := evaluator.Policy(AppToolPolicyInput{ConnectorID: "drive", ToolName: "files/read"}); policy.Enabled {
		t.Fatalf("managed disablement did not disable the tool: %#v", policy)
	}
	// A managed tool approval still applies when no apps config exists.
	approvalRequirement := AppsRequirements{"drive": {Tools: map[string]AppToolRequirement{
		"files/read": {ApprovalMode: requirementApproval(AppToolApprovalPrompt)},
	}}}
	approvalEvaluator := NewAppToolPolicyEvaluatorWithRequirements(nil, approvalRequirement)
	if policy := approvalEvaluator.Policy(AppToolPolicyInput{ConnectorID: "drive", ToolName: "files/read"}); policy.Approval != AppToolApprovalPrompt || !policy.Enabled {
		t.Fatalf("managed approval without config = %#v", policy)
	}

	// The applied state must not mutate the caller's config snapshot.
	config := appsConfigFromTOMLShape(map[string]any{"drive": map[string]any{"enabled": true}})
	_ = NewAppToolPolicyEvaluatorWithRequirements(config, requirements)
	if enabled := config.Apps["drive"].Enabled; enabled == nil || !*enabled {
		t.Fatalf("caller config mutated: %#v", config.Apps["drive"])
	}
}
