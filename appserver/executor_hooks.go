package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codex_go/apps"
	"codex_go/config"
	execserverclient "codex_go/execserver"
	"codex_go/mcp"
	"codex_go/session"
)

// executorPluginHookSource is the Go counterpart of Rust
// ExecutorPluginHookSource. Hooks come from an executor-discovered inline
// plugin manifest and remain bound to that environment.
type executorPluginHookSource struct {
	PluginID         string
	EnvironmentID    string
	MCPEnvironmentID string
	MCPMetadata      map[string]any
	PluginRoot       string
	ManifestPath     string
	SourceRelative   string
	Hooks            map[HookEventName][]hookJSONMatcherGroupWire
}

// executorCleanupTarget is one deduplicated MCP cleanup call. Bundled plugins
// can contribute the same target from more than one environment, so the
// registration key is event + MCP environment + server + tool.
type executorCleanupTarget struct {
	Event            HookEventName
	Server           string
	Tool             string
	Input            map[string]any
	Timeout          time.Duration
	MCPEnvironmentID string
	MCPMetadata      map[string]any
}

type executorManifestHooks struct {
	Hooks hooksJSONFileWire `json:"hooks"`
}

// executorPluginHookSources discovers inline cleanup hooks from executor V2
// plugin inventories. Non-inline hook declarations are intentionally ignored:
// executor manifests cannot yet be trusted enough to read arbitrary files.
func executorPluginHookSources(snapshots []execserverclient.ExecutorCapabilityDiscoveryV2Snapshot, lookup func(server string, tool string) *mcp.RuntimeToolInfo) []executorPluginHookSource {
	var sources []executorPluginHookSource
	for _, snapshot := range snapshots {
		if snapshot.Response == nil {
			continue
		}
		for _, plugin := range snapshot.Response.Plugins {
			pluginID := strings.TrimSpace(plugin.ID)
			if pluginID == "" {
				continue
			}
			hookFiles, ok := inlineExecutorPluginHooks(plugin.Files.Manifest.Contents)
			if !ok {
				continue
			}
			for index, file := range hookFiles {
				source := executorPluginHookSource{
					PluginID:       pluginID,
					EnvironmentID:  strings.TrimSpace(snapshot.EnvironmentID),
					PluginRoot:     strings.TrimSpace(plugin.Root),
					ManifestPath:   strings.TrimSpace(plugin.Files.Manifest.Path),
					SourceRelative: fmt.Sprintf("%s#hooks[%d]", executorManifestRelativePath(plugin.Root, plugin.Files.Manifest.Path), index),
					Hooks:          cloneExecutorHookEvents(file.Hooks),
				}
				if source.EnvironmentID == "" {
					source.EnvironmentID = execserverclient.LocalEnvironmentID
				}
				if filtered, ok := filterExecutorPluginHookSource(source, lookup); ok {
					sources = append(sources, filtered)
				}
			}
		}
	}
	return sources
}

// inlineExecutorPluginHooks extracts the inline hook documents from a plugin
// manifest. Rust accepts either a single HookEventsToml object or a list of
// them; a string is a file-backed declaration and is skipped.
func inlineExecutorPluginHooks(contents string) ([]hooksJSONFileWire, bool) {
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal([]byte(contents), &manifest); err != nil {
		return nil, false
	}
	raw, ok := manifest["hooks"]
	if !ok || len(raw) == 0 {
		return nil, false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	switch typed := value.(type) {
	case string:
		return nil, false
	case map[string]any:
		var file hooksJSONFileWire
		if err := json.Unmarshal(raw, &file); err != nil || len(file.Hooks) == 0 {
			return nil, false
		}
		return []hooksJSONFileWire{file}, true
	case []any:
		files := make([]hooksJSONFileWire, 0, len(typed))
		for _, item := range typed {
			data, err := json.Marshal(item)
			if err != nil {
				continue
			}
			var declaration executorManifestHooks
			if err := json.Unmarshal(data, &declaration); err != nil || len(declaration.Hooks.Hooks) == 0 {
				continue
			}
			files = append(files, declaration.Hooks)
		}
		return files, len(files) > 0
	default:
		return nil, false
	}
}

// filterExecutorPluginHookSource keeps only allowlisted, matcher-free MCP
// cleanup handlers. App targets additionally require the registered connector
// and an empty manifest-provided input, matching Rust's collision guard.
func filterExecutorPluginHookSource(source executorPluginHookSource, lookup func(server string, tool string) *mcp.RuntimeToolInfo) (executorPluginHookSource, bool) {
	filtered := executorPluginHookSource{
		PluginID:         source.PluginID,
		EnvironmentID:    source.EnvironmentID,
		MCPEnvironmentID: source.MCPEnvironmentID,
		MCPMetadata:      cloneAnyMap(source.MCPMetadata),
		PluginRoot:       source.PluginRoot,
		ManifestPath:     source.ManifestPath,
		SourceRelative:   source.SourceRelative,
		Hooks:            make(map[HookEventName][]hookJSONMatcherGroupWire),
	}
	allowedApp := false
	for event, groups := range source.Hooks {
		for _, group := range groups {
			if group.Matcher != nil {
				continue
			}
			kept := make([]hookJSONHandlerConfigWire, 0, len(group.Hooks))
			for _, handler := range group.Hooks {
				allowed, appTarget := executorCleanupHandlerAllowed(source.PluginID, event, handler, lookup)
				if !allowed {
					continue
				}
				kept = append(kept, handler)
				allowedApp = allowedApp || appTarget
			}
			if len(kept) > 0 {
				filtered.Hooks[event] = append(filtered.Hooks[event], hookJSONMatcherGroupWire{
					Matcher: cloneStringPtrAppserver(group.Matcher),
					Hooks:   kept,
				})
			}
		}
	}
	if len(filtered.Hooks) == 0 {
		return executorPluginHookSource{}, false
	}
	if allowedApp {
		metadata, ok := executorAppHookMetadata(filtered.Hooks, lookup)
		if !ok {
			return executorPluginHookSource{}, false
		}
		filtered.MCPEnvironmentID = mcp.DefaultMCPServerEnvironmentID
		filtered.MCPMetadata = metadata
	}
	return filtered, true
}

func executorCleanupHandlerAllowed(pluginID string, event HookEventName, handler hookJSONHandlerConfigWire, lookup func(server string, tool string) *mcp.RuntimeToolInfo) (bool, bool) {
	if handler.hookHandlerType() != HookHandlerMCPTool {
		return false, false
	}
	server := strings.TrimSpace(handler.Server)
	toolName := strings.TrimSpace(handler.Tool)
	for _, hook := range allowlistedBundledHooks {
		if hook.pluginID != pluginID || !bundledHookCoversEvent(hook, event) ||
			server != hook.server || toolName != hook.tool {
			continue
		}
		if hook.connectorID == "" {
			return true, false
		}
		if len(handler.Input) != 0 || lookup == nil {
			continue
		}
		info := lookup(server, toolName)
		if info != nil && strings.TrimSpace(info.ConnectorID) == hook.connectorID {
			return true, true
		}
	}
	return false, false
}

// executorAppHookMetadata carries the tool's trusted _codex_apps routing
// metadata. A missing or invalid resource URI cannot route the cleanup call.
func executorAppHookMetadata(events map[HookEventName][]hookJSONMatcherGroupWire, lookup func(server string, tool string) *mcp.RuntimeToolInfo) (map[string]any, bool) {
	for _, groups := range events {
		for _, group := range groups {
			for _, handler := range group.Hooks {
				if strings.TrimSpace(handler.Server) != mcp.RuntimeCodexAppsMCPServerName || lookup == nil {
					continue
				}
				info := lookup(strings.TrimSpace(handler.Server), strings.TrimSpace(handler.Tool))
				if info == nil {
					continue
				}
				routing := codexAppsRoutingMeta(info.Meta)
				if routing == nil {
					continue
				}
				if resourceURI, ok := routing["resource_uri"].(string); !ok || strings.TrimSpace(resourceURI) == "" {
					continue
				}
				return map[string]any{mcp.MCPToolCodexAppsMetaKey: cloneAnyMap(routing)}, true
			}
		}
	}
	return nil, false
}

func codexAppsRoutingMeta(meta any) map[string]any {
	object, ok := meta.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{mcp.MCPToolCodexAppsMetaKey, "codex_apps", "codexApps"} {
		if nested, ok := object[key].(map[string]any); ok {
			return nested
		}
	}
	return nil
}

// executorCleanupTargets flattens admitted sources into deduplicated calls.
func executorCleanupTargets(sources []executorPluginHookSource) []executorCleanupTarget {
	seen := map[string]bool{}
	var targets []executorCleanupTarget
	for _, source := range sources {
		mcpEnvironmentID := strings.TrimSpace(source.MCPEnvironmentID)
		if mcpEnvironmentID == "" {
			mcpEnvironmentID = strings.TrimSpace(source.EnvironmentID)
		}
		for event, groups := range source.Hooks {
			for _, group := range groups {
				for _, handler := range group.Hooks {
					if handler.hookHandlerType() != HookHandlerMCPTool {
						continue
					}
					server := strings.TrimSpace(handler.Server)
					toolName := strings.TrimSpace(handler.Tool)
					key := strings.Join([]string{string(event), mcpEnvironmentID, server, toolName}, "\x00")
					if seen[key] {
						continue
					}
					seen[key] = true
					target := executorCleanupTarget{
						Event:            event,
						Server:           server,
						Tool:             toolName,
						Input:            cloneAnyMap(handler.Input),
						Timeout:          executorHookTimeout(handler),
						MCPEnvironmentID: mcpEnvironmentID,
						MCPMetadata:      cloneAnyMap(source.MCPMetadata),
					}
					targets = append(targets, target)
				}
			}
		}
	}
	return targets
}

func executorHookTimeout(handler hookJSONHandlerConfigWire) time.Duration {
	seconds := int64(5)
	switch {
	case handler.TimeoutSec != nil:
		seconds = int64(*handler.TimeoutSec)
	case handler.Timeout != nil:
		seconds = int64(*handler.Timeout)
	case handler.TimeoutSecAlias != nil:
		seconds = int64(*handler.TimeoutSecAlias)
	}
	if seconds < 1 {
		seconds = 1
	}
	return time.Duration(seconds) * time.Second
}

// executorCleanupInput builds the hook event payload used to expand the
// handler's input template.
func executorCleanupInput(request any) (map[string]any, error) {
	data, err := HookInputJSON(request)
	if err != nil {
		return nil, err
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(data), &input); err != nil {
		return nil, err
	}
	return input, nil
}

// runExecutorCleanupHooks executes admitted cleanup targets without reporting
// them as user-visible hooks. Failures are best-effort and never affect turn
// completion.
func (r *RuntimeRouter) runExecutorCleanupHooks(ctx context.Context, snapshots []execserverclient.ExecutorCapabilityDiscoveryV2Snapshot, threadID string, event HookEventName, hookInput map[string]any) {
	if r == nil || len(snapshots) == 0 {
		return
	}
	lookup := r.executorHookToolLookup(threadID)
	sources := executorPluginHookSources(snapshots, lookup)
	targets := executorCleanupTargets(sources)
	if len(targets) == 0 {
		return
	}
	runner := r.requireHookRunner()
	executor := runner.McpToolHookExecutor
	if executor == nil {
		return
	}
	executeExecutorCleanupTargets(ctx, executor, targets, event, hookInput)
}

func executeExecutorCleanupTargets(ctx context.Context, executor McpToolHookExecutor, targets []executorCleanupTarget, event HookEventName, hookInput map[string]any) {
	if executor == nil {
		return
	}
	for _, target := range targets {
		if target.Event != event {
			continue
		}
		args, err := expandMcpArgumentTemplate(target.Input, hookInput)
		if err != nil {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, target.Timeout)
		_, _ = executor.Execute(callCtx, HookMcpCall{
			Server:        target.Server,
			Tool:          target.Tool,
			EnvironmentID: target.MCPEnvironmentID,
			Metadata:      cloneAnyMap(target.MCPMetadata),
			Input:         args,
			Timeout:       target.Timeout,
		})
		cancel()
	}
}

// executorHookToolLookup resolves enabled MCP tools, including app-only tools
// that are not model-visible, while excluding tools disabled by app policy.
func (r *RuntimeRouter) executorHookToolLookup(threadID string) func(server string, tool string) *mcp.RuntimeToolInfo {
	if r == nil {
		return nil
	}
	service := r.mcpServiceForThread(threadID, nil)
	if service == nil {
		return nil
	}
	cfg := r.effectiveMCPConfigForThread(threadID)
	tools := executorHookRuntimeTools(service, cfg, threadID)
	byKey := make(map[string]*mcp.RuntimeToolInfo, len(tools))
	for i := range tools {
		info := &tools[i]
		key := strings.TrimSpace(info.ServerName) + "\x00" + strings.TrimSpace(info.Tool.Name)
		byKey[key] = info
	}
	return func(server string, tool string) *mcp.RuntimeToolInfo {
		return byKey[strings.TrimSpace(server)+"\x00"+strings.TrimSpace(tool)]
	}
}

func executorHookRuntimeTools(service *mcp.MCPService, cfg *config.Config, threadID string) []mcp.RuntimeToolInfo {
	if service == nil {
		return nil
	}
	response, err := service.ListStatusChecked(&mcp.MCPListServerStatusParams{
		ThreadID:            stringPtrIfNotEmpty(strings.TrimSpace(threadID)),
		Detail:              &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailToolsAndAuthOnly},
		NonBlockingOptional: true,
	})
	if err != nil || response == nil {
		return nil
	}
	tools := mcp.RuntimeToolsFromStatuses(response.Data)
	var evaluator *apps.AppToolPolicyEvaluator
	if cfg != nil {
		evaluator = apps.NewAppToolPolicyEvaluatorWithRequirements(
			apps.AppsConfigFromValues(cfg.Values),
			appsRequirementsForConfig(cfg),
		)
	}
	filtered := tools[:0]
	for _, tool := range tools {
		if !mcp.IsCodexAppsMCPServerName(tool.ServerName) {
			filtered = append(filtered, tool)
			continue
		}
		connectorID := strings.TrimSpace(tool.ConnectorID)
		if connectorID == "" {
			continue
		}
		if !codexAppsToolPolicyAllows(&tool, evaluator) {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func codexAppsToolPolicyAllows(tool *mcp.RuntimeToolInfo, evaluator *apps.AppToolPolicyEvaluator) bool {
	if tool == nil || evaluator == nil {
		return true
	}
	var destructiveHint, openWorldHint *bool
	if tool.Tool.Annotations != nil {
		destructiveHint = tool.Tool.Annotations.DestructiveHint
		openWorldHint = tool.Tool.Annotations.OpenWorldHint
	}
	return evaluator.Policy(apps.AppToolPolicyInput{
		ConnectorID:     strings.TrimSpace(tool.ConnectorID),
		ToolName:        tool.Tool.Name,
		ToolTitle:       tool.Tool.Title,
		DestructiveHint: destructiveHint,
		OpenWorldHint:   openWorldHint,
	}).Enabled
}

func executorManifestRelativePath(root string, manifestPath string) string {
	root = strings.TrimSuffix(strings.ReplaceAll(strings.TrimSpace(root), "\\", "/"), "/")
	manifestPath = strings.TrimSpace(manifestPath)
	normalized := strings.ReplaceAll(manifestPath, "\\", "/")
	if root != "" && strings.HasPrefix(normalized, root+"/") {
		return strings.TrimPrefix(normalized, root+"/")
	}
	return normalized
}

func cloneExecutorHookEvents(events map[string][]hookJSONMatcherGroupWire) map[HookEventName][]hookJSONMatcherGroupWire {
	out := make(map[HookEventName][]hookJSONMatcherGroupWire, len(events))
	for name, groups := range events {
		event := hookEventFromJSONName(name)
		if event == "" {
			continue
		}
		for _, group := range groups {
			cloned := hookJSONMatcherGroupWire{Matcher: cloneStringPtrAppserver(group.Matcher)}
			for _, handler := range group.Hooks {
				handler.Input = cloneAnyMap(handler.Input)
				handler.StatusMessage = cloneStringPtrAppserver(handler.StatusMessage)
				handler.StatusMessageAlias = cloneStringPtrAppserver(handler.StatusMessageAlias)
				cloned.Hooks = append(cloned.Hooks, handler)
			}
			out[event] = append(out[event], cloned)
		}
	}
	return out
}

func executorCleanupEventForThreadSource(source string) HookEventName {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "subagent") {
		return HookEventSubagentStop
	}
	return HookEventStop
}

// scheduleExecutorCleanupHooks captures the step inventory and runs admitted
// cleanup hooks in the background. It never blocks turn completion and drops
// failures because they are diagnostic only.
func (r *RuntimeRouter) scheduleExecutorCleanupHooks(threadID string, snapshot *runtimeStepCapabilitySnapshot, event HookEventName, hookInput map[string]any) {
	if r == nil || snapshot == nil || len(hookInput) == 0 {
		return
	}
	snapshots := snapshot.executorCapabilityDiscoveryV2()
	if len(snapshots) == 0 {
		return
	}
	threadID = strings.TrimSpace(threadID)
	go func() {
		r.runExecutorCleanupHooks(context.Background(), snapshots, threadID, event, hookInput)
	}()
}

func (r *RuntimeRouter) executorCleanupEventForThread(threadID string) HookEventName {
	if r == nil {
		return HookEventStop
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, false)
	if err != nil || record == nil {
		return HookEventStop
	}
	return executorCleanupEventForThreadSource(record.Metadata.ThreadSource)
}

// executorStopCleanupInput builds the event payload used by Stop and
// SubagentStop cleanup hooks. Executor hook templates typically only need the
// session, turn, and event identity; the remaining fields keep the payload
// compatible with ordinary stop hooks.
func executorStopCleanupInput(event HookEventName, threadID string, turnID string, cwd string, model string, permissionMode string, lastAssistantMessage string) (map[string]any, error) {
	if event == HookEventSubagentStop {
		return executorCleanupInput(&HookSubagentStopRequest{
			ThreadID:             threadID,
			TurnID:               turnID,
			CWD:                  cwd,
			Model:                model,
			PermissionMode:       permissionMode,
			LastAssistantMessage: stringPtrIfNotEmpty(lastAssistantMessage),
		})
	}
	return executorCleanupInput(&HookStopRequest{
		ThreadID:             threadID,
		TurnID:               turnID,
		CWD:                  cwd,
		Model:                model,
		PermissionMode:       permissionMode,
		LastAssistantMessage: stringPtrIfNotEmpty(lastAssistantMessage),
	})
}
