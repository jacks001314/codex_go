package sandbox

import (
	"strings"
	"testing"
)

func workspaceProfileWithDeniedReadsForTest(t *testing.T) PermissionProfile {
	t.Helper()
	profile := WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []FileSystemSandboxEntry{
		{Path: FileSystemPath{Type: "path", Path: "blocked"}, Access: FileSystemAccessDeny},
		{Path: FileSystemPath{Type: "glob_pattern", Pattern: "~/private/**"}, Access: FileSystemAccessDeny},
		{Path: FileSystemPath{Type: "special", Value: &FileSystemSpecialPath{Kind: "tmpdir"}}, Access: FileSystemAccessDeny},
	}
	return profile
}

// TestPermissionPromptRemoteWindowsPathsLikeRust pins Rust #47867: a remote
// executor's cwd/home/platform resolve writable roots and deny entries in its
// native spelling instead of the controller's host paths.
func TestPermissionPromptRemoteWindowsPathsLikeRust(t *testing.T) {
	profile := workspaceProfileWithDeniedReadsForTest(t)
	body := BuildPermissionPromptForProfile(&profile, "/unused-local-cwd", PermissionPromptProfileOptions{
		ApprovalPolicy: ApprovalOnRequest,
		ExecutorContext: &PermissionPromptExecutorContext{
			CWD:         "file:///C:/workspace",
			UserHomeDir: `C:\Users\me`,
			PlatformOS:  "windows",
		},
	})
	for _, want := range []string{
		"`sandbox_mode` is `workspace-write`",
		"`C:\\workspace`",
		"path `C:\\workspace\\blocked`",
		"glob `C:\\Users\\me\\private\\**`",
		"Do not request escalation or additional permissions",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("permission prompt missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "/unused-local-cwd") {
		t.Fatalf("permission prompt used the controller cwd:\n%s", body)
	}
}

// TestPermissionPromptRemoteMissingFactsOmitOnlyUnresolvedEntriesLikeRust
// covers Rust #47867's per-entry resolution: without an executor home only the
// home-relative deny entry disappears.
func TestPermissionPromptRemoteMissingFactsOmitOnlyUnresolvedEntriesLikeRust(t *testing.T) {
	profile := workspaceProfileWithDeniedReadsForTest(t)
	body := BuildPermissionPromptForProfile(&profile, "/unused-local-cwd", PermissionPromptProfileOptions{
		ApprovalPolicy: ApprovalOnRequest,
		ExecutorContext: &PermissionPromptExecutorContext{
			CWD:        "file:///workspace",
			PlatformOS: "linux",
		},
	})
	if !strings.Contains(body, "path `/workspace/blocked`") {
		t.Fatalf("resolved deny path missing:\n%s", body)
	}
	if strings.Contains(body, "/private/**") || strings.Contains(body, `\private\**`) {
		t.Fatalf("unresolved home-relative glob was rendered:\n%s", body)
	}
}

// TestPermissionPromptBoundsPathsAtWholeEntriesLikeRust pins the shared 32 KiB
// display budget from Rust #47867: entries are omitted whole and a warning says
// all restrictions still apply.
func TestPermissionPromptBoundsPathsAtWholeEntriesLikeRust(t *testing.T) {
	writable := "/writable/" + strings.Repeat("é", 6000)
	denied := "/denied/" + strings.Repeat("é", 6000)
	glob := "/glob/" + strings.Repeat("é", 6000) + "/**"
	oversized := "/oversized/" + strings.Repeat("x", maxPermissionPromptPathBytes)

	full := BuildPermissionPrompt(&PermissionPromptConfig{
		SandboxMode:     PermissionPromptReadOnly,
		ApprovalPolicy:  PermissionPromptApprovalOnRequest,
		WritableRoots:   []string{writable},
		DeniedReadPaths: []string{denied},
		DeniedReadGlobs: []string{"/short/**"},
	})
	bounded := BuildPermissionPrompt(&PermissionPromptConfig{
		SandboxMode:     PermissionPromptReadOnly,
		ApprovalPolicy:  PermissionPromptApprovalOnRequest,
		WritableRoots:   []string{oversized, writable},
		DeniedReadPaths: []string{denied},
		DeniedReadGlobs: []string{glob, "/short/**"},
	})
	want := full + permissionPromptOmittedPathsText + "\n"
	if bounded != want {
		t.Fatalf("bounded prompt = %q, want %q", bounded, want)
	}
}

func TestPermissionPromptWithoutExecutorContextKeepsLocalResolution(t *testing.T) {
	profile := workspaceProfileWithDeniedReadsForTest(t)
	body := BuildPermissionPromptForProfile(&profile, "/local/workspace", PermissionPromptProfileOptions{
		ApprovalPolicy: ApprovalOnRequest,
	})
	if !strings.Contains(body, "blocked") {
		t.Fatalf("local resolution lost the deny path:\n%s", body)
	}
	if !strings.Contains(body, "local") || !strings.Contains(body, "workspace") {
		t.Fatalf("local resolution lost the controller cwd:\n%s", body)
	}
}
