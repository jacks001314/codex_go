package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func unavailableThreadModel() (*Model, *[]SubmitRequest) {
	submitted := []SubmitRequest{}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-unavailable")
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})
	model.setThreadUnavailable(true)
	return model, &submitted
}

// TestUnavailableThreadPreservesOrdinaryDraftLikeRust covers Rust #46566:
// ordinary prompts and non-recovery commands are not submitted.
func TestUnavailableThreadPreservesOrdinaryDraftLikeRust(t *testing.T) {
	for _, draft := range []string{"keep my draft", "/review", "/rename changed"} {
		model, submitted := unavailableThreadModel()
		typeText(t, model, draft)
		model.Update(key(bubbletea.KeyEnter))
		if len(*submitted) != 0 {
			t.Fatalf("%q submitted while the thread was unavailable", draft)
		}
		if got := model.composerText(); got != draft {
			t.Fatalf("draft %q changed to %q", draft, got)
		}
	}
}

// TestUnavailableThreadRunsRecoveryCommandLikeRust covers the recovery/local
// command allowlist.
func TestUnavailableThreadRunsRecoveryCommandLikeRust(t *testing.T) {
	model, submitted := unavailableThreadModel()
	typeText(t, model, "/status")
	model.Update(key(bubbletea.KeyEnter))
	if len(*submitted) != 0 {
		t.Fatalf("/status was submitted as a prompt: %#v", *submitted)
	}
	if got := model.composerText(); got != "" {
		t.Fatalf("composer was not cleared after /status: %q", got)
	}
	if !strings.Contains(modelMessageText(model), "Model:") {
		t.Fatalf("status output missing: %q", modelMessageText(model))
	}
}

// TestUnavailableThreadNewAndClearRestoreWritableSessionLikeRust covers
// /new and /clear leaving the restricted mode.
func TestUnavailableThreadNewAndClearRestoreWritableSessionLikeRust(t *testing.T) {
	for _, command := range []string{"/new", "/clear"} {
		model, _ := unavailableThreadModel()
		typeText(t, model, command)
		model.Update(key(bubbletea.KeyEnter))
		if model.threadUnavailable {
			t.Fatalf("%s left the thread unavailable", command)
		}
		if model.State.ThreadID != "" {
			t.Fatalf("%s kept thread id %q", command, model.State.ThreadID)
		}
	}
}

// TestUnavailableThreadUsesConfiguredSubmitKeyLikeRust covers custom submit
// bindings.
func TestUnavailableThreadUsesConfiguredSubmitKeyLikeRust(t *testing.T) {
	keymapConfig := codextui.NewKeymapConfig()
	if err := keymapConfig.Set("composer", "submit", []string{"ctrl-s"}); err != nil {
		t.Fatalf("configure submit: %v", err)
	}
	model, _ := unavailableThreadModel()
	model.keymapConfig = keymapConfig
	typeText(t, model, "/status")
	model.Update(key(bubbletea.KeyEnter))
	if strings.Contains(modelMessageText(model), "Model:") {
		t.Fatalf("Enter bypassed the custom submit binding: %q", modelMessageText(model))
	}
	model.resetComposer()
	typeText(t, model, "/status")
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlS})
	if !strings.Contains(modelMessageText(model), "Model:") {
		t.Fatalf("custom submit did not run /status: %q", modelMessageText(model))
	}
}

// TestUnavailableThreadClearsStaleActivityLikeRust covers review and MCP
// startup state that would otherwise keep recovery commands blocked.
func TestUnavailableThreadClearsStaleActivityLikeRust(t *testing.T) {
	model, _ := unavailableThreadModel()
	model.reviewState.IsReviewMode = true
	model.mcpStartupActive = true
	model.mcpStartupHeader = "starting slow"
	model.setStatus("running")
	model.setThreadUnavailable(true)
	if model.reviewState.IsReviewMode || model.mcpStartupActive || model.mcpStartupHeader != "" {
		t.Fatalf("stale activity survived: review=%v mcp=%v header=%q",
			model.reviewState.IsReviewMode, model.mcpStartupActive, model.mcpStartupHeader)
	}
	if model.State.Status != "idle" {
		t.Fatalf("status = %q, want idle", model.State.Status)
	}
}

// TestReadOnlyThreadOutranksUnavailableLikeRust keeps another app's writer
// conflict fully read-only.
func TestReadOnlyThreadOutranksUnavailableLikeRust(t *testing.T) {
	model, _ := unavailableThreadModel()
	model.setReadOnlyThread(true)
	model.setThreadUnavailable(true)
	if model.threadUnavailable || !model.readOnlyThread {
		t.Fatalf("read-only/unavailable = %v/%v", model.readOnlyThread, model.threadUnavailable)
	}
}

// TestClosedAgentSwitchEntersUnavailableModeLikeRust covers the replay-only
// attachment that precedes restricted input.
func TestClosedAgentSwitchEntersUnavailableModeLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.State.SetThreadID("root")
	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "closed-agent",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "closed-agent", ParentThreadID: "root", IsClosed: true},
			Status: "idle",
		},
	})
	if !model.threadUnavailable || model.readOnlyThread {
		t.Fatalf("closed switch read-only/unavailable = %v/%v", model.readOnlyThread, model.threadUnavailable)
	}

	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "live-agent",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "live-agent", ParentThreadID: "root", IsRunning: true},
			Status: "running",
		},
	})
	if model.threadUnavailable {
		t.Fatal("live agent switch stayed unavailable")
	}
}

// TestUnavailableThreadAllowsLocalSetupAndQuitCommandsLikeRust covers Rust
// #57820: restricted input keeps local warnings, configuration, and quit
// commands usable, in addition to the recovery allowlist.
func TestUnavailableThreadAllowsLocalSetupAndQuitCommandsLikeRust(t *testing.T) {
	cases := []struct {
		command string
		check   func(m *Model) bool
		want    string
	}{
		{command: "/warnings", check: func(m *Model) bool { return m.warningsView != nil }, want: "warnings view"},
		{command: "/config", check: func(m *Model) bool { return m.modal != nil && m.modal.configPanel != nil }, want: "config panel"},
	}
	for _, tc := range cases {
		model, submitted := unavailableThreadModel()
		typeText(t, model, tc.command)
		model.Update(key(bubbletea.KeyEnter))
		if len(*submitted) != 0 {
			t.Fatalf("%q was submitted as a prompt while unavailable", tc.command)
		}
		if got := model.composerText(); got != "" {
			t.Fatalf("%q did not clear the composer: %q", tc.command, got)
		}
		if !tc.check(model) {
			t.Fatalf("%q did not open the %s: notice=%q", tc.command, tc.want, model.notice)
		}
	}

	for _, command := range []string{"/quit", "/exit"} {
		model, submitted := unavailableThreadModel()
		typeText(t, model, command)
		_, cmd := model.Update(key(bubbletea.KeyEnter))
		if len(*submitted) != 0 {
			t.Fatalf("%q was submitted as a prompt while unavailable", command)
		}
		if cmd == nil {
			t.Fatalf("%q did not request exit while unavailable", command)
		}
		if _, ok := cmd().(bubbletea.QuitMsg); !ok {
			t.Fatalf("%q did not return a quit command", command)
		}
	}
}
