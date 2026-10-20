package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	execserverclient "codex_go/execserver"
	"codex_go/features"
	"codex_go/plugin"
	"codex_go/session"
	"codex_go/turn"
)

// runtimeStepCapabilitySnapshot freezes the capability inputs captured for one
// sampling step. Environment updates replace the snapshot only when the next
// step begins, so model-visible skills and the skills tools observe the same
// roots for the step that is already running (Rust #50050).
type runtimeStepCapabilitySnapshot struct {
	mu                 sync.RWMutex
	roots              []SelectedCapabilityRoot
	disabledPluginIDs  []string
	pluginCapabilities []plugin.CapabilitySummary
	pluginSkillRoots   []plugin.EnabledSkillRoot
	sandboxContexts    map[string]*execserverclient.FileSystemSandboxContext
	discoveryV2Cache   *runtimeCapabilityDiscoveryV2Cache
	discoveryV2        *[]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot
	// executorPluginsEnabled is nil until the snapshot is refreshed, so callers
	// can distinguish "not captured" from an explicit feature-off value.
	executorPluginsEnabled *bool
}

func newRuntimeStepCapabilitySnapshot(router *RuntimeRouter, threadID string, params *turn.TurnStartParams) *runtimeStepCapabilitySnapshot {
	return newRuntimeStepCapabilitySnapshotContext(context.Background(), router, threadID, params)
}

func newRuntimeStepCapabilitySnapshotContext(ctx context.Context, router *RuntimeRouter, threadID string, params *turn.TurnStartParams) *runtimeStepCapabilitySnapshot {
	snapshot := &runtimeStepCapabilitySnapshot{}
	if router != nil && router.threads != nil {
		snapshot.discoveryV2Cache = router.threads.capabilityDiscoveryV2Cache(threadID)
	}
	snapshot.refreshContext(ctx, router, threadID, params)
	return snapshot
}

func (r *RuntimeRouter) activeStepCapabilitySnapshot(threadID string) *runtimeStepCapabilitySnapshot {
	if r == nil || r.threads == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	active := r.threads.ActiveTurn(strings.TrimSpace(threadID))
	if active == nil {
		return nil
	}
	return active.StepCapabilities
}

func (s *runtimeStepCapabilitySnapshot) refresh(router *RuntimeRouter, threadID string, params *turn.TurnStartParams) {
	s.refreshContext(context.Background(), router, threadID, params)
}

func (s *runtimeStepCapabilitySnapshot) refreshContext(ctx context.Context, router *RuntimeRouter, threadID string, params *turn.TurnStartParams) {
	if s == nil {
		return
	}
	roots := router.readySelectedCapabilityRootsForStep(threadID, params)
	disabled := append([]string(nil), router.threadDisabledPluginIDs(threadID)...)
	var capabilities []plugin.CapabilitySummary
	var skillRoots []plugin.EnabledSkillRoot
	var sandboxContexts map[string]*execserverclient.FileSystemSandboxContext
	var discoveryV2 *[]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot
	var executorPluginsEnabled *bool
	if router != nil && router.services.Plugins != nil {
		capabilities = cloneRuntimePluginCapabilities(router.services.Plugins.EnabledCapabilities())
		capabilities = filterCanonicalDisabledConnectors(capabilities, router.canonicalDisabledConnectorIDs(disabled))
		skillRoots = cloneRuntimeEnabledSkillRoots(router.services.Plugins.EnabledSkillRoots())
	}
	if router != nil {
		cfg, _ := router.effectiveConfigForTurn(params)
		sandboxContexts, _ = router.executorSkillSandboxContextsForTurn(cfg, turnCWD(params), params)
		discoveryV2 = router.executorCapabilityDiscoveryV2ForStep(ctx, threadID, params, cfg, sandboxContexts, s.discoveryV2Cache)
		if discoveryV2 != nil && router.services.Plugins != nil && cfg != nil {
			executorCatalog := plugin.CatalogFromExecutorSnapshots(*discoveryV2)
			merged := plugin.MergePluginCatalogs(executorCatalog, router.services.Plugins.CloudPluginCatalog())
			policy := plugin.PluginsConfigTomlFromValues(cfg.Values)
			capabilities = plugin.MergeCapabilitySummaries(capabilities, merged.CapabilitySummaries(policy.Plugins))
		}
		enabled := cfg == nil || features.Enabled(cfg.FeatureSettings(), "plugins")
		executorPluginsEnabled = &enabled
	}

	s.mu.Lock()
	s.roots = roots
	s.disabledPluginIDs = disabled
	s.pluginCapabilities = capabilities
	s.pluginSkillRoots = skillRoots
	s.sandboxContexts = sandboxContexts
	s.discoveryV2 = discoveryV2
	s.executorPluginsEnabled = executorPluginsEnabled
	s.mu.Unlock()
}

func (s *runtimeStepCapabilitySnapshot) view() ([]SelectedCapabilityRoot, []string, []plugin.CapabilitySummary, []plugin.EnabledSkillRoot) {
	if s == nil {
		return nil, nil, nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSelectedCapabilityRoots(s.roots),
		append([]string(nil), s.disabledPluginIDs...),
		cloneRuntimePluginCapabilities(s.pluginCapabilities),
		cloneRuntimeEnabledSkillRoots(s.pluginSkillRoots)
}

func (s *runtimeStepCapabilitySnapshot) selectedRoots() []SelectedCapabilityRoot {
	roots, _, _, _ := s.view()
	return roots
}

func (s *runtimeStepCapabilitySnapshot) capabilities() []plugin.CapabilitySummary {
	_, _, capabilities, _ := s.view()
	return capabilities
}

func (s *runtimeStepCapabilitySnapshot) enabledPluginSkillRoots() ([]plugin.EnabledSkillRoot, []string) {
	_, disabled, _, roots := s.view()
	return roots, disabled
}

func (s *runtimeStepCapabilitySnapshot) disabledPluginIDsSnapshot() []string {
	_, disabled, _, _ := s.view()
	return disabled
}

// executorPluginCatalog projects the step's V2 discovery snapshots. A nil
// result means no V2 inventory was supplied (legacy discovery); an empty
// catalog is a complete empty inventory (Rust #55301).
func (s *runtimeStepCapabilitySnapshot) executorPluginCatalog() *plugin.PluginCatalog {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	snapshots := s.discoveryV2
	s.mu.RUnlock()
	if snapshots == nil {
		return nil
	}
	return plugin.CatalogFromExecutorSnapshots(*snapshots)
}

func (s *runtimeStepCapabilitySnapshot) executorSandboxContexts() map[string]*execserverclient.FileSystemSandboxContext {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneExecutorSkillSandboxContexts(s.sandboxContexts)
}

func (s *runtimeStepCapabilitySnapshot) executorCapabilityDiscoveryV2() []execserverclient.ExecutorCapabilityDiscoveryV2Snapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.discoveryV2 == nil {
		return nil
	}
	return execserverclient.CloneExecutorCapabilityDiscoveryV2Snapshots(*s.discoveryV2)
}

func (s *runtimeStepCapabilitySnapshot) executorCapabilityDiscoveryV2Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.discoveryV2 != nil
}

func (s *runtimeStepCapabilitySnapshot) executorPluginsEnabledForStep() (bool, bool) {
	if s == nil {
		return false, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.executorPluginsEnabled == nil {
		return false, false
	}
	return *s.executorPluginsEnabled, true
}

// readySelectedCapabilityRootsForStep resolves the roots visible to a sampling
// step from the thread's retained roots and the step's environment selections.
// Thread roots are restricted to selected environments first, then refreshed by
// any ready attachment roots reported by the environment manager.
func (r *RuntimeRouter) readySelectedCapabilityRootsForStep(threadID string, params *turn.TurnStartParams) []SelectedCapabilityRoot {
	if r == nil || strings.TrimSpace(threadID) == "" || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil
	}
	threadRoots := threadSelectedCapabilityRoots(record)
	attachmentRoots := readyAttachmentRootsFromSelections(record)
	if params != nil && params.Environments != nil {
		threadRoots = restrictCapabilityRootsToSelections(threadRoots, params.Environments)
		attachmentRoots = readyAttachmentRootsFromEnvironmentSelections(params.Environments)
	}
	merged := combineSelectedCapabilityRoots(threadRoots, attachmentRoots)
	seen := make(map[string]struct{}, len(merged))
	unique := make([]SelectedCapabilityRoot, 0, len(merged))
	for _, root := range merged {
		if _, duplicate := seen[strings.TrimSpace(root.ID)]; duplicate {
			continue
		}
		seen[strings.TrimSpace(root.ID)] = struct{}{}
		unique = append(unique, root)
	}
	if r.services.Environment == nil || params == nil || params.Environments == nil {
		return unique
	}
	return r.services.Environment.InspectSelectedCapabilityRoots(unique).ReadyRoots
}

func readyAttachmentRootsFromEnvironmentSelections(selections []map[string]any) []SelectedCapabilityRoot {
	var roots []SelectedCapabilityRoot
	for _, selection := range selections {
		state, err := environmentConfigStateFromAnyMap(selection)
		if err != nil || state.Kind != EnvironmentConfigReady || state.Config == nil {
			continue
		}
		roots = append(roots, cloneSelectedCapabilityRoots(state.Config.SelectedCapabilityRoots)...)
	}
	return roots
}

func cloneRuntimeEnabledSkillRoots(roots []plugin.EnabledSkillRoot) []plugin.EnabledSkillRoot {
	if roots == nil {
		return nil
	}
	return append([]plugin.EnabledSkillRoot(nil), roots...)
}

func cloneRuntimePluginCapabilities(capabilities []plugin.CapabilitySummary) []plugin.CapabilitySummary {
	if capabilities == nil {
		return nil
	}
	out := make([]plugin.CapabilitySummary, len(capabilities))
	for i, capability := range capabilities {
		out[i] = capability
		out[i].MCPServers = append([]string(nil), capability.MCPServers...)
		out[i].AppConnectors = append([]string(nil), capability.AppConnectors...)
		out[i].Apps = append([]plugin.AppSummary(nil), capability.Apps...)
		out[i].AppTemplates = append([]plugin.AppTemplateSummary(nil), capability.AppTemplates...)
	}
	return out
}

func cloneExecutorSkillSandboxContexts(contexts map[string]*execserverclient.FileSystemSandboxContext) map[string]*execserverclient.FileSystemSandboxContext {
	if contexts == nil {
		return nil
	}
	out := make(map[string]*execserverclient.FileSystemSandboxContext, len(contexts))
	for environmentID, context := range contexts {
		if context == nil {
			out[environmentID] = nil
			continue
		}
		copy := *context
		copy.Permissions = append(json.RawMessage(nil), context.Permissions...)
		copy.WorkspaceRoots = append([]string(nil), context.WorkspaceRoots...)
		out[environmentID] = &copy
	}
	return out
}
