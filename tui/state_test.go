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
		CWD:            `D:\repo`,
		Search:         true,
		NoAltScreen:    true,
	})
	state.SetThreadID("thread-1")
	state.AddMessage(RoleUser, "hello")
	state.AddMessage(RoleAssistant, "hi there")
	state.AddHistoryLines([]string{"• MCP Tools", "  • docs"}, []string{"MCP Tools", "docs"})

	welcome := state.RenderWelcome()
	for _, want := range []string{"OpenAI Codex", "Model:", "gpt-test", "on-request", "workspace-write", `Directory:`, `D:\repo`} {
		if !strings.Contains(welcome, want) {
			t.Fatalf("welcome = %q, missing %q", welcome, want)
		}
	}
	card := state.RenderStatusCard()
	for _, want := range []string{"OpenAI Codex", "Model:", "gpt-test", "Session:", "thread-1", "Limits:"} {
		if !strings.Contains(card, want) {
			t.Fatalf("status card = %q, missing %q", card, want)
		}
	}

	frame := state.RenderFrame()
	for _, want := range []string{"Thread: thread-1", "User:", "Assistant:", "• MCP Tools", "  • docs", "Commands:"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame = %q, missing %q", frame, want)
		}
	}
	if strings.Contains(frame, "History:") {
		t.Fatalf("frame rendered history role header:\n%s", frame)
	}
}

func TestStateRenderStatusCardUsesRuntimeUsageAndLimits(t *testing.T) {
	window := int64(200000)
	state := NewState(nil)
	state.TotalTokenUsage = TokenUsage{InputTokens: 50000, CachedInputTokens: 10000, OutputTokens: 5000, TotalTokens: 55000}
	state.LastTokenUsage = TokenUsage{TotalTokens: 50000}
	state.ModelContextWindow = &window
	state.RateLimits = []RateLimitStatus{{Label: "5h", UsedPercent: 37}, {Label: "weekly", UsedPercent: 81}}
	card := state.RenderStatusCardWidth(100)
	for _, want := range []string{"45,000 total  (40,000 input + 5,000 output)", "80% left (50,000 used / 200,000)", "5h 63% left", "weekly 19% left"} {
		if !strings.Contains(card, want) {
			t.Fatalf("status card missing %q:\n%s", want, card)
		}
	}
}

func TestStateResetThreadClearsRuntimeUsage(t *testing.T) {
	window := int64(200000)
	state := NewState(nil)
	state.TotalTokenUsage = TokenUsage{TotalTokens: 10}
	state.LastTokenUsage = TokenUsage{TotalTokens: 10}
	state.ModelContextWindow = &window
	state.RateLimits = []RateLimitStatus{{Label: "5h", UsedPercent: 50}}
	state.ResetThread()
	if !state.TotalTokenUsage.IsZero() || !state.LastTokenUsage.IsZero() || state.ModelContextWindow != nil || len(state.RateLimits) != 0 {
		t.Fatalf("ResetThread retained runtime usage: %+v", state)
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
		{input: "/keymap", command: CommandKeymap, ok: true},
		{input: "/usage weekly", command: CommandUsage, args: "weekly", ok: true},
		{input: "/goal set ship tui parity", command: CommandGoal, args: "set ship tui parity", ok: true},
		{input: "/statusline model current-dir", command: CommandStatusline, args: "model current-dir", ok: true},
		{input: "/title app-name project-name", command: CommandTitle, args: "app-name project-name", ok: true},
		{input: "/debug-config", command: CommandDebugConfig, ok: true},
		{input: "/copy", command: CommandCopy, ok: true},
		{input: "/raw on", command: CommandRaw, args: "on", ok: true},
		{input: "/diff", command: CommandDiff, ok: true},
		{input: "/ps", command: CommandPs, ok: true},
		{input: "/stop", command: CommandStop, ok: true},
		{input: "/clean", command: CommandStop, ok: true},
		{input: "/permissions", command: CommandPermissions, ok: true},
		{input: "/personality", command: CommandPersonality, ok: true},
		{input: "/experimental", command: CommandExperimental, ok: true},
		{input: "/mcp verbose", command: CommandMcp, args: "verbose", ok: true},
		{input: "/skills", command: CommandSkills, ok: true},
		{input: "/plugins", command: CommandPlugins, ok: true},
		{input: "/apps", command: CommandApps, ok: true},
		{input: "/review custom", command: CommandReview, args: "custom", ok: true},
		{input: "/rename work", command: CommandRename, args: "work", ok: true},
		{input: "/theme", command: CommandTheme, ok: true},
		{input: "/pet off", command: CommandPets, args: "off", ok: true},
		{input: "/plan investigate", command: CommandPlan, args: "investigate", ok: true},
		{input: "/btw quick question", command: CommandSide, args: "quick question", ok: true},
		{input: "/subagents", command: CommandAgent, ok: true},
		{input: "/ide", command: CommandIde, ok: true},
		{input: "/vim", command: CommandVim, ok: true},
		{input: "/mention", command: CommandMention, ok: true},
		{input: "/approve", command: CommandAutoReview, ok: true},
		{input: "/import", command: CommandImport, ok: true},
		{input: "/setup-default-sandbox", command: CommandElevateSandbox, ok: true},
		{input: "/sandbox-add-read-dir D:\\tmp", command: CommandSandboxReadRoot, args: "D:\\tmp", ok: true},
		{input: "/rollout", command: CommandRollout, ok: true},
		{input: "/test-approval", command: CommandTestApproval, ok: true},
		{input: "/debug-m-drop", command: CommandMemoryDrop, ok: true},
		{input: "/debug-m-update", command: CommandMemoryUpdate, ok: true},
		{input: "/model gpt-5", command: CommandModel, args: "gpt-5", ok: true},
		{input: "/approval on-request", command: CommandApproval, args: "on-request", ok: true},
		{input: "/editor", command: CommandEditor, ok: true},
		{input: "/logout", command: CommandLogout, ok: true},
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

func TestSlashCommandFrameDescriptionsMatchRust(t *testing.T) {
	frames := map[string]SlashCommandFrame{}
	for _, frame := range SlashCommandFrames() {
		frames[frame.Name] = frame
	}
	want := map[string]string{
		"model":                "choose what model and reasoning effort to use",
		"ide":                  "include current selection, open files, and other context from your IDE",
		"permissions":          "choose what Codex is allowed to do",
		"keymap":               "remap TUI shortcuts",
		"review":               "review my current changes and find issues",
		"side":                 "start a side conversation in an ephemeral fork",
		"copy":                 "copy last response as markdown",
		"raw":                  "toggle raw scrollback mode for copy-friendly terminal selection",
		"diff":                 "show git diff (including untracked files)",
		"status":               "show current session configuration and token usage",
		"usage":                "view account usage or use a usage limit reset",
		"mcp":                  "list configured MCP tools; use /mcp verbose for details",
		"approve":              "approve one retry of a recent auto-review denial",
		"memories":             "configure memory use and generation",
		"app":                  "continue this session in Codex Desktop",
		"import":               "import setup, this project, and recent chats from Claude Code",
		"sandbox-add-read-dir": "let sandbox read a directory: /sandbox-add-read-dir <absolute_path>",
		"rollout":              "print the rollout file path",
	}
	for name, description := range want {
		frame, ok := frames[name]
		if !ok {
			t.Fatalf("missing slash command frame %q", name)
		}
		if frame.Description != description {
			t.Fatalf("%s description = %q, want %q", name, frame.Description, description)
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
