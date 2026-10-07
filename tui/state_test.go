package tui

import (
	"strings"
	"testing"
	"time"
)

func TestStateRenderWelcomeAndFrame(t *testing.T) {
	state := NewState(&Options{
		Model:          "gpt-test",
		ApprovalPolicy: "on-request",
		Sandbox:        "workspace-write",
		CWD:            `D:\repo`,
		Search:         true,
		NoAltScreen:    true,
		CLIVersion:     "0.145.0",
		AccountDisplay: "dev@example.com (plus)",
		AgentsSummary:  "AGENTS.md",
	})
	state.SetThreadName("Release triage")
	state.SetThreadID("thread-1")
	state.AddMessage(RoleUser, "hello")
	state.AddMessage(RoleAssistant, "hi there")
	state.AddHistoryLines([]string{"• MCP Tools", "  • docs"}, []string{"MCP Tools", "docs"})

	welcome := state.RenderWelcome()
	for _, want := range []string{"gcode", "Model:", "gpt-test", "Workspace (Ask for approval)", `Directory:`, `D:\repo`} {
		if !strings.Contains(welcome, want) {
			t.Fatalf("welcome = %q, missing %q", welcome, want)
		}
	}
	card := state.RenderStatusCard()
	for _, want := range []string{"gcode (v0.145.0)", "Model:", "gpt-test", "Agents.md:", "AGENTS.md", "Account:", "dev@example.com (plus)", "Thread name:", "Release triage", "Session:", "thread-1", "Limits:"} {
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
	capturedAt := time.Date(2026, 7, 29, 3, 4, 0, 0, time.Local)
	reset := capturedAt.Add(10 * time.Minute)
	state.RateLimits = []RateLimitStatus{
		{Label: "5h", UsedPercent: 37, CapturedAt: capturedAt, ResetsAt: &reset},
		{Label: "weekly", UsedPercent: 81, CapturedAt: capturedAt},
		{Label: "Credits", Text: "38 credits", IsText: true, CapturedAt: capturedAt},
	}
	card := state.RenderStatusCardWidth(100)
	for _, want := range []string{"45K total  (40K input + 5K output)", "80% left (50K used / 200K)", "5h limit:", "63% left", "(resets 03:14)", "Weekly limit:", "19% left", "Credits:", "38 credits"} {
		if !strings.Contains(card, want) {
			t.Fatalf("status card missing %q:\n%s", want, card)
		}
	}
	// Rust #48754: the status summary is borderless.
	if strings.ContainsAny(card, "\u256d\u256e\u2570\u256f\u2502") {
		t.Fatalf("status card must not draw a border:\n%s", card)
	}
}

// TestStateRenderStatusCardWrapsLongValuesLikeRust mirrors Rust #48754
// (status/tests.rs `status_wraps_long_paths_and_session_ids_without_losing_text`):
// long Unicode paths and session ids wrap instead of truncating, no row exceeds
// the terminal width, and no text is lost at any width.
func TestStateRenderStatusCardWrapsLongValuesLikeRust(t *testing.T) {
	const directory = "/workspace/projects/界界/ｶﾞﾞ/a-very-long-directory-name/codex"
	const sessionID = "00000000-0000-0000-0000-000000000123"
	state := NewState(&Options{
		Model:          "gpt-5.5",
		CWD:            directory,
		ApprovalPolicy: "on-request",
		Sandbox:        "workspace-write",
	})
	state.SetThreadID(sessionID)
	state.SetThreadName("A thread with a long descriptive name")
	for _, width := range []int{7, 12, 17, 18, 24, 25, 40, 80} {
		card := state.RenderStatusCardWidth(width)
		var joined strings.Builder
		for _, line := range strings.Split(card, "\n") {
			if got := statusRowVisibleWidth(line); got > width {
				t.Fatalf("row wider than %d at width %d: %q (%d)", width, width, line, got)
			}
			joined.WriteString(strings.TrimSpace(line))
		}
		if !strings.Contains(joined.String(), directory) {
			t.Fatalf("status card lost the directory at width %d:\n%s", width, card)
		}
		if !strings.Contains(joined.String(), sessionID) {
			t.Fatalf("status card lost the session id at width %d:\n%s", width, card)
		}
		if strings.ContainsAny(card, "\u256d\u256e\u2570\u256f\u2502") {
			t.Fatalf("status card must not draw a border at width %d:\n%s", width, card)
		}
	}
}

// TestStateRenderStatusCardWideLayoutStable guards the Go-specific width policy
// from Rust #48754: the 44-column floor is gone so narrow terminals wrap, while
// the upper bound and the wide-terminal value column are unchanged.
func TestStateRenderStatusCardWideLayoutStable(t *testing.T) {
	state := NewState(&Options{
		Model:           "gpt-5.1-codex-max",
		ReasoningEffort: "high",
		CWD:             "/workspace/tests",
		ApprovalPolicy:  "on-request",
		Sandbox:         "workspace-write",
		CLIVersion:      "0.145.0",
	})
	state.SetThreadID("thread-1")
	wide := state.RenderStatusCardWidth(100)
	if state.RenderStatusCardWidth(101) != wide || state.RenderStatusCardWidth(200) != wide {
		t.Fatalf("wide terminals must render the same card as width 100")
	}
	if state.RenderStatusCardWidth(43) == state.RenderStatusCardWidth(44) {
		t.Fatalf("removing the 44-column floor must only affect widths below 44")
	}
	// The value column is where the bordered card put it: the old
	// 1 + labelWidth + 1 + 3 equals the new 2 + labelWidth + 1 + 2.
	labelWidth := 18 // "Collaboration mode"
	valueOffset := DisplayWidth(statusCardIndent) + labelWidth + 1 + 2
	if valueOffset != 1+labelWidth+1+3 {
		t.Fatalf("value column moved to %d", valueOffset)
	}
	for _, entry := range []struct{ label, value string }{
		{"Model", "gpt-5.1-codex-max (reasoning high, summaries auto)"},
		{"Directory", "/workspace/tests"},
		{"Permissions", "Workspace (Ask for approval)"},
		{"Session", "thread-1"},
	} {
		prefix := statusCardIndent + entry.label + ":" + strings.Repeat(" ", 2+labelWidth-DisplayWidth(entry.label))
		line := ""
		for _, candidate := range strings.Split(wide, "\n") {
			if strings.HasPrefix(candidate, prefix) {
				line = candidate
				break
			}
		}
		if line == "" {
			t.Fatalf("wide card missing %q:\n%s", entry.label, wide)
		}
		if DisplayWidth(prefix) != valueOffset {
			t.Fatalf("%s value column = %d, want %d", entry.label, DisplayWidth(prefix), valueOffset)
		}
		if line != prefix+entry.value {
			t.Fatalf("wide %s row wrapped: %q", entry.label, line)
		}
	}
}

func TestStateRenderStatusCardHidesTokenUsageForChatGPTAccount(t *testing.T) {
	state := NewState(&Options{AccountDisplay: "dev@example.com (plus)", HasChatGPTAccount: true})
	card := state.RenderStatusCardWidth(80)
	if !strings.Contains(card, "dev@example.com (plus)") {
		t.Fatalf("status card missing account:\n%s", card)
	}
	if strings.Contains(card, "Token usage:") {
		t.Fatalf("ChatGPT status card should hide token usage:\n%s", card)
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
	if !state.TotalTokenUsage.IsZero() || !state.LastTokenUsage.IsZero() || state.ModelContextWindow != nil || len(state.RateLimits) != 0 || state.RateLimitsLoaded || state.RateLimitsRefreshing {
		t.Fatalf("ResetThread retained runtime usage: %+v", state)
	}
}

func TestStateRenderStatusCardOmitsDefaultProviderAndShowsDefaultCollaborationMode(t *testing.T) {
	state := NewState(&Options{Provider: "OpenAI", ApprovalPolicy: "never", Sandbox: "danger-full-access"})
	card := state.RenderStatusCardWidth(80)
	if strings.Contains(card, "Model provider:") {
		t.Fatalf("default provider leaked into status card:\n%s", card)
	}
	if !strings.Contains(card, "Collaboration mode:") || !strings.Contains(card, "Default") {
		t.Fatalf("status card missing default collaboration mode:\n%s", card)
	}
	if !strings.Contains(card, "Permissions:") || !strings.Contains(card, "Full Access") {
		t.Fatalf("status card missing Rust permissions label:\n%s", card)
	}
}

func TestStateRenderStatusCardUsesRustPermissionDefaults(t *testing.T) {
	card := NewState(nil).RenderStatusCardWidth(80)
	if !strings.Contains(card, "Permissions:") || !strings.Contains(card, "Read Only (Ask for approval)") {
		t.Fatalf("status card missing Rust default permissions:\n%s", card)
	}
}

// TestStateRenderStatusCardShowsServerProviderForAttachedThread covers Rust
// #43359: the provider id comes from the attached thread's server metadata and
// is shown verbatim (built-in providers included); without a thread it is
// omitted.
func TestStateRenderStatusCardShowsServerProviderForAttachedThread(t *testing.T) {
	unattached := NewState(&Options{Provider: "openai"})
	if card := unattached.RenderStatusCardWidth(80); strings.Contains(card, "Model provider:") {
		t.Fatalf("provider must be omitted without a thread:\n%s", card)
	}

	attached := NewState(&Options{Provider: "openai"})
	attached.SetThreadID("thread-1")
	if card := attached.RenderStatusCardWidth(80); !statusCardHasProvider(card, "openai") {
		t.Fatalf("provider missing for an attached thread:\n%s", card)
	}

	custom := NewState(&Options{Provider: "server-ollama"})
	custom.SetThreadID("thread-1")
	if card := custom.RenderStatusCardWidth(80); !statusCardHasProvider(card, "server-ollama") {
		t.Fatalf("custom provider missing:\n%s", card)
	}
}

func statusCardHasProvider(card string, provider string) bool {
	for _, line := range strings.Split(card, "\n") {
		if !strings.Contains(line, "Model provider:") {
			continue
		}
		return strings.Contains(line, provider)
	}
	return false
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
		{input: "/agents", command: CommandAgents, ok: true},
		{input: "/multi-agents", command: CommandUnknown, ok: true},
		{input: "/ide", command: CommandIde, ok: true},
		{input: "/vim", command: CommandVim, ok: true},
		{input: "/mention", command: CommandMention, ok: true},
		{input: "/approve", command: CommandAutoReview, ok: true},
		{input: "/import", command: CommandImport, ok: true},
		{input: "/setup-default-sandbox", command: CommandElevateSandbox, ok: true},
		{input: "/rollout", command: CommandRollout, ok: true},
		{input: "/test-approval", command: CommandTestApproval, ok: true},
		{input: "/cd /tmp", command: CommandCd, args: "/tmp", ok: true},
		{input: "/pwd", command: CommandPwd, ok: true},
		{input: "/cwd", command: CommandPwd, ok: true},
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
		"model":       "choose what model and reasoning effort to use",
		"ide":         "include current selection, open files, and other context from your IDE",
		"permissions": "choose what Codex is allowed to do",
		"keymap":      "remap TUI shortcuts",
		"review":      "review my current changes and find issues",
		"side":        "start a side conversation in an ephemeral fork",
		"copy":        "copy last response as markdown",
		"raw":         "toggle raw scrollback mode for copy-friendly terminal selection",
		"diff":        "show git diff (including untracked files)",
		"status":      "show current session configuration and token usage",
		"usage":       "view account usage or use a usage limit reset",
		"mcp":         "list configured MCP tools; use /mcp verbose for details",
		"approve":     "approve one retry of a recent auto-review denial",
		"memories":    "configure memory use and generation",
		"app":         "continue this session in Codex Desktop",
		"import":      "import setup, this project, and recent chats from Claude Code",
		"rollout":     "print the rollout file path",
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

// TestStatusCardLinksChatGPTUsageURLLikeRust mirrors Rust #51457: the ChatGPT
// usage link renders as a terminal hyperlink whose complete destination is
// preserved.
func TestStatusCardLinksChatGPTUsageURLLikeRust(t *testing.T) {
	const url = "https://chatgpt.com/codex/settings/usage"
	card := NewState(nil).RenderStatusCardWidth(100)
	if !strings.Contains(card, "\x1b]8;;"+url+"\x07") {
		t.Fatalf("status card missing hyperlink to %q:\n%s", url, card)
	}
	if !strings.Contains(StripOSC8(card), url) {
		t.Fatalf("status card missing complete URL %q:\n%s", url, card)
	}
}

// TestStateRenderStatusCardHidesReasoningSummariesForServerConnectionLikeRust
// mirrors Rust #49145: the /status model row keeps "summaries auto" only while
// no server connection owns the model settings. Rust's snapshots read
// "gpt-5.1-codex-max (reasoning medium, summaries auto)" with no connection and
// "gpt-5.1-codex-max (reasoning medium)" for a remote server or the local
// background daemon.
func TestStateRenderStatusCardHidesReasoningSummariesForServerConnectionLikeRust(t *testing.T) {
	newState := func(remote bool) *State {
		state := NewState(&Options{
			Model:           "gpt-5.1-codex-max",
			ReasoningEffort: "medium",
			CWD:             "/workspace/tests",
			ApprovalPolicy:  "on-request",
			Sandbox:         "workspace-write",
		})
		state.RemoteConnection = remote
		return state
	}
	card := newState(false).RenderStatusCardWidth(100)
	if !strings.Contains(card, "gpt-5.1-codex-max (reasoning medium, summaries auto)") {
		t.Fatalf("no-connection card must keep the summaries setting:\n%s", card)
	}
	for _, connection := range []string{"remote server", "local background server"} {
		card := newState(true).RenderStatusCardWidth(100)
		if !strings.Contains(card, "gpt-5.1-codex-max (reasoning medium)") {
			t.Fatalf("%s card must render the bare reasoning details:\n%s", connection, card)
		}
		if strings.Contains(card, "summaries auto") {
			t.Fatalf("%s card must hide the summaries setting:\n%s", connection, card)
		}
	}
}
