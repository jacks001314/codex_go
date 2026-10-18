package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func mcpLoginTestModel(t *testing.T, starter func(name, threadID string) (MCPLoginStart, error)) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-login")
	return NewModel(state, Options{
		Width:           120,
		Height:          36,
		OnStartMCPLogin: starter,
	})
}

func modelHistoryContains(model *Model, value string) bool {
	for _, message := range model.State.Messages {
		if strings.Contains(message.Text, value) {
			return true
		}
	}
	return false
}

// TestMCPLoginCommandStartsAndCompletesLikeRust pins #49290's command path:
// `/mcp login <name>` starts one attempt, blocks overlapping starts, and turns
// the matching completion into thread history.
func TestMCPLoginCommandStartsAndCompletesLikeRust(t *testing.T) {
	completions := make(chan MCPLoginCompletedMsg, 1)
	model := mcpLoginTestModel(t, func(name, threadID string) (MCPLoginStart, error) {
		if name != "enterprise" || threadID != "thread-login" {
			t.Fatalf("start args = %q %q", name, threadID)
		}
		return MCPLoginStart{LoginID: "login-1", Completions: completions}, nil
	})

	startCmd := model.applyMCPCommand("login enterprise")
	if startCmd == nil || model.pendingMCPLoginStart == nil {
		t.Fatalf("start cmd=%v pending=%#v", startCmd, model.pendingMCPLoginStart)
	}
	model.applyMCPCommand("login other")
	if model.pendingMCPLoginStart == nil || model.pendingMCPLoginStart.name != "enterprise" {
		t.Fatalf("overlapping start replaced pending request: %#v", model.pendingMCPLoginStart)
	}
	if !modelHistoryContains(model, "MCP sign-in is starting. Wait for it to finish before trying again.") {
		t.Fatalf("missing overlapping-start message: %#v", model.State.Messages)
	}

	started, ok := startCmd().(MCPLoginStartedMsg)
	if !ok {
		t.Fatalf("start command returned %T", startCmd())
	}
	waitCmd := model.applyMCPLoginStarted(started)
	if waitCmd == nil || model.activeMCPLoginIDs["enterprise"] != "login-1" {
		t.Fatalf("started state active=%#v wait=%v", model.activeMCPLoginIDs, waitCmd != nil)
	}

	completions <- MCPLoginCompletedMsg{Name: "enterprise", ThreadID: "thread-login", LoginID: "login-1", Success: true}
	completed, ok := waitCmd().(MCPLoginCompletedMsg)
	if !ok {
		t.Fatalf("wait command returned %T", waitCmd())
	}
	model.applyMCPLoginCompleted(completed)
	if !modelHistoryContains(model, "Signed in to enterprise.") {
		t.Fatalf("missing completion history: %#v", model.State.Messages)
	}
	if _, ok := model.activeMCPLoginIDs["enterprise"]; ok {
		t.Fatalf("active login id was not cleared: %#v", model.activeMCPLoginIDs)
	}
}

// TestMCPLoginCompletionIgnoresStaleAttemptsLikeRust covers per-server attempt
// tracking: an older cancellation must not overwrite a successful retry, while
// another server's outstanding result remains independent.
func TestMCPLoginCompletionIgnoresStaleAttemptsLikeRust(t *testing.T) {
	model := mcpLoginTestModel(t, nil)
	model.activeMCPLoginIDs = map[string]string{"enterprise": "new", "other": "other-login"}

	model.applyMCPLoginCompleted(MCPLoginCompletedMsg{
		Name: "enterprise", ThreadID: "thread-login", LoginID: "old", Success: false, Error: "Enterprise sign-in failed.",
	})
	if modelHistoryContains(model, "Enterprise sign-in failed.") {
		t.Fatalf("stale completion reached history: %#v", model.State.Messages)
	}

	model.applyMCPLoginCompleted(MCPLoginCompletedMsg{
		Name: "enterprise", ThreadID: "thread-login", LoginID: "new", Success: true,
	})
	model.applyMCPLoginCompleted(MCPLoginCompletedMsg{
		Name: "other", ThreadID: "thread-login", LoginID: "other-login", Success: true,
	})
	if !modelHistoryContains(model, "Signed in to enterprise.") || !modelHistoryContains(model, "Signed in to other.") {
		t.Fatalf("independent completion history missing: %#v", model.State.Messages)
	}
}

// TestMCPLoginResultWaitsForOriginThreadLikeRust retains a completion for an
// inactive origin thread and surfaces it when that thread is active again.
func TestMCPLoginResultWaitsForOriginThreadLikeRust(t *testing.T) {
	model := mcpLoginTestModel(t, nil)
	model.activeMCPLoginIDs["enterprise"] = "login-1"
	model.State.SetThreadID("thread-other")
	model.applyMCPLoginCompleted(MCPLoginCompletedMsg{
		Name: "enterprise", ThreadID: "thread-login", LoginID: "login-1", Success: true,
	})
	if modelHistoryContains(model, "Signed in to enterprise.") {
		t.Fatalf("inactive-thread completion rendered early: %#v", model.State.Messages)
	}
	if len(model.mcpLoginResultsByThread["thread-login"]) != 1 {
		t.Fatalf("retained results = %#v", model.mcpLoginResultsByThread)
	}
	model.State.SetThreadID("thread-login")
	model.flushMCPLoginResultsForCurrentThread()
	if !modelHistoryContains(model, "Signed in to enterprise.") {
		t.Fatalf("retained completion did not flush: %#v", model.State.Messages)
	}
}

func TestMCPLoginCommandErrorsAndUsageLikeRust(t *testing.T) {
	model := mcpLoginTestModel(t, nil)
	model.applyMCPCommand("login")
	if model.notice != "Usage: /mcp [verbose | login <name>]" {
		t.Fatalf("missing-name notice = %q", model.notice)
	}
	model.State.SetThreadID("")
	model.applyMCPCommand("login enterprise")
	if !modelHistoryContains(model, "MCP sign-in requires an active session.") {
		t.Fatalf("missing active-session error: %#v", model.State.Messages)
	}

	model = mcpLoginTestModel(t, func(string, string) (MCPLoginStart, error) {
		return MCPLoginStart{}, errors.New("Unknown MCP server typo")
	})
	command := model.applyMCPCommand("login typo")
	started := command().(MCPLoginStartedMsg)
	model.applyMCPLoginStarted(started)
	if !modelHistoryContains(model, "Unknown MCP server typo") {
		t.Fatalf("missing start failure: %#v", model.State.Messages)
	}
}
