package tea

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// editorTestWorkspacePolicy builds a workspace-write policy whose only
// writable roots are the explicit workspace, isolating the test temp dirs
// from the OS temp dir that GetWritableRootsWithCWD would otherwise add.
func editorTestWorkspacePolicy(workspace string) *sandbox.SandboxPolicy {
	policy := sandbox.NewWorkspaceWritePolicy()
	policy.WritableRoots = []string{workspace}
	policy.ExcludeTmpdirEnvVar = true
	policy.ExcludeSlashTmp = true
	return policy
}

func TestEditorDirectoryAcceptsProtectedHome(t *testing.T) {
	workspace := t.TempDir()
	codexHome := t.TempDir()
	policy := editorTestWorkspacePolicy(workspace)

	dir, err := EditorDirectory([]string{codexHome}, policy, filepath.Join(workspace, "src"))
	if err != nil {
		t.Fatalf("EditorDirectory() error = %v", err)
	}
	if want := filepath.Join(codexHome, "editor"); dir != want {
		t.Fatalf("editor directory = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("editor directory not created: %v", err)
	}
}

func TestEditorDirectoryRejectsWritableHome(t *testing.T) {
	workspace := t.TempDir()
	codexHome := filepath.Join(workspace, "codex")
	policy := editorTestWorkspacePolicy(workspace)

	dir, err := EditorDirectory([]string{codexHome}, policy, filepath.Join(workspace, "src"))
	if err == nil || !strings.Contains(err.Error(), "editor directory must not be writable") {
		t.Fatalf("EditorDirectory() = (%q, %v), want writable rejection", dir, err)
	}
}

func TestEditorDirectoryFullDiskWriteSkipsRejection(t *testing.T) {
	codexHome := t.TempDir()
	policy := sandbox.NewDangerFullAccessPolicy()

	dir, err := EditorDirectory([]string{codexHome}, policy, t.TempDir())
	if err != nil {
		t.Fatalf("EditorDirectory() error = %v", err)
	}
	if want := filepath.Join(codexHome, "editor"); dir != want {
		t.Fatalf("editor directory = %q, want %q", dir, want)
	}
}

func TestEditorDirectoryFallsBackToNextHome(t *testing.T) {
	workspace := t.TempDir()
	writableHome := filepath.Join(workspace, "codex")
	protectedHome := t.TempDir()
	policy := editorTestWorkspacePolicy(workspace)

	dir, err := EditorDirectory([]string{writableHome, protectedHome}, policy, filepath.Join(workspace, "src"))
	if err != nil {
		t.Fatalf("EditorDirectory() error = %v", err)
	}
	if want := filepath.Join(protectedHome, "editor"); dir != want {
		t.Fatalf("editor directory = %q, want fallback %q", dir, want)
	}
}

func TestEditorDirectoryRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	codexHome := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(codexHome, "editor")); err != nil {
		t.Fatalf("Symlink error = %v", err)
	}
	policy := sandbox.NewReadOnlyPolicy()

	_, err := EditorDirectory([]string{codexHome}, policy, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "symbolic links") {
		t.Fatalf("EditorDirectory() error = %v, want symlink rejection", err)
	}
}

func TestEditorDirectoryNoCandidates(t *testing.T) {
	workspace := t.TempDir()
	policy := editorTestWorkspacePolicy(workspace)

	_, err := EditorDirectory([]string{workspace, filepath.Join(workspace, "codex")}, policy, filepath.Join(workspace, "src"))
	if err == nil || !strings.Contains(err.Error(), "editor directory must not be writable") {
		t.Fatalf("EditorDirectory() error = %v, want writable rejection", err)
	}
}

func TestEditorDirectoryReadOnlyPolicy(t *testing.T) {
	codexHome := t.TempDir()
	dir, err := EditorDirectory([]string{codexHome}, sandbox.NewReadOnlyPolicy(), t.TempDir())
	if err != nil {
		t.Fatalf("EditorDirectory() error = %v", err)
	}
	if want := filepath.Join(codexHome, "editor"); dir != want {
		t.Fatalf("editor directory = %q, want %q", dir, want)
	}
}
