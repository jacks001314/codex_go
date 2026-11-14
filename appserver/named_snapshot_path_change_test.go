package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/sandbox"
	"codex_go/turn"
)

func TestNamedPermissionSnapshotInvalidatedByPathChange(t *testing.T) {
	for _, change := range []string{"cwd", "roots"} {
		t.Run(change, func(t *testing.T) {
			service := NewThreadExtraService()
			oldCWD, newCWD := t.TempDir(), t.TempDir()
			name := ":workspace"
			raw, err := sandbox.RuntimePermissionProfileJSON(sandbox.WorkspaceWritePermissionProfile())
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = service.UpdateSettingsWithCommit(&SettingsUpdateParams{ThreadID: "thread", CWD: &oldCWD, Permissions: &name, RuntimeWorkspaceRoots: []string{oldCWD}, normalizedPermissionProfile: json.RawMessage(raw)})
			if err != nil {
				t.Fatal(err)
			}
			update := &SettingsUpdateParams{ThreadID: "thread"}
			if change == "cwd" {
				update.CWD = &newCWD
			} else {
				update.RuntimeWorkspaceRoots = []string{newCWD}
			}
			_, settings, err := service.UpdateSettingsWithCommit(update)
			if err != nil {
				t.Fatal(err)
			}
			if settings.ActivePermissionProfile == nil || *settings.ActivePermissionProfile != name {
				t.Fatal("lost named identity")
			}
			next := &turn.TurnStartParams{ThreadID: "thread", CWD: settings.CWD}
			applyThreadSettingsEnvironmentConfigToTurnStartParams(next, settings)
			if next.InheritedPermissionProfileJSON != "" {
				t.Fatal("next turn reused a snapshot bound to old paths")
			}
		})
	}
}
