package execserver

import (
	"testing"

	"codex_go/sandbox"
)

func TestFSOperationSandboxSelectionSeparatesReadsAndWritesLikeRust(t *testing.T) {
	fullWriteDenyRead := &sandbox.PermissionProfile{
		SandboxPolicy: &sandbox.SandboxPolicy{Kind: sandbox.SandboxDangerFullAccess},
		DeniedReadEntries: []sandbox.FileSystemSandboxEntry{{
			Path:   sandbox.FileSystemPath{Type: "path", Path: "/secret"},
			Access: sandbox.FileSystemAccessDeny,
		}},
	}
	if !fsOperationRequiresSandbox(MethodFSReadFile, fullWriteDenyRead) ||
		!fsOperationRequiresSandbox(MethodFSWalk, fullWriteDenyRead) {
		t.Fatal("full-disk-write policy with deny-read entries must still sandbox reads")
	}
	if fsOperationRequiresSandbox(MethodFSWriteFile, fullWriteDenyRead) ||
		fsOperationRequiresSandbox(MethodFSCopy, fullWriteDenyRead) {
		t.Fatal("full-disk-write policy must not sandbox writes")
	}

	restrictedWrites := &sandbox.PermissionProfile{
		SandboxPolicy: &sandbox.SandboxPolicy{Kind: sandbox.SandboxWorkspaceWrite},
	}
	if fsOperationRequiresSandbox(MethodFSReadFile, restrictedWrites) {
		t.Fatal("full-disk-read policy must permit direct reads when writes are restricted")
	}
	if !fsOperationRequiresSandbox(MethodFSWriteFile, restrictedWrites) ||
		!fsOperationRequiresSandbox(MethodFSRemove, restrictedWrites) {
		t.Fatal("restricted writes must use the sandbox")
	}

	disabled := &sandbox.PermissionProfile{Disabled: true}
	if fsOperationRequiresSandbox(MethodFSReadFile, disabled) || fsOperationRequiresSandbox(MethodFSWriteFile, disabled) {
		t.Fatal("disabled profile must not request sandboxing")
	}
}
