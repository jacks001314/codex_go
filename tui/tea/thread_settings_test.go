package tea

import (
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// TestApplyThreadSettingsUpdatedAppliesServerSettings covers Rust
// #43330/#43340: the active thread's server settings replace the TUI's
// inherited model, provider, permissions, cwd, and optional overrides.
func TestApplyThreadSettingsUpdatedAppliesServerSettings(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.State.SetThreadID("thread-a")
	model.State.CWD = `D:\repo`
	model.State.Model = "local-model"
	model.State.Provider = "local-provider"
	model.State.ApprovalPolicy = "never"
	model.State.Sandbox = "read-only"
	model.State.ReasoningEffort = "low"
	model.State.ServiceTier = "default"
	model.State.Personality = "pragmatic"

	effort := "high"
	tier := "flex"
	personality := "friendly"
	model.applyThreadSettingsUpdated(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-a",
		Settings: appserver.Settings{
			CWD:            `D:\other`,
			Model:          "server-model",
			ModelProvider:  "server-provider",
			ApprovalPolicy: "on-request",
			SandboxPolicy:  "workspace-write",
			Effort:         &effort,
			ServiceTier:    &tier,
			Personality:    &personality,
		},
	})
	if model.State.CWD != `D:\other` || model.sessionCWD != `D:\other` {
		t.Fatalf("cwd = (%q, %q)", model.State.CWD, model.sessionCWD)
	}
	if model.State.Model != "server-model" || model.State.Provider != "server-provider" {
		t.Fatalf("model/provider = (%q, %q)", model.State.Model, model.State.Provider)
	}
	if model.State.ApprovalPolicy != "on-request" || model.State.Sandbox != "workspace-write" {
		t.Fatalf("permissions = (%q, %q)", model.State.ApprovalPolicy, model.State.Sandbox)
	}
	if model.State.ReasoningEffort != "high" || model.State.ServiceTier != "flex" || model.State.Personality != "friendly" {
		t.Fatalf("optional settings = (%q, %q, %q)", model.State.ReasoningEffort, model.State.ServiceTier, model.State.Personality)
	}

	// Absent optional settings clear the local override, matching Rust's
	// Option assignment.
	model.applyThreadSettingsUpdated(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-a",
		Settings: appserver.Settings{Model: "server-model", ApprovalPolicy: "on-request", SandboxPolicy: "workspace-write"},
	})
	if model.State.ReasoningEffort != "" || model.State.ServiceTier != "" || model.State.Personality != "" {
		t.Fatalf("cleared optional settings = (%q, %q, %q)", model.State.ReasoningEffort, model.State.ServiceTier, model.State.Personality)
	}
}

// TestApplyThreadSettingsUpdatedIgnoresOtherThreads pins Rust's thread routing:
// a settings update for a different thread never mutates the active state.
func TestApplyThreadSettingsUpdatedIgnoresOtherThreads(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.State.SetThreadID("thread-a")
	model.State.Model = "local-model"
	model.State.Sandbox = "read-only"

	model.applyThreadSettingsUpdated(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-b",
		Settings: appserver.Settings{Model: "other-model", SandboxPolicy: "danger-full-access"},
	})
	if model.State.Model != "local-model" || model.State.Sandbox != "read-only" {
		t.Fatalf("state = (%q, %q), want it untouched", model.State.Model, model.State.Sandbox)
	}
}
