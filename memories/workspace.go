package memories

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"codex_go/config"
	"codex_go/envutil"
)

const (
	WorkspaceDiffFilename = "phase2_workspace_diff.md"
	WorkspaceDiffMaxBytes = 4 * 1024 * 1024
	baselineCommitMessage = "Initialize Codex git baseline\n\nCo-authored-by: Codex <noreply@openai.com>"

	// maxBaselineFileBytes matches Rust's MAX_BASELINE_FILE_BYTES (#57240): a
	// baseline is derived data, so an oversized working file is skipped instead
	// of being read into the snapshot or the consolidation diff.
	maxBaselineFileBytes = 10 * 1024 * 1024
	baselineVersionFile  = "codex-baseline-version"
	baselineVersion      = "1\n"
	// baselineControlDir holds the cross-process lock. Its nested .git keeps the
	// lock outside the replaceable baseline and outside memory scans.
	baselineControlDir = ".codex-baseline"
	baselineLockFile   = "codex-baseline.lock"
)

// errBaselineFileTooLarge marks a working file above maxBaselineFileBytes.
var errBaselineFileTooLarge = errors.New("memory workspace file exceeds the baseline limit")

// baselineContents selects what a reset records: an empty tree for recovery, or
// the current working files when consolidation advances the baseline.
type baselineContents int

const (
	baselineContentsEmpty baselineContents = iota
	baselineContentsCurrent
)

type GitBaselineChangeStatus string

const (
	GitBaselineAdded    GitBaselineChangeStatus = "A"
	GitBaselineModified GitBaselineChangeStatus = "M"
	GitBaselineDeleted  GitBaselineChangeStatus = "D"
)

type GitBaselineChange struct {
	Status GitBaselineChangeStatus
	Path   string
}

type GitBaselineDiff struct {
	Changes     []GitBaselineChange
	UnifiedDiff string
}

func (d GitBaselineDiff) HasChanges() bool {
	return len(d.Changes) > 0
}

func PrepareWorkspace(ctx context.Context, root string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create memory workspace %s: %w", root, err)
	}
	if err := removeMemorySymlinks(root); err != nil {
		return err
	}
	if err := removeWorkspaceDiff(root); err != nil {
		return err
	}
	return withBaselineLock(ctx, root, func(root string) error {
		if baselineUsable(ctx, root) {
			return nil
		}
		return resetGitBaseline(ctx, root, baselineContentsEmpty)
	})
}

// withBaselineLock serializes baseline initialization, resets, and diffs across
// app-server processes (Rust #57240). It fails rather than treating an
// interrupted control directory as a repository.
func withBaselineLock(ctx context.Context, root string, operation func(root string) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create memory workspace %s: %w", root, err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve memory workspace %s: %w", root, err)
	}
	absolute, err := filepath.Abs(canonical)
	if err != nil {
		return fmt.Errorf("resolve memory workspace %s: %w", root, err)
	}
	controlRoot := filepath.Join(absolute, baselineControlDir)
	control := filepath.Join(controlRoot, ".git")
	for _, dir := range []string{controlRoot, control} {
		if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
			return fmt.Errorf("create baseline control directory %s: %w", dir, err)
		}
		info, statErr := os.Lstat(dir)
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("baseline control path is not a directory: %s", dir)
		}
		resolved, resolveErr := filepath.EvalSymlinks(dir)
		if resolveErr != nil || resolved != dir {
			return fmt.Errorf("baseline control path is not canonical: %s", dir)
		}
	}
	if _, err := os.Lstat(filepath.Join(control, "HEAD")); err == nil {
		return errors.New("baseline control directory contains a repository")
	}
	lock := flock.New(filepath.Join(control, baselineLockFile))
	locked, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("lock git baseline: %w", err)
	}
	if !locked {
		return fmt.Errorf("lock git baseline: %w", ctx.Err())
	}
	defer func() { _ = lock.Unlock() }()
	return operation(absolute)
}

// removeMemorySymlinks recursively removes symbolic links so a memory
// workspace cannot redirect writes (or reads) outside the workspace, and
// rejects a symlinked memory root (Rust #39205).
func removeMemorySymlinks(root string) error {
	root = filepath.Clean(root)
	if info, err := os.Lstat(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("memory root cannot be a symbolic link: %s", root)
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return os.Remove(path)
		}
		return nil
	})
}

func WorkspaceDiff(ctx context.Context, root string) (GitBaselineDiff, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var diff GitBaselineDiff
	err := withBaselineLock(ctx, root, func(root string) error {
		if err := removeWorkspaceDiff(root); err != nil {
			return err
		}
		if !baselineUsable(ctx, root) {
			if err := resetGitBaseline(ctx, root, baselineContentsEmpty); err != nil {
				return err
			}
		}
		var err error
		diff, err = workspaceDiffLocked(ctx, root)
		return err
	})
	if err != nil {
		return GitBaselineDiff{}, err
	}
	return diff, nil
}

// workspaceDiffLocked bounds the snapshot to readable working files and
// invalidates a baseline whose tracked file grew past the limit, so the file
// cannot remain silently "already processed" (Rust #57240).
func workspaceDiffLocked(ctx context.Context, root string) (GitBaselineDiff, error) {
	changes, err := listBaselineChanges(ctx, root)
	if err != nil {
		return GitBaselineDiff{}, err
	}
	oversized := oversizedChangedPaths(root, changes)
	if len(oversized) > 0 {
		tracked, err := baselineTracksAny(ctx, root, oversized)
		if err != nil {
			return GitBaselineDiff{}, err
		}
		if tracked {
			if err := resetGitBaseline(ctx, root, baselineContentsEmpty); err != nil {
				return GitBaselineDiff{}, err
			}
			changes, err = listBaselineChanges(ctx, root)
			if err != nil {
				return GitBaselineDiff{}, err
			}
			oversized = oversizedChangedPaths(root, changes)
		}
	}
	retained := make([]GitBaselineChange, 0, len(changes))
	for _, change := range changes {
		if oversized[change.Path] {
			continue
		}
		retained = append(retained, change)
	}
	var unified strings.Builder
	for _, change := range retained {
		section, err := renderBaselineChange(ctx, root, change)
		if err != nil {
			return GitBaselineDiff{}, err
		}
		unified.WriteString(section)
	}
	return GitBaselineDiff{Changes: retained, UnifiedDiff: unified.String()}, nil
}

// oversizedChangedPaths reports working files above maxBaselineFileBytes.
// Deleted files have no working content, so they stay reportable from the
// baseline blob.
func oversizedChangedPaths(root string, changes []GitBaselineChange) map[string]bool {
	oversized := map[string]bool{}
	for _, change := range changes {
		if change.Status == GitBaselineDeleted {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(change.Path)))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > maxBaselineFileBytes {
			oversized[change.Path] = true
		}
	}
	return oversized
}

func baselineTracksAny(ctx context.Context, root string, paths map[string]bool) (bool, error) {
	for path := range paths {
		output, err := runBaselineGit(ctx, root, "ls-tree", "-z", "--name-only", "HEAD", "--", path)
		if err != nil {
			return false, fmt.Errorf("inspect baseline entry for %s: %w", path, err)
		}
		if len(splitNUL(output)) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func WriteWorkspaceDiff(root string, diff GitBaselineDiff) error {
	path := filepath.Join(root, WorkspaceDiffFilename)
	if err := os.WriteFile(path, []byte(RenderWorkspaceDiff(diff)), 0o666); err != nil {
		return fmt.Errorf("write memory workspace diff file %s: %w", path, err)
	}
	return nil
}

func ResetWorkspaceBaseline(ctx context.Context, root string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return withBaselineLock(ctx, root, func(root string) error {
		if err := removeWorkspaceDiff(root); err != nil {
			return err
		}
		return resetGitBaseline(ctx, root, baselineContentsCurrent)
	})
}

// MemoryStorageBytes sums the regular files under root from filesystem metadata,
// without reading their contents, and excludes git metadata and symbolic links
// (Rust `workspace::memory_storage_bytes`, #45956). Lstat is used rather than
// Stat, so a link contributes nothing and a link back to an ancestor cannot
// make the walk loop; the total saturates instead of wrapping.
func MemoryStorageBytes(root string) (int64, error) {
	var total int64
	pending := []string{root}
	for len(pending) > 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		info, err := os.Lstat(path)
		if err != nil {
			return 0, err
		}
		switch {
		case info.Mode().IsRegular():
			size := info.Size()
			if size > 0 && total > math.MaxInt64-size {
				total = math.MaxInt64
				continue
			}
			total += size
		case info.IsDir():
			entries, err := os.ReadDir(path)
			if err != nil {
				return 0, err
			}
			for _, entry := range entries {
				if entry.Name() != ".git" {
					pending = append(pending, filepath.Join(path, entry.Name()))
				}
			}
		}
	}
	return total, nil
}

func ValidateConsolidationArtifacts(root string) error {
	return ValidateConsolidationArtifactsForVersion(root, config.MemoryVersionV1)
}

// ValidateConsolidationArtifactsForVersion verifies the artifacts required by the
// selected memory version (Rust #43813): v1 requires MEMORY.md plus a summary
// starting with `v1`; v2 requires only memory_summary.md, with every required
// section and under 10,000 UTF-8 bytes.
func ValidateConsolidationArtifactsForVersion(root string, version config.MemoryVersion) error {
	if version != config.MemoryVersionV2 {
		memoryPath := filepath.Join(root, MemoryFilename)
		info, err := os.Stat(memoryPath)
		if err != nil {
			return fmt.Errorf("read consolidated memory artifact %s: %w", memoryPath, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("consolidated memory artifact is not a file: %s", memoryPath)
		}
	}
	summaryPath := filepath.Join(root, MemorySummaryFilename)
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		return fmt.Errorf("read memory summary artifact %s: %w", summaryPath, err)
	}
	firstLine := string(summary)
	if index := strings.IndexByte(firstLine, '\n'); index >= 0 {
		firstLine = strings.TrimSuffix(firstLine[:index], "\r")
	}
	if firstLine != "v1" {
		return fmt.Errorf("memory summary artifact does not start with v1: %s", summaryPath)
	}
	if version == config.MemoryVersionV2 {
		if len(summary) >= maxV2SummaryBytes {
			return fmt.Errorf("v2 memory summary must be under 10000 UTF-8 bytes")
		}
		lines := splitV2SummaryLines(string(summary))
		for _, heading := range v2SummaryHeadings {
			found := false
			for _, line := range lines {
				if strings.TrimSpace(line) == heading {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("v2 memory summary missing %s", heading)
			}
		}
	}
	return nil
}

func RenderWorkspaceDiff(diff GitBaselineDiff) string {
	var rendered strings.Builder
	rendered.WriteString("# Memory Workspace Diff\n\n")
	rendered.WriteString("Generated by Codex before Phase 2 memory consolidation. Read this file first and do not edit it.\n\n")
	rendered.WriteString("## Status\n")
	if !diff.HasChanges() {
		rendered.WriteString("- none\n")
		return rendered.String()
	}
	for _, change := range diff.Changes {
		fmt.Fprintf(&rendered, "- %s %s\n", change.Status, change.Path)
	}
	rendered.WriteString("\n## Diff\n\n```diff\n")
	appendBoundedWorkspaceDiff(&rendered, diff.UnifiedDiff)
	rendered.WriteString("```\n")
	return rendered.String()
}

func resetGitBaseline(ctx context.Context, root string, contents baselineContents) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create git baseline root %s: %w", root, err)
	}
	gitPath := filepath.Join(root, ".git")
	if err := discardBaselineGitDir(gitPath); err != nil {
		return err
	}
	if _, err := runBaselineGit(ctx, root, "init", "--quiet"); err != nil {
		return fmt.Errorf("init git repo %s: %w", root, err)
	}
	switch contents {
	case baselineContentsCurrent:
		// --force tracks files the workspace's own .gitignore excludes; the
		// lock control directory is excluded so it never enters the snapshot.
		// Oversized files are excluded before git reads them, matching Rust's
		// size check in write_tree.
		oversized, err := oversizedBaselineFiles(root)
		if err != nil {
			return err
		}
		args := []string{"add", "--all", "--force", "--", ".", ":(exclude)" + baselineControlDir}
		for _, path := range oversized {
			args = append(args, ":(exclude,literal)"+path)
		}
		if _, err := runBaselineGit(ctx, root, args...); err != nil {
			return fmt.Errorf("stage git baseline %s: %w", root, err)
		}
	default:
		if _, err := runBaselineGit(ctx, root, "read-tree", "--empty"); err != nil {
			return fmt.Errorf("prepare empty git baseline %s: %w", root, err)
		}
	}
	if _, err := runBaselineGit(ctx, root,
		"-c", "user.name=Codex", "-c", "user.email=noreply@openai.com",
		"commit", "--quiet", "--allow-empty", "--no-gpg-sign", "--no-verify", "-m", baselineCommitMessage,
	); err != nil {
		return fmt.Errorf("commit git baseline repo %s: %w", root, err)
	}
	if _, err := runBaselineGitWithEnv(ctx, root, baselineRepositoryEnv,
		"read-tree", "--reset", "HEAD",
	); err != nil {
		return fmt.Errorf("write git baseline index %s: %w", root, err)
	}
	versionPath := filepath.Join(gitPath, baselineVersionFile)
	if err := os.WriteFile(versionPath, []byte(baselineVersion), 0o644); err != nil {
		return fmt.Errorf("write git baseline version %s: %w", versionPath, err)
	}
	return nil
}

// oversizedBaselineFiles lists regular working files above maxBaselineFileBytes.
// Git stages whole blobs, so they must be excluded before `git add` reads them.
// Nested .git directories and the lock control directory are not snapshot input.
func oversizedBaselineFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == ".git" || entry.Name() == baselineControlDir) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() <= maxBaselineFileBytes {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan git baseline files %s: %w", root, err)
	}
	sort.Strings(paths)
	return paths, nil
}

// baselineRepositoryEnv pins every git path to this worktree, so an inherited
// GIT_DIR or a verbatim working directory cannot redirect the index write.
var baselineRepositoryEnv = []string{
	"GIT_DIR=.git",
	"GIT_COMMON_DIR=.git",
	"GIT_INDEX_FILE=.git/index",
	"GIT_OBJECT_DIRECTORY=.git/objects",
	"GIT_WORK_TREE=.",
}

// discardBaselineGitDir removes disposable .git metadata. The version marker is
// deleted first, so an interrupted reset cannot leave a trusted partial
// baseline behind.
func discardBaselineGitDir(gitPath string) error {
	info, err := os.Lstat(gitPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", gitPath, err)
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		if err := os.Remove(filepath.Join(gitPath, baselineVersionFile)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("invalidate %s: %w", gitPath, err)
		}
		if err := os.RemoveAll(gitPath); err != nil {
			return fmt.Errorf("remove %s: %w", gitPath, err)
		}
		return nil
	}
	if err := os.Remove(gitPath); err != nil {
		return fmt.Errorf("remove %s: %w", gitPath, err)
	}
	return nil
}

// baselineUsable requires the current baseline version marker and a HEAD tree;
// missing, outdated, or interrupted metadata is replaced by an empty baseline.
func baselineUsable(ctx context.Context, root string) bool {
	gitPath := filepath.Join(root, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	version, err := os.ReadFile(filepath.Join(gitPath, baselineVersionFile))
	if err != nil || string(version) != baselineVersion {
		return false
	}
	_, err = runBaselineGit(ctx, root, "rev-parse", "--verify", "HEAD^{tree}")
	return err == nil
}

func listBaselineChanges(ctx context.Context, root string) ([]GitBaselineChange, error) {
	tracked, err := runBaselineGit(ctx, root, "diff", "--name-status", "--no-renames", "-z", "HEAD", "--")
	if err != nil {
		return nil, fmt.Errorf("scan tracked memory workspace changes: %w", err)
	}
	byPath := map[string]GitBaselineChangeStatus{}
	parts := splitNUL(tracked)
	for index := 0; index+1 < len(parts); index += 2 {
		status := string(parts[index])
		path := filepath.ToSlash(string(parts[index+1]))
		switch {
		case strings.HasPrefix(status, "D"):
			byPath[path] = GitBaselineDeleted
		case strings.HasPrefix(status, "A"):
			byPath[path] = GitBaselineAdded
		default:
			byPath[path] = GitBaselineModified
		}
	}
	untracked, err := runBaselineGit(ctx, root, "ls-files", "--others", "--full-name", "-z")
	if err != nil {
		return nil, fmt.Errorf("scan untracked memory workspace files: %w", err)
	}
	for _, value := range splitNUL(untracked) {
		path := filepath.ToSlash(string(value))
		if path != "" {
			byPath[path] = GitBaselineAdded
		}
	}
	for path := range byPath {
		if isBaselineControlPath(path) {
			delete(byPath, path)
		}
	}
	changes := make([]GitBaselineChange, 0, len(byPath))
	for path, status := range byPath {
		changes = append(changes, GitBaselineChange{Status: status, Path: path})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

func isBaselineControlPath(path string) bool {
	path = strings.TrimPrefix(filepath.ToSlash(path), "./")
	return path == baselineControlDir || strings.HasPrefix(path, baselineControlDir+"/")
}

func renderBaselineChange(ctx context.Context, root string, change GitBaselineChange) (string, error) {
	if change.Status == GitBaselineAdded {
		data, mode, err := readWorkspaceEntry(filepath.Join(root, filepath.FromSlash(change.Path)))
		if errors.Is(err, errBaselineFileTooLarge) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("read current content for %s: %w", change.Path, err)
		}
		return renderAddedFileDiff(change.Path, mode, data), nil
	}
	data, err := runBaselineGit(ctx, root,
		"-c", "core.quotePath=false", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--unified=3", "HEAD", "--", change.Path,
	)
	if err != nil {
		return "", fmt.Errorf("render memory workspace diff for %s: %w", change.Path, err)
	}
	return normalizeGitDiff(data), nil
}

func renderAddedFileDiff(path, mode string, data []byte) string {
	text := strings.ToValidUTF8(string(data), "\uFFFD")
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "diff --git a/%s b/%s\n", path, path)
	fmt.Fprintf(&rendered, "new file mode %s\n", mode)
	fmt.Fprintf(&rendered, "--- /dev/null\n+++ b/%s\n", path)
	if len(text) == 0 {
		return rendered.String()
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 1 {
		rendered.WriteString("@@ -0,0 +1 @@\n")
	} else {
		fmt.Fprintf(&rendered, "@@ -0,0 +1,%d @@\n", len(lines))
	}
	for _, line := range lines {
		rendered.WriteByte('+')
		rendered.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			rendered.WriteByte('\n')
		}
	}
	return rendered.String()
}

func normalizeGitDiff(data []byte) string {
	text := strings.ToValidUTF8(string(data), "\uFFFD")
	var rendered strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(trimmed, "index ") || trimmed == `\ No newline at end of file` {
			continue
		}
		rendered.WriteString(line)
	}
	if rendered.Len() > 0 && !strings.HasSuffix(rendered.String(), "\n") {
		rendered.WriteByte('\n')
	}
	return rendered.String()
}

func readWorkspaceEntry(path string) ([]byte, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		return []byte(target), "120000", err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	// Reserve an EOF probe byte so a stable file needs no reallocation while a
	// file that grew during the read is still rejected (Rust #57240).
	data := make([]byte, 0, info.Size()+1)
	data, err = io.ReadAll(io.LimitReader(file, maxBaselineFileBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxBaselineFileBytes {
		return nil, "", errBaselineFileTooLarge
	}
	mode := "100644"
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 != 0 {
		mode = "100755"
	}
	return data, mode, nil
}

func removeWorkspaceDiff(root string) error {
	path := filepath.Join(root, WorkspaceDiffFilename)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove memory workspace diff file %s: %w", path, err)
	}
	return nil
}

func appendBoundedWorkspaceDiff(rendered *strings.Builder, diff string) {
	if len(diff) <= WorkspaceDiffMaxBytes {
		rendered.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			rendered.WriteByte('\n')
		}
		return
	}
	boundary := WorkspaceDiffMaxBytes
	for boundary > 0 && !isUTF8Boundary(diff, boundary) {
		boundary--
	}
	rendered.WriteString(diff[:boundary])
	if !strings.HasSuffix(rendered.String(), "\n") {
		rendered.WriteByte('\n')
	}
	fmt.Fprintf(rendered, "\n[workspace diff truncated at %d bytes]\n", WorkspaceDiffMaxBytes)
}

func isUTF8Boundary(value string, index int) bool {
	return index == 0 || index == len(value) || value[index]&0xc0 != 0x80
}

func splitNUL(data []byte) [][]byte {
	parts := bytes.Split(data, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func runBaselineGit(ctx context.Context, root string, args ...string) ([]byte, error) {
	return runBaselineGitWithEnv(ctx, root, nil, args...)
}

func runBaselineGitWithEnv(ctx context.Context, root string, extraEnv []string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=.codex-disabled-hooks"}, args...)...)
	command.Dir = root
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C", "GIT_TERMINAL_PROMPT=0")
	command.Env = append(command.Env, extraEnv...)
	// Rust #48483: the baseline probe is piped, so it must not allocate a console
	// window when the CLI itself has no console.
	envutil.SuppressConsoleWindow(command)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
