package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
	codextea "codex_go/tui/tea"
)

func fixedPreviewPermissions(profile *sandbox.PermissionProfile, roots ...string) sidebarPreviewPermissions {
	return func(string) (*sandbox.PermissionProfile, []string, error) {
		return profile, append([]string(nil), roots...), nil
	}
}

// Rust #63184 model_read.rs: an embedded preview reads with the calling
// thread's own read permissions, so a path outside its workspace, a deny-read
// entry, or a file above the 8 MiB model limit is refused.
func TestEmbeddedSidebarDocumentHostRechecksReadPermissionsLikeRust(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "docs"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "docs", "readme.md"), []byte("# ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	profile := sandbox.WorkspaceWritePermissionProfile()
	host := newEmbeddedSidebarDocumentHost(func() string { return workspace }, fixedPreviewPermissions(&profile, workspace))
	view := host.open(codextea.SidebarDocumentRequest{SourceThreadID: "thread-a", Path: "docs/readme.md", Link: -1})
	if view.Err != "" || view.Text != "# ok\n" {
		t.Fatalf("permitted preview = %#v", view)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	view = host.open(codextea.SidebarDocumentRequest{SourceThreadID: "thread-a", Path: filepath.Join(outside, "secret.md"), Link: -1})
	if view.Err != errPreviewOutsideWorkspace.Error() {
		t.Fatalf("outside-workspace error = %q, want %q", view.Err, errPreviewOutsideWorkspace.Error())
	}

	denied := sandbox.WorkspaceWritePermissionProfile()
	denied.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "path", Path: filepath.Join(workspace, "docs")},
		Access: sandbox.FileSystemAccessDeny,
	}}
	deniedHost := newEmbeddedSidebarDocumentHost(func() string { return workspace }, fixedPreviewPermissions(&denied, workspace))
	view = deniedHost.open(codextea.SidebarDocumentRequest{SourceThreadID: "thread-a", Path: "docs/readme.md", Link: -1})
	if view.Err != errPreviewReadDenied.Error() {
		t.Fatalf("deny-read error = %q, want %q", view.Err, errPreviewReadDenied.Error())
	}

	big := filepath.Join(workspace, "big.md")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a"), embeddedSidebarPreviewLimit+1), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	view = host.open(codextea.SidebarDocumentRequest{SourceThreadID: "thread-a", Path: big, Link: -1})
	if view.Err != errPreviewTooLarge.Error() {
		t.Fatalf("oversized preview error = %q, want %q", view.Err, errPreviewTooLarge.Error())
	}
}

// Rust re-reads the preview workspace after the read and refuses a preview the
// workspace changed underneath.
func TestEmbeddedSidebarDocumentHostRejectsWorkspaceChangeDuringReadLikeRust(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "note.md"), []byte("note"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	profile := sandbox.WorkspaceWritePermissionProfile()
	calls := 0
	cwd := func() string {
		calls++
		if calls == 1 {
			return workspace
		}
		return ""
	}
	host := newEmbeddedSidebarDocumentHost(cwd, fixedPreviewPermissions(&profile, workspace))
	view := host.open(codextea.SidebarDocumentRequest{SourceThreadID: "thread-a", Path: "note.md", Link: -1})
	if view.Err != errPreviewOutsideWorkspace.Error() {
		t.Fatalf("workspace-change error = %q, want %q", view.Err, errPreviewOutsideWorkspace.Error())
	}
}
