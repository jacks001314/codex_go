package worktree

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
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
	root := filepath.Join(m.settings.Root, repoName, uuid.NewString())
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
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			if existing, ownerErr := m.Owner(checkout); ownerErr == nil && existing == threadID {
				return nil
			}
			return fmt.Errorf("worktree was concurrently assigned to another thread")
		}
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func worktreeMetadataPath(checkout string) (string, error) {
	relative, err := gitStdout(checkout, "rev-parse", "--git-path", worktreeOwnerFilename)
	if err != nil {
		return "", err
	}
	relative = filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(relative) {
		return relative, nil
	}
	return filepath.Join(checkout, relative), nil
}

func repositoryRoot(cwd string) (string, error) {
	root, err := gitStdout(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("git repository root is empty")
	}
	return filepath.Abs(root)
}

func gitStdout(dir string, args ...string) (string, error) {
	output, err := gitOutput(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = isolatedGitEnv()
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
	hooksPath := "/dev/null"
	if os.PathSeparator == '\\' {
		hooksPath = "NUL"
	}
	return append(out,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_DISCOVERY_ACROSS_FILESYSTEM=0",
		"GIT_HOOKS_PATH="+hooksPath,
	)
}

func hasManagedLayout(managedRoot, root string) bool {
	managedRoot = filepath.Clean(managedRoot)
	root = filepath.Clean(root)
	relative, err := filepath.Rel(managedRoot, root)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	return true
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
