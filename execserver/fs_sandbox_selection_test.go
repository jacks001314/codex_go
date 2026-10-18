package execserver

import (
	"testing"

	"codex_go/sandbox"
	"codex_go/utils"
)

func TestFSOperationSandboxSelectionSeparatesReadsAndWritesLikeRust(t *testing.T) {
	fullWriteDenyRead := &sandbox.PermissionProfile{
		SandboxPolicy: &sandbox.SandboxPolicy{Kind: sandbox.SandboxDangerFullAccess},
		DeniedReadEntries: []sandbox.FileSystemSandboxEntry{{
			Path:   sandbox.FileSystemPath{Type: "path", Path: "/secret"},
			Access: sandbox.FileSystemAccessDeny,
		}},
	}
	native := utils.NativePathConvention()
	if !fsOperationRequiresSandbox(MethodFSReadFile, fullWriteDenyRead, native) ||
		!fsOperationRequiresSandbox(MethodFSWalk, fullWriteDenyRead, native) {
		t.Fatal("full-disk-write policy with deny-read entries must still sandbox reads")
	}
	if fsOperationRequiresSandbox(MethodFSWriteFile, fullWriteDenyRead, native) ||
		fsOperationRequiresSandbox(MethodFSCopy, fullWriteDenyRead, native) {
		t.Fatal("full-disk-write policy must not sandbox writes")
	}

	restrictedWrites := &sandbox.PermissionProfile{
		SandboxPolicy: &sandbox.SandboxPolicy{Kind: sandbox.SandboxWorkspaceWrite},
	}
	if fsOperationRequiresSandbox(MethodFSReadFile, restrictedWrites, native) {
		t.Fatal("full-disk-read policy must permit direct reads when writes are restricted")
	}
	if !fsOperationRequiresSandbox(MethodFSWriteFile, restrictedWrites, native) ||
		!fsOperationRequiresSandbox(MethodFSRemove, restrictedWrites, native) {
		t.Fatal("restricted writes must use the sandbox")
	}

	disabled := &sandbox.PermissionProfile{Disabled: true}
	if fsOperationRequiresSandbox(MethodFSReadFile, disabled, native) || fsOperationRequiresSandbox(MethodFSWriteFile, disabled, native) {
		t.Fatal("disabled profile must not request sandboxing")
	}
}

// Mirrors Rust #46122: symbolic `:slash_tmp` read denials depend on the
// executor's path convention; an unknown convention keeps the restriction.
func TestFSOperationSandboxSelectionUsesExecutorPathConventionLikeRust(t *testing.T) {
	slashTmpDeny := &sandbox.PermissionProfile{
		SandboxPolicy: &sandbox.SandboxPolicy{Kind: sandbox.SandboxDangerFullAccess},
		DeniedReadEntries: []sandbox.FileSystemSandboxEntry{{
			Path:   sandbox.FileSystemPath{Type: "special", Value: &sandbox.FileSystemSpecialPath{Kind: "slash_tmp"}},
			Access: sandbox.FileSystemAccessDeny,
		}},
	}
	if !fsOperationRequiresSandbox(MethodFSReadFile, slashTmpDeny, "") {
		t.Fatal("unknown convention must fail closed for a :slash_tmp denial")
	}
	if !fsOperationRequiresSandbox(MethodFSReadFile, slashTmpDeny, utils.ConventionPosix) {
		t.Fatal("POSIX :slash_tmp denial must sandbox reads")
	}
	if fsOperationRequiresSandbox(MethodFSReadFile, slashTmpDeny, utils.ConventionWindows) {
		t.Fatal("Windows executor must ignore symbolic :slash_tmp for read routing")
	}

	if got := fsSandboxPathConvention(`C:\repo`); got != utils.ConventionWindows {
		t.Fatalf("fsSandboxPathConvention(Windows) = %q, want windows", got)
	}
	if got := fsSandboxPathConvention("/repo"); got != utils.ConventionPosix {
		t.Fatalf("fsSandboxPathConvention(POSIX) = %q, want posix", got)
	}
}
