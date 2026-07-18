package windowssandbox

import coresandbox "codex_go/sandbox"

func WorkspaceWritePermissionProfileForTest() coresandbox.PermissionProfile {
	return coresandbox.WorkspaceWritePermissionProfile()
}
