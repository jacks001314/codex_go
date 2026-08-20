package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const InstalledMarketplacesDir = ".tmp/marketplaces"
const InstalledMarketplacePluginsDir = "plugins"

// marketplaceInstallStateFilename keeps activated marketplace revision state
// out of config.toml (Rust #39595).
const marketplaceInstallStateFilename = ".codex-marketplace-install.json"

type marketplaceInstallState struct {
	LastUpdated  string `json:"last_updated"`
	LastRevision string `json:"last_revision"`
}

func installedMarketplaceRevision(root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, marketplaceInstallStateFilename))
	if err != nil {
		return ""
	}
	var state marketplaceInstallState
	if err := json.Unmarshal(data, &state); err != nil {
		return ""
	}
	return strings.TrimSpace(state.LastRevision)
}

func writeMarketplaceInstallState(root string, revision string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	state := marketplaceInstallState{
		LastUpdated:  time.Now().UTC().Format(time.RFC3339),
		LastRevision: strings.TrimSpace(revision),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, marketplaceInstallStateFilename), data, 0o600)
}

type MarketplaceMaterializer interface {
	MaterializeMarketplace(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error
}

type MarketplacePluginMaterializer interface {
	MaterializeMarketplacePlugin(source *ParsedMarketplaceSource, destination string) error
}

type MarketplaceUpgrader interface {
	UpgradeMarketplace(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error
}

type MarketplaceRevisionResolver interface {
	MarketplaceRevision(source *ParsedMarketplaceSource) (string, error)
}

type MarketplaceMaterializerFunc func(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error

func (f MarketplaceMaterializerFunc) MaterializeMarketplace(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error {
	return f(source, sparsePaths, destination)
}

type MarketplacePluginMaterializerFunc func(source *ParsedMarketplaceSource, destination string) error

func (f MarketplacePluginMaterializerFunc) MaterializeMarketplacePlugin(source *ParsedMarketplaceSource, destination string) error {
	return f(source, destination)
}

type GitMarketplaceMaterializer struct{}

type GitMarketplaceRevisionResolver struct{}

type MarketplaceRevisionResolverFunc func(source *ParsedMarketplaceSource) (string, error)

func (f MarketplaceRevisionResolverFunc) MarketplaceRevision(source *ParsedMarketplaceSource) (string, error) {
	return f(source)
}

func defaultMarketplaceInstallRoot() string {
	return filepath.Join(InstalledMarketplacesDir)
}

func (m *GitMarketplaceMaterializer) MaterializeMarketplace(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error {
	return materializeGitMarketplace(source, sparsePaths, destination, false)
}

func (m *GitMarketplaceMaterializer) MaterializeMarketplacePlugin(source *ParsedMarketplaceSource, destination string) error {
	return materializeGitMarketplace(source, nil, destination, false)
}

func (m *GitMarketplaceMaterializer) UpgradeMarketplace(source *ParsedMarketplaceSource, sparsePaths []string, destination string) error {
	return materializeGitMarketplace(source, sparsePaths, destination, true)
}

func materializeGitMarketplace(source *ParsedMarketplaceSource, sparsePaths []string, destination string, replaceExisting bool) error {
	if source == nil || source.Kind != MarketplaceSourceGit {
		return nil
	}
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("%w: marketplace install destination is required", ErrInvalidPluginRequest)
	}
	if info, err := os.Stat(destination); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%w: marketplace install destination exists and is not a directory", ErrInvalidPluginRequest)
		}
		if !replaceExisting {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat marketplace install destination %s: %w", destination, err)
	}

	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("failed to create marketplace install directory %s: %w", parent, err)
	}
	stagingParent := filepath.Join(parent, ".staging")
	if err := os.MkdirAll(stagingParent, 0o755); err != nil {
		return fmt.Errorf("failed to create marketplace staging directory %s: %w", stagingParent, err)
	}
	stagedRoot, err := os.MkdirTemp(stagingParent, "marketplace-add-")
	if err != nil {
		return fmt.Errorf("failed to create temporary marketplace directory in %s: %w", stagingParent, err)
	}
	keepStaged := false
	defer func() {
		if !keepStaged {
			_ = os.RemoveAll(stagedRoot)
		}
	}()

	if err := cloneGitMarketplaceSource(source.URL, source.RefName, sparsePaths, stagedRoot); err != nil {
		return err
	}
	if _, err := os.Stat(destination); err == nil {
		if !replaceExisting {
			return fmt.Errorf("%w: marketplace install destination already exists", ErrInvalidPluginRequest)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat marketplace install destination %s: %w", destination, err)
	}
	if replaceExisting {
		if err := replaceMarketplaceDestination(stagedRoot, destination); err != nil {
			return err
		}
		keepStaged = true
		return nil
	}
	if err := os.Rename(stagedRoot, destination); err != nil {
		return fmt.Errorf("failed to install marketplace at %s: %w", destination, err)
	}
	keepStaged = true
	return nil
}

func removeMaterializedMarketplacePlugins(installRoot string, marketplaceName string) error {
	installRoot = strings.TrimSpace(installRoot)
	marketplaceName = strings.TrimSpace(marketplaceName)
	if installRoot == "" || marketplaceName == "" {
		return nil
	}
	root := filepath.Join(installRoot, InstalledMarketplacePluginsDir, sanitize(marketplaceName))
	return os.RemoveAll(root)
}

func replaceMarketplaceDestination(stagedRoot string, destination string) error {
	backup := destination + ".old"
	if _, err := os.Stat(destination); err == nil {
		_ = os.RemoveAll(backup)
		if err := os.Rename(destination, backup); err != nil {
			return fmt.Errorf("failed to move existing marketplace at %s: %w", destination, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat marketplace install destination %s: %w", destination, err)
	}
	if err := os.Rename(stagedRoot, destination); err != nil {
		if _, backupErr := os.Stat(backup); backupErr == nil {
			_ = os.Rename(backup, destination)
		}
		return fmt.Errorf("failed to install marketplace at %s: %w", destination, err)
	}
	_ = os.RemoveAll(backup)
	return nil
}

func (r *GitMarketplaceRevisionResolver) MarketplaceRevision(source *ParsedMarketplaceSource) (string, error) {
	if source == nil || source.Kind != MarketplaceSourceGit {
		return "", nil
	}
	ref := "HEAD"
	if source.RefName != nil && strings.TrimSpace(*source.RefName) != "" {
		ref = strings.TrimSpace(*source.RefName)
	}
	output, err := runMarketplaceGitOutput(nil, "ls-remote", source.URL, ref)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return "", fmt.Errorf("git ls-remote returned no revision for %s", source.Display())
	}
	return fields[0], nil
}

func cloneGitMarketplaceSource(url string, refName *string, sparsePaths []string, destination string) error {
	if len(sparsePaths) == 0 {
		if err := runMarketplaceGit(nil, "clone", url, destination); err != nil {
			return err
		}
		if refName != nil && strings.TrimSpace(*refName) != "" {
			if err := runMarketplaceGit(&destination, "checkout", strings.TrimSpace(*refName)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := runMarketplaceGit(nil, "clone", "--filter=blob:none", "--no-checkout", url, destination); err != nil {
		return err
	}
	args := append([]string{"sparse-checkout", "set"}, sparsePaths...)
	if err := runMarketplaceGit(&destination, args...); err != nil {
		return err
	}
	checkoutRef := "HEAD"
	if refName != nil && strings.TrimSpace(*refName) != "" {
		checkoutRef = strings.TrimSpace(*refName)
	}
	return runMarketplaceGit(&destination, "checkout", checkoutRef)
}

func runMarketplaceGit(cwd *string, args ...string) error {
	_, err := runMarketplaceGitOutput(cwd, args...)
	return err
}

func runMarketplaceGitOutput(cwd *string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if cwd != nil {
		command.Dir = *cwd
	}
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), nil
	}
	return "", fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
}
