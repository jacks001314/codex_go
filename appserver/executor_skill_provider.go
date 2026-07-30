package appserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	execserverclient "codex_go/execserver"
	"codex_go/session"
	"codex_go/skillprovider"
)

const maxExecutorSkillResourceBytes = 1024 * 1024

func (r *RuntimeRouter) executorSkillProviderForThread(threadID string) *skillprovider.Registry {
	return r.executorSkillProviderForThreadWithSandbox(threadID, nil)
}

func (r *RuntimeRouter) executorSkillProviderForThreadWithSandbox(threadID string, sandboxContexts map[string]*execserverclient.FileSystemSandboxContext) *skillprovider.Registry {
	if r == nil || !r.threadHasSelectedCapabilityRoots(threadID) {
		return nil
	}
	provider := skillprovider.ProviderFuncs{
		ListFunc: func(ctx context.Context, _ skillprovider.ListQuery) (skillprovider.Catalog, error) {
			return r.executorSkillProviderCatalog(ctx, threadID, sandboxContexts)
		},
		ReadFunc: func(ctx context.Context, request skillprovider.ReadRequest) (skillprovider.ReadResult, error) {
			return r.readExecutorSkillProviderResource(ctx, threadID, request, sandboxContexts)
		},
	}
	return skillprovider.NewRegistry(skillprovider.Source{Kind: skillprovider.SourceExecutor, Label: "executor", Provider: provider})
}

func (r *RuntimeRouter) threadHasSelectedCapabilityRoots(threadID string) bool {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return false
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	return err == nil && record != nil && len(record.Metadata.SelectedCapabilityRoots) > 0
}

func (r *RuntimeRouter) executorSkillProviderCatalog(ctx context.Context, threadID string, sandboxContexts map[string]*execserverclient.FileSystemSandboxContext) (skillprovider.Catalog, error) {
	entries, warnings, err := r.selectedCapabilitySkillEntriesForRuntimeWithSandbox(ctx, threadID, sandboxContexts)
	if err != nil {
		return skillprovider.Catalog{}, err
	}
	catalog := skillprovider.Catalog{Warnings: append([]string(nil), warnings...)}
	for _, entry := range entries {
		if !entry.Enabled || entry.AuthorityKind != string(skillprovider.SourceExecutor) || entry.AuthorityID == "" || entry.PackageID == "" || entry.ResourceID == "" {
			continue
		}
		catalog.Entries = append(catalog.Entries, skillprovider.CatalogEntry{
			PackageID:        entry.PackageID,
			Authority:        skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: entry.AuthorityID},
			Name:             entry.Name,
			Description:      entry.Description,
			ShortDescription: entry.ShortDescription,
			MainResource:     entry.ResourceID,
			DisplayPath:      entry.DisplayPath,
			Enabled:          entry.Enabled,
			PromptVisible:    entry.AllowsImplicitInvocation(),
		})
	}
	return catalog, nil
}

func (r *RuntimeRouter) readExecutorSkillProviderResource(ctx context.Context, threadID string, request skillprovider.ReadRequest, sandboxContexts map[string]*execserverclient.FileSystemSandboxContext) (skillprovider.ReadResult, error) {
	if request.Authority.Kind != skillprovider.SourceExecutor {
		return skillprovider.ReadResult{}, fmt.Errorf("executor skill provider cannot read %s resources", request.Authority.Kind)
	}
	entries, _, err := r.selectedCapabilitySkillEntriesForRuntimeWithSandbox(ctx, threadID, sandboxContexts)
	if err != nil {
		return skillprovider.ReadResult{}, err
	}
	var selected *SkillsListEntry
	for i := range entries {
		entry := &entries[i]
		if entry.Enabled && entry.AuthorityID == request.Authority.ID && entry.PackageID == request.PackageID {
			selected = entry
			break
		}
	}
	if selected == nil {
		return skillprovider.ReadResult{}, errors.New("skill package is not available from the requested authority")
	}
	relative, ok := executorSkillRelativeResource(request.Authority.ID, request.PackageID, request.Resource)
	if !ok {
		return skillprovider.ReadResult{}, errors.New("executor skill resource does not match its package")
	}
	sandboxContext, err := requireExecutorSkillSandboxContext(sandboxContexts, selected.EnvironmentID, selected.SourcePath)
	if err != nil {
		return skillprovider.ReadResult{}, err
	}
	contents, err := r.readExecutorSkillEntryResource(ctx, selected, relative, request.Resource, sandboxContext)
	if err != nil {
		return skillprovider.ReadResult{}, err
	}
	return skillprovider.ReadResult{Resource: request.Resource, Contents: contents}, nil
}

func executorSkillRelativeResource(authorityID string, packageID string, resourceID string) (string, bool) {
	prefix := "skill://" + authorityID + "/"
	if !strings.HasPrefix(packageID, prefix) || !strings.HasPrefix(resourceID, prefix) || strings.ContainsAny(packageID, "?#") || strings.ContainsAny(resourceID, "?#") {
		return "", false
	}
	packagePrefix := strings.TrimRight(packageID, "/") + "/"
	relative := strings.TrimPrefix(resourceID, packagePrefix)
	if relative == resourceID || relative == "" {
		return "", false
	}
	for _, segment := range strings.Split(relative, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	return relative, true
}

func (r *RuntimeRouter) readExecutorSkillEntryResource(ctx context.Context, entry *SkillsListEntry, relative string, resourceID string, sandboxContext *execserverclient.FileSystemSandboxContext) (string, error) {
	if entry == nil {
		return "", errors.New("executor skill entry is unavailable")
	}
	if entry.EnvironmentID != "" && entry.EnvironmentID != "local" {
		if r == nil || r.services.Environment == nil {
			return "", fmt.Errorf("executor skill resource references unavailable environment `%s`", entry.EnvironmentID)
		}
		record, ok := r.services.Environment.Record(entry.EnvironmentID)
		if !ok || record == nil {
			return "", fmt.Errorf("executor skill resource references unavailable environment `%s`", entry.EnvironmentID)
		}
		resourcePath := remoteJoin(remoteSkillDir(entry.SourcePath), relative)
		contents, err := readRemoteEnvironmentSkillTextWithSandbox(ctx, record, resourcePath, sandboxContext)
		if err != nil {
			return "", fmt.Errorf("failed to read executor skill resource %s: %w", resourceID, err)
		}
		return validateExecutorSkillResourceContents(resourceID, contents)
	}
	sourcePath := executorEnvironmentNativePath(entry.SourcePath)
	root := filepath.Dir(sourcePath)
	target := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", errors.New("executor skill resource does not match its package")
	}
	if sandboxContext != nil {
		contents, readErr := readLocalEnvironmentSkillTextWithSandbox(ctx, target, sandboxContext)
		if readErr != nil {
			return "", fmt.Errorf("failed to read executor skill resource %s: %w", resourceID, readErr)
		}
		return validateExecutorSkillResourceContents(resourceID, contents)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("failed to read executor skill resource %s: %w", resourceID, err)
	}
	return validateExecutorSkillResourceContents(resourceID, string(data))
}

func validateExecutorSkillResourceContents(resourceID string, contents string) (string, error) {
	if len(contents) > maxExecutorSkillResourceBytes {
		return "", fmt.Errorf("executor skill resource %s exceeds %d bytes", resourceID, maxExecutorSkillResourceBytes)
	}
	if !utf8.ValidString(contents) {
		return "", fmt.Errorf("executor skill resource %s is not valid UTF-8", resourceID)
	}
	return contents, nil
}
