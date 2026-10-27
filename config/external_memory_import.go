package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex_go/memorylock"
)

const (
	externalMemorySource           = "external_agent_import"
	externalMemoryScopeFile        = "scope.json"
	externalMemoryInstructionsName = "instructions.md"
)

//go:embed templates/external_agent_import_instructions.md
var externalMemoryInstructions string

// MemoryImportSettings supplies the active memory pipeline to external-agent
// memory detection and import (Rust #56414) and the store used to enqueue
// consolidation for each imported namespace (Rust #56462).
type MemoryImportSettings struct {
	// Version is the active memories.version selection; empty selects v1.
	Version MemoryVersion
	// EnqueueConsolidation queues one consolidation for a namespace. A nil hook
	// skips queueing.
	EnqueueConsolidation func(context.Context, MemoryVersion) error
}

// SetMemoryImportSettings installs the host's memory pipeline settings. The
// zero value keeps v1 semantics and queues no consolidation.
func (s *ConfigService) SetMemoryImportSettings(settings MemoryImportSettings) {
	if s == nil {
		return
	}
	s.memoryImportMu.Lock()
	defer s.memoryImportMu.Unlock()
	s.memoryImport = settings
}

func (s *ConfigService) memoryImportSettings() MemoryImportSettings {
	if s == nil {
		return MemoryImportSettings{}
	}
	s.memoryImportMu.RLock()
	defer s.memoryImportMu.RUnlock()
	settings := s.memoryImport
	if settings.Version != MemoryVersionV2 {
		settings.Version = MemoryVersionV1
	}
	return settings
}

func defaultExternalAgentHome() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude")
	}
	return ""
}

func (s *ConfigService) detectExternalMemoryMigration(cwds []string) (ExternalAgentConfigMigrationItem, bool) {
	files := discoverExternalMemoryFiles(s.externalAgentHome, cwds)
	settings := s.memoryImportSettings()
	pending, err := externalMemoryProjectsPendingImport(s.codexHome, settings.Version, files)
	if err != nil {
		// Detection has no error channel in the protocol, so fall back to the
		// discovered files instead of hiding a migration the client can retry.
		if len(files) == 0 {
			return ExternalAgentConfigMigrationItem{}, false
		}
	} else if len(pending) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	selected := make([]MemoryFileMigration, 0, len(files))
	for _, file := range files {
		if err != nil || pending[file.ProjectKey] {
			selected = append(selected, file)
		}
	}
	description := "Import memory files from " + externalMemoryProjectsRoot(s.externalAgentHome) +
		" to " + externalMemoryResourcesRoot(externalMemoryRoot(s.codexHome, MemoryVersionV2))
	return ExternalAgentConfigMigrationItem{
		ItemType:    MigrationMemory,
		Description: description,
		Details:     &MigrationDetails{MemoryFiles: selected},
	}, true
}

// externalMemoryProjectsPendingImport unions the projects that need work in
// every applicable namespace, including owned projects whose source disappeared.
func externalMemoryProjectsPendingImport(codexHome string, selection MemoryVersion, files []MemoryFileMigration) (map[string]bool, error) {
	pending := map[string]bool{}
	for _, version := range externalMemoryVersions(codexHome, selection) {
		projects, err := externalMemoryProjectsNeedingImport(externalMemoryRoot(codexHome, version), files)
		if err != nil {
			return nil, err
		}
		for project := range projects {
			pending[project] = true
		}
	}
	return pending, nil
}

// externalMemoryVersions mirrors Rust's memory_versions: v2 is always imported,
// and v1 joins whenever it is selected or a legacy workspace already exists.
func externalMemoryVersions(codexHome string, selection MemoryVersion) []MemoryVersion {
	_, err := os.Stat(filepath.Join(codexHome, MemoryVersionV1.DirectoryName()))
	if err == nil || selection == MemoryVersionV1 {
		return []MemoryVersion{MemoryVersionV1, MemoryVersionV2}
	}
	return []MemoryVersion{MemoryVersionV2}
}

func externalMemoryRoot(codexHome string, version MemoryVersion) string {
	return filepath.Join(codexHome, version.DirectoryName())
}

func externalMemoryExtensionRoot(memoryRoot string) string {
	return filepath.Join(memoryRoot, "extensions", externalMemorySource)
}

func externalMemoryResourcesRoot(memoryRoot string) string {
	return filepath.Join(externalMemoryExtensionRoot(memoryRoot), "resources")
}

func externalMemoryProjectsRoot(externalAgentHome string) string {
	return filepath.Join(strings.TrimSpace(externalAgentHome), "projects")
}

func discoverExternalMemoryFiles(home string, cwds []string) []MemoryFileMigration {
	projectsRoot := externalMemoryProjectsRoot(home)
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		return nil
	}
	cwdByKey := map[string]string{}
	for _, cwd := range cwds {
		cwd = strings.TrimSpace(cwd)
		if cwd != "" {
			cwdByKey[externalMemoryProjectKey(cwd)] = cwd
		}
	}
	var out []MemoryFileMigration
	for _, project := range entries {
		if !project.IsDir() {
			continue
		}
		projectKey := project.Name()
		memoryRoot := filepath.Join(projectsRoot, projectKey, "memory")
		_ = filepath.WalkDir(memoryRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry == nil || entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			relative, relErr := filepath.Rel(memoryRoot, path)
			if relErr != nil || strings.HasPrefix(relative, "..") {
				return nil
			}
			hash := sha256.Sum256(content)
			var cwd *string
			if value := cwdByKey[projectKey]; value != "" {
				cwd = &value
			}
			out = append(out, MemoryFileMigration{ProjectKey: projectKey, CWD: cwd, SourcePath: path, SourceFile: filepath.ToSlash(relative), ContentSHA256: hex.EncodeToString(hash[:])})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectKey != out[j].ProjectKey {
			return out[i].ProjectKey < out[j].ProjectKey
		}
		return out[i].SourceFile < out[j].SourceFile
	})
	return out
}

// ExternalMemoryProjectKey returns the external memory project directory key
// for a working directory (the source layout's directory name).
func ExternalMemoryProjectKey(cwd string) string {
	return externalMemoryProjectKey(cwd)
}

func externalMemoryProjectKey(cwd string) string {
	cleaned := filepath.Clean(cwd)
	cleaned = strings.TrimPrefix(cleaned, filepath.VolumeName(cleaned))
	cleaned = strings.Trim(cleaned, `/\`)
	replacer := strings.NewReplacer("/", "-", "\\", "-")
	return replacer.Replace(cleaned)
}

func groupExternalMemoryFiles(files []MemoryFileMigration) map[string][]MemoryFileMigration {
	grouped := map[string][]MemoryFileMigration{}
	for _, file := range files {
		grouped[file.ProjectKey] = append(grouped[file.ProjectKey], file)
	}
	return grouped
}

func (s *ConfigService) importExternalMemory(item *ExternalAgentConfigMigrationItem, source *string) ExternalAgentConfigImportTypeResult {
	result := ExternalAgentConfigImportTypeResult{ItemType: MigrationMemory, Successes: []ExternalAgentConfigImportItemTypeSuccess{}, Failures: []ExternalAgentConfigImportItemTypeFailure{}}
	if item == nil || item.Details == nil {
		return result
	}
	settings := s.memoryImportSettings()
	codexHome := s.codexHome
	if codexHome != "" {
		if err := os.MkdirAll(codexHome, 0o755); err != nil {
			result.Failures = append(result.Failures, externalMemoryRequestFailure(err))
			return result
		}
	}
	versions := externalMemoryVersions(codexHome, settings.Version)
	roots := make([]string, len(versions))
	locks := make([]*memorylock.Lock, 0, len(versions))
	for i, version := range versions {
		roots[i] = externalMemoryRoot(codexHome, version)
		lock, err := memorylock.TryAcquire(roots[i])
		if err != nil {
			closeExternalMemoryLocks(locks)
			result.Failures = append(result.Failures, externalMemoryRequestFailure(err))
			return result
		}
		locks = append(locks, lock)
	}
	defer closeExternalMemoryLocks(locks)

	// Re-discover the source files under the locks so the import copies current
	// content and can remove projects whose sources disappeared (Rust's
	// copy_resources works from a fresh discovery pass).
	files := item.Details.MemoryFiles
	cwds := make([]string, 0, len(files))
	for _, file := range files {
		if file.CWD != nil {
			cwds = append(cwds, *file.CWD)
		}
	}
	filesByProject := groupExternalMemoryFiles(discoverExternalMemoryFiles(s.externalAgentHome, cwds))
	selectedSet := map[string]bool{}
	for _, file := range files {
		selectedSet[file.ProjectKey] = true
	}
	selected := make([]string, 0, len(selectedSet))
	for project := range selectedSet {
		selected = append(selected, project)
	}
	sort.Strings(selected)

	synchronized := map[string]bool{}
	failureMessages := map[string]string{}
	var failureOrder []string
	for i, version := range versions {
		root := roots[i]
		synchronizedHere := 0
		for _, project := range selected {
			projectFiles := filesByProject[project]
			var cwd *string
			if len(projectFiles) > 0 {
				cwd = projectFiles[0].CWD
			}
			changed, err := importExternalMemoryProject(root, project, cwd, projectFiles)
			if err != nil {
				if _, seen := failureMessages[project]; !seen {
					failureOrder = append(failureOrder, project)
				}
				failureMessages[project] = fmt.Sprintf("failed to synchronize selected memory %s: %v", project, err)
				continue
			}
			if changed {
				synchronized[project] = true
				synchronizedHere++
			}
		}
		// Detection carries only pending projects' files, so owned projects
		// without a current source are cleaned up here (Rust's
		// projects_needing_import lists them as selected).
		owned, err := ownedExternalMemoryProjects(root)
		if err != nil {
			result.Failures = append(result.Failures, externalMemoryRequestFailure(err))
			return result
		}
		for project := range owned {
			if _, present := filesByProject[project]; present {
				continue
			}
			changed, err := removeExternalMemoryProject(filepath.Join(externalMemoryResourcesRoot(root), project))
			if err != nil {
				if _, seen := failureMessages[project]; !seen {
					failureOrder = append(failureOrder, project)
				}
				failureMessages[project] = fmt.Sprintf("failed to synchronize selected memory %s: %v", project, err)
				continue
			}
			if changed {
				synchronized[project] = true
				synchronizedHere++
			}
		}
		if synchronizedHere == 0 {
			continue
		}
		if err := ensureExternalMemoryInstructions(root); err != nil {
			// Rust fails the whole import when the instructions cannot be written.
			result.Failures = append(result.Failures, externalMemoryRequestFailure(err))
			return result
		}
		if settings.EnqueueConsolidation != nil {
			if err := settings.EnqueueConsolidation(context.Background(), version); err != nil {
				slog.Warn("failed to enqueue imported memory consolidation", "memory_version", string(version), "error", err)
			}
		}
	}

	// Cleanup may affect only one namespace, but a real failure in either wins.
	for _, project := range selected {
		if _, failed := failureMessages[project]; failed {
			delete(synchronized, project)
		} else if !synchronized[project] {
			failureMessages[project] = "selected memory was not found: " + project
			failureOrder = append(failureOrder, project)
		}
	}
	target := externalMemoryResourcesRoot(externalMemoryRoot(codexHome, MemoryVersionV2))
	successes := make([]string, 0, len(synchronized))
	for project := range synchronized {
		successes = append(successes, project)
	}
	sort.Strings(successes)
	for _, project := range successes {
		projectKey := project
		targetPath := target
		result.Successes = append(result.Successes, ExternalAgentConfigImportItemTypeSuccess{ItemType: MigrationMemory, CWD: &projectKey, Target: &targetPath})
	}
	for _, project := range failureOrder {
		message, ok := failureMessages[project]
		if !ok {
			continue
		}
		projectKey := project
		result.Failures = append(result.Failures, ExternalAgentConfigImportItemTypeFailure{ItemType: MigrationMemory, FailureStage: "memory_import", Message: message, Source: &projectKey})
	}
	return result
}

func closeExternalMemoryLocks(locks []*memorylock.Lock) {
	for _, lock := range locks {
		_ = lock.Close()
	}
}

func externalMemoryRequestFailure(err error) ExternalAgentConfigImportItemTypeFailure {
	errorType := "external_agent_config_import_error"
	return ExternalAgentConfigImportItemTypeFailure{
		ItemType:     MigrationMemory,
		ErrorType:    &errorType,
		FailureStage: "import_request_failed",
		Message:      err.Error(),
	}
}

// importExternalMemoryProject synchronizes one project in one namespace and
// reports whether the workspace changed (including removals).
func importExternalMemoryProject(memoryRoot, project string, cwd *string, files []MemoryFileMigration) (bool, error) {
	targetRoot := filepath.Join(externalMemoryResourcesRoot(memoryRoot), project)
	projectCWD := ""
	if cwd != nil {
		projectCWD = strings.TrimSpace(*cwd)
	}
	if len(files) > 0 && projectCWD != "" {
		return true, replaceExternalMemoryProject(targetRoot, projectCWD, files)
	}
	if len(files) > 0 {
		unscoped, err := externalMemoryProjectHasUnscopedTarget(targetRoot)
		if err != nil {
			return false, err
		}
		if !unscoped {
			return false, nil
		}
	}
	return removeExternalMemoryProject(targetRoot)
}

func replaceExternalMemoryProject(targetRoot, cwd string, files []MemoryFileMigration) error {
	type stagedFile struct {
		relative string
		content  []byte
	}
	staged := make([]stagedFile, 0, len(files))
	for _, file := range files {
		content, err := os.ReadFile(file.SourcePath)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(content)
		if !strings.EqualFold(hex.EncodeToString(hash[:]), strings.ToLower(file.ContentSHA256)) {
			return errors.New("memory content changed after detection")
		}
		relative := filepath.FromSlash(file.SourceFile)
		if relative == "" || filepath.IsAbs(relative) || strings.HasPrefix(filepath.Clean(relative), "..") {
			return fmt.Errorf("invalid memory target path %q", file.SourceFile)
		}
		staged = append(staged, stagedFile{relative: relative, content: content})
	}
	if _, err := removeExternalMemoryProject(targetRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(targetRoot, 0o700); err != nil {
		return err
	}
	scope, err := projectScopeContent(cwd)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(targetRoot, externalMemoryScopeFile), scope, 0o600); err != nil {
		return err
	}
	for _, file := range staged {
		target := filepath.Join(targetRoot, file.relative)
		if !pathWithinConfig(target, targetRoot) {
			return errors.New("memory target escapes project root")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, file.content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func projectScopeContent(cwd string) ([]byte, error) {
	return json.Marshal(map[string]string{"cwd": cwd})
}

func removeExternalMemoryProject(targetRoot string) (bool, error) {
	info, err := os.Lstat(targetRoot)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return true, os.RemoveAll(targetRoot)
	}
	return true, os.Remove(targetRoot)
}

func externalMemoryProjectHasUnscopedTarget(targetRoot string) (bool, error) {
	info, err := os.Lstat(targetRoot)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return true, nil
	}
	return !externalMemoryScopeIsFile(targetRoot), nil
}

func externalMemoryScopeIsFile(targetRoot string) bool {
	info, err := os.Lstat(filepath.Join(targetRoot, externalMemoryScopeFile))
	return err == nil && info.Mode().IsRegular()
}

// externalMemoryProjectsNeedingImport mirrors Rust's projects_needing_import.
func externalMemoryProjectsNeedingImport(memoryRoot string, files []MemoryFileMigration) (map[string]bool, error) {
	pending := map[string]bool{}
	grouped := groupExternalMemoryFiles(files)
	sourceProjects := make(map[string]bool, len(grouped))
	for project, projectFiles := range grouped {
		sourceProjects[project] = true
		var cwd *string
		if len(projectFiles) > 0 {
			cwd = projectFiles[0].CWD
		}
		if cwd == nil || strings.TrimSpace(*cwd) == "" {
			unscoped, err := externalMemoryProjectHasUnscopedTarget(filepath.Join(externalMemoryResourcesRoot(memoryRoot), project))
			if err != nil {
				return nil, err
			}
			if unscoped {
				pending[project] = true
			}
			continue
		}
		needs, err := externalMemoryProjectNeedsImport(memoryRoot, project, strings.TrimSpace(*cwd), projectFiles)
		if err != nil {
			return nil, err
		}
		if needs {
			pending[project] = true
		}
	}
	owned, err := ownedExternalMemoryProjects(memoryRoot)
	if err != nil {
		return nil, err
	}
	for project := range owned {
		if !sourceProjects[project] {
			pending[project] = true
		}
	}
	return pending, nil
}

func externalMemoryProjectNeedsImport(memoryRoot, project, cwd string, files []MemoryFileMigration) (bool, error) {
	targetRoot := filepath.Join(externalMemoryResourcesRoot(memoryRoot), project)
	info, err := os.Lstat(targetRoot)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return true, nil
	}
	expected := map[string]bool{}
	for _, file := range files {
		expected[filepath.ToSlash(file.SourceFile)] = true
		sourceContent, err := os.ReadFile(file.SourcePath)
		if err != nil {
			return false, err
		}
		targetContent, err := os.ReadFile(filepath.Join(targetRoot, filepath.FromSlash(file.SourceFile)))
		if err != nil || !bytes.Equal(sourceContent, targetContent) {
			return true, nil
		}
	}
	expected[externalMemoryScopeFile] = true
	scope, err := projectScopeContent(cwd)
	if err != nil {
		return false, err
	}
	targetScope, err := os.ReadFile(filepath.Join(targetRoot, externalMemoryScopeFile))
	if err != nil || !bytes.Equal(scope, targetScope) {
		return true, nil
	}
	targetPaths, err := externalMemoryRelativeFiles(targetRoot)
	if err != nil {
		return false, err
	}
	if len(targetPaths) != len(expected) {
		return true, nil
	}
	for path := range targetPaths {
		if !expected[path] {
			return true, nil
		}
	}
	return false, nil
}

func ownedExternalMemoryProjects(memoryRoot string) (map[string]bool, error) {
	owned := map[string]bool{}
	entries, err := os.ReadDir(externalMemoryResourcesRoot(memoryRoot))
	if errors.Is(err, os.ErrNotExist) {
		return owned, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if externalMemoryScopeIsFile(filepath.Join(externalMemoryResourcesRoot(memoryRoot), entry.Name())) {
			owned[entry.Name()] = true
		}
	}
	return owned, nil
}

func externalMemoryRelativeFiles(root string) (map[string]bool, error) {
	paths := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths[filepath.ToSlash(relative)] = true
		return nil
	})
	return paths, err
}

func ensureExternalMemoryInstructions(memoryRoot string) error {
	path := filepath.Join(externalMemoryExtensionRoot(memoryRoot), externalMemoryInstructionsName)
	current, err := os.ReadFile(path)
	if err == nil && string(current) == externalMemoryInstructions {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(externalMemoryInstructions), 0o600)
}

func pathWithinConfig(path string, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
