package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/state"
	"codex_go/telemetry"
)

// TestCommandExecutionApprovalKindDefaultsToCommandLikeRust mirrors Rust
// #40528: missing kinds keep command semantics, while stdin approvals carry an
// explicit writeStdin kind.
func TestCommandExecutionApprovalKindDefaultsToCommandLikeRust(t *testing.T) {
	params := &CommandExecutionRequestApprovalParams{ThreadID: "thread-1", TurnID: "turn-1", ItemID: "exec-call"}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"kind":"command"`) {
		t.Fatalf("default kind missing: %s", data)
	}
	var decoded CommandExecutionRequestApprovalParams
	if err := json.Unmarshal([]byte(`{"kind":"writeStdin","threadId":"thread-1"}`), &decoded); err != nil {
		t.Fatalf("Unmarshal(writeStdin) error = %v", err)
	}
	if decoded.Kind != ExecApprovalKindWriteStdin {
		t.Fatalf("decoded kind = %q", decoded.Kind)
	}
	var legacy CommandExecutionRequestApprovalParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1"}`), &legacy); err != nil {
		t.Fatalf("Unmarshal(legacy) error = %v", err)
	}
	if legacy.Kind.orDefault() != ExecApprovalKindCommand {
		t.Fatalf("legacy kind = %q", legacy.Kind)
	}
}

// TestGuardianWriteStdinActionNotificationLikeRust pins the app-server action
// payload: the parent command item stays the target, while approvalId and
// processId identify the child terminal write.
func TestGuardianWriteStdinActionNotificationLikeRust(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{})
	router.SetNotificationSink(sink)
	event := &state.Event{
		ID:           "review-write",
		TargetItemID: "exec-call",
		TurnID:       "turn-1",
		StartedAtMS:  1,
		Status:       state.StatusInProgress,
		Action: state.Action{
			Type:       "write_stdin",
			CWD:        "/repo",
			Chars:      "ls\n",
			ApprovalID: "write-call",
			ProcessID:  "7",
		},
	}
	router.notifyGuardianReviewEvent("thread-1", event)
	var payload *ItemGuardianApprovalReviewStartedNotification
	for _, notification := range sink.List() {
		if notification.Method != NotificationItemGuardianApprovalReviewStarted {
			continue
		}
		payload, _ = notification.Params.(*ItemGuardianApprovalReviewStartedNotification)
	}
	if payload == nil {
		t.Fatal("guardian review started notification missing")
	}
	if payload.TargetItemID == nil || *payload.TargetItemID != "exec-call" {
		t.Fatalf("target item = %#v", payload.TargetItemID)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal(notification) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`"type":"writeStdin"`,
		`"approvalId":"write-call"`,
		`"processId":"7"`,
		`"stdin":"ls\n"`,
		`"cwd":"/repo"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("notification missing %s: %s", want, text)
		}
	}
}

func TestGuardianWriteStdinAnalyticsClassificationLikeRust(t *testing.T) {
	action := &GuardianApprovalReviewAction{Type: "write_stdin"}
	if got := guardianReviewActionType(action); got != "writeStdin" {
		t.Fatalf("action type = %q", got)
	}
	kind, subject, trigger := guardianReviewSubjectMetadata(action)
	if kind != telemetry.ReviewSubjectKindWriteStdin || subject != "write_stdin" || trigger != telemetry.ReviewTriggerInitial {
		t.Fatalf("subject metadata = %q, %q, %q", kind, subject, trigger)
	}
	if guardianReviewRequestedAdditionalPermissions(action) || guardianReviewRequestedNetworkAccess(action) {
		t.Fatal("stdin action must not request permissions/network")
	}
}
