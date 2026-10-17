package worktree

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"codex_go/envutil"
)

const (
	worktreeOwnerFilename = "codex-thread.json"
	worktreeOwnerVersion  = 1
)

type worktreeOwnerRecord struct {
	Version       int    `json:"version"`
	OwnerThreadID string `json:"ownerThreadId"`
}

// ManagedWorktree describes a Desktop-compatible checkout and the working
// directory that should be used to start its thread.
type ManagedWorktree struct {
	Root       string
	CWD        string
	SourceRoot string
	SourceCWD  string
	HeadSHA    string
	Branch     *string
}

// WorktreeManager creates and lists managed linked worktrees using the
// existing Desktop contract.
type WorktreeManager struct {
	settings WorktreeSettings
}

// NewWorktreeManager returns a manager for the supplied worktree settings.
func NewWorktreeManager(settings WorktreeSettings) *WorktreeManager {
	return &WorktreeManager{settings: settings}
}

// Settings returns the manager's resolved worktree settings.
func (m *WorktreeManager) Settings() WorktreeSettings {
	if m == nil {
		return WorktreeSettings{}
	}
	return m.settings
}

// Create allocates a detached managed worktree from HEAD or an explicit base
// and preserves the source repository-relative working directory.
func (m *WorktreeManager) Create(sourceCWD string, base string) (ManagedWorktree, error) {
	if m == nil {
		return ManagedWorktree{}, fmt.Errorf("worktree manager is nil")
	}
	if !filepath.IsAbs(m.settings.Root) {
		return ManagedWorktree{}, fmt.Errorf("managed worktree root must be an absolute path")
	}
	sourceCWD, err := filepath.Abs(sourceCWD)
	if err != nil {
		return ManagedWorktree{}, err
	}
	sourceRoot, err := repositoryRoot(sourceCWD)
	if err != nil {
		return ManagedWorktree{}, err
	}
	relativeCWD, err := filepath.Rel(sourceRoot, sourceCWD)
	if err != nil || relativeCWD == ".." || strings.HasPrefix(relativeCWD, ".."+string(filepath.Separator)) {
		return ManagedWorktree{}, fmt.Errorf("working directory is outside the repository root")
	}
	revision := strings.TrimSpace(base)
	if revision == "" {
		revision = "HEAD"
	}
	headSHA, err := gitStdout(sourceRoot, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return ManagedWorktree{}, err
	}
	repoName := filepath.Base(sourceRoot)
	bucket, err := createManagedWorktreeBucket(m.settings.Root)
	if err != nil {
		return ManagedWorktree{}, err
	}
	root := filepath.Join(m.settings.Root, bucket, repoName)
	if _, err := gitOutput(sourceRoot, "worktree", "add", "--detach", "--no-checkout", root, headSHA); err != nil {
		_ = removeEmptyBucket(root)
		return ManagedWorktree{}, err
	}
	if _, err := gitOutput(root, "reset", "--hard", headSHA); err != nil {
		_, _ = gitOutput(sourceRoot, "worktree", "remove", "--force", root)
		_ = removeEmptyBucket(root)
		return ManagedWorktree{}, err
	}
	cwd := filepath.Join(root, relativeCWD)
	if !safeWorktreeCWD(root, cwd) {
		_, _ = gitOutput(sourceRoot, "worktree", "remove", "--force", root)
		_ = removeEmptyBucket(root)
		return ManagedWorktree{}, fmt.Errorf("requested base does not contain a safe working directory %s", relativeCWD)
	}
	return ManagedWorktree{
		Root:       root,
		CWD:        cwd,
		SourceRoot: sourceRoot,
		SourceCWD:  sourceCWD,
		HeadSHA:    headSHA,
	}, nil
}

// List returns managed linked worktrees associated with the repository
// containing sourceCWD, sorted by root.
func (m *WorktreeManager) List(sourceCWD string) ([]ManagedWorktree, error) {
	if m == nil {
		return nil, fmt.Errorf("worktree manager is nil")
	}
	sourceCWD, err := filepath.Abs(sourceCWD)
	if err != nil {
		return nil, err
	}
	sourceRoot, err := repositoryRoot(sourceCWD)
	if err != nil {
		return nil, err
	}
	relativeCWD, err := filepath.Rel(sourceRoot, sourceCWD)
	if err != nil || relativeCWD == ".." || strings.HasPrefix(relativeCWD, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("working directory is outside the repository root")
	}
	sourceCommonDir, err := gitStdout(sourceRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	output, err := gitOutput(sourceRoot, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	managedRoot := m.settings.Root
	if resolved, resolveErr := filepath.EvalSymlinks(managedRoot); resolveErr == nil {
		managedRoot = resolved
	}
	var worktrees []ManagedWorktree
	for _, entry := range bytes.Split(output, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		if !bytes.HasPrefix(entry, []byte("worktree ")) {
			continue
		}
		root := strings.TrimSpace(strings.TrimPrefix(string(entry), "worktree "))
		if root == "" {
			continue
		}
		root = filepath.Clean(filepath.FromSlash(root))
		if !hasManagedLayout(managedRoot, root) {
			continue
		}
		info, statErr := os.Stat(filepath.Join(root, ".git"))
		if statErr != nil || info.IsDir() {
			continue
		}
		commonDir, gitErr := gitStdout(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if gitErr != nil || !samePath(commonDir, sourceCommonDir) {
			continue
		}
		cwd := filepath.Join(root, relativeCWD)
		if !safeWorktreeCWD(root, cwd) {
			continue
		}
		headSHA, headErr := gitStdout(root, "rev-parse", "HEAD")
		if headErr != nil {
			continue
		}
		worktrees = append(worktrees, ManagedWorktree{
			Root:       root,
			CWD:        cwd,
			SourceRoot: sourceRoot,
			SourceCWD:  sourceCWD,
			HeadSHA:    headSHA,
		})
	}
	sort.Slice(worktrees, func(i, j int) bool { return worktrees[i].Root < worktrees[j].Root })
	return worktrees, nil
}

// Owner returns the thread ID bound to a managed worktree, or an empty string
// when no owner is recorded.
func (m *WorktreeManager) Owner(checkout string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("worktree manager is nil")
	}
	checkout, err := m.managedCheckout(checkout)
	if err != nil {
		return "", err
	}
	path, err := worktreeMetadataPath(checkout)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var record worktreeOwnerRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return "", fmt.Errorf("invalid worktree owner at %s", path)
	}
	if record.Version != worktreeOwnerVersion || record.OwnerThreadID == "" {
		return "", fmt.Errorf("invalid worktree owner at %s", path)
	}
	return record.OwnerThreadID, nil
}

// BindThread atomically binds a managed worktree to a thread ID without
// replacing another owner.
func (m *WorktreeManager) BindThread(checkout string, threadID string) error {
	if m == nil {
		return fmt.Errorf("worktree manager is nil")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return fmt.Errorf("worktree owner thread ID cannot be empty")
	}
	checkout, err := m.managedCheckout(checkout)
	if err != nil {
		return err
	}
	if existing, err := m.Owner(checkout); err != nil {
		return err
	} else if existing == threadID {
		return nil
	} else if existing != "" {
		return fmt.Errorf("worktree already belongs to thread %s", existing)
	}
	path, err := worktreeMetadataPath(checkout)
	if err != nil {
		return err
	}
	record := worktreeOwnerRecord{Version: worktreeOwnerVersion, OwnerThreadID: threadID}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".codex-thread-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if os.IsExist(err) {
			if existing, ownerErr := m.Owner(checkout); ownerErr == nil && existing == threadID {
				return nil
			}
			return fmt.Errorf("worktree was concurrently assigned to another thread")
		}
		return err
	}
	return nil
}

// Remove force-removes a managed worktree and its empty allocation bucket.
func (m *WorktreeManager) Remove(checkout string) error {
	if m == nil {
		return fmt.Errorf("worktree manager is nil")
	}
	sourceRoot, err := repositoryRoot(checkout)
	if err != nil {
		return err
	}
	if _, err := gitOutput(sourceRoot, "worktree", "remove", "--force", checkout); err != nil {
		_ = os.RemoveAll(checkout)
		return removeEmptyBucket(checkout)
	}
	return removeEmptyBucket(checkout)
}

// RemoveManaged removes a currently registered managed worktree from the
// repository containing sourceCWD, refusing the current checkout (including
// path aliases) and any checkout with local changes, untracked files, or
// ignored files (Rust #43942). Thread history is preserved.
func (m *WorktreeManager) RemoveManaged(sourceCWD string, root string) error {
	if m == nil {
		return fmt.Errorf("worktree manager is nil")
	}
	checkouts, err := m.List(sourceCWD)
	if err != nil {
		return err
	}
	var checkout *ManagedWorktree
	for i := range checkouts {
		if checkouts[i].Root == root {
			checkout = &checkouts[i]
			break
		}
	}
	if checkout == nil {
		return fmt.Errorf("%s is not a managed worktree in this repository", root)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourceCWD)
	if err != nil {
		return fmt.Errorf("cannot resolve current directory %s", sourceCWD)
	}
	resolvedRoot, err := filepath.EvalSymlinks(checkout.Root)
	if err != nil {
		return fmt.Errorf("cannot resolve worktree %s", checkout.Root)
	}
	if pathWithin(resolvedRoot, resolvedSource) {
		return errors.New("switch to another checkout before deleting the current worktree")
	}
	// git worktree remove refuses dirty or untracked files but not ignored ones.
	ignored, err := gitOutput(checkout.Root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	if len(ignored) > 0 {
		return errors.New("worktree contains ignored local files; remove them before deleting it")
	}
	if _, err := gitOutput(checkout.SourceRoot, "worktree", "remove", root); err != nil {
		return err
	}
	return removeEmptyBucket(root)
}

// pathWithin reports whether child is parent or lives inside parent. Both
// paths are expected to be canonical.
func pathWithin(parent string, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func (m *WorktreeManager) managedCheckout(checkout string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("worktree manager is nil")
	}
	managedRoot, err := filepath.Abs(m.settings.Root)
	if err != nil {
		return "", err
	}
	managedRoot, err = filepath.EvalSymlinks(managedRoot)
	if err != nil {
		return "", fmt.Errorf("cannot resolve managed worktree root %s", m.settings.Root)
	}
	managedRoot, err = filepath.Abs(managedRoot)
	if err != nil {
		return "", err
	}
	checkout, err = filepath.Abs(checkout)
	if err != nil {
		return "", err
	}
	checkout, err = filepath.EvalSymlinks(checkout)
	if err != nil {
		return "", fmt.Errorf("cannot resolve worktree %s", checkout)
	}
	checkout, err = filepath.Abs(checkout)
	if err != nil {
		return "", err
	}
	if !hasManagedLayout(managedRoot, checkout) {
		return "", fmt.Errorf("%s is not a managed worktree", checkout)
	}
	root, err := repositoryRoot(checkout)
	if err != nil {
		return "", err
	}
	if !samePath(root, checkout) {
		return "", fmt.Errorf("%s is not a worktree root", checkout)
	}
	gitDir, err := resolveGitPath(checkout, "--git-dir")
	if err != nil {
		return "", err
	}
	commonDir, err := resolveGitPath(checkout, "--git-common-dir")
	if err != nil {
		return "", err
	}
	if samePath(gitDir, commonDir) {
		return "", fmt.Errorf("%s is not a linked worktree", checkout)
	}
	return checkout, nil
}

func resolveGitPath(checkout string, argument string) (string, error) {
	path, err := gitPathStdout(checkout, "rev-parse", argument)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(checkout, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve Git path %s", path)
	}
	return filepath.Abs(resolved)
}

func createManagedWorktreeBucket(root string) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	for range 16 {
		var random [2]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		bucket := hex.EncodeToString(random[:])
		if err := os.Mkdir(filepath.Join(root, bucket), 0o700); err == nil {
			return bucket, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("cannot allocate managed worktree bucket")
}

func worktreeMetadataPath(checkout string) (string, error) {
	relative, err := gitPathStdout(checkout, "rev-parse", "--git-path", worktreeOwnerFilename)
	if err != nil {
		return "", err
	}
	relative = filepath.Clean(relative)
	if filepath.IsAbs(relative) {
		return relative, nil
	}
	return filepath.Join(checkout, relative), nil
}

func repositoryRoot(cwd string) (string, error) {
	root, err := gitPathStdout(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", fmt.Errorf("git repository root is empty")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func gitStdout(dir string, args ...string) (string, error) {
	output, err := gitOutput(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func gitPathStdout(dir string, args ...string) (string, error) {
	output, err := gitOutput(dir, args...)
	if err != nil {
		return "", err
	}
	return gitPathFromBytes(output)
}

func gitPathFromBytes(output []byte) (string, error) {
	output = bytes.TrimSuffix(output, []byte("\n"))
	if os.PathSeparator == '\\' {
		output = bytes.TrimSuffix(output, []byte("\r"))
	}
	if len(output) == 0 {
		return "", fmt.Errorf("git returned an empty path")
	}
	return string(output), nil
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	commandArgs := append([]string{
		"-c", "safe.bareRepository=explicit",
		"-c", "core.hooksPath=" + isolatedGitHooksPath(),
		"-c", "core.fsmonitor=",
		"-c", "attr.tree=",
		"-c", "core.attributesFile=",
	}, args...)
	cmd := exec.Command("git", commandArgs...)
	cmd.Dir = dir
	cmd.Env = isolatedGitEnv()
	// Rust #48483: worktree probes are piped, so they must not allocate a console
	// window when the app server itself has no console.
	envutil.SuppressConsoleWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return output, nil
}

func isolatedGitEnv() []string {
	blocked := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_CONFIG": true, "GIT_CONFIG_PARAMETERS": true, "GIT_CONFIG_COUNT": true,
		"GIT_CONFIG_SYSTEM": true, "GIT_CONFIG_GLOBAL": true,
		"GIT_CEILING_DIRECTORIES": true, "GIT_DISCOVERY_ACROSS_FILESYSTEM": true,
		"GIT_IMPLICIT_WORK_TREE": true, "GIT_GRAFT_FILE": true,
		"GIT_NO_REPLACE_OBJECTS": true, "GIT_REPLACE_REF_BASE": true,
		"GIT_PREFIX": true, "GIT_SHALLOW_FILE": true, "GIT_COMMON_DIR": true,
	}
	out := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		if !blocked[key] {
			out = append(out, entry)
		}
	}
	return append(out,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_DISCOVERY_ACROSS_FILESYSTEM=0",
	)
}

func isolatedGitHooksPath() string {
	if os.PathSeparator == '\\' {
		return "NUL"
	}
	return "/dev/null"
}

func hasManagedLayout(managedRoot, root string) bool {
	relative, err := filepath.Rel(managedRoot, root)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	if len(parts) != 2 || len(parts[0]) != 4 {
		return false
	}
	for _, character := range parts[0] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return parts[1] != "" && parts[1] != "." && parts[1] != ".."
}

func safeWorktreeCWD(root, cwd string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return false
	}
	info, err := os.Stat(resolvedCWD)
	if err != nil || !info.IsDir() {
		return false
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedCWD)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// PathsEqual reports whether two checkout paths name the same directory,
// resolving symlinks when both exist (Rust's cwds_differ comparison).
func PathsEqual(left string, right string) bool {
	return samePath(left, right)
}

func samePath(left, right string) bool {
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	resolvedLeft, leftErr := filepath.EvalSymlinks(left)
	resolvedRight, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(resolvedLeft) == filepath.Clean(resolvedRight)
}

func removeEmptyBucket(root string) error {
	bucket := filepath.Dir(root)
	_ = os.Remove(root)
	return os.Remove(bucket)
}
