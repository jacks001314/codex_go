package app

import (
	"reflect"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

func TestOverriddenWriteMessageMatchRust(t *testing.T) {
	if got := OverriddenWriteMessage(nil); got != "the effective config is overridden by a higher-priority layer" {
		t.Fatalf("OverriddenWriteMessage(nil) = %q", got)
	}
	response := &config.ConfigWriteResponse{
		OverriddenMetadata: &config.OverriddenMetadata{Message: "model was written but overridden"},
	}
	if got := OverriddenWriteMessage(response); got != "model was written but overridden" {
		t.Fatalf("OverriddenWriteMessage(response) = %q", got)
	}
}

func TestEffectiveConfigFeatureHelpersMatchRust(t *testing.T) {
	effective := &config.ConfigReadResponse{Config: map[string]any{
		"features": map[string]any{
			"plugins":     false,
			"mentions_v2": true,
			"ignored":     "true",
		},
	}}
	if FeatureEnabledFromEffectiveConfig(effective, "plugins") {
		t.Fatal("plugins feature = true, want false from effective config")
	}
	if !FeatureEnabledFromEffectiveConfig(effective, "mentions_v2") {
		t.Fatal("mentions_v2 feature = false, want true")
	}
	if !FeatureEnabledFromEffectiveConfig(&config.ConfigReadResponse{Config: map[string]any{}}, "shell_tool") {
		t.Fatal("shell_tool default = false, want true")
	}
	want := map[string]bool{"plugins": false, "mentions_v2": true}
	if got := FeaturesFromEffectiveConfig(effective); !reflect.DeepEqual(got, want) {
		t.Fatalf("FeaturesFromEffectiveConfig() = %#v, want %#v", got, want)
	}
}

func TestEffectiveConfigPermissionFieldsMatchRust(t *testing.T) {
	effective := &config.ConfigReadResponse{Config: map[string]any{
		"approval_policy":    "on-request",
		"approvals_reviewer": "auto_review",
		"sandbox_mode":       "workspace-write",
	}}
	if got, ok := ApprovalPolicyFromEffectiveConfig(effective); !ok || got != sandbox.ApprovalOnRequest {
		t.Fatalf("ApprovalPolicyFromEffectiveConfig() = %q/%v", got, ok)
	}
	if got, ok := ApprovalsReviewerFromEffectiveConfig(effective); !ok || got != config.ApprovalsReviewerAutoReview {
		t.Fatalf("ApprovalsReviewerFromEffectiveConfig() = %q/%v", got, ok)
	}
	if got, ok := SandboxModeFromEffectiveConfig(effective); !ok || got != sandbox.SandboxWorkspaceWrite {
		t.Fatalf("SandboxModeFromEffectiveConfig() = %q/%v", got, ok)
	}

	invalid := &config.ConfigReadResponse{Config: map[string]any{
		"approval_policy":    "sometimes",
		"approvals_reviewer": "robot",
		"sandbox_mode":       "unknown",
	}}
	if _, ok := ApprovalPolicyFromEffectiveConfig(invalid); ok {
		t.Fatal("invalid approval policy ok = true")
	}
	if _, ok := ApprovalsReviewerFromEffectiveConfig(invalid); ok {
		t.Fatal("invalid reviewer ok = true")
	}
	if _, ok := SandboxModeFromEffectiveConfig(invalid); ok {
		t.Fatal("invalid sandbox ok = true")
	}

	spaced := &config.ConfigReadResponse{Config: map[string]any{
		"approval_policy":    " on-request ",
		"approvals_reviewer": " auto_review ",
		"sandbox_mode":       " workspace-write ",
	}}
	if _, ok := ApprovalPolicyFromEffectiveConfig(spaced); ok {
		t.Fatal("spaced approval policy ok = true")
	}
	if _, ok := ApprovalsReviewerFromEffectiveConfig(spaced); ok {
		t.Fatal("spaced reviewer ok = true")
	}
	if _, ok := SandboxModeFromEffectiveConfig(spaced); ok {
		t.Fatal("spaced sandbox ok = true")
	}
}

func TestEffectiveConfigMemoriesAndWindowsHelpersMatchRust(t *testing.T) {
	effective := &config.ConfigReadResponse{Config: map[string]any{
		"memories": map[string]any{"enabled": true, "max": float64(3)},
		"windows":  map[string]any{"sandbox": "elevated"},
	}}
	memories, ok := MemoriesFromEffectiveConfig(effective)
	if !ok || memories["enabled"] != true || memories["max"] != float64(3) {
		t.Fatalf("MemoriesFromEffectiveConfig() = %#v/%v", memories, ok)
	}
	memories["enabled"] = false
	again, _ := MemoriesFromEffectiveConfig(effective)
	if again["enabled"] != true {
		t.Fatal("MemoriesFromEffectiveConfig() did not clone result")
	}
	if got, ok := WindowsSandboxModeFromEffectiveConfig(effective); !ok || got != config.WindowsSandboxSetupElevated {
		t.Fatalf("WindowsSandboxModeFromEffectiveConfig() = %q/%v", got, ok)
	}

	missing := &config.ConfigReadResponse{Config: map[string]any{"windows": map[string]any{"sandbox": "bad"}}}
	if _, ok := WindowsSandboxModeFromEffectiveConfig(missing); ok {
		t.Fatal("invalid windows sandbox mode ok = true")
	}
	spaced := &config.ConfigReadResponse{Config: map[string]any{"windows": map[string]any{"sandbox": " elevated "}}}
	if _, ok := WindowsSandboxModeFromEffectiveConfig(spaced); ok {
		t.Fatal("spaced windows sandbox mode ok = true")
	}
}
