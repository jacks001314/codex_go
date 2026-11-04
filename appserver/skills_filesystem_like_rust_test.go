package appserver

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

func writeRepositorySkill(t *testing.T, dir string, name string) string {
	t.Helper()
	path := filepath.Join(dir, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", dir, err)
	}
	body := "---\nname: " + name + "\ndescription: test skill\n---\nbody\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
	return path
}

// TestPermissionSkillFileSystemEnforcesReadsLikeRust pins Rust #59279: the
// accessor reads through the captured permissions, so a denied subtree is
// unreachable even though the process could read it.
func TestPermissionSkillFileSystemEnforcesReadsLikeRust(t *testing.T) {
	workspace := t.TempDir()
	visible := writeRepositorySkill(t, filepath.Join(workspace, ".agents", "skills"), "visible")
	private := writeRepositorySkill(t, filepath.Join(workspace, "private"), "private")
	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{
		Access: sandbox.FileSystemAccessDeny,
		Path:   sandbox.FileSystemPath{Type: "path", Path: filepath.Join(workspace, "private")},
	}}
	access, err := NewPermissionSkillFileSystem(&profile, workspace, []string{workspace})
	if err != nil {
		t.Fatalf("NewPermissionSkillFileSystem() error = %v", err)
	}
	if _, err := access.ReadFile(visible); err != nil {
		t.Fatalf("granted read error = %v", err)
	}
	if _, err := access.ReadFile(private); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("denied read error = %v, want a permission error", err)
	}
	if access.WatchSupported() {
		t.Fatal("a restricted accessor must decline unsandboxed watches")
	}
	if access.Key() == "" || access.Key() == HostSkillFileSystemKey {
		t.Fatalf("accessor key = %q, want a permission identity", access.Key())
	}
}

// TestSkillsServiceRestrictedAccessorHidesSkillsLikeRust pins #59280: a
// repository skill under a denied path is skipped with a warning while the
// granted sibling is still discovered.
func TestSkillsServiceRestrictedAccessorHidesSkillsLikeRust(t *testing.T) {
	workspace := t.TempDir()
	repoRoot := filepath.Join(workspace, ".agents", "skills")
	writeRepositorySkill(t, repoRoot, "visible")
	writeRepositorySkill(t, repoRoot, "private")
	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{
		Access: sandbox.FileSystemAccessDeny,
		Path:   sandbox.FileSystemPath{Type: "path", Path: filepath.Join(repoRoot, "private")},
	}}
	access, err := NewPermissionSkillFileSystem(&profile, workspace, []string{workspace})
	if err != nil {
		t.Fatalf("NewPermissionSkillFileSystem() error = %v", err)
	}
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{})
	defer service.Close()
	restricted, err := service.List(&SkillsListParams{CWDs: []string{workspace}, FileSystem: access})
	if err != nil {
		t.Fatalf("restricted List() error = %v", err)
	}
	names := map[string]bool{}
	for _, entry := range restricted.Skills {
		names[entry.Name] = true
	}
	if !names["visible"] {
		t.Fatalf("granted skill missing: %#v", names)
	}
	if names["private"] {
		t.Fatalf("denied skill was discovered: %#v", names)
	}
	// The accessor identity participates in the cache key, so an unrestricted
	// listing cannot reuse the restricted snapshot.
	unrestricted, err := service.List(&SkillsListParams{CWDs: []string{workspace}})
	if err != nil {
		t.Fatalf("unrestricted List() error = %v", err)
	}
	all := map[string]bool{}
	for _, entry := range unrestricted.Skills {
		all[entry.Name] = true
	}
	if !all["visible"] || !all["private"] {
		t.Fatalf("unrestricted skills = %#v, want both", all)
	}
}
