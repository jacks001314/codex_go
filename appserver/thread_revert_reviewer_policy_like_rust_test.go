package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
)

// Rust #63390: thread/revert resolves the saved reviewer request against the
// approvals policy the replacement runtime starts with, instead of carrying the
// previously effective reviewer forward.
func TestThreadRevertResolvesApprovalsReviewerAgainstCurrentPolicyLikeRust(t *testing.T) {
	router, record, _ := newPersistedResumeSettingsFixture(t)
	record.Metadata.HistoryMode = string(ThreadHistoryPaginated)
	effective, requested := "auto_review", "user"
	setThreadRecordConfigOverride(record, "approvals_reviewer", effective)
	setThreadRecordConfigOverride(record, "requested_approvals_reviewer", requested)
	if err := router.store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	extras := NewThreadExtraService()
	reviewer, request := effective, requested
	if _, err := extras.UpdateSettings(&SettingsUpdateParams{
		ThreadID:                   string(record.ID),
		ApprovalsReviewer:          &reviewer,
		RequestedApprovalsReviewer: &request,
	}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}

	home := t.TempDir()
	writeRequirements := func(allowed string) {
		body := "allowed_approvals_reviewers = [\"" + allowed + "\"]\n"
		if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	writeRequirements("auto_review")

	runtimeRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: router,
		Config:       config.NewConfigService(home),
		ThreadExtras: extras,
	})
	captured := extras.Settings(string(record.ID))
	if captured.ApprovalsReviewer != effective || captured.RequestedApprovalsReviewer != requested {
		t.Fatalf("captured settings = %q/%q, want %q/%q", captured.ApprovalsReviewer, captured.RequestedApprovalsReviewer, effective, requested)
	}

	// The policy changes while the revert is in flight.
	writeRequirements("user")
	runtimeRouter.applyThreadRevertApprovalsReviewer(string(record.ID))

	replacement := extras.Settings(string(record.ID))
	if replacement.ApprovalsReviewer != "user" || replacement.RequestedApprovalsReviewer != "user" {
		t.Fatalf("replacement reviewer = %q/%q, want user/user", replacement.ApprovalsReviewer, replacement.RequestedApprovalsReviewer)
	}
	updated, err := router.store.Read(record.ID, true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	overrides := threadRecordConfigOverrides(updated)
	if got, _ := threadConfigStringValue(overrides, "approvals_reviewer"); got != "user" {
		t.Fatalf("record approvals_reviewer = %q, want user", got)
	}
	if got, _ := threadConfigStringValue(overrides, "requested_approvals_reviewer"); got != "user" {
		t.Fatalf("record requested_approvals_reviewer = %q, want user", got)
	}
}
