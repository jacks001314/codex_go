package windowssandbox

import coresandbox "codex_go/internal/sandbox"

func WorkspaceWritePermissionProfileForTest() coresandbox.PermissionProfile {
	return coresandbox.WorkspaceWritePermissionProfile()
}
