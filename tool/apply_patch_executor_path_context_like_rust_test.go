package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// TestApplyPatchAuthorizesInExecutorPathContextLikeRust mirrors Rust #42391
// (`Authorize apply_patch in the executor path context`): patch targets are
// classified against the selected executor's filesystem policy context, so a
// Windows executor addressed from a different host convention is neither
// refused inside its own workspace nor allowed outside it.
func TestApplyPatchAuthorizesInExecutorPathContextLikeRust(t *testing.T) {
	remote := newFakeEnvironmentFileSystem("file:///C:/workspace")
	localDir := t.TempDir()

	policy := sandbox.NewWorkspaceWritePolicy()
	policy.ExcludeTmpdirEnvVar = true
	policy.ExcludeSlashTmp = true
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{
		CWD:                  localDir,
		IncludeEnvironmentID: true,
		SandboxPolicy:        policy,
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote-x"},
			ReadyEnvironmentCount:  1,
			StableEnvironmentTools: true,
		},
		EnvironmentFileSystems: fakeEnvironmentFileSystemProvider{fileSystem: remote},
	})

	run := func(t *testing.T, callID string, target string) *Output {
		t.Helper()
		patch := "*** Begin Patch\n" +
			"*** Environment ID: remote-x\n" +
			"*** Add File: " + target + "\n" +
			"+created\n" +
			"*** End Patch\n"
		output, err := executor.Execute(context.Background(), &Invocation{
			CallID:   callID,
			ToolName: PlainName(DefaultApplyPatchToolName),
			Payload:  Payload{Kind: PayloadCustom, Input: patch},
		})
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", callID, err)
		}
		if output == nil {
			t.Fatalf("Execute(%s) = nil output", callID)
		}
		return output
	}

	inside := run(t, "executor-context-inside", `C:\workspace\created.txt`)
	if !inside.Success {
		t.Fatalf("inside-workspace patch = %#v, want success", inside)
	}
	if len(remote.writes) != 1 {
		t.Fatalf("environment writes = %v, want one write", remote.writes)
	}

	outside := run(t, "executor-context-outside", `C:\outside\notes.txt`)
	if outside.Success {
		t.Fatalf("outside-workspace patch = %#v, want refusal", outside)
	}
	if !strings.Contains(outside.Body, "outside of the project workspace roots") {
		t.Fatalf("outside body = %q, want the workspace-roots refusal", outside.Body)
	}
	if !strings.Contains(outside.Body, `C:\outside\notes.txt`) {
		t.Fatalf("outside body = %q, want it to name the refused executor path", outside.Body)
	}
	if len(remote.writes) != 1 {
		t.Fatalf("environment writes after refusal = %v, want the refused patch not to write", remote.writes)
	}
	// The host workspace never sees the executor's files.
	if _, err := os.Stat(filepath.Join(localDir, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("host workspace created.txt exists (err = %v), want the write confined to the executor", err)
	}
}

// TestApplyPatchRefusesProtectedMetadataInExecutorContextLikeRust keeps the
// workspace metadata protection while classifying targets with the executor
// convention: `.GIT` is a case alias of `.git` on a Windows executor.
func TestApplyPatchRefusesProtectedMetadataInExecutorContextLikeRust(t *testing.T) {
	remote := newFakeEnvironmentFileSystem(`C:\workspace`)
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{
		CWD:                  t.TempDir(),
		IncludeEnvironmentID: true,
		SandboxPolicy:        sandbox.NewWorkspaceWritePolicy(),
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote-x"},
			ReadyEnvironmentCount:  1,
			StableEnvironmentTools: true,
		},
		EnvironmentFileSystems: fakeEnvironmentFileSystemProvider{fileSystem: remote},
	})
	patch := "*** Begin Patch\n" +
		"*** Environment ID: remote-x\n" +
		"*** Add File: C:\\workspace\\.GIT\\config\n" +
		"+created\n" +
		"*** End Patch\n"
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "executor-context-metadata",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: patch},
	})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if output == nil || output.Success || !strings.Contains(output.Body, "outside of the project workspace roots") {
		t.Fatalf("metadata patch output = %#v, want the workspace-roots refusal", output)
	}
	if len(remote.writes) != 0 {
		t.Fatalf("environment writes = %v, want the refused patch not to write", remote.writes)
	}
}

// TestApplyPatchAuthorizesPosixExecutorFromWindowsHostLikeRust covers the
// opposite direction of Rust #42391: a POSIX executor addressed from a Windows
// host. Host-native conversion would treat `/srv/outside/notes.txt` as a
// relative path below the Codex host cwd and let it through.
func TestApplyPatchAuthorizesPosixExecutorFromWindowsHostLikeRust(t *testing.T) {
	remote := newFakeEnvironmentFileSystem("file:///srv/workspace")
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{
		CWD:                  t.TempDir(),
		IncludeEnvironmentID: true,
		SandboxPolicy:        sandbox.NewWorkspaceWritePolicy(),
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote-x"},
			ReadyEnvironmentCount:  1,
			StableEnvironmentTools: true,
		},
		EnvironmentFileSystems: fakeEnvironmentFileSystemProvider{fileSystem: remote},
	})

	run := func(t *testing.T, callID string, target string) *Output {
		t.Helper()
		patch := "*** Begin Patch\n" +
			"*** Environment ID: remote-x\n" +
			"*** Add File: " + target + "\n" +
			"+created\n" +
			"*** End Patch\n"
		output, err := executor.Execute(context.Background(), &Invocation{
			CallID:   callID,
			ToolName: PlainName(DefaultApplyPatchToolName),
			Payload:  Payload{Kind: PayloadCustom, Input: patch},
		})
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", callID, err)
		}
		if output == nil {
			t.Fatalf("Execute(%s) = nil output", callID)
		}
		return output
	}

	if output := run(t, "posix-executor-inside", "/srv/workspace/created.txt"); !output.Success {
		t.Fatalf("inside-workspace patch = %#v, want success", output)
	}
	outside := run(t, "posix-executor-outside", "/srv/outside/notes.txt")
	if outside.Success || !strings.Contains(outside.Body, "outside of the project workspace roots") {
		t.Fatalf("outside-workspace patch = %#v, want the workspace-roots refusal", outside)
	}
	if len(remote.writes) != 1 {
		t.Fatalf("environment writes = %v, want only the inside patch to write", remote.writes)
	}
}
