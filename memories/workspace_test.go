package memories

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/config"
)

func TestWorkspaceBaselineReportsAddedModifiedDeletedAndResets(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "memories")
	if err := os.MkdirAll(filepath.Join(root, "rollout_summaries"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemoryFilename), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleted := filepath.Join(root, "rollout_summaries", "deleted.md")
	if err := os.WriteFile(deleted, []byte("important stale evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareWorkspace(ctx, root); err != nil {
		t.Fatal(err)
	}
	// PrepareWorkspace installs an empty recovery baseline, so the first
	// consolidation advance records the workspace as it stands (Rust #57240).
	if err := ResetWorkspaceBaseline(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemoryFilename), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemorySummaryFilename), []byte("v1\nsummary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ignored"), []byte("ignored-but-tracked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}
	diff, err := WorkspaceDiff(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []GitBaselineChange{
		{Status: GitBaselineAdded, Path: ".ignored"},
		{Status: GitBaselineModified, Path: MemoryFilename},
		{Status: GitBaselineAdded, Path: MemorySummaryFilename},
		{Status: GitBaselineDeleted, Path: "rollout_summaries/deleted.md"},
	}
	if len(diff.Changes) != len(want) {
		t.Fatalf("changes = %+v", diff.Changes)
	}
	for index := range want {
		if diff.Changes[index] != want[index] {
			t.Fatalf("change %d = %+v, want %+v", index, diff.Changes[index], want[index])
		}
	}
	for _, fragment := range []string{
		"diff --git a/MEMORY.md b/MEMORY.md", "-old", "+new",
		"new file mode 100644", "+v1", "deleted file mode 100644", "-important stale evidence",
	} {
		if !strings.Contains(diff.UnifiedDiff, fragment) {
			t.Fatalf("unified diff missing %q:\n%s", fragment, diff.UnifiedDiff)
		}
	}
	if err := WriteWorkspaceDiff(root, diff); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, WorkspaceDiffFilename)); err != nil {
		t.Fatal(err)
	}
	if err := ResetWorkspaceBaseline(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, WorkspaceDiffFilename)); !os.IsNotExist(err) {
		t.Fatalf("workspace diff survived reset: %v", err)
	}
	clean, err := WorkspaceDiff(ctx, root)
	if err != nil || clean.HasChanges() || clean.UnifiedDiff != "" {
		t.Fatalf("diff after reset = %+v, %v", clean, err)
	}
}

func TestPrepareWorkspaceRejectsSymlinkRootLikeRust(t *testing.T) {
	home := t.TempDir()
	outside := filepath.Join(home, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "memories")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := PrepareWorkspace(context.Background(), link); err == nil || !strings.Contains(err.Error(), "memory root cannot be a symbolic link") {
		t.Fatalf("PrepareWorkspace(symlink root) error = %v", err)
	}
}

func TestPrepareWorkspaceRemovesSymlinksLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "extensions")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := PrepareWorkspace(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("symlink %s was not removed", link)
	}
}

func TestPrepareWorkspaceRecoversUnusableGitDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memories")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemoryFilename), []byte("memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareWorkspace(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	// Rust #57240: replacing unusable metadata installs an empty baseline, so
	// existing memory files stay visible to the next consolidation as additions.
	diff, err := WorkspaceDiff(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 1 || diff.Changes[0] != (GitBaselineChange{Status: GitBaselineAdded, Path: MemoryFilename}) {
		t.Fatalf("recovered workspace diff = %+v, %v", diff.Changes, err)
	}
	if !strings.Contains(diff.UnifiedDiff, "+memory") {
		t.Fatalf("recovered workspace diff missing working content:\n%s", diff.UnifiedDiff)
	}
}

// Rust #57240: baselines carry a version marker, keep the cross-process lock
// outside the replaceable .git, and never report the control directory itself.
func TestPrepareWorkspaceVersionsBaselineAndHidesControlDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemoryFilename), []byte("memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareWorkspace(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	version, err := os.ReadFile(filepath.Join(root, ".git", baselineVersionFile))
	if err != nil || string(version) != baselineVersion {
		t.Fatalf("baseline version = %q, %v", version, err)
	}
	if _, err := os.Stat(filepath.Join(root, baselineControlDir, ".git", baselineLockFile)); err != nil {
		t.Fatalf("baseline lock file missing: %v", err)
	}
	diff, err := WorkspaceDiff(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range diff.Changes {
		if isBaselineControlPath(change.Path) {
			t.Fatalf("control directory leaked into the diff: %+v", diff.Changes)
		}
	}
}

// Rust #57240: legacy baselines without the current version marker are replaced
// by an empty baseline that keeps working files as additions.
func TestPrepareWorkspaceMigratesLegacyBaselineLikeRust(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemoryFilename), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runBaselineGit(ctx, root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if _, err := runBaselineGit(ctx, root, "add", "--all", "--force"); err != nil {
		t.Fatal(err)
	}
	if _, err := runBaselineGit(ctx, root,
		"-c", "user.name=Codex", "-c", "user.email=noreply@openai.com",
		"commit", "--quiet", "--allow-empty", "--no-gpg-sign", "--no-verify", "-m", "legacy",
	); err != nil {
		t.Fatal(err)
	}
	if err := PrepareWorkspace(ctx, root); err != nil {
		t.Fatal(err)
	}
	diff, err := WorkspaceDiff(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 1 || diff.Changes[0] != (GitBaselineChange{Status: GitBaselineAdded, Path: MemoryFilename}) {
		t.Fatalf("migrated baseline diff = %+v", diff.Changes)
	}
}

// Rust #57240: oversized working files are skipped rather than read into the
// snapshot or the consolidation diff.
func TestWorkspaceDiffSkipsOversizedFilesLikeRust(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "memories")
	if err := PrepareWorkspace(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "rollout_summaries"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(root, "rollout_summaries", "big.md")
	file, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxBaselineFileBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemorySummaryFilename), []byte("v1\nsummary"), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, err := WorkspaceDiff(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 1 || diff.Changes[0] != (GitBaselineChange{Status: GitBaselineAdded, Path: MemorySummaryFilename}) {
		t.Fatalf("oversized workspace diff = %+v", diff.Changes)
	}
	if strings.Contains(diff.UnifiedDiff, "big.md") {
		t.Fatalf("oversized file entered the diff:\n%s", diff.UnifiedDiff)
	}
}

// Rust #57240: the stored snapshot itself is bounded, so an oversized file
// that is later deleted cannot be reported from a baseline blob.
func TestResetWorkspaceBaselineSkipsOversizedFilesLikeRust(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemoryFilename), []byte("memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(root, "big.md")
	file, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxBaselineFileBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ResetWorkspaceBaseline(ctx, root); err != nil {
		t.Fatal(err)
	}
	tracked, err := runBaselineGit(ctx, root, "ls-tree", "-r", "--name-only", "-z", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if names := splitNUL(tracked); len(names) != 1 || string(names[0]) != MemoryFilename {
		t.Fatalf("baseline entries = %q", names)
	}
	if err := os.Remove(big); err != nil {
		t.Fatal(err)
	}
	diff, err := WorkspaceDiff(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if diff.HasChanges() {
		t.Fatalf("deleted oversized file reported: %+v", diff.Changes)
	}
}

// Rust #57240: a tracked file that grows past the limit invalidates the
// baseline instead of staying silently processed, while later changes still
// report normally.
func TestWorkspaceDiffInvalidatesBaselineForGrownFileLikeRust(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemoryFilename), []byte("small"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareWorkspace(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := ResetWorkspaceBaseline(ctx, root); err != nil {
		t.Fatal(err)
	}
	if diff, err := WorkspaceDiff(ctx, root); err != nil || diff.HasChanges() {
		t.Fatalf("clean baseline diff = %+v, %v", diff, err)
	}
	file, err := os.OpenFile(filepath.Join(root, MemoryFilename), os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxBaselineFileBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("new note"), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, err := WorkspaceDiff(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 1 || diff.Changes[0] != (GitBaselineChange{Status: GitBaselineAdded, Path: "notes.md"}) {
		t.Fatalf("grown file diff = %+v", diff.Changes)
	}
	if _, err := os.Stat(filepath.Join(root, MemoryFilename)); err != nil {
		t.Fatalf("grown memory file must stay on disk: %v", err)
	}
}

func TestRenderWorkspaceDiffBoundsUTF8AndNoChanges(t *testing.T) {
	if got := RenderWorkspaceDiff(GitBaselineDiff{}); got != "# Memory Workspace Diff\n\nGenerated by Codex before Phase 2 memory consolidation. Read this file first and do not edit it.\n\n## Status\n- none\n" {
		t.Fatalf("empty rendered diff = %q", got)
	}
	large := strings.Repeat("a", WorkspaceDiffMaxBytes-1) + "茅" + strings.Repeat("b", 128)
	rendered := RenderWorkspaceDiff(GitBaselineDiff{
		Changes:     []GitBaselineChange{{Status: GitBaselineModified, Path: MemoryFilename}},
		UnifiedDiff: large,
	})
	if !strings.Contains(rendered, "- M MEMORY.md") || !strings.Contains(rendered, "[workspace diff truncated at 4194304 bytes]") || !strings.HasSuffix(rendered, "```\n") {
		t.Fatalf("bounded rendered diff markers missing")
	}
	if strings.ToValidUTF8(rendered, "") != rendered {
		t.Fatal("rendered diff is not valid UTF-8")
	}
}

func TestValidateConsolidationArtifacts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, MemoryFilename), []byte("memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemorySummaryFilename), []byte("outdated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConsolidationArtifacts(root); err == nil || !strings.Contains(err.Error(), "does not start with v1") {
		t.Fatalf("invalid summary error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, MemorySummaryFilename), []byte("v1\nsummary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConsolidationArtifacts(root); err != nil {
		t.Fatal(err)
	}
}

// MemoryStorageBytes sums regular files from metadata, so a run that leaves git
// metadata or a symlink behind reports only the memory artifacts themselves
// (Rust #45956).
func TestMemoryStorageBytesCountsRegularFilesOnly(t *testing.T) {
	home := t.TempDir()
	for _, version := range []config.MemoryVersion{config.MemoryVersionV1, config.MemoryVersionV2} {
		root := RootForVersion(home, version)
		for _, dir := range []string{
			filepath.Join(root, "rollout_summaries"),
			filepath.Join(root, "extensions", "notes"),
			filepath.Join(root, ".git", "objects"),
		} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, ".git", "objects", "baseline"), []byte("git metadata is not memory"), 0o600); err != nil {
			t.Fatal(err)
		}
		files := []struct {
			path    string
			content string
		}{
			{MemorySummaryFilename, "summary"},
			{filepath.Join("rollout_summaries", "thread.md"), "rollout"},
			{filepath.Join("extensions", "notes", "note.md"), "café"},
		}
		var want int64
		for _, file := range files {
			if err := os.WriteFile(filepath.Join(root, file.path), []byte(file.content), 0o600); err != nil {
				t.Fatal(err)
			}
			want += int64(len(file.content))
		}
		if runtime.GOOS != "windows" {
			// Symlinks contribute nothing and are never followed, so a link back
			// to the root cannot loop and a broken link cannot fail the walk.
			for link, target := range map[string]string{
				"file-link":      filepath.Join(root, MemorySummaryFilename),
				"directory-loop": root,
				"broken-link":    filepath.Join(root, "missing"),
			} {
				if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
					t.Fatal(err)
				}
			}
		}
		got, err := MemoryStorageBytes(root)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s storage bytes = %d, want %d", version.DirectoryName(), got, want)
		}
	}
}
