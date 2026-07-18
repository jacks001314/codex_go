//go:build windows

package execserver

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
	windowsunified "codex_go/sandbox/windowssandbox/unified_exec"
)

func startExecServerSandboxProcess(params *ExecParams) (*startedExecServerSandboxProcess, bool, error) {
	if params == nil || !hasJSONValue(params.Sandbox) {
		return nil, false, nil
	}
	if params.EnforceManagedNetwork && params.ManagedNetwork == nil {
		return nil, true, requestError(-32602, "managed network enforcement requires managedNetwork context")
	}
	var sandboxContext FileSystemSandboxContext
	if err := json.Unmarshal(params.Sandbox, &sandboxContext); err != nil {
		return nil, true, requestError(-32602, fmt.Sprintf("invalid sandbox context: %v", err))
	}
	if !hasJSONValue(sandboxContext.Permissions) {
		return nil, true, requestError(-32602, "invalid sandbox context: permissions are required")
	}
	cwd, err := nativeExecServerPath(params.CWD, "cwd")
	if err != nil {
		return nil, true, err
	}
	if strings.TrimSpace(cwd) == "" {
		return nil, true, requestError(-32602, "cwd is required for Windows sandbox process launch")
	}
	profileJSON, err := nativePermissionProfileJSON(sandboxContext.Permissions)
	if err != nil {
		return nil, true, requestError(-32602, fmt.Sprintf("invalid sandbox permission path URI: %v", err))
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(profileJSON)
	if err != nil {
		return nil, true, requestError(-32602, fmt.Sprintf("invalid sandbox permission profile: %v", err))
	}
	if profile.Disabled {
		return nil, true, requestError(-32602, "sandbox intent cannot be enforced on this executor")
	}
	workspaceRoots := make([]string, 0, len(sandboxContext.WorkspaceRoots))
	for _, root := range sandboxContext.WorkspaceRoots {
		native, rootErr := nativeExecServerPath(root, "workspace root")
		if rootErr != nil {
			return nil, true, rootErr
		}
		workspaceRoots = append(workspaceRoots, native)
	}
	if len(workspaceRoots) == 0 {
		workspaceRoots = []string{cwd}
	}
	level := windowsunified.WindowsSandboxLevelLegacy
	if sandboxContext.WindowsSandboxLevel == "elevated" || profile.HasDenyReadEntries() {
		level = windowsunified.WindowsSandboxLevelElevated
	}
	session, err := windowsunified.SpawnWindowsSandboxLiveSessionForLevel(&windowsunified.WindowsSandboxSessionRequest{
		Capture: windowssandbox.CaptureRequest{
			PermissionProfileID:              "exec-server",
			PermissionProfile:                profile,
			WorkspaceRoots:                   workspaceRoots,
			CodexHome:                        fsHelperCodexHome(),
			Command:                          append([]string(nil), params.Argv...),
			CWD:                              cwd,
			Env:                              childEnv(params),
			TTY:                              params.TTY,
			StdinOpen:                        params.TTY || params.PipeStdin,
			UsePrivateDesktop:                sandboxContext.WindowsSandboxPrivateDesktop,
			ProxyEnforced:                    params.EnforceManagedNetwork,
			ProxySettingsMode:                windowssandbox.ProxySettingsPreserve,
			ReadRootsIncludePlatformDefaults: true,
		},
		PTY:                 params.TTY,
		WindowsSandboxLevel: level,
		ProxyEnforced:       params.EnforceManagedNetwork,
	})
	if err != nil {
		return nil, true, err
	}
	return &startedExecServerSandboxProcess{
		stdin:     session.Stdin,
		readers:   append([]io.ReadCloser(nil), session.Readers...),
		wait:      session.Wait,
		terminate: session.Terminate,
		close:     session.Close,
	}, true, nil
}
