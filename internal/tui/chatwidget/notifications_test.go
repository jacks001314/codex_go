package chatwidget

import (
	"strings"
	"testing"
)

func TestNotificationDisplay(t *testing.T) {
	if got := AgentTurnCompleteNotification(" done\n\nwell ").Display(); got != "done well" {
		t.Fatalf("agent display = %q", got)
	}
	if got := AgentTurnCompleteNotification("   ").Display(); got != "Agent turn complete" {
		t.Fatalf("empty agent display = %q", got)
	}
	longCommand := "echo " + strings.Repeat("x", 40)
	if got := ExecApprovalRequestedNotification(longCommand).Display(); got != "Approval requested: echo xxxxxxxxxxxxxxxxxxxxxx..." {
		t.Fatalf("exec display = %q", got)
	}
	if got := ExecApprovalRequestedNotification("  go test  ").Display(); got != "Approval requested:   go test  " {
		t.Fatalf("exec display should preserve command spacing, got %q", got)
	}
	if got := EditApprovalRequestedNotification(`D:\repo`, []string{`D:\repo\src\main.go`}).Display(); got != "Codex wants to edit src/main.go" {
		t.Fatalf("edit one display = %q", got)
	}
	if got := EditApprovalRequestedNotification(`D:\repo`, []string{`D:\repo\a`, `D:\repo\b`}).Display(); got != "Codex wants to edit 2 files" {
		t.Fatalf("edit many display = %q", got)
	}
	if got := ElicitationRequestedNotification("docs").Display(); got != "Approval requested by docs" {
		t.Fatalf("elicitation display = %q", got)
	}
	if got := ElicitationRequestedNotification(" docs ").Display(); got != "Approval requested by  docs " {
		t.Fatalf("elicitation display should preserve server spacing, got %q", got)
	}
	if got := PlanModePromptNotification("Review plan").Display(); got != "Plan mode prompt: Review plan" {
		t.Fatalf("plan mode display = %q", got)
	}
	if got := PlanModePromptNotification(" Review plan ").Display(); got != "Plan mode prompt:  Review plan " {
		t.Fatalf("plan mode display should preserve title spacing, got %q", got)
	}
}

func TestNotificationAllowedFor(t *testing.T) {
	agent := AgentTurnCompleteNotification("done")
	approval := ExecApprovalRequestedNotification("go test")
	if !agent.AllowedFor(NotificationsSetting{Enabled: true}) {
		t.Fatal("enabled notifications should allow agent")
	}
	if agent.AllowedFor(NotificationsSetting{}) {
		t.Fatal("disabled notifications should reject agent")
	}
	custom := NotificationsSetting{Custom: []string{"approval-requested"}}
	if agent.AllowedFor(custom) {
		t.Fatal("custom approval should reject agent")
	}
	if !approval.AllowedFor(custom) {
		t.Fatal("custom approval should allow approval")
	}
	if approval.AllowedFor(NotificationsSetting{CustomSet: true}) {
		t.Fatal("empty custom notifications should reject approval")
	}
	if approval.AllowedFor(NotificationsSetting{Custom: []string{" approval-requested "}, CustomSet: true}) {
		t.Fatal("custom notifications should use exact Rust type matching")
	}
}

func TestNotificationStateCoalescesByPriority(t *testing.T) {
	var state NotificationState
	setting := NotificationsSetting{Enabled: true}
	if !state.Notify(ExecApprovalRequestedNotification("go test"), setting) {
		t.Fatal("approval notify = false")
	}
	if state.Notify(AgentTurnCompleteNotification("done"), setting) {
		t.Fatal("low priority agent should not replace approval")
	}
	got, ok := state.TakePendingDisplay()
	if !ok || got != "Approval requested: go test" {
		t.Fatalf("pending display = %q ok=%v", got, ok)
	}
	if got, ok := state.TakePendingDisplay(); ok || got != "" {
		t.Fatalf("second pending display = %q ok=%v", got, ok)
	}

	if !state.Notify(AgentTurnCompleteNotification("done"), setting) {
		t.Fatal("agent notify = false")
	}
	if !state.Notify(PlanModePromptNotification("Plan"), setting) {
		t.Fatal("plan prompt should replace agent")
	}
	got, ok = state.TakePendingDisplay()
	if !ok || got != "Plan mode prompt: Plan" {
		t.Fatalf("pending display after replacement = %q ok=%v", got, ok)
	}
}

func TestAgentTurnPreviewAndUserInputSummary(t *testing.T) {
	preview, ok := AgentTurnPreview("hello\n\nworld")
	if !ok || preview != "hello world" {
		t.Fatalf("preview = %q ok=%v", preview, ok)
	}
	if preview, ok := AgentTurnPreview(strings.Repeat("a", 220)); !ok || len([]rune(preview)) != AgentNotificationPreviewRunes {
		t.Fatalf("long preview len=%d ok=%v", len([]rune(preview)), ok)
	}
	if got, ok := UserInputRequestSummary(" Header ", "Question"); !ok || got != "Header" {
		t.Fatalf("summary header = %q ok=%v", got, ok)
	}
	if got, ok := UserInputRequestSummary("", "What should happen?"); !ok || got != "What should happen?" {
		t.Fatalf("summary question = %q ok=%v", got, ok)
	}
	if got := TruncateRunes("  abc  ", 20); got != "  abc  " {
		t.Fatalf("truncate should preserve spacing, got %q", got)
	}
	if got := TruncateRunes("abcdef", 3); got != "..." {
		t.Fatalf("truncate max 3 = %q", got)
	}
	if got := TruncateRunes("abcdef", 2); got != "ab" {
		t.Fatalf("truncate max 2 = %q", got)
	}
}
