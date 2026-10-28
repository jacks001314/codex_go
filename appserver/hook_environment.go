package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/utils"
)

// environmentHookSource is one ready environment's owner-admitted hook
// declarations, mirroring Rust EnvironmentHookSource (#56304).
type environmentHookSource struct {
	EnvironmentID string
	CWD           string
	Hooks         HookEvents
}

// EnvironmentHookOutput is the result of running one command hook on its
// owning execution environment.
type EnvironmentHookOutput struct {
	ExitCode int32
	Stdout   string
	Stderr   string
}

// HookEnvironmentExecutor runs a command hook on the execution environment
// that owns it (Rust HookEnvironmentExecutor). The app-server supplies an
// implementation that dials the environment's exec-server; environments that
// run in this process keep the local command runner.
type HookEnvironmentExecutor interface {
	Execute(ctx context.Context, environmentID string, command string, env map[string]string, inputJSON string, timeout time.Duration) (*EnvironmentHookOutput, error)
}

// environmentHookDiscovery is the Go shape of Rust's DiscoveryResult for
// environment hooks: one listing entry per environment plus the flattened
// handler and diagnostic lists callers need for dispatch and fail-closed
// readiness checks.
type environmentHookDiscovery struct {
	Entries            []HookListEntry
	Handlers           []HookMetadata
	Warnings           []string
	RequiredLoadErrors []string
}

// discoverEnvironmentHooks mirrors Rust discover_environment_hooks (#56304):
// each environment's hooks are discovered as a required, managed source whose
// synthetic path is `<environment:{id}>/requirements.toml`. Every load failure
// is recorded as a required load error so callers can fail closed.
func discoverEnvironmentHooks(sources []environmentHookSource, displayOrder int64, executors map[string]HookEnvironmentExecutor) environmentHookDiscovery {
	var discovered environmentHookDiscovery
	for _, source := range sources {
		if source.Hooks.IsEmpty() {
			continue
		}
		appendEnvironmentHooks(&discovered, source, &displayOrder, executors[source.EnvironmentID])
	}
	return discovered
}

func appendEnvironmentHooks(discovered *environmentHookDiscovery, source environmentHookSource, displayOrder *int64, executor HookEnvironmentExecutor) {
	if discovered == nil {
		return
	}
	environmentID := strings.TrimSpace(source.EnvironmentID)
	path := fmt.Sprintf("<environment:%s>/requirements.toml", environmentID)
	// Resolve command overrides against the executor platform before normal
	// local discovery (Rust resolves command_windows from the environment cwd).
	convention, ok := environmentPathConvention(source.CWD)
	windows := ok && convention == utils.ConventionWindows
	entry := HookListEntry{CWD: source.CWD}
	discoverySource := &hookDiscoverySource{
		Path:      path,
		KeySource: path,
		Source:    HookSourceSystem,
		// Managed requirements load MCP tool hooks; the runner routes them to
		// this environment through HookMetadata.EnvironmentID.
		McpToolHooksEnabled: true,
	}
	warningStart := len(entry.Warnings)
	for _, event := range source.Hooks.matcherGroups() {
		for groupIndex, group := range event.groups {
			wire := hookJSONMatcherGroupFromConfig(group, windows)
			*displayOrder = appendDiscoveredHookGroup(&entry, discoverySource, event.name, wire, groupIndex, *displayOrder)
		}
	}
	// Rust's required sources record every load failure in both the warnings
	// and the required-load-error list (HookHandlerSource::record_load_failure).
	entry.RequiredLoadErrors = append(entry.RequiredLoadErrors, entry.Warnings[warningStart:]...)
	for i := range entry.Hooks {
		hook := &entry.Hooks[i]
		hook.SourcePath = path
		hook.Source = HookSourceSystem
		hook.IsManaged = true
		hook.Enabled = true
		hook.TrustStatus = HookTrustManaged
		hook.EnvironmentID = &environmentID
		hook.environmentCWD = source.CWD
		hook.withholdTranscript = true
		hook.environmentExecutor = executor
	}
	discovered.Entries = append(discovered.Entries, entry)
	discovered.Handlers = append(discovered.Handlers, entry.Hooks...)
	discovered.Warnings = append(discovered.Warnings, entry.Warnings...)
	discovered.RequiredLoadErrors = append(discovered.RequiredLoadErrors, entry.RequiredLoadErrors...)
}

// hookJSONMatcherGroupFromConfig adapts one owner-supplied matcher group to the
// hooks-file wire shape the discovery helpers already understand. When the
// environment runs on Windows, command_windows replaces command exactly once
// during discovery (Rust append_environment_hooks).
func hookJSONMatcherGroupFromConfig(group config.ConfiguredHookGroup, windows bool) hookJSONMatcherGroupWire {
	wire := hookJSONMatcherGroupWire{Matcher: cloneStringPtrAppserver(group.Matcher)}
	for _, handler := range group.Hooks {
		item := hookJSONHandlerConfigWire{
			Type:           handler.Type,
			Command:        handler.Command,
			CommandWindows: cloneStringPtrAppserver(handler.CommandWindows),
			Server:         handler.Server,
			Tool:           handler.Tool,
			Input:          cloneHookInput(handler.Input),
			TimeoutSec:     handler.TimeoutSec,
			Async:          handler.Async,
			StatusMessage:  cloneStringPtrAppserver(handler.StatusMessage),
		}
		if windows && item.CommandWindows != nil {
			item.Command = *item.CommandWindows
			item.CommandWindows = nil
		}
		wire.Hooks = append(wire.Hooks, item)
	}
	return wire
}

// environmentPathConvention infers the path grammar of an environment cwd. A
// file URI carries its own convention; otherwise the spelling decides. An
// unknown convention keeps the host spelling (Rust reports None and skips the
// Windows command override).
func environmentPathConvention(cwd string) (utils.PathConvention, bool) {
	cwd = strings.TrimSpace(cwd)
	if strings.HasPrefix(strings.ToLower(cwd), "file:") {
		if uri, err := utils.Parse(cwd); err == nil {
			if convention, ok := uri.InferConvention(); ok {
				return convention, true
			}
		}
		return "", false
	}
	legacy := utils.NewLegacyAppPathString(cwd)
	return legacy.InferAbsolutePathConvention()
}

// environmentNativeCWD renders an environment cwd as a native path string for
// hook input, matching Rust PathUri::inferred_native_path_string.
func environmentNativeCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if !strings.HasPrefix(strings.ToLower(cwd), "file:") {
		return cwd
	}
	uri, err := utils.Parse(cwd)
	if err != nil {
		return cwd
	}
	convention, ok := uri.InferConvention()
	if !ok {
		return cwd
	}
	rendered, err := utils.LegacyAppPathStringFromURI(uri, convention)
	if err != nil {
		return cwd
	}
	return rendered.Value
}

// environmentHookInputJSON mirrors Rust HandlerSourcePath::event_input: a hook
// owned by an execution environment sees that environment's cwd and never the
// controller-owned transcript paths.
func environmentHookInputJSON(inputJSON string, environmentCWD string) string {
	if strings.TrimSpace(inputJSON) == "" {
		return inputJSON
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &payload); err != nil {
		return inputJSON
	}
	payload["cwd"] = environmentNativeCWD(environmentCWD)
	payload["transcript_path"] = nil
	if _, ok := payload["agent_transcript_path"]; ok {
		payload["agent_transcript_path"] = nil
	}
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return inputJSON
	}
	return string(rewritten)
}
