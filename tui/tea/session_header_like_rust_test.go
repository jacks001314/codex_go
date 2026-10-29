package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

func sessionHeaderModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(&codextui.Options{
		Model:           "gpt-5",
		ReasoningEffort: "high",
		CWD:             `D:\repo\project`,
	})
	return NewModel(state, Options{
		Width:                90,
		Height:               24,
		ShowSessionHeader:    true,
		SessionHeaderVersion: "1.2.3",
		OnClearTerminal: func(string) bubbletea.Cmd {
			return nil
		},
	})
}

func assertBorderlessHeader(t *testing.T, message codextui.Message) {
	t.Helper()
	if message.Role != codextui.RoleHistory {
		t.Fatalf("header role = %q, want history", message.Role)
	}
	for _, want := range []string{">_ OpenAI Codex (v1.2.3)", `D:\repo\project`} {
		if !strings.Contains(message.Text, want) {
			t.Fatalf("display header missing %q: %q", want, message.Text)
		}
	}
	for _, unwanted := range []string{"model:", "\u256d", "╭"} {
		if strings.Contains(message.Text, unwanted) {
			t.Fatalf("borderless display header carries %q: %q", unwanted, message.Text)
		}
	}
	for _, want := range []string{"OpenAI Codex (v1.2.3)", "model: gpt-5 high", "directory: "} {
		if !strings.Contains(message.RawText, want) {
			t.Fatalf("raw header missing %q: %q", want, message.RawText)
		}
	}
}

// Rust #48562: /clear drops the old transcript but re-inserts the same
// compact borderless header instead of leaving an empty session.
func TestClearReinsertsBorderlessSessionHeaderLikeRust(t *testing.T) {
	model := sessionHeaderModel(t)
	model.State.AddMessage(codextui.RoleUser, "old transcript")

	typeText(t, model, "/clear")
	model.Update(key(bubbletea.KeyEnter))

	if len(model.State.Messages) != 1 {
		t.Fatalf("messages after clear = %#v, want one session header", model.State.Messages)
	}
	assertBorderlessHeader(t, model.State.Messages[0])
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, ">_ OpenAI Codex (v1.2.3)") || !strings.Contains(view, `D:\repo\project`) {
		t.Fatalf("cleared view missing the fresh header:\n%s", view)
	}
	if strings.Contains(view, "old transcript") {
		t.Fatalf("cleared view kept the old transcript:\n%s", view)
	}
}

// Rust #48562 renders display_lines_for_mode when the header is re-inserted, so
// raw output mode keeps the raw header fields.
func TestClearHeaderHonorsRawOutputModeLikeRust(t *testing.T) {
	model := sessionHeaderModel(t)
	model.rawOutput = true

	typeText(t, model, "/clear")
	model.Update(key(bubbletea.KeyEnter))

	view := utils.StripANSI(model.View())
	for _, want := range []string{"OpenAI Codex (v1.2.3)", "model: gpt-5 high", `directory: D:\repo\project`} {
		if !strings.Contains(view, want) {
			t.Fatalf("raw cleared view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, ">_ OpenAI Codex") {
		t.Fatalf("raw cleared view kept the rich title:\n%s", view)
	}
}

// Rust #48562 uses the compact header for resumed and switched threads too.
func TestResumeAndAgentSwitchInsertBorderlessHeaderLikeRust(t *testing.T) {
	model := sessionHeaderModel(t)
	model.applyResumeResponse("thread-resumed", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-resumed"},
		Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "replayed"}},
	})
	if len(model.State.Messages) != 2 {
		t.Fatalf("resumed messages = %#v, want header plus replay", model.State.Messages)
	}
	assertBorderlessHeader(t, model.State.Messages[0])
	if model.State.Messages[1].Text != "replayed" {
		t.Fatalf("replay moved behind header: %#v", model.State.Messages)
	}

	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "agent-2",
		Response: AgentThreadSwitchResponse{
			Entry:    codextui.AgentThreadEntry{ThreadID: "agent-2", AgentNickname: "worker"},
			Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "agent reply"}},
		},
	})
	if len(model.State.Messages) != 2 {
		t.Fatalf("switched messages = %#v, want header plus replay", model.State.Messages)
	}
	assertBorderlessHeader(t, model.State.Messages[0])
	if model.State.Messages[1].Text != "agent reply" {
		t.Fatalf("switched replay moved behind header: %#v", model.State.Messages)
	}
}

// Rust #48562 keeps the YOLO permissions indicator on the borderless header.
func TestSessionHeaderYoloIndicatorLikeRust(t *testing.T) {
	model := sessionHeaderModel(t)
	model.State.ApprovalPolicy = string(chatwidget.ApprovalNever)
	model.State.Sandbox = chatwidget.DangerFullAccessProfile
	model.startFreshNamedSession("", "new")
	if !strings.Contains(model.State.Messages[0].Text, "permissions: YOLO mode") ||
		!strings.Contains(model.State.Messages[0].RawText, "permissions: YOLO mode") {
		t.Fatalf("yolo header = %#v", model.State.Messages[0])
	}

	model.State.Sandbox = chatwidget.WorkspaceProfile
	model.startFreshNamedSession("", "new")
	if strings.Contains(model.State.Messages[0].Text, "permissions: YOLO mode") {
		t.Fatalf("workspace header kept the YOLO indicator: %#v", model.State.Messages[0])
	}
}
