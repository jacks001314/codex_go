package appserver

import (
	"testing"

	"codex_go/session"
)

// Rust #59272 restores the requested reviewer rather than a value a model
// requirement imposed, and keeps it separate for later checkpoints.
func TestResumeRestoresRequestedApprovalsReviewerLikeRust(t *testing.T) {
	record := &session.Record{ID: "thread-a", SessionID: "thread-a"}
	restored := persistedResumeSettings{
		ApprovalPolicy:    "on-request",
		ApprovalsReviewer: "user",
	}.restore(record, threadSettingsRequestOverrides{})
	if restored.ApprovalsReviewer == nil || *restored.ApprovalsReviewer != "user" {
		t.Fatalf("restored reviewer = %v", restored.ApprovalsReviewer)
	}
	overrides := threadRecordConfigOverrides(record)
	if overrides["approvals_reviewer"] != "user" || overrides["requested_approvals_reviewer"] != "user" {
		t.Fatalf("record overrides = %#v", overrides)
	}

	// An explicit request value still wins over the persisted selection.
	explicit := &session.Record{ID: "thread-b", SessionID: "thread-b"}
	restored = persistedResumeSettings{ApprovalsReviewer: "user"}.restore(explicit, threadSettingsRequestOverrides{ApprovalsReviewer: true})
	if restored.ApprovalsReviewer != nil {
		t.Fatalf("explicit reviewer override was replaced: %v", *restored.ApprovalsReviewer)
	}
}

// The persisted snapshot keeps an explicit requested reviewer and leaves the
// field unset when the value is only inherited.
func TestRequestedApprovalsReviewerSnapshotLikeRust(t *testing.T) {
	settings := &Settings{ApprovalsReviewer: "auto_review", RequestedApprovalsReviewer: "user"}
	if got := requestedApprovalsReviewerSnapshot(settings, nil); got == nil || *got != "user" {
		t.Fatalf("snapshot reviewer = %v", got)
	}
	if got := requestedApprovalsReviewerSnapshot(&Settings{ApprovalsReviewer: "auto_review"}, nil); got != nil {
		t.Fatalf("inherited reviewer must not persist as requested: %v", got)
	}
	record := &session.Record{ID: "thread-c", SessionID: "thread-c"}
	setThreadRecordConfigOverride(record, "requested_approvals_reviewer", "user")
	if got := requestedApprovalsReviewerSnapshot(nil, record); got == nil || *got != "user" {
		t.Fatalf("record reviewer = %v", got)
	}
}
