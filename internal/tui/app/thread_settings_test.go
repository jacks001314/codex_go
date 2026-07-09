package app

import (
	"testing"

	"codex_go/internal/appserver"
)

func TestThreadSettingsUpdateHasChangesMatchRustFields(t *testing.T) {
	text := "value"
	serviceTier := &appserver.ThreadExtraOptionalString{Set: true, Value: &text}
	cases := []struct {
		name   string
		params appserver.SettingsUpdateParams
	}{
		{"cwd", appserver.SettingsUpdateParams{CWD: &text}},
		{"approval policy", appserver.SettingsUpdateParams{ApprovalPolicy: &text}},
		{"approvals reviewer", appserver.SettingsUpdateParams{ApprovalsReviewer: &text}},
		{"sandbox policy", appserver.SettingsUpdateParams{SandboxPolicy: &text}},
		{"permissions", appserver.SettingsUpdateParams{Permissions: &text}},
		{"model", appserver.SettingsUpdateParams{Model: &text}},
		{"service tier", appserver.SettingsUpdateParams{ServiceTier: serviceTier}},
		{"effort", appserver.SettingsUpdateParams{Effort: &text}},
		{"summary", appserver.SettingsUpdateParams{Summary: &text}},
		{"collaboration mode", appserver.SettingsUpdateParams{CollaborationMode: map[string]any{"mode": "default"}}},
		{"personality", appserver.SettingsUpdateParams{Personality: &text}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			tt.params.ThreadID = "thread-1"
			if !ThreadSettingsUpdateHasChanges(&tt.params) {
				t.Fatalf("ThreadSettingsUpdateHasChanges(%s) = false, want true", tt.name)
			}
		})
	}
}

func TestThreadSettingsUpdateHasChangesNoopAndGoExtensions(t *testing.T) {
	if ThreadSettingsUpdateHasChanges(nil) {
		t.Fatal("ThreadSettingsUpdateHasChanges(nil) = true, want false")
	}
	if ThreadSettingsUpdateHasChanges(&appserver.SettingsUpdateParams{ThreadID: "thread-1"}) {
		t.Fatal("ThreadSettingsUpdateHasChanges(thread id only) = true, want false")
	}

	multiAgent := "on"
	if !ThreadSettingsUpdateHasChanges(&appserver.SettingsUpdateParams{ThreadID: "thread-1", MultiAgentMode: &multiAgent}) {
		t.Fatal("ThreadSettingsUpdateHasChanges(multi agent) = false, want true")
	}
	if !ThreadSettingsUpdateHasChanges(&appserver.SettingsUpdateParams{ThreadID: "thread-1", RuntimeWorkspaceRoots: []string{"/repo"}}) {
		t.Fatal("ThreadSettingsUpdateHasChanges(runtime roots) = false, want true")
	}
	if !ThreadSettingsUpdateHasChanges(&appserver.SettingsUpdateParams{ThreadID: "thread-1", Extra: map[string]string{"k": "v"}}) {
		t.Fatal("ThreadSettingsUpdateHasChanges(extra) = false, want true")
	}
}

func TestThreadSettingUpdateBuildersMatchRust(t *testing.T) {
	mode := map[string]any{"mode": "plan"}
	model := ThreadModelSettingUpdateParams("thread-1", "gpt-5", mode)
	if model == nil || model.ThreadID != "thread-1" || model.Model == nil || *model.Model != "gpt-5" {
		t.Fatalf("ThreadModelSettingUpdateParams() = %#v", model)
	}
	mode["mode"] = "default"
	if model.CollaborationMode["mode"] != "plan" {
		t.Fatalf("model collaboration mode was not cloned: %#v", model.CollaborationMode)
	}

	effortValue := "high"
	reasoning := ThreadReasoningSettingUpdateParams("thread-1", &effortValue, map[string]any{"mode": "default"})
	if reasoning == nil || reasoning.Effort == nil || *reasoning.Effort != "high" || reasoning.CollaborationMode["mode"] != "default" {
		t.Fatalf("ThreadReasoningSettingUpdateParams() = %#v", reasoning)
	}
	effortValue = "low"
	if *reasoning.Effort != "high" {
		t.Fatalf("reasoning effort pointer was not cloned: %q", *reasoning.Effort)
	}

	plan := ThreadPlanModeSettingUpdateParams("thread-1", map[string]any{"mode": "plan"})
	if plan == nil || plan.CollaborationMode["mode"] != "plan" {
		t.Fatalf("ThreadPlanModeSettingUpdateParams() = %#v", plan)
	}

	personality := ThreadPersonalitySettingUpdateParams("thread-1", "pragmatic")
	if personality == nil || personality.Personality == nil || *personality.Personality != "pragmatic" {
		t.Fatalf("ThreadPersonalitySettingUpdateParams() = %#v", personality)
	}

	if ThreadModelSettingUpdateParams("", "gpt-5", nil) != nil ||
		ThreadReasoningSettingUpdateParams("", nil, nil) != nil ||
		ThreadPlanModeSettingUpdateParams("", nil) != nil ||
		ThreadPersonalitySettingUpdateParams("", "pragmatic") != nil {
		t.Fatal("empty thread id builders should return nil")
	}
}
