package historycell

import (
	"strings"
	"testing"

	"codex_go/internal/tui"
	"codex_go/internal/utils"
)

func TestBaseCellsDisplayRawAndHyperlinks(t *testing.T) {
	plain := NewPlainHistoryCell([]string{"one", "two"})
	if got := strings.Join(plain.DisplayLines(10), "|"); got != "one|two" {
		t.Fatalf("plain display = %q", got)
	}
	if got := strings.Join(plain.RawLines(), "|"); got != "one|two" {
		t.Fatalf("plain raw = %q", got)
	}

	wrapped := NewPrefixedWrappedHistoryCell("hello world from codex", "> ", "  ")
	lines := wrapped.DisplayLines(12)
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "> ") {
		t.Fatalf("wrapped lines = %#v", lines)
	}

	web := NewWebHyperlinkHistoryCell([]string{"see https://example.com/a."})
	links := web.DisplayHyperlinkLines(80)
	if len(links) != 1 || len(links[0].Links) != 1 || links[0].Links[0].Destination != "https://example.com/a" {
		t.Fatalf("hyperlinks = %#v", links)
	}

	composite := NewCompositeHistoryCell([]HistoryCell{
		NewPlainHistoryCell([]string{"a"}),
		NewPlainHistoryCell([]string{"b"}),
	})
	if got := strings.Join(composite.DisplayLines(80), "|"); got != "a||b" {
		t.Fatalf("composite display = %q", got)
	}
	if got := strings.Join(composite.RawLines(), "|"); got != "a||b" {
		t.Fatalf("composite raw = %q", got)
	}
}

func TestRawLinesFromSourceMatchesRustTranscriptSemantics(t *testing.T) {
	if got := strings.Join(rawLinesFromSource("alpha\n\nbeta\n"), "|"); got != "alpha||beta" {
		t.Fatalf("explicit blank lines = %q", got)
	}
	if got := strings.Join(rawLinesFromSource("alpha\n\n"), "|"); got != "alpha|" {
		t.Fatalf("trailing blank without trailing newline = %q", got)
	}
	if got := rawLinesFromSource(""); len(got) != 0 {
		t.Fatalf("empty source raw lines = %#v", got)
	}
}

func TestUserAndAgentMessageCells(t *testing.T) {
	user := NewUserPrompt("hello\nworld\n", nil, nil, []string{"https://example.com/image.png", "https://example.com/2.png"})
	display := user.DisplayLines(40)
	joined := strings.Join(display, "\n")
	clean := utils.StripANSI(joined)
	for _, want := range []string{"[image]", "[image 2]", "› hello", "  world"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("user display missing %q:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "\x1b[48;5;235m") {
		t.Fatalf("user display should use the Rust-style shaded background:\n%q", joined)
	}
	for _, line := range display {
		if got := tui.DisplayWidth(utils.StripANSI(line)); got != 40 {
			t.Fatalf("user line width = %d, want 40: %q", got, line)
		}
	}
	raw := strings.Join(user.RawLines(), "\n")
	if !strings.Contains(raw, "hello\nworld") || !strings.Contains(raw, "[image 2]") {
		t.Fatalf("user raw = %q", raw)
	}

	agent := NewAgentMessageCell([]string{"assistant says hello", "second line"}, true)
	agentDisplay := strings.Join(agent.DisplayLines(16), "\n")
	if !strings.Contains(agentDisplay, "• assistant") || !strings.Contains(agentDisplay, "  second line") {
		t.Fatalf("agent display:\n%s", agentDisplay)
	}
	if got := strings.Join(agent.RawLines(), "|"); got != "assistant says hello|second line" {
		t.Fatalf("agent raw = %q", got)
	}

	reasoning := NewReasoningSummaryCell("thinking through files", false)
	if got := strings.Join(reasoning.DisplayLines(12), "\n"); !strings.Contains(got, "• thinking") {
		t.Fatalf("reasoning display = %q", got)
	}
	transcriptOnly := NewReasoningSummaryCell("hidden", true)
	if len(transcriptOnly.DisplayLines(80)) != 0 || len(transcriptOnly.RawLines()) != 0 {
		t.Fatalf("transcript only reasoning leaked display/raw")
	}
}

func TestPlanCells(t *testing.T) {
	cell := NewPlanUpdate("Need to update TUI.", []PlanItemArg{
		{Step: "scan Rust modules", Status: StepCompleted},
		{Step: "port cells", Status: StepInProgress},
		{Step: "write snapshots", Status: StepPending},
	})
	display := strings.Join(cell.DisplayLines(40), "\n")
	for _, want := range []string{"Updated Plan", "✓ scan Rust modules", "▶ port cells", "□ write snapshots"} {
		if !strings.Contains(display, want) {
			t.Fatalf("plan display missing %q:\n%s", want, display)
		}
	}
	raw := strings.Join(cell.RawLines(), "\n")
	for _, want := range []string{"Completed: scan Rust modules", "InProgress: port cells", "Pending: write snapshots"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("plan raw missing %q:\n%s", want, raw)
		}
	}

	empty := NewPlanUpdate("", nil)
	if got := strings.Join(empty.DisplayLines(40), "\n"); !strings.Contains(got, "(no steps provided)") {
		t.Fatalf("empty plan display = %q", got)
	}

	proposed := NewProposedPlan("- one\n- two")
	proposedDisplay := strings.Join(proposed.DisplayLines(40), "\n")
	if !strings.Contains(proposedDisplay, "Proposed Plan") || !strings.Contains(proposedDisplay, "- one") {
		t.Fatalf("proposed display:\n%s", proposedDisplay)
	}
	if got := strings.Join(proposed.RawLines(), "|"); got != "- one|- two" {
		t.Fatalf("proposed raw = %q", got)
	}
}

func TestHookHistoryCells(t *testing.T) {
	running := NewRunningHookRun("preToolUse", "checking command")
	if got := strings.Join(running.DisplayLines(80), "\n"); !strings.Contains(got, "• Running PreToolUse hook: checking command") {
		t.Fatalf("running hook display:\n%s", got)
	}
	if got := strings.Join(running.RawLines(), "|"); got != "Running PreToolUse hook: checking command" {
		t.Fatalf("running hook raw = %q", got)
	}

	completed := NewHookRun("postToolUse", "failed", "", []HookOutputEntry{
		{Kind: HookOutputWarning, Text: "Heads up from the hook"},
		{Kind: HookOutputContext, Text: "Remember the startup checklist."},
		{Kind: HookOutputError, Text: "hook exited with code 7"},
	})
	display := strings.Join(completed.DisplayLines(80), "\n")
	for _, want := range []string{
		"• PostToolUse hook (failed)",
		"warning: Heads up from the hook",
		"hook context: Remember the startup checklist.",
		"error: hook exited with code 7",
	} {
		if !strings.Contains(display, want) {
			t.Fatalf("completed hook display missing %q:\n%s", want, display)
		}
	}
	raw := strings.Join(completed.RawLines(), "\n")
	if !strings.Contains(raw, "PostToolUse hook (failed)") || !strings.Contains(raw, "error: hook exited with code 7") {
		t.Fatalf("completed hook raw:\n%s", raw)
	}
}

func TestApprovalHistoryCells(t *testing.T) {
	approved := NewApprovalDecisionCell(NewCommandApprovalSubject([]string{"/bin/bash", "-lc", "go test ./...\nsecond"}), ReviewApprovedForSession, ApprovalActorUser)
	display := strings.Join(approved.DisplayLines(80), "\n")
	for _, want := range []string{"✔ You approved codex to run go test ./... ... every time this session"} {
		if !strings.Contains(display, want) {
			t.Fatalf("approval display missing %q:\n%s", want, display)
		}
	}

	quoted := NewApprovalDecisionCell(NewCommandApprovalSubject([]string{"foo", "bar baz", "weird&stuff"}), ReviewApproved, ApprovalActorUser)
	if got := strings.Join(quoted.RawLines(), "\n"); !strings.Contains(got, "You approved codex to run foo 'bar baz' 'weird&stuff' this time") {
		t.Fatalf("quoted command raw:\n%s", got)
	}

	fish := NewApprovalDecisionCell(NewCommandApprovalSubject([]string{"fish", "-lc", "echo hello"}), ReviewApproved, ApprovalActorUser)
	if got := strings.Join(fish.RawLines(), "\n"); !strings.Contains(got, "You approved codex to run fish -lc 'echo hello' this time") {
		t.Fatalf("fish command should not be stripped like Rust:\n%s", got)
	}

	powershell := NewApprovalDecisionCell(NewCommandApprovalSubject([]string{"pwsh", "-NoProfile", "-Command", "Get-ChildItem"}), ReviewApproved, ApprovalActorUser)
	if got := strings.Join(powershell.RawLines(), "\n"); !strings.Contains(got, "You approved codex to run Get-ChildItem this time") {
		t.Fatalf("powershell command raw:\n%s", got)
	}

	emptyExecpolicyPrefix := NewApprovalDecisionCell(NewCommandApprovalSubject(nil), ReviewApprovedExecpolicyAmendment, ApprovalActorUser)
	if got := strings.Join(emptyExecpolicyPrefix.RawLines(), "\n"); got != "You approved codex to always run commands that start with " {
		t.Fatalf("empty execpolicy prefix raw = %q", got)
	}

	longGraphemeCommand := strings.Repeat("e\u0301", 90)
	longGrapheme := NewApprovalDecisionCell(NewCommandApprovalSubject([]string{"bash", "-lc", longGraphemeCommand}), ReviewApproved, ApprovalActorUser)
	wantLong := "You approved codex to run " + strings.Repeat("e\u0301", 77) + "... this time"
	if got := strings.Join(longGrapheme.RawLines(), "\n"); !strings.Contains(got, wantLong) {
		t.Fatalf("long grapheme command raw:\n%s\nwant contains:\n%s", got, wantLong)
	}

	denied := NewApprovalDecisionCell(NewNetworkApprovalSubject("api.example.com"), ReviewDenied, ApprovalActorUser)
	if got := strings.Join(denied.DisplayLines(80), "\n"); !strings.Contains(got, "✗ You did not approve codex network access to api.example.com") {
		t.Fatalf("network denial display:\n%s", got)
	}

	allowPolicy := NewApprovalDecisionCell(NewNetworkPolicyApprovalSubject("api.example.com", NetworkPolicyRuleAllow), ReviewNetworkPolicyAmendment, ApprovalActorUser)
	if got := strings.Join(allowPolicy.RawLines(), "\n"); got != "You persisted Codex network access to api.example.com" {
		t.Fatalf("network policy allow raw = %q", got)
	}
	denyPolicy := NewApprovalDecisionCell(NewNetworkPolicyApprovalSubject("api.example.com", NetworkPolicyRuleDeny), ReviewNetworkPolicyAmendment, ApprovalActorUser)
	if got := strings.Join(denyPolicy.DisplayLines(120), "\n"); !strings.Contains(got, "✗ You denied codex network access to api.example.com and saved that rule") {
		t.Fatalf("network policy deny display:\n%s", got)
	}

	patch := NewGuardianDeniedPatchRequest([]string{"a.go", "b.go"})
	if got := strings.Join(patch.RawLines(), "|"); got != "Request denied for codex to apply a patch touching 2 files" {
		t.Fatalf("patch raw = %q", got)
	}

	patchWithWhitespace := NewGuardianDeniedPatchRequest([]string{"  a.go  "})
	if got := strings.Join(patchWithWhitespace.RawLines(), "|"); got != "Request denied for codex to apply a patch touching   a.go  " {
		t.Fatalf("patch whitespace raw = %q", got)
	}
	manyFiles := make([]string, 1000)
	manyPatch := NewGuardianTimedOutPatchRequest(manyFiles)
	if got := strings.Join(manyPatch.RawLines(), "|"); got != "Review timed out before codex could apply a patch touching 1000 files" {
		t.Fatalf("many patch raw = %q", got)
	}

	approvedAction := NewGuardianApprovedActionRequest("  run check  ")
	if got := strings.Join(approvedAction.DisplayLines(120), "\n"); !strings.Contains(got, "✔ Request approved for run check") {
		t.Fatalf("guardian approved action display:\n%s", got)
	}
	if got := strings.Join(approvedAction.RawLines(), "|"); got != "Request approved for   run check  " {
		t.Fatalf("guardian approved action raw = %q", got)
	}
	deniedAction := NewGuardianDeniedActionRequest("  run check  ")
	if got := strings.Join(deniedAction.RawLines(), "|"); got != "Request denied for   run check  " {
		t.Fatalf("guardian denied action raw = %q", got)
	}
	status := NewReviewStatusLine("  still reviewing  ")
	if got := strings.Join(status.RawLines(), "|"); got != "  still reviewing  " {
		t.Fatalf("review status raw = %q", got)
	}
}

func TestRequestUserInputHistoryCell(t *testing.T) {
	cell := NewRequestUserInputResult([]RequestUserInputQuestion{
		{ID: "scope", Question: "Where should this apply?", Options: []string{"Plan", "All"}},
		{ID: "secret", Question: "Enter token", IsSecret: true},
		{ID: "missing", Question: "Anything else?"},
	}, map[string]RequestUserInputAnswer{
		"scope":  {Answers: []string{"All", "user_note: include tests"}},
		"secret": {Answers: []string{"sk-test"}},
	}, true)
	display := strings.Join(cell.DisplayLines(80), "\n")
	for _, want := range []string{
		"• Questions 2/3 answered (interrupted)",
		"  • Where should this apply?",
		"    answer: All",
		"    note: include tests",
		"    answer: ••••••",
		"  • Anything else? (unanswered)",
		"interrupted with 1 unanswered",
	} {
		if !strings.Contains(display, want) {
			t.Fatalf("request_user_input display missing %q:\n%s", want, display)
		}
	}
	raw := strings.Join(cell.RawLines(), "\n")
	for _, want := range []string{"Questions 2/3 answered", "answer: ******", "note: include tests", "(unanswered)"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("request_user_input raw missing %q:\n%s", want, raw)
		}
	}
}

func TestSplitRequestUserInputAnswerPreservesRustWhitespace(t *testing.T) {
	options, note := splitRequestUserInputAnswer(RequestUserInputAnswer{
		Answers: []string{"  All  ", "", "user_note:   keep spacing  "},
	})
	if len(options) != 2 || options[0] != "  All  " || options[1] != "" {
		t.Fatalf("options = %#v", options)
	}
	if note != "  keep spacing  " {
		t.Fatalf("note = %q", note)
	}
}

func TestNoticeHistoryCells(t *testing.T) {
	warning := NewWarningEvent("Careful with this command")
	if got := strings.Join(warning.DisplayLines(80), "\n"); !strings.Contains(got, "⚠ Careful with this command") {
		t.Fatalf("warning display:\n%s", got)
	}

	info := NewInfoEvent("Background task finished", "open /ps")
	if got := strings.Join(info.DisplayLines(80), "\n"); got != "• Background task finished open /ps" {
		t.Fatalf("info display = %q", got)
	}
	info = NewInfoEvent("  Background task finished  ", " open /ps ")
	if got := strings.Join(info.DisplayLines(80), "\n"); got != "•   Background task finished    open /ps " {
		t.Fatalf("info whitespace display = %q", got)
	}
	err := NewErrorEvent(" launch failed ")
	if got := strings.Join(err.DisplayLines(80), "\n"); got != "■  launch failed " {
		t.Fatalf("error whitespace display = %q", got)
	}

	safety := NewSafetyAccessBlockEvent()
	safetyDisplay := strings.Join(safety.DisplayLines(80), "\n")
	if !strings.Contains(safetyDisplay, SafetyAccessBlockTitle) || !strings.Contains(safetyDisplay, "you’re a researcher") || !strings.Contains(safetyDisplay, "Trusted Access: https://openai.com/form/trusted-access-for-life-sciences") {
		t.Fatalf("safety display:\n%s", safetyDisplay)
	}
	cyber := NewCyberPolicyErrorEvent()
	if got := strings.Join(cyber.RawLines(), "\n"); !strings.Contains(got, "you’re a security professional") || !strings.Contains(got, "https://openai.com/form/enterprise-trusted-access-for-cyber/") {
		t.Fatalf("cyber raw:\n%s", got)
	}

	deprecation := NewDeprecationNotice("Old flag is deprecated", "Use --new instead.")
	if got := strings.Join(deprecation.RawLines(), "|"); got != "Old flag is deprecated|Use --new instead." {
		t.Fatalf("deprecation raw = %q", got)
	}
	deprecation = NewDeprecationNotice("  Old flag is deprecated  ", "  Use --new instead.  ")
	if got := strings.Join(deprecation.RawLines(), "|"); got != "  Old flag is deprecated  |  Use --new instead.  " {
		t.Fatalf("deprecation whitespace raw = %q", got)
	}
	longSummary := NewDeprecationNotice("abcdef ghijkl", "")
	if got := strings.Join(longSummary.DisplayLines(8), "\n"); got != "⚠ abcdef ghijkl" {
		t.Fatalf("deprecation summary should not wrap like Rust = %q", got)
	}
}

func TestFinalMessageSeparatorHistoryCell(t *testing.T) {
	elapsed := int64(125)
	cell := NewFinalMessageSeparator(&elapsed, &RuntimeMetricsSummary{
		ToolCalls:                    RuntimeMetricCountDuration{Count: 2, DurationMS: 1250},
		APICalls:                     RuntimeMetricCountDuration{Count: 1, DurationMS: 900},
		ResponsesAPIOverheadMS:       150,
		ResponsesAPIEngineIAPITTFTMS: 70,
	})
	display := strings.Join(cell.DisplayLines(220), "\n")
	for _, want := range []string{"Worked for 2m 5s", "Local tools: 2 calls (1.2s)", "Inference: 1 call (900ms)", "TTFT: 70ms (iapi)"} {
		if !strings.Contains(display, want) {
			t.Fatalf("separator display missing %q:\n%s", want, display)
		}
	}
	raw := strings.Join(cell.RawLines(), "\n")
	if !strings.Contains(raw, "Responses API overhead: 150ms") {
		t.Fatalf("separator raw:\n%s", raw)
	}

	empty := NewFinalMessageSeparator(nil, nil)
	if got := empty.DisplayLines(8)[0]; got != "────────" {
		t.Fatalf("empty separator = %q", got)
	}
	if raw := empty.RawLines(); len(raw) != 0 {
		t.Fatalf("empty separator raw = %#v", raw)
	}
}

func TestMCPHistoryCells(t *testing.T) {
	active := NewActiveMcpToolCall("call-1", McpInvocation{Server: "docs", Tool: "lookup", Arguments: `{"q":"rust"}`})
	if got := strings.Join(active.DisplayLines(80), "\n"); !strings.Contains(got, `Calling docs.lookup({"q":"rust"})`) {
		t.Fatalf("active mcp display:\n%s", got)
	}
	if got := active.DisplayLines(22); len(got) < 2 || got[0] != "\u2022 Calling" || !strings.HasPrefix(got[1], "  \u2514 docs.lookup(") {
		t.Fatalf("narrow active MCP call should use Rust's tree layout: %#v", got)
	}

	completed := NewMcpToolCall("call-1", McpInvocation{Server: "docs", Tool: "lookup"}, McpToolResult{Content: []string{"first line\nsecond line"}})
	if got := strings.Join(completed.DisplayLines(80), "\n"); !strings.Contains(got, "Called docs.lookup()") || !strings.Contains(got, "└ first line") {
		t.Fatalf("completed mcp display:\n%s", got)
	}

	failed := NewActiveMcpToolCall("call-2", McpInvocation{Server: "docs", Tool: "bad"})
	failed.MarkFailed("interrupted")
	if got := strings.Join(failed.RawLines(), "|"); !strings.Contains(got, "Error: interrupted") {
		t.Fatalf("failed mcp raw = %q", got)
	}

	empty := EmptyMCPOutput()
	if got := strings.Join(empty.DisplayLines(80), "\n"); !strings.Contains(got, "No MCP servers configured") || !strings.Contains(got, "https://developers.openai.com/codex/mcp") {
		t.Fatalf("empty mcp display:\n%s", got)
	}

	inventory := NewMCPToolsOutputFromStatuses([]McpServerStatus{
		{
			Name:  "docs",
			Auth:  "OAuth",
			Tools: []string{"search", "read"},
			Resources: []McpResource{
				{Name: "guide", URI: "file://guide"},
			},
			ResourceTemplates: []McpResourceTemplate{
				{Name: "topic", URITemplate: "docs://{topic}"},
			},
		},
	}, true)
	if got := strings.Join(inventory.DisplayLines(80), "\n"); !strings.Contains(got, "• Tools: read, search") || !strings.Contains(got, "Resources: guide (file://guide)") {
		t.Fatalf("inventory display:\n%s", got)
	}
	ordered := NewMCPToolsOutputFromStatuses([]McpServerStatus{
		{
			Name:  "docs",
			Auth:  "OAuth",
			Tools: []string{"zeta", "alpha"},
			Resources: []McpResource{
				{Name: "z-resource", URI: "file://z"},
				{Name: "a-resource", URI: "file://a"},
			},
			ResourceTemplates: []McpResourceTemplate{
				{Name: "z-template", URITemplate: "docs://z/{id}"},
				{Name: "a-template", URITemplate: "docs://a/{id}"},
			},
		},
	}, true)
	orderedDisplay := strings.Join(ordered.DisplayLines(120), "\n")
	if !strings.Contains(orderedDisplay, "• Tools: alpha, zeta") {
		t.Fatalf("tools should stay sorted like Rust:\n%s", orderedDisplay)
	}
	if !strings.Contains(orderedDisplay, "Resources: z-resource (file://z), a-resource (file://a)") {
		t.Fatalf("resources should preserve app-server order like Rust:\n%s", orderedDisplay)
	}
	if !strings.Contains(orderedDisplay, "Resource templates: z-template (docs://z/{id}), a-template (docs://a/{id})") {
		t.Fatalf("resource templates should preserve app-server order like Rust:\n%s", orderedDisplay)
	}

	wrapped := NewMCPToolsOutputFromStatuses([]McpServerStatus{{
		Name:  "many-tools",
		Auth:  "Unsupported",
		Tools: []string{"gamma_tool", "alpha_tool", "beta_tool"},
	}}, false)
	wrappedLines := wrapped.DisplayLines(34)
	foundContinuation := false
	for _, line := range wrappedLines {
		if len([]rune(line)) > 34 {
			t.Fatalf("inventory line exceeds width 34: %q", line)
		}
		if strings.HasPrefix(line, strings.Repeat(" ", len([]rune("    \u2022 Tools: ")))) && strings.Contains(line, "beta_tool") {
			foundContinuation = true
		}
	}
	if !foundContinuation {
		t.Fatalf("many tools should wrap with an aligned continuation:\n%s", strings.Join(wrappedLines, "\n"))
	}
}

func TestPatchSearchAndSessionHistoryCells(t *testing.T) {
	patch := NewPatchEvent(map[string]tui.FileChange{
		`D:\repo\a.go`: tui.NewAddFileChange("package main\n"),
	}, `D:\repo`)
	patchDisplay := strings.Join(patch.DisplayLines(80), "\n")
	if !strings.Contains(patchDisplay, "Added a.go (+1 -0)") || !strings.Contains(patchDisplay, "package main") {
		t.Fatalf("patch display:\n%s", patchDisplay)
	}

	failure := NewPatchApplyFailure("bad patch\n")
	if got := strings.Join(failure.DisplayLines(80), "\n"); !strings.Contains(got, "✘ Failed to apply patch") || !strings.Contains(got, "| bad patch") {
		t.Fatalf("patch failure display:\n%s", got)
	}

	image := NewImageGenerationCall("img-1", "completed", "a red cube", `D:\repo\out.png`)
	if got := strings.Join(image.DisplayLines(80), "\n"); !strings.Contains(got, "Generated Image") || !strings.Contains(got, "Saved to:") {
		t.Fatalf("image generation display:\n%s", got)
	}

	search := NewWebSearchCall("search-1", "", WebSearchAction{Kind: WebSearchActionFindInPage, URL: "https://example.com", Pattern: "needle"})
	if got := strings.Join(search.DisplayLines(80), "\n"); !strings.Contains(got, "Searched the web for 'needle' in https://example.com") {
		t.Fatalf("search display:\n%s", got)
	}
	activeSearch := NewActiveWebSearchCall("search-2", "codex tui")
	if got := strings.Join(activeSearch.RawLines(), "|"); got != "Searching the web codex tui" {
		t.Fatalf("active search raw = %q", got)
	}

	header := NewSessionHeader("gpt-5", "high", true, `D:\repo\project`, "1.2.3").WithYoloMode(true)
	headerDisplay := strings.Join(header.DisplayLines(80), "\n")
	for _, want := range []string{"OpenAI Codex (v1.2.3)", "model: gpt-5 high", "fast", "permissions: YOLO mode"} {
		if !strings.Contains(headerDisplay, want) {
			t.Fatalf("session header missing %q:\n%s", want, headerDisplay)
		}
	}
	info := NewSessionInfo(header, true, "")
	if got := strings.Join(info.RawLines(), "\n"); !strings.Contains(got, "/init - create an AGENTS.md") {
		t.Fatalf("session info raw:\n%s", got)
	}
}

func TestExecHistoryCells(t *testing.T) {
	waited := NewUnifiedExecInteraction("go test ./...", "")
	if got := strings.Join(waited.DisplayLines(80), "\n"); !strings.Contains(got, "Waited for background terminal") || !strings.Contains(got, "go test ./...") {
		t.Fatalf("waited display = %q", got)
	}
	if got := strings.Join(waited.RawLines(), "|"); got != "Waited for background terminal: go test ./..." {
		t.Fatalf("waited raw = %q", got)
	}

	interaction := NewUnifiedExecInteraction("python", "print(1)\nprint(2)")
	display := strings.Join(interaction.DisplayLines(80), "\n")
	if !strings.Contains(display, "Interacted with background terminal") || !strings.Contains(display, "└ print(1)") {
		t.Fatalf("interaction display:\n%s", display)
	}
	raw := strings.Join(interaction.RawLines(), "\n")
	if !strings.Contains(raw, "Interacted with background terminal: python") || !strings.Contains(raw, "print(2)") {
		t.Fatalf("interaction raw:\n%s", raw)
	}

	processes := make([]UnifiedExecProcessDetails, 18)
	for i := range processes {
		processes[i] = UnifiedExecProcessDetails{
			CommandDisplay: "command " + string(rune('a'+i)),
			RecentChunks:   []string{"chunk"},
		}
	}
	cell := NewUnifiedExecProcessesOutput(processes)
	lines := strings.Join(cell.DisplayLines(40), "\n")
	if !strings.Contains(lines, "/ps") || !strings.Contains(lines, "Background terminals") || !strings.Contains(lines, "... and 2 more running") {
		t.Fatalf("process lines:\n%s", lines)
	}
	emptyProcesses := UnifiedExecProcessesCell{}
	if got := strings.Join(emptyProcesses.DisplayLines(80), "\n"); !strings.Contains(got, "  • No background terminals running.") {
		t.Fatalf("empty process lines:\n%s", got)
	}
	multiline := UnifiedExecProcessesCell{Processes: []UnifiedExecProcessDetails{{
		CommandDisplay: "echo one\necho two",
		RecentChunks:   []string{"recent output"},
	}}}
	multilineDisplay := strings.Join(multiline.DisplayLines(80), "\n")
	if !strings.Contains(multilineDisplay, "  • echo one [...]") || !strings.Contains(multilineDisplay, "    ↳ recent output") {
		t.Fatalf("multiline process display:\n%s", multilineDisplay)
	}
	narrow := UnifiedExecProcessesCell{Processes: []UnifiedExecProcessDetails{{
		CommandDisplay: "部署计划下一步",
		RecentChunks:   []string{"输出内容很长"},
	}}}
	for _, line := range narrow.DisplayLines(12)[2:] {
		if tui.DisplayWidth(line) > 12 {
			t.Fatalf("process line exceeds width: %q width=%d", line, tui.DisplayWidth(line))
		}
	}
}
