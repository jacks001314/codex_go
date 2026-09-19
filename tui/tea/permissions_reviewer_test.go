package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// Mirrors Rust #46036: the approvals reviewer chosen in the permissions menu is
// persisted through the app server's config/batchWrite so it survives the
// session.
func TestModelPermissionSelectionPersistsApprovalsReviewerLikeRust(t *testing.T) {
	var writes [][]SettingsEdit
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 40,
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			return SettingsWriteResult{}, nil
		},
	})
	reviewer := chatwidget.ApprovalsReviewerAutoReview
	cmd := model.applyPermissionSelection(chatwidget.PermissionMenuItem{
		ID:       "workspace:auto_review",
		Name:     "Approve for me",
		Reviewer: &reviewer,
	})
	runTeaCmd(t, model, cmd)

	if len(writes) != 1 || len(writes[0]) != 1 {
		t.Fatalf("approvals reviewer writes = %#v", writes)
	}
	if writes[0][0].KeyPath != "approvals_reviewer" || writes[0][0].Value != "auto_review" {
		t.Fatalf("approvals reviewer edit = %#v", writes[0][0])
	}
	if model.approvalsReviewer != reviewer {
		t.Fatalf("live reviewer = %q, want %q", model.approvalsReviewer, reviewer)
	}
}

// Rust's approvals_reviewer_error_retains_config_cause: a failed save keeps the
// backend's actionable cause (config file location and parse error) instead of
// hiding it behind a bare save failure.
func TestModelApprovalsReviewerSaveFailureKeepsConfigCauseLikeRust(t *testing.T) {
	cause := errors.New("config/batchWrite failed: failed to load configuration: " +
		"C:\\codex\\config.toml:1:24: unclosed array, expected `]` (code -32603)")
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 40,
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			return SettingsWriteResult{}, cause
		},
	})
	reviewer := chatwidget.ApprovalsReviewerUser
	cmd := model.applyPermissionSelection(chatwidget.PermissionMenuItem{
		ID:       "workspace:user",
		Name:     "Ask for approval",
		Reviewer: &reviewer,
	})
	runTeaCmd(t, model, cmd)

	text := modelMessageText(model)
	if !strings.Contains(text, "Failed to save approvals reviewer: "+cause.Error()) {
		t.Fatalf("approvals reviewer failure lost the config cause:\n%s", text)
	}
}
