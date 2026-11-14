package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// configureTrustedGitRepository selects a lock-owned repository under an
// explicitly supplied Codex home. The caller must close the guard after Git exits.
func configureTrustedGitRepository(command *exec.Cmd, codexHome string) (*MarketplaceStagingDir, error) {
	home, err := filepath.Abs(codexHome)
	if err != nil || strings.TrimSpace(codexHome) == "" {
		return nil, fmt.Errorf("trusted Codex home must be specified")
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("resolve trusted Codex home: %w", err)
	}
	root := filepath.Join(home, ".tmp")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(home, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("trusted Git repository root escapes Codex home")
	}
	if info, err := os.Lstat(filepath.Join(root, marketplaceStagingDirName)); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("trusted Git staging directory is a symlink")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	cleanupMarketplaceStaging(root, time.Now())
	repository, err := newMarketplaceStagingDir(root)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			repository.Close()
		}
	}()
	for _, dir := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(repository.Path(), dir), 0700); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(repository.Path(), "HEAD"), []byte("ref: refs/heads/main\n"), 0600); err != nil {
		return nil, err
	}
	env := command.Env
	if env == nil {
		env = os.Environ()
	}
	filtered := make([]string, 0, len(env)+1)
	for _, pair := range env {
		name, _, _ := strings.Cut(pair, "=")
		if !strings.EqualFold(name, "GIT_DIR") {
			filtered = append(filtered, pair)
		}
	}
	command.Env = append(filtered, "GIT_DIR="+repository.Path())
	ok = true
	return repository, nil
}
