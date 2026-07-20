package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const externalMemorySource = "external_agent_import"

func defaultExternalAgentHome() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude")
	}
	return ""
}

func (s *ConfigService) detectExternalMemoryMigration(cwds []string) (ExternalAgentConfigMigrationItem, bool) {
	files := discoverExternalMemoryFiles(s.externalAgentHome, cwds)
	if len(files) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	return ExternalAgentConfigMigrationItem{
		ItemType:    MigrationMemory,
		Description: "Synchronize external-agent project memory",
		Details:     &MigrationDetails{MemoryFiles: files},
	}, true
}

func discoverExternalMemoryFiles(home string, cwds []string) []MemoryFileMigration {
	projectsRoot := filepath.Join(strings.TrimSpace(home), "projects")
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

func externalMemoryProjectKey(cwd string) string {
	cleaned := filepath.Clean(cwd)
	cleaned = strings.TrimPrefix(cleaned, filepath.VolumeName(cleaned))
	cleaned = strings.Trim(cleaned, `/\\`)
	replacer := strings.NewReplacer("/", "-", "\\", "-")
	return replacer.Replace(cleaned)
}

func (s *ConfigService) importExternalMemory(item *ExternalAgentConfigMigrationItem, source *string) ExternalAgentConfigImportTypeResult {
	result := ExternalAgentConfigImportTypeResult{ItemType: MigrationMemory, Successes: []ExternalAgentConfigImportItemTypeSuccess{}, Failures: []ExternalAgentConfigImportItemTypeFailure{}}
	if item == nil || item.Details == nil {
		return result
	}
	resourcesRoot := filepath.Join(s.codexHome, "memories", "extensions", externalMemorySource, "resources")
	owned := map[string]bool{}
	for _, file := range item.Details.MemoryFiles {
		owned[file.ProjectKey] = true
		content, err := os.ReadFile(file.SourcePath)
		if err != nil {
			result.Failures = append(result.Failures, memoryImportFailure(file, "memory_source_read", "failed_to_read_memory_source", err))
			continue
		}
		hash := sha256.Sum256(content)
		if hex.EncodeToString(hash[:]) != strings.ToLower(file.ContentSHA256) {
			result.Failures = append(result.Failures, memoryImportFailure(file, "memory_content_hash", "memory_content_hash_mismatch", fmt.Errorf("memory content changed after detection")))
			continue
		}
		projectRoot := filepath.Join(resourcesRoot, file.ProjectKey)
		target := filepath.Join(projectRoot, filepath.FromSlash(file.SourceFile))
		if !pathWithinConfig(target, projectRoot) {
			result.Failures = append(result.Failures, memoryImportFailure(file, "memory_target_path", "invalid_memory_target_path", fmt.Errorf("memory target escapes project root")))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			result.Failures = append(result.Failures, memoryImportFailure(file, "memory_target_create", "failed_to_create_memory_target", err))
			continue
		}
		metadata, _ := json.Marshal(map[string]any{"source": externalMemorySource, "projectKey": file.ProjectKey, "cwd": file.CWD, "sourceFile": file.SourceFile, "sourcePath": file.SourcePath, "sha256": file.ContentSHA256})
		staged := append([]byte("<!-- codex-external-memory-metadata\n"+strings.ReplaceAll(string(metadata), "--", "\\u002d\\u002d")+"\n-->\n\n"), content...)
		if err := os.WriteFile(target, staged, 0o600); err != nil {
			result.Failures = append(result.Failures, memoryImportFailure(file, "memory_target_write", "failed_to_write_memory_target", err))
			continue
		}
		scope, _ := json.MarshalIndent(map[string]any{"cwd": file.CWD, "projectKey": file.ProjectKey, "source": externalMemorySource}, "", "  ")
		_ = os.WriteFile(filepath.Join(projectRoot, "scope.json"), append(scope, '\n'), 0o600)
		sourcePath, targetPath := file.SourcePath, target
		result.Successes = append(result.Successes, ExternalAgentConfigImportItemTypeSuccess{ItemType: MigrationMemory, CWD: cloneStringPtr(file.CWD), Source: &sourcePath, Target: &targetPath})
	}
	entries, _ := os.ReadDir(resourcesRoot)
	for _, entry := range entries {
		if entry.IsDir() && !owned[entry.Name()] {
			_ = os.RemoveAll(filepath.Join(resourcesRoot, entry.Name()))
		}
	}
	return result
}

func memoryImportFailure(file MemoryFileMigration, stage string, subType string, err error) ExternalAgentConfigImportItemTypeFailure {
	errorType := stage
	source := file.SourcePath
	return ExternalAgentConfigImportItemTypeFailure{ItemType: MigrationMemory, ErrorType: &errorType, SubErrorType: &subType, FailureStage: stage, Message: err.Error(), CWD: cloneStringPtr(file.CWD), Source: &source}
}

func pathWithinConfig(path string, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
