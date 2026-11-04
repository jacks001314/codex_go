package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// A daemon-backed session creates its thread with the first turn. A built-in
// permission choice made before that turn is staged locally and sent by the
// first turn/start instead of failing on the missing thread id.
func TestModelBuiltInPermissionsBeforeChatStageLocallyLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		ApprovalPolicy: "on-request",
		Sandbox:        chatwidget.WorkspaceProfile,
	})
	updateCalls := 0
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnUpdateThreadPermissions: func(string, PermissionSelection) error {
			updateCalls++
			return nil
		},
	})

	typeText(t, model, "/permissions")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyEnter))

	if updateCalls != 0 {
		t.Fatalf("pre-thread selection called the server %d times", updateCalls)
	}
	// The session keeps the picker profile id; the daemon request builders map
	// it onto a sandbox mode, and the first turn/start carries it.
	if state.Sandbox != chatwidget.ReadOnlyProfile || state.ApprovalPolicy != string(chatwidget.ApprovalOnRequest) {
		t.Fatalf("staged permissions sandbox=%q approval=%q", state.Sandbox, state.ApprovalPolicy)
	}
	if state.ActivePermissionProfile != "" {
		t.Fatalf("built-in selection set a named profile: %q", state.ActivePermissionProfile)
	}
	if state.ApprovalsReviewer != string(chatwidget.ApprovalsReviewerUser) {
		t.Fatalf("staged reviewer = %q, want user", state.ApprovalsReviewer)
	}
	if !strings.Contains(model.View(), "Permissions updated to Read Only") {
		t.Fatalf("permission confirmation missing:\n%s", model.View())
	}
}

func TestModelNamedPermissionsBeforeChatNeedThreadLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnUpdateThreadPermissions: func(string, PermissionSelection) error {
			return nil
		},
	})
	model.permissionProfilesDiscovered = true
	model.permissionProfilesExplicit = true
	model.permissionProfiles = []chatwidget.CustomPermissionProfile{{
		ID:          "server-only",
		Description: "Server-owned profile",
		Allowed:     true,
	}}

	model.applyPermissionSelection(chatwidget.PermissionMenuItem{
		ID:        "server-only",
		Name:      "Server Only",
		ProfileID: "server-only",
	})

	if !strings.Contains(model.notice, "Wait for the task to connect before selecting permissions.") {
		t.Fatalf("notice = %q", model.notice)
	}
	if state.Sandbox == "server-only" {
		t.Fatal("named profile was staged without server resolution")
	}
}
