package appserver

import (
	"strings"

	"codex_go/execserver"
	"codex_go/mcp"
	"codex_go/session"
	"codex_go/turn"
)

// hostMCPPathContext keeps host-owned MCP processes on the local executor's
// working directory while retaining each remote selection's captured cwd for
// sandbox metadata.
type hostMCPPathContext struct {
	CWD             string
	EnvironmentCWDs map[string]string
}

func (r *RuntimeRouter) hostMCPPathContextForTurn(threadID string, params *turn.TurnStartParams) hostMCPPathContext {
	context := hostMCPPathContext{EnvironmentCWDs: map[string]string{}}
	if params != nil {
		for _, selection := range params.Environments {
			environmentID := selectionEnvironmentID(selection)
			if environmentID == "" {
				continue
			}
			cwd := strings.TrimSpace(threadItemStringFromAnyMap(selection, "cwd"))
			if cwd == "" {
				cwd = strings.TrimSpace(threadItemStringFromAnyMap(selection, "CWD"))
			}
			if environmentID == execserver.LocalEnvironmentID {
				if cwd != "" {
					context.CWD = cwd
				}
				continue
			}
			if cwd != "" {
				context.EnvironmentCWDs[environmentID] = cwd
			}
		}
		if local := localTurnEnvironmentSelection(params); local != nil {
			if cwd := turnSelectionCWD(local); cwd != "" {
				context.CWD = cwd
			}
		}
	}
	if context.CWD == "" && r != nil {
		if record, err := r.threadRecord(session.ThreadID(threadID), false, false); err == nil && record != nil {
			context.CWD = strings.TrimSpace(record.Metadata.CWD)
		}
	}
	if context.CWD == "" && r != nil {
		context.CWD = strings.TrimSpace(r.services.DefaultCWD)
	}
	if context.CWD == "" {
		context.CWD = processCWD()
	}
	for key, value := range context.EnvironmentCWDs {
		if strings.TrimSpace(value) == "" {
			delete(context.EnvironmentCWDs, key)
		}
	}
	if len(context.EnvironmentCWDs) == 0 {
		context.EnvironmentCWDs = nil
	}
	return context
}

func localTurnEnvironmentSelection(params *turn.TurnStartParams) map[string]any {
	if params == nil {
		return nil
	}
	for _, selection := range params.Environments {
		if selectionEnvironmentID(selection) == execserver.LocalEnvironmentID {
			return selection
		}
	}
	return nil
}

func turnSelectionCWD(selection map[string]any) string {
	return strings.TrimSpace(firstNonEmpty(
		threadItemStringFromAnyMap(selection, "cwd"),
		threadItemStringFromAnyMap(selection, "CWD"),
	))
}

func localTurnEnvironmentCWD(params *turn.TurnStartParams, fallback string) string {
	if selection := localTurnEnvironmentSelection(params); selection != nil {
		if cwd := turnSelectionCWD(selection); cwd != "" {
			return cwd
		}
	}
	return strings.TrimSpace(fallback)
}

func localTurnEnvironmentWorkspaceRoots(params *turn.TurnStartParams, fallback []string) []string {
	if selection := localTurnEnvironmentSelection(params); selection != nil {
		return stringSliceFromAny(firstNonNil(selection["workspaceRoots"], selection["workspace_roots"]))
	}
	return append([]string(nil), fallback...)
}

// applyHostMCPRuntimePaths preserves explicit per-server cwd values while
// giving local stdio servers the host working directory when their command is
// relative or cwd is omitted. Remote selection cwds feed sandbox metadata only.
func applyHostMCPRuntimePaths(runtime *mcp.RuntimeConfig, context hostMCPPathContext) {
	if runtime == nil {
		return
	}
	if len(context.EnvironmentCWDs) > 0 {
		runtime.EnvironmentCWDs = make(map[string]string, len(context.EnvironmentCWDs))
		for key, value := range context.EnvironmentCWDs {
			runtime.EnvironmentCWDs[key] = value
		}
	}
	if strings.TrimSpace(context.CWD) == "" {
		return
	}
	for name, registration := range runtime.Servers {
		if registration.Config.Command == "" || !registration.Config.IsLocalEnvironment() ||
			strings.TrimSpace(registration.Config.CWD) != "" {
			continue
		}
		registration.Config.CWD = context.CWD
		runtime.Servers[name] = registration
	}
}
