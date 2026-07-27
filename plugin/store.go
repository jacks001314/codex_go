package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	// DefaultPluginVersion is the version used when a plugin has no explicit version.
	DefaultPluginVersion = "local"

	// PluginsCacheDir is the directory under codex home for installed plugin files.
	PluginsCacheDir = "plugins/cache"

	// PluginsDataDir is the directory under codex home for plugin runtime data.
	PluginsDataDir = "plugins/data"

	// RemotePluginInstallMetadataFile is the filename for remote plugin identifier metadata.
	RemotePluginInstallMetadataFile = ".codex-remote-plugin-install.json"

	// RemotePluginInstallMetadataSchemaVersion is the current schema version for remote metadata.
	RemotePluginInstallMetadataSchemaVersion = 1
)

// PluginInstallResult holds the result of a plugin installation into the store.
type PluginInstallResult struct {
	PluginID      *PluginId
	PluginVersion string
	InstalledPath string
}

// PluginStoreError represents an error from the PluginStore.
type PluginStoreError struct {
	message string
	cause   error
}

func (e *PluginStoreError) Error() string {
	if e.cause != nil {
		return e.message + ": " + e.cause.Error()
	}
	return e.message
}

func (e *PluginStoreError) Unwrap() error {
	return e.cause
}

// IsNotFound returns true if the error is because a plugin was not found.
func (e *PluginStoreError) NotFound() bool {
	return strings.Contains(e.Error(), "is not installed")
}

func newPluginStoreError(msg string, cause error) *PluginStoreError {
	return &PluginStoreError{message: msg, cause: cause}
}

func pluginStoreIOError(context string, cause error) *PluginStoreError {
	return newPluginStoreError("plugin store io error: "+context, cause)
}

func pluginStoreInvalidError(msg string) *PluginStoreError {
	return newPluginStoreError(msg, nil)
}

// RemotePluginInstallMetadata stores the remote identity of an installed plugin.
type remotePluginInstallMetadata struct {
	SchemaVersion  uint8  `json:"schema_version"`
	RemotePluginID string `json:"remote_plugin_id"`
}

// PluginStore is a persistent registry of installed plugins with version tracking.
//
// Installed plugins are stored on disk under the cache directory with the layout:
//
//	<root>/<marketplace_name>/<plugin_name>/<version>/
//
// Active version selection prefers "local" (the default version), then the highest
// semver-comparable version. Remote plugins store their remote identity in a
// .codex-remote-plugin-install.json metadata file at the plugin base root.
type PluginStore struct {
	codexHome string
	root      string
	dataRoot  string
}

// NewPluginStore creates a PluginStore rooted at the given codex home directory.
func NewPluginStore(codexHome string) (*PluginStore, error) {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil, pluginStoreInvalidError("codex home must not be empty")
	}
	root := filepath.Join(codexHome, PluginsCacheDir)
	dataRoot := filepath.Join(codexHome, PluginsDataDir)
	return &PluginStore{
		codexHome: codexHome,
		root:      root,
		dataRoot:  dataRoot,
	}, nil
}

// Root returns the cache root directory.
func (s *PluginStore) Root() string {
	return s.root
}

// CodexHome returns the codex home directory.
func (s *PluginStore) CodexHome() string {
	return s.codexHome
}

// DataRoot returns the runtime data root directory.
func (s *PluginStore) DataRoot() string {
	return s.dataRoot
}

// PluginBaseRoot returns the base directory for a plugin (without version suffix).
func (s *PluginStore) PluginBaseRoot(pluginID *PluginId) string {
	if pluginID == nil {
		return ""
	}
	return filepath.Join(s.root, pluginID.MarketplaceName, pluginID.PluginName)
}

// PluginRoot returns the versioned directory for a plugin.
func (s *PluginStore) PluginRoot(pluginID *PluginId, pluginVersion string) string {
	if pluginID == nil {
		return ""
	}
	return filepath.Join(s.PluginBaseRoot(pluginID), pluginVersion)
}

// PluginDataRoot returns the runtime data directory for a plugin.
func (s *PluginStore) PluginDataRoot(pluginID *PluginId) string {
	if pluginID == nil {
		return ""
	}
	return filepath.Join(s.dataRoot, pluginID.PluginName+"-"+pluginID.MarketplaceName)
}

// ActivePluginVersion returns the currently active version for a plugin.
// Prefers "local" as the default version, otherwise picks the highest semver-comparable version.
func (s *PluginStore) ActivePluginVersion(pluginID *PluginId) (string, bool) {
	if pluginID == nil {
		return "", false
	}
	baseRoot := s.PluginBaseRoot(pluginID)
	entries, err := os.ReadDir(baseRoot)
	if err != nil {
		return "", false
	}
	var versions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		version := entry.Name()
		if err := ValidatePluginVersionSegment(version); err != nil {
			continue
		}
		versions = append(versions, version)
	}
	if len(versions) == 0 {
		return "", false
	}

	// Prefer "local"
	for _, v := range versions {
		if v == DefaultPluginVersion {
			return DefaultPluginVersion, true
		}
	}
	// Pick highest by semver, fallback to lexicographic
	sort.SliceStable(versions, func(i, j int) bool {
		return comparePluginVersions(versions[i], versions[j]) >= 0
	})
	return versions[0], true
}

// ActivePluginRoot returns the root directory for the currently active plugin version.
func (s *PluginStore) ActivePluginRoot(pluginID *PluginId) (string, bool) {
	version, ok := s.ActivePluginVersion(pluginID)
	if !ok {
		return "", false
	}
	return s.PluginRoot(pluginID, version), true
}

// IsInstalled returns whether a plugin is currently installed.
func (s *PluginStore) IsInstalled(pluginID *PluginId) bool {
	_, ok := s.ActivePluginVersion(pluginID)
	return ok
}

// RemotePluginID reads the remote plugin identifier from the installed plugin's metadata.
func (s *PluginStore) RemotePluginID(pluginID *PluginId) (string, error) {
	if !s.IsInstalled(pluginID) {
		return "", pluginStoreInvalidError(fmt.Sprintf("plugin %q is not installed", pluginID.Key()))
	}
	path := s.remotePluginInstallMetadataPath(pluginID)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", pluginStoreIOError("failed to read remote plugin install metadata", err)
	}
	var metadata remotePluginInstallMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", pluginStoreInvalidError(fmt.Sprintf("failed to parse remote plugin install metadata: %v", err))
	}
	if metadata.SchemaVersion != RemotePluginInstallMetadataSchemaVersion {
		return "", pluginStoreInvalidError(fmt.Sprintf(
			"unsupported remote plugin install metadata schema version: %d", metadata.SchemaVersion))
	}
	remoteID := strings.TrimSpace(metadata.RemotePluginID)
	if remoteID == "" {
		return "", pluginStoreInvalidError("invalid remote plugin install metadata: remote plugin id must not be blank")
	}
	return remoteID, nil
}

// WriteRemotePluginID writes the remote plugin identifier to the installed plugin's metadata.
func (s *PluginStore) WriteRemotePluginID(pluginID *PluginId, remotePluginID string) error {
	if !s.IsInstalled(pluginID) {
		return pluginStoreInvalidError(fmt.Sprintf(
			"cannot write remote identity for uninstalled plugin %q", pluginID.Key()))
	}
	remotePluginID = strings.TrimSpace(remotePluginID)
	if remotePluginID == "" {
		return pluginStoreInvalidError("invalid remote plugin install metadata: remote plugin id must not be blank")
	}
	path := s.remotePluginInstallMetadataPath(pluginID)
	parent := filepath.Dir(path)
	data, err := json.MarshalIndent(remotePluginInstallMetadata{
		SchemaVersion:  RemotePluginInstallMetadataSchemaVersion,
		RemotePluginID: remotePluginID,
	}, "", "  ")
	if err != nil {
		return pluginStoreInvalidError(fmt.Sprintf("failed to serialize remote plugin install metadata: %v", err))
	}
	data = append(data, '\n')
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return pluginStoreIOError("failed to create remote plugin metadata directory", err)
	}
	// Write atomically via temp file
	tmp, err := os.CreateTemp(parent, ".codex-remote-plugin-install-")
	if err != nil {
		return pluginStoreIOError("failed to create temporary remote plugin install metadata", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return pluginStoreIOError("failed to write remote plugin install metadata", err)
	}
	tmp.Close()
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return pluginStoreIOError("failed to persist remote plugin install metadata", err)
	}
	return nil
}

// Install copies a plugin source directory into the store, determining the version from
// the plugin manifest. Returns the installed plugin id, version, and path.
func (s *PluginStore) Install(sourcePath string, pluginID *PluginId) (*PluginInstallResult, error) {
	return s.installWithManifest(sourcePath, pluginID, nil)
}

// InstallWithVersion copies a plugin source directory into the store with an explicit version.
func (s *PluginStore) InstallWithVersion(sourcePath string, pluginID *PluginId, pluginVersion string) (*PluginInstallResult, error) {
	return s.installWithVersionAndManifest(sourcePath, pluginID, pluginVersion, nil)
}

func (s *PluginStore) installWithManifest(sourcePath string, pluginID *PluginId, fallbackManifest []byte) (*PluginInstallResult, error) {
	manifest := resolveInstallManifest(sourcePath, fallbackManifest)
	pluginVersion, err := pluginVersionForInstallManifest(sourcePath, manifest)
	if err != nil {
		return nil, err
	}
	return s.installWithVersionAndManifest(sourcePath, pluginID, pluginVersion, manifest)
}

func (s *PluginStore) installWithVersionAndManifest(sourcePath string, pluginID *PluginId, pluginVersion string, fallbackManifest []byte) (*PluginInstallResult, error) {
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return nil, pluginStoreIOError("failed to stat plugin source path", err)
	}
	if !sourceInfo.IsDir() {
		return nil, pluginStoreInvalidError(fmt.Sprintf("plugin source path is not a directory: %s", sourcePath))
	}

	manifest := resolveInstallManifest(sourcePath, fallbackManifest)
	pluginName, err := pluginNameForSource(sourcePath, manifest)
	if err != nil {
		return nil, err
	}
	if pluginName != pluginID.PluginName {
		return nil, pluginStoreInvalidError(fmt.Sprintf(
			"plugin.json name %q does not match marketplace plugin name %q", pluginName, pluginID.PluginName))
	}
	if err := ValidatePluginVersionSegment(pluginVersion); err != nil {
		return nil, pluginStoreInvalidError(err.Error())
	}

	installedPath := s.PluginRoot(pluginID, pluginVersion)
	if err := replacePluginRootAtomically(sourcePath, s.PluginBaseRoot(pluginID), pluginVersion, fallbackManifest); err != nil {
		return nil, err
	}
	// Remove any stale remote metadata
	s.removeRemotePluginInstallMetadata(pluginID)

	return &PluginInstallResult{
		PluginID:      pluginID.Clone(),
		PluginVersion: pluginVersion,
		InstalledPath: installedPath,
	}, nil
}

// Uninstall removes a plugin from the store.
func (s *PluginStore) Uninstall(pluginID *PluginId) error {
	if pluginID == nil {
		return nil
	}
	return removeExistingTarget(s.PluginBaseRoot(pluginID))
}

func (s *PluginStore) remotePluginInstallMetadataPath(pluginID *PluginId) string {
	return filepath.Join(s.PluginBaseRoot(pluginID), RemotePluginInstallMetadataFile)
}

func (s *PluginStore) removeRemotePluginInstallMetadata(pluginID *PluginId) error {
	path := s.remotePluginInstallMetadataPath(pluginID)
	if err := os.Remove(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return pluginStoreIOError("failed to remove remote plugin install metadata", err)
	}
	return nil
}

// PluginVersionForSource retrieves the version from a plugin's manifest at the given source path.
func PluginVersionForSource(sourcePath string) (string, error) {
	return pluginVersionForInstallManifest(sourcePath, nil)
}

func pluginVersionForInstallManifest(sourcePath string, fallbackManifest []byte) (string, error) {
	version, err := pluginManifestVersionForSource(sourcePath, fallbackManifest)
	if err != nil {
		return "", err
	}
	if version == "" {
		version = DefaultPluginVersion
	}
	if err := ValidatePluginVersionSegment(version); err != nil {
		return "", pluginStoreInvalidError(err.Error())
	}
	return version, nil
}

// ValidatePluginVersionSegment validates a plugin version string.
// The version must not be empty, must not be "." or "..", and must contain only
// ASCII alphanumeric, '.', '+', '_', and '-' characters.
func ValidatePluginVersionSegment(version string) error {
	if version == "" {
		return fmt.Errorf("invalid plugin version: must not be empty")
	}
	if version == "." || version == ".." {
		return fmt.Errorf("invalid plugin version: path traversal is not allowed")
	}
	for _, ch := range version {
		if ch > 127 {
			return fmt.Errorf("invalid plugin version: only ASCII letters, digits, '.', '+', '_', and '-' are allowed")
		}
		b := byte(ch)
		if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') ||
			b == '-' || b == '_' || b == '.' || b == '+') {
			return fmt.Errorf("invalid plugin version: only ASCII letters, digits, '.', '+', '_', and '-' are allowed")
		}
	}
	return nil
}

// ComparePluginVersions compares two plugin version strings using semver when possible,
// falling back to lexicographic comparison. Returns negative if a < b, 0 if equal,
// positive if a > b.
func ComparePluginVersions(a, b string) int {
	return comparePluginVersions(a, b)
}

func comparePluginVersions(a, b string) int {
	aSemver := semverToCanonical(a)
	bSemver := semverToCanonical(b)
	if aSemver != "" && bSemver != "" {
		return semver.Compare(aSemver, bSemver)
	}
	return strings.Compare(a, b)
}

// semverToCanonical converts a string to a canonical semver prefix if possible.
// Returns empty string if not parseable as semver.
func semverToCanonical(v string) string {
	if strings.HasPrefix(v, "v") {
		c := semver.Canonical(v)
		if semver.IsValid(c) {
			return c
		}
	}
	return ""
}

func resolveInstallManifest(sourcePath string, fallbackManifest []byte) []byte {
	// A real plugin manifest always wins. The fallback only fills the gap for marketplace
	// sources that cannot be changed in place because they may be user-owned directories.
	if fallbackManifest != nil {
		manifestPath := findPluginManifestPathOnly(sourcePath)
		if manifestPath != "" {
			return nil // OnDisk takes priority
		}
	}
	return fallbackManifest
}

func findPluginManifestPathOnly(sourcePath string) string {
	path, _ := findPluginManifestPath(sourcePath)
	return path
}

func pluginManifestVersionForSource(sourcePath string, fallbackManifest []byte) (string, error) {
	manifest := resolveInstallManifest(sourcePath, fallbackManifest)
	var data []byte
	var err error
	if manifest != nil {
		data = manifest
	} else {
		manifestPath := findPluginManifestPathOnly(sourcePath)
		if manifestPath == "" {
			return "", pluginStoreInvalidError("missing plugin.json")
		}
		data, err = os.ReadFile(manifestPath)
		if err != nil {
			return "", pluginStoreIOError("failed to read plugin.json", err)
		}
	}

	var raw struct {
		Version any `json:"version"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", pluginStoreInvalidError(fmt.Sprintf("failed to parse plugin.json: %v", err))
	}
	if raw.Version == nil {
		return "", nil
	}
	switch v := raw.Version.(type) {
	case string:
		version := strings.TrimSpace(v)
		if version == "" {
			return "", nil
		}
		return version, nil
	default:
		return "", pluginStoreInvalidError("invalid plugin version in plugin.json: expected string")
	}
}

func pluginNameForSource(sourcePath string, fallbackManifest []byte) (string, error) {
	manifest := resolveInstallManifest(sourcePath, fallbackManifest)
	var data []byte
	var err error
	if manifest != nil {
		data = manifest
	} else {
		manifestPath := findPluginManifestPathOnly(sourcePath)
		if manifestPath == "" {
			return "", pluginStoreInvalidError("missing or invalid plugin.json")
		}
		data, err = os.ReadFile(manifestPath)
		if err != nil {
			return "", pluginStoreIOError("failed to read plugin.json", err)
		}
	}

	var raw struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", pluginStoreInvalidError(fmt.Sprintf("failed to parse plugin.json: %v", err))
	}
	pluginName := strings.TrimSpace(raw.Name)
	if pluginName == "" {
		return "", pluginStoreInvalidError("plugin name is missing in plugin.json")
	}
	if err := ValidatePluginSegment(pluginName, "plugin name"); err != nil {
		return "", pluginStoreInvalidError(err.Error())
	}
	return pluginName, nil
}

func removeExistingTarget(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(path)
}

func replacePluginRootAtomically(source string, targetRoot string, pluginVersion string, fallbackManifest []byte) error {
	parent := filepath.Dir(targetRoot)
	if parent == "" {
		return pluginStoreInvalidError("plugin cache path has no parent")
	}

	if err := os.MkdirAll(parent, 0o755); err != nil {
		return pluginStoreIOError("failed to create plugin cache directory", err)
	}

	pluginDirName := filepath.Base(targetRoot)
	if pluginDirName == "" || pluginDirName == "." || pluginDirName == string(filepath.Separator) {
		return pluginStoreInvalidError("plugin cache path has no directory name")
	}

	stagedDir, err := os.MkdirTemp(parent, "plugin-install-")
	if err != nil {
		return pluginStoreIOError("failed to create temporary plugin cache directory", err)
	}
	defer os.RemoveAll(stagedDir)

	stagedRoot := filepath.Join(stagedDir, pluginDirName)
	stagedVersionRoot := filepath.Join(stagedRoot, pluginVersion)

	if err := copyDirRecursive(source, stagedVersionRoot); err != nil {
		return err
	}

	// Inject fallback manifest if provided
	if fallbackManifest != nil {
		manifestPath := filepath.Join(stagedVersionRoot, ".codex-plugin", "plugin.json")
		manifestParent := filepath.Dir(manifestPath)
		if err := os.MkdirAll(manifestParent, 0o755); err != nil {
			return pluginStoreIOError("failed to create plugin manifest directory", err)
		}
		if err := os.WriteFile(manifestPath, fallbackManifest, 0o600); err != nil {
			return pluginStoreIOError("failed to write fallback plugin manifest", err)
		}
	}

	targetVersionRoot := filepath.Join(targetRoot, pluginVersion)
	targetExists := false
	if _, err := os.Stat(targetRoot); err == nil {
		targetExists = true
	}
	_, targetVersionExists := os.Stat(targetVersionRoot)

	// If target root exists but target version does not, we can place the new version alongside
	if targetExists && os.IsNotExist(targetVersionExists) {
		if err := os.Rename(stagedVersionRoot, targetVersionRoot); err != nil {
			return pluginStoreIOError("failed to activate updated plugin cache version", err)
		}
		// Clean up old versions
		_ = removeOldPluginVersions(targetRoot, pluginVersion)
		return nil
	}

	if targetExists {
		// Backup existing target, then replace
		backupDir, err := os.MkdirTemp(parent, "plugin-backup-")
		if err != nil {
			return pluginStoreIOError("failed to create plugin cache backup directory", err)
		}
		backupRoot := filepath.Join(backupDir, pluginDirName)
		if err := os.Rename(targetRoot, backupRoot); err != nil {
			os.RemoveAll(backupDir)
			return pluginStoreIOError("failed to back up plugin cache entry", err)
		}
		if err := os.Rename(stagedRoot, targetRoot); err != nil {
			// Rollback
			os.Rename(backupRoot, targetRoot)
			return pluginStoreIOError("failed to activate updated plugin cache entry", err)
		}
		os.RemoveAll(backupDir)
	} else {
		if err := os.Rename(stagedRoot, targetRoot); err != nil {
			return pluginStoreIOError("failed to activate plugin cache entry", err)
		}
	}

	return nil
}

func removeOldPluginVersions(targetRoot string, currentVersion string) error {
	entries, err := os.ReadDir(targetRoot)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		version := entry.Name()
		if version == currentVersion {
			continue
		}
		if ValidatePluginVersionSegment(version) != nil {
			continue
		}
		if err := os.RemoveAll(filepath.Join(targetRoot, version)); err != nil {
			if oldPluginVersionWouldStayActive(version, currentVersion) {
				return pluginStoreInvalidError(fmt.Sprintf(
					"failed to activate updated plugin cache version %q while %q remains active", currentVersion, version))
			}
		}
	}
	return nil
}

func oldPluginVersionWouldStayActive(oldVersion string, newVersion string) bool {
	return oldVersion == DefaultPluginVersion || comparePluginVersions(oldVersion, newVersion) > 0
}

func copyDirRecursive(source string, target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return pluginStoreIOError("failed to create plugin target directory", err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return pluginStoreIOError("failed to read plugin source directory", err)
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		targetPath := filepath.Join(target, entry.Name())
		if entry.IsDir() {
			if err := copyDirRecursive(sourcePath, targetPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(sourcePath, targetPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(source string, target string) error {
	srcFile, err := os.Open(source)
	if err != nil {
		return pluginStoreIOError("failed to open source file", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(target)
	if err != nil {
		return pluginStoreIOError("failed to create target file", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return pluginStoreIOError("failed to copy file", err)
	}

	// Copy file mode
	srcInfo, err := os.Stat(source)
	if err == nil {
		if err := os.Chmod(target, srcInfo.Mode()); err != nil {
			return pluginStoreIOError("failed to set file permissions", err)
		}
	}

	return nil
}

// Ensure interfaces
var _ fs.FileInfo = nil
