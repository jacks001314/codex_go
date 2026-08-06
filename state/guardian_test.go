package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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
	if err != nil || timed.Status != StatusTimedOut || timed.Rationale != GuardianTimeoutMessage() {
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
	if !strings.Contains(prompt, "Action:") || !strings.Contains(prompt, "user: list files") {
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
	const prefix = "Review the planned action and decide whether to allow it.\n\nAction:\n"
	if !strings.HasPrefix(prompt, prefix) {
		t.Fatalf("prompt = %q", prompt)
	}
	var action map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(prompt, prefix)), &action); err != nil {
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

func fixedGuardianTime() time.Time {
	return time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC)
}
