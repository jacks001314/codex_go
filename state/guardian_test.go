package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"codex_go/context"
)

func TestReviewerRoutes(t *testing.T) {
	for _, value := range []string{"auto_review", "guardian_subagent"} {
		reviewer := ReviewerFromString(value)
		if !reviewer.RoutesToGuardian() {
			t.Fatalf("%s should route to guardian", value)
		}
	}
	reviewer := ReviewerFromString("user")
	if reviewer.RoutesToGuardian() {
		t.Fatal("user reviewer should not route to guardian")
	}
}

func TestActionValidation(t *testing.T) {
	valid := []Action{
		{Type: "command", Command: "ls", CWD: "/tmp", Source: CommandSourceShell},
		{Type: "execve", Program: "python", Argv: []string{"python", "-V"}, CWD: "/tmp", Source: CommandSourceUnifiedExec},
		{Type: "apply_patch", CWD: "/tmp", Files: []string{"/tmp/a.txt"}},
		{Type: "network_access", Host: "example.com", Protocol: "https", Port: 443},
		{Type: "mcp_tool_call", Server: "server", ToolName: "tool"},
		{Type: "request_permissions", Permissions: map[string]any{"network": true}},
	}
	for _, action := range valid {
		if err := action.Validate(); err != nil {
			t.Fatalf("valid action rejected: %+v err=%v", action, err)
		}
	}
	if err := (&Action{Type: "command", Command: "ls"}).Validate(); !errors.Is(err, ErrInvalidGuardianRequest) {
		t.Fatalf("expected invalid command, got %v", err)
	}
}

func TestEventLifecycle(t *testing.T) {
	now := fixedGuardianTime()
	action := Action{Type: "command", Command: "rm -rf /tmp/x", CWD: "/repo", Source: CommandSourceShell}
	event, err := NewInProgressEvent("review-a", "turn-a", "item-a", action, now)
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	if event.Status != StatusInProgress || event.StartedAtMS != now.UnixMilli() {
		t.Fatalf("event = %+v", event)
	}
	completed, err := event.Complete(Assessment{
		RiskLevel:         RiskHigh,
		UserAuthorization: AuthorizationLow,
		Outcome:           OutcomeDeny,
		Rationale:         "too risky",
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completed.Status != StatusDenied || !completed.Terminal() {
		t.Fatalf("completed = %+v", completed)
	}
	if DecisionFromEvent(completed) != DecisionDenied {
		t.Fatalf("decision = %s", DecisionFromEvent(completed))
	}
	if GuardianRejectionMessage(completed) != "too risky" {
		t.Fatalf("message = %q", GuardianRejectionMessage(completed))
	}
}

func TestTimeoutAndAbort(t *testing.T) {
	event, err := NewInProgressEvent("review-a", "turn-a", "", Action{Type: "network_access", Host: "example.com", Protocol: "https", Port: 443}, fixedGuardianTime())
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	timedOut := event.Timeout(fixedGuardianTime().Add(time.Second))
	if timedOut.Status != StatusTimedOut || DecisionFromEvent(timedOut) != DecisionTimedOut {
		t.Fatalf("timed out = %+v", timedOut)
	}
	aborted := event.Aborted(fixedGuardianTime().Add(time.Second), "stopped")
	if aborted.Status != StatusAborted || DecisionFromEvent(aborted) != DecisionAborted {
		t.Fatalf("aborted = %+v", aborted)
	}
}

func TestCircuitBreaker(t *testing.T) {
	breaker := NewCircuitBreaker()
	if action := breaker.RecordDenial("turn-a"); action.InterruptTurn {
		t.Fatalf("first denial should continue: %+v", action)
	}
	if action := breaker.RecordDenial("turn-a"); action.InterruptTurn {
		t.Fatalf("second denial should continue: %+v", action)
	}
	action := breaker.RecordDenial("turn-a")
	if !action.InterruptTurn || action.ConsecutiveDenials != MaxConsecutiveDenialsPerTurn {
		t.Fatalf("third denial should interrupt: %+v", action)
	}
	breaker.RecordNonDenial("turn-a")
	if action := breaker.RecordDenial("turn-a"); action.ConsecutiveDenials != 1 || action.InterruptTurn {
		t.Fatalf("non denial should reset consecutive count: %+v", action)
	}
	breaker.ClearTurn("turn-a")
	if action := breaker.RecordDenial("turn-a"); action.ConsecutiveDenials != 1 {
		t.Fatalf("clear did not reset: %+v", action)
	}
}

func TestCircuitBreakerCyberPolicyInterruptsAfterOneDenial(t *testing.T) {
	breaker := NewCircuitBreaker()
	action := breaker.RecordDenialWithPolicy("turn-cyber", CircuitBreakerPolicyCyber)
	if !action.InterruptTurn || action.ConsecutiveDenials != MaxConsecutiveCyberDenialsPerTurn {
		t.Fatalf("first cyber denial should interrupt: %+v", action)
	}
	// A subsequent denial in the same turn stays interrupted (no duplicate).
	again := breaker.RecordDenialWithPolicy("turn-cyber", CircuitBreakerPolicyCyber)
	if again.InterruptTurn || again.ConsecutiveDenials != 2 {
		t.Fatalf("interrupt should trigger once: %+v", again)
	}

	// Standard policy still uses its own thresholds on a separate turn.
	breaker2 := NewCircuitBreaker()
	if action := breaker2.RecordDenialWithPolicy("turn-standard", CircuitBreakerPolicyStandard); action.InterruptTurn {
		t.Fatalf("standard first denial should continue: %+v", action)
	}
}

func TestReviewStore(t *testing.T) {
	store := NewReviewStore()
	now := fixedGuardianTime()
	store.SetClock(func() time.Time { return now })
	started, err := store.Start("turn-a", "item-a", Action{Type: "mcp_tool_call", Server: "mcp", ToolName: "search"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if started.ID != "guardian-review-1" || started.Status != StatusInProgress {
		t.Fatalf("started = %+v", started)
	}
	now = now.Add(time.Second)
	completed, err := store.Complete(started.ID, Assessment{
		RiskLevel:         RiskLow,
		UserAuthorization: AuthorizationHigh,
		Outcome:           OutcomeAllow,
		Rationale:         "authorized",
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completed.Status != StatusApproved || completed.CompletedAtMS == nil {
		t.Fatalf("completed = %+v", completed)
	}
	got, ok := store.Get(started.ID)
	if !ok || got.Status != StatusApproved {
		t.Fatalf("get = %+v ok=%v", got, ok)
	}
}

func TestReviewStoreTimeoutAndAbort(t *testing.T) {
	store := NewReviewStore()
	store.SetClock(fixedGuardianTime)
	timed, err := store.Start("turn-a", "item-a", Action{Type: "mcp_tool_call", Server: "mcp", ToolName: "search"})
	if err != nil {
		t.Fatal(err)
	}
	timed, err = store.Timeout(timed.ID)
	if err != nil || timed.Status != StatusTimedOut || timed.Rationale != GuardianTimeoutRationale() {
		t.Fatalf("timed=%#v err=%v", timed, err)
	}
	aborted, err := store.Start("turn-a", "item-b", Action{Type: "mcp_tool_call", Server: "mcp", ToolName: "write"})
	if err != nil {
		t.Fatal(err)
	}
	aborted, err = store.Abort(aborted.ID, "stopped")
	if err != nil || aborted.Status != StatusAborted || aborted.Rationale != "stopped" {
		t.Fatalf("aborted=%#v err=%v", aborted, err)
	}
}

func TestNotifications(t *testing.T) {
	event, err := NewInProgressEvent("review-a", "turn-a", "item-a", Action{Type: "apply_patch", CWD: "/repo", Files: []string{"/repo/a.txt"}}, fixedGuardianTime())
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	notification := NotificationFromEvent("thread-a", event)
	if notification.Method != NotificationReviewStarted {
		t.Fatalf("started notification = %+v", notification)
	}
	completed, err := event.Complete(Assessment{RiskLevel: RiskMedium, UserAuthorization: AuthorizationMedium, Outcome: OutcomeAllow, Rationale: "ok"}, fixedGuardianTime())
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	notification = NotificationFromEvent("thread-a", completed)
	if notification.Method != NotificationReviewCompleted {
		t.Fatalf("completed notification = %+v", notification)
	}
}

func TestParseAssessmentAndPrompt(t *testing.T) {
	assessment, err := ParseAssessment([]byte(`{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`))
	if err != nil {
		t.Fatalf("parse assessment: %v", err)
	}
	if assessment.Outcome != OutcomeAllow {
		t.Fatalf("assessment = %+v", assessment)
	}
	prompt, err := BuildPrompt(Action{Type: "command", Command: "ls", CWD: "/repo"}, []string{"user: list files"})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if !strings.Contains(prompt, "Planned action JSON:") || !strings.Contains(prompt, "user: list files") {
		t.Fatalf("prompt = %s", prompt)
	}
}

func TestBuildPromptSerializesNetworkActionLikeRust(t *testing.T) {
	prompt, err := BuildPrompt(Action{
		Type:     "network_access",
		Host:     "example.test",
		Protocol: "http",
		Port:     80,
		Target:   "http://example.test:80",
		Extra: map[string]any{"trigger": map[string]any{
			"callId":             "call-1",
			"command":            []string{"/bin/sh", "-c", "curl example.test"},
			"cwd":                "/repo",
			"sandboxPermissions": "use_default",
			"toolName":           "exec_command",
			"tty":                false,
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Rust's network framing: the request markers and the network-specific
	// scope, then the JSON under its own label (guardian-context action.rs).
	const prefix = ">>> APPROVAL REQUEST START\n" +
		"Below is a proposed network access request under review.\n" +
		"The network access was triggered by the action in the `trigger` entry. When assessing this request, focus primarily on whether the triggering command is authorised by the user and whether it is within the rules. The user does not need to have explicitly authorised this exact network connection, as long as the network access is a reasonable consequence of the triggering command.\n\n" +
		"Assess the exact network access below. Use read-only tool checks when local state matters.\n" +
		"Network access JSON:\n"
	if !strings.HasPrefix(prompt, prefix) {
		t.Fatalf("prompt = %q", prompt)
	}
	const suffix = "\n>>> APPROVAL REQUEST END"
	if !strings.HasSuffix(prompt, suffix) {
		t.Fatalf("prompt = %q", prompt)
	}
	var action map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(prompt, prefix), suffix)), &action); err != nil {
		t.Fatalf("decode action: %v", err)
	}
	if len(action) != 6 || action["tool"] != "network_access" || action["host"] != "example.test" || action["protocol"] != "http" || action["target"] != "http://example.test:80" || action["port"] != float64(80) {
		t.Fatalf("action = %#v", action)
	}
	trigger, ok := action["trigger"].(map[string]any)
	if !ok || len(trigger) != 6 || trigger["callId"] != "call-1" || trigger["cwd"] != "/repo" || trigger["sandboxPermissions"] != "use_default" || trigger["toolName"] != "exec_command" || trigger["tty"] != false {
		t.Fatalf("trigger = %#v", action["trigger"])
	}
	command, ok := trigger["command"].([]any)
	if !ok || len(command) != 3 || command[0] != "/bin/sh" || command[1] != "-c" || command[2] != "curl example.test" {
		t.Fatalf("trigger command = %#v", trigger["command"])
	}
	if _, ok := action["type"]; ok {
		t.Fatalf("network action leaked internal type: %#v", action)
	}
	if _, ok := action["extra"]; ok {
		t.Fatalf("network action leaked internal extra wrapper: %#v", action)
	}
}

// TestBuildPromptFramingMatchesRust mirrors Rust core/src/guardian/tests.rs:
// every reviewed action uses the planned-action framing, and the node-REPL
// rules are never inlined into it (they are a separate developer fragment).
func TestBuildPromptFramingMatchesRust(t *testing.T) {
	const commandFraming = "The Codex agent has requested the following action:\n" +
		">>> APPROVAL REQUEST START\n" +
		"Assess the exact planned action below. Use read-only tool checks when local state matters.\n" +
		"Planned action JSON:\n"
	for _, action := range []Action{
		{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js"},
		{Type: "mcp_tool_call", Server: "node_repl", ToolName: "inspect"},
		{Type: "mcp_tool_call", Server: "another_server", ToolName: "js"},
		{Type: "command", Command: "ls", CWD: "/repo"},
	} {
		prompt, err := BuildPromptWithOptions(action, nil, BuildPromptOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(prompt, commandFraming) {
			t.Fatalf("prompt = %q", prompt)
		}
		if !strings.HasSuffix(prompt, ">>> APPROVAL REQUEST END") {
			t.Fatalf("prompt = %q", prompt)
		}
		if strings.Contains(prompt, "Node REPL action JSON:") || strings.Contains(prompt, "Distinguish preparation") {
			t.Fatalf("prompt inlined node-REPL guidance: %q", prompt)
		}
	}

	// A retry reason lands between the markers and the scope line, like Rust.
	reasoned, err := BuildPromptWithOptions(Action{Type: "command", Command: "ls", CWD: "/repo", Reason: "retry after scope change"}, nil, BuildPromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reasoned, ">>> APPROVAL REQUEST START\nRetry reason:\nretry after scope change\n\nAssess the exact planned action below.") {
		t.Fatalf("reasoned prompt = %q", reasoned)
	}
}

// TestBuildPromptAppendsGuardianToolDescriptionsLikeRust mirrors Rust's
// planned-action composition: the invoked MCP tool's own descriptions follow
// the action items as their own bounded untrusted fragment, while the action
// JSON itself keeps only the projected fields.
func TestBuildPromptAppendsGuardianToolDescriptionsLikeRust(t *testing.T) {
	action := Action{
		Type:            "mcp_tool_call",
		Server:          "apps",
		ToolName:        "calendar.create",
		Arguments:       map[string]any{"title": "Lunch"},
		ToolDescription: "Create a calendar event.",
	}
	prompt, err := BuildPromptWithOptions(action, nil, BuildPromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	actionEnd := strings.Index(prompt, ">>> APPROVAL REQUEST END\n")
	descriptions := strings.Index(prompt, "<guardian_tool_descriptions>")
	if actionEnd < 0 || descriptions != actionEnd+len(">>> APPROVAL REQUEST END\n\n") {
		t.Fatalf("descriptions block placement = %q", prompt)
	}
	if !strings.Contains(prompt, "Tool description:\nCreate a calendar event.\nConnector description:\n") {
		t.Fatalf("descriptions block = %q", prompt)
	}
	if strings.Contains(prompt, "Create a calendar event."+`"`) || strings.Contains(prompt, `"tool_description"`) {
		t.Fatalf("action JSON leaked the description: %q", prompt)
	}

	// Without descriptions nothing is appended.
	plain, err := BuildPromptWithOptions(Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar.create"}, nil, BuildPromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "guardian_tool_descriptions") {
		t.Fatalf("prompt without descriptions = %q", plain)
	}
}

// TestGuardianActionJSONMatchesRust pins Rust's per-variant
// guardian_approval_request_to_json projection: snake_case keys, `tool` naming
// the variant, absent optional fields omitted, and alphabetical key order (Rust
// sorts every object before pretty printing).
func TestGuardianActionJSONMatchesRust(t *testing.T) {
	tty := true
	readOnly := true
	sessionID := 7
	tests := []struct {
		name   string
		action Action
		want   string
	}{
		{
			name: "exec_command",
			action: Action{
				Type:               "command",
				Source:             CommandSourceUnifiedExec,
				CommandArgv:        []string{"ls", "-la"},
				CWD:                "/repo",
				SandboxPermissions: "require_escalated",
				Justification:      "list the workspace",
				TTY:                &tty,
				Reason:             "retry after scope change",
			},
			want: "{\n  \"command\": [\n    \"ls\",\n    \"-la\"\n  ],\n  \"cwd\": \"/repo\",\n  \"justification\": \"list the workspace\",\n  \"sandbox_permissions\": \"require_escalated\",\n  \"tool\": \"exec_command\",\n  \"tty\": true\n}",
		},
		{
			name:   "exec_command without argv",
			action: Action{Type: "command", Command: "ls -la", CWD: "/repo"},
			want:   "{\n  \"command\": [\n    \"ls -la\"\n  ],\n  \"cwd\": \"/repo\",\n  \"tool\": \"exec_command\"\n}",
		},
		{
			name: "mcp_tool_call",
			action: Action{
				Type:          "mcp_tool_call",
				Server:        "apps",
				ToolName:      "calendar.create",
				Arguments:     map[string]any{"title": "Lunch"},
				ConnectorID:   "connector_calendar",
				ConnectorName: "Calendar",
				ToolTitle:     "Create event",
				Annotations:   &ActionAnnotations{ReadOnlyHint: &readOnly},
			},
			want: "{\n  \"annotations\": {\n    \"read_only_hint\": true\n  },\n  \"arguments\": {\n    \"title\": \"Lunch\"\n  },\n  \"connector_id\": \"connector_calendar\",\n  \"connector_name\": \"Calendar\",\n  \"server\": \"apps\",\n  \"tool\": \"mcp_tool_call\",\n  \"tool_name\": \"calendar.create\",\n  \"tool_title\": \"Create event\"\n}",
		},
		{
			name:   "apply_patch",
			action: Action{Type: "apply_patch", CWD: "/repo", Files: []string{"a.txt"}, Patch: "*** Begin Patch"},
			want:   "{\n  \"cwd\": \"/repo\",\n  \"files\": [\n    \"a.txt\"\n  ],\n  \"patch\": \"*** Begin Patch\",\n  \"tool\": \"apply_patch\"\n}",
		},
		{
			name: "request_permissions",
			action: Action{
				Type: "request_permissions", TurnID: "turn-1", Reason: "needs network",
				Permissions: map[string]any{
					"network":    map[string]any{"enabled": true},
					"fileSystem": map[string]any{"write": []string{"/repo/out"}},
				},
			},
			want: "{\n  \"permissions\": {\n    \"file_system\": {\n      \"write\": [\n        \"/repo/out\"\n      ]\n    },\n    \"network\": {\n      \"enabled\": true\n    }\n  },\n  \"reason\": \"needs network\",\n  \"tool\": \"request_permissions\",\n  \"turn_id\": \"turn-1\"\n}",
		},
		{
			name:   "execve",
			action: Action{Type: "execve", Source: CommandSourceShell, Program: "/bin/rm", Argv: []string{"-rf", "build"}, CWD: "/repo"},
			want:   "{\n  \"argv\": [\n    \"-rf\",\n    \"build\"\n  ],\n  \"cwd\": \"/repo\",\n  \"program\": \"/bin/rm\",\n  \"tool\": \"shell\"\n}",
		},
		{
			name: "write_stdin",
			action: Action{
				Type: "write_stdin", EnvironmentID: "local", SessionID: &sessionID, Chars: "ls\n",
				CWD: "/repo", SandboxPermissions: "use_default",
			},
			want: "{\n  \"chars\": \"ls\\n\",\n  \"cwd\": \"/repo\",\n  \"environment_id\": \"local\",\n  \"sandbox_permissions\": \"use_default\",\n  \"session_id\": 7,\n  \"tool\": \"write_stdin\",\n  \"tty\": false\n}",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := marshalGuardianPromptAction(testCase.action)
			if err != nil {
				t.Fatalf("marshalGuardianPromptAction() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("action JSON = %s\nwant %s", got, testCase.want)
			}
		})
	}
}

// TestRenderPlannedActionFramingsMatchRust pins the remaining Rust
// PlannedAction::render branches: the delta presentation, the terminal-input
// scope, and the network scope without a trigger.
func TestRenderPlannedActionFramingsMatchRust(t *testing.T) {
	json := "{\n  \"tool\": \"apply_patch\"\n}"
	command := renderPlannedAction(json, plannedActionCommand, false, "", ActionPresentationSyncFull)
	if got := strings.Join(command, ""); got != "The Codex agent has requested the following action:\n>>> APPROVAL REQUEST START\nAssess the exact planned action below. Use read-only tool checks when local state matters.\nPlanned action JSON:\n"+json+"\n>>> APPROVAL REQUEST END\n" {
		t.Fatalf("sync full command framing = %q", got)
	}
	delta := renderPlannedAction(json, plannedActionCommand, false, "", ActionPresentationSyncDelta)
	if got := strings.Join(delta, ""); got != "The Codex agent has requested the following next action:\n>>> APPROVAL REQUEST START\nAssess the exact planned action below. Use read-only tool checks when local state matters.\nPlanned action JSON:\n"+json+"\n>>> APPROVAL REQUEST END\n" {
		t.Fatalf("sync delta command framing = %q", got)
	}
	async := renderPlannedAction(json, plannedActionCommand, false, "retry", ActionPresentationAsync)
	if got := strings.Join(async, ""); got != "The Codex agent has requested the following action:\n>>> APPROVAL REQUEST START\nPlanned action JSON:\n"+json+"\n>>> APPROVAL REQUEST END\n" {
		t.Fatalf("async framing = %q", got)
	}
	terminal := renderPlannedAction(json, plannedActionTerminalInput, false, "", ActionPresentationSyncFull)
	if got := strings.Join(terminal, ""); got != "The Codex agent has requested the following action:\n>>> APPROVAL REQUEST START\nAssess input to the existing terminal, not a fresh command. The `cwd` field is its launch directory; the terminal's current directory and state may have changed. Use the retained transcript and read-only checks when that state matters.\nPlanned action JSON:\n"+json+"\n>>> APPROVAL REQUEST END\n" {
		t.Fatalf("terminal framing = %q", got)
	}
	network := renderPlannedAction(json, plannedActionNetwork, false, "", ActionPresentationSyncFull)
	if got := strings.Join(network, ""); got != ">>> APPROVAL REQUEST START\nBelow is a proposed network access request under review.\nNo trigger action was captured for this network access request. When performing the assessment, use the retained transcript and network access JSON to evaluate user authorization and risk.\n\nAssess the exact network access below. Use read-only tool checks when local state matters.\nNetwork access JSON:\n"+json+"\n>>> APPROVAL REQUEST END\n" {
		t.Fatalf("network framing without trigger = %q", got)
	}
}

func TestBuildPromptIncludesNodeReplReviewEvidence(t *testing.T) {
	evidence := &context.NodeReplReviewEvidence{}
	evidence.Record("js", "cell-1", "call-1", []string{"evidence-text"})
	fragment := evidence.SnapshotSince(0)
	if fragment == nil {
		t.Fatal("expected evidence fragment")
	}

	action := Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js"}
	prompt, err := BuildPromptWithOptions(action, nil, BuildPromptOptions{NodeReplEvidence: fragment})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "<node_repl_review_evidence>") ||
		!strings.Contains(prompt, "evidence-text") ||
		!strings.Contains(prompt, "untrusted evidence") {
		t.Fatalf("prompt missing node_repl evidence: %s", prompt)
	}

	without, err := BuildPromptWithOptions(action, nil, BuildPromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without, "node_repl_review_evidence") {
		t.Fatalf("prompt should not include evidence when omitted: %s", without)
	}
}

func fixedGuardianTime() time.Time {
	return time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC)
}
