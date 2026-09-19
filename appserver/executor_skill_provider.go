package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	execserverclient "codex_go/execserver"
	"codex_go/features"
	"codex_go/session"
	"codex_go/skillprovider"
	"codex_go/turn"
	"codex_go/utils"
)

const maxExecutorSkillResourceBytes = 1024 * 1024

func (r *RuntimeRouter) executorSkillProviderForThread(threadID string) *skillprovider.Registry {
	return r.executorSkillProviderForThreadWithSandbox(threadID, nil)
}

func (r *RuntimeRouter) executorSkillProviderForThreadWithSandbox(threadID string, sandboxContexts map[string]*execserverclient.FileSystemSandboxContext) *skillprovider.Registry {
	if r == nil || !r.threadHasSelectedCapabilityRoots(threadID) {
		return nil
	}
	// Rust #40640: managed requirements may disable the `plugins` feature, in
	// which case selected executor plugin roots must not expose capabilities.
	if !r.executorPluginsFeatureEnabledForThread(threadID) {
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

func (r *RuntimeRouter) executorPluginsFeatureEnabledForThread(threadID string) bool {
	if r == nil {
		return false
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return true
	}
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{CWD: strings.TrimSpace(record.Metadata.CWD), ThreadID: threadID})
	if err != nil || cfg == nil {
		return true
	}
	return features.Enabled(cfg.FeatureSettings(), "plugins")
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
	disabled := r.disabledExecutorSkillPaths()
	rootEnvironments := r.executorSkillRootEnvironments(threadID)
	for _, entry := range entries {
		if !entry.Enabled || entry.AuthorityKind != string(skillprovider.SourceExecutor) || entry.AuthorityID == "" || entry.PackageID == "" || entry.ResourceID == "" {
			continue
		}
		// Rust #46015: a caller may disable specific executor skills per
		// environment; the entry stays out of the model-visible catalog.
		if executorSkillDisabledForEnvironment(&entry, disabled, rootEnvironments) {
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
			PluginID:         entry.PluginID,
		})
	}
	return catalog, nil
}

// disabledExecutorSkillPaths returns the caller-owned disablement map, if any.
func (r *RuntimeRouter) disabledExecutorSkillPaths() map[string][]string {
	if r == nil || r.services.DisabledExecutorSkillPaths == nil {
		return nil
	}
	return r.services.DisabledExecutorSkillPaths
}

// executorSkillRootEnvironments maps selected capability root ids to the
// environment they were selected from, so caller-owned disablement (keyed by
// environment, Rust #46015) can be applied to an executor catalog entry whose
// authority is the root id.
func (r *RuntimeRouter) executorSkillRootEnvironments(threadID string) map[string]string {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil
	}
	out := map[string]string{}
	for _, raw := range record.Metadata.SelectedCapabilityRoots {
		var selected SelectedCapabilityRoot
		if err := json.Unmarshal(raw, &selected); err != nil || strings.TrimSpace(selected.ID) == "" {
			continue
		}
		if selected.Location.Type != CapabilityRootLocationEnvironment {
			continue
		}
		environmentID := strings.TrimSpace(selected.Location.EnvironmentID)
		if environmentID == "" {
			environmentID = "local"
		}
		out[strings.TrimSpace(selected.ID)] = environmentID
	}
	return out
}

// executorSkillDisabledForEnvironment mirrors Rust #46015: a skill is disabled
// when its environment lists its SKILL.md document. The configured path may be
// the executor's own path or the skill locator the app-server exposes; both are
// compared as path URIs so alias differences (for example macOS /var ->
// /private/var) still match, falling back to an exact string comparison.
func executorSkillDisabledForEnvironment(entry *SkillsListEntry, disabled map[string][]string, rootEnvironments map[string]string) bool {
	if entry == nil || len(disabled) == 0 {
		return false
	}
	environmentID := strings.TrimSpace(entry.AuthorityID)
	if mapped := strings.TrimSpace(rootEnvironments[environmentID]); mapped != "" {
		environmentID = mapped
	} else if entryEnvironment := strings.TrimSpace(entry.EnvironmentID); entryEnvironment != "" {
		environmentID = entryEnvironment
	}
	if environmentID == "" {
		return false
	}
	configured := disabled[environmentID]
	if len(configured) == 0 {
		return false
	}
	candidates := []string{entry.SourcePath, entry.Path}
	for _, disabledPath := range configured {
		for _, candidate := range candidates {
			if executorSkillPathEquals(disabledPath, candidate) {
				return true
			}
		}
	}
	return false
}

func executorSkillPathEquals(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	leftURI := executorSkillPathURI(left)
	rightURI := executorSkillPathURI(right)
	if leftURI == nil || rightURI == nil {
		return false
	}
	return leftURI.Equal(rightURI)
}

// executorSkillPathURI resolves a configured or discovered SKILL.md path to the
// same path-URI form. A value carrying a URI scheme is parsed as a locator; a
// host-native path is converted from the host, so equivalent spellings compare
// equal.
func executorSkillPathURI(value string) *utils.PathURI {
	if strings.Contains(value, "://") {
		if uri, err := utils.Parse(value); err == nil && uri != nil {
			return uri
		}
	}
	if uri, err := utils.FromHostNativePath(value); err == nil && uri != nil {
		return uri
	}
	if uri, err := utils.Parse(value); err == nil && uri != nil {
		return uri
	}
	return nil
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
