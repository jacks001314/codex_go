package tui

import (
	"strings"
	"testing"
)

func TestStateRenderWelcomeAndFrame(t *testing.T) {
	state := NewState(&Options{
		Model:          "gpt-test",
		ApprovalPolicy: "on-request",
		Sandbox:        "workspace-write",
		Search:         true,
		NoAltScreen:    true,
	})
	state.SetThreadID("thread-1")
	state.AddMessage(RoleUser, "hello")
	state.AddMessage(RoleAssistant, "hi there")

	welcome := state.RenderWelcome()
	for _, want := range []string{"Codex interactive session", "Model: gpt-test", "Approval: on-request", "Sandbox: workspace-write"} {
		if !strings.Contains(welcome, want) {
			t.Fatalf("welcome = %q, missing %q", welcome, want)
		}
	}

	frame := state.RenderFrame()
	for _, want := range []string{"Thread: thread-1", "User:", "Assistant:", "Commands:"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame = %q, missing %q", frame, want)
		}
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input   string
		command Command
		args    string
		ok      bool
	}{
		{input: "hello", ok: false},
		{input: "/help", command: CommandHelp, ok: true},
		{input: "/model gpt-5", command: CommandModel, args: "gpt-5", ok: true},
		{input: "/approval on-request", command: CommandApproval, args: "on-request", ok: true},
		{input: "quit", command: CommandExit, ok: true},
		{input: "/wat", command: CommandUnknown, ok: true},
	}
	for _, test := range tests {
		invocation, ok := ParseCommand(test.input)
		if ok != test.ok {
			t.Fatalf("ParseCommand(%q) ok = %v, want %v", test.input, ok, test.ok)
		}
		if !ok {
			continue
		}
		if invocation.Command != test.command || invocation.Args != test.args {
			t.Fatalf("ParseCommand(%q) = %#v, want command %q args %q", test.input, invocation, test.command, test.args)
		}
	}
}

func TestValidApprovalPolicy(t *testing.T) {
	for _, value := range []string{"untrusted", "on-request", "never"} {
		if !ValidApprovalPolicy(value) {
			t.Fatalf("ValidApprovalPolicy(%q) = false", value)
		}
	}
	if ValidApprovalPolicy("always") {
		t.Fatalf("ValidApprovalPolicy(always) = true, want false")
	}
}
