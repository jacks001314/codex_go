//go:build windows

package execserver

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"codex_go/sandbox"
	"codex_go/sandbox/mxc"
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
	if err := sandboxContext.SandboxOverride.Validate(); err != nil {
		return nil, true, requestError(-32602, "invalid sandbox context: "+err.Error())
	}
	// Rust deserializes windows_sandbox_level as a kebab-case enum, so an
	// unrecognized level is an invalid-params error rather than a silent
	// fallback. MXC rejects custom argv0 and private-desktop launches and
	// otherwise requires a usable native process security environment.
	selection, err := selectWindowsSandboxForExecutor(sandboxContext.WindowsSandboxLevel, params.CWD)
	if err != nil {
		return nil, true, requestError(-32602, "invalid sandbox context: "+err.Error())
	}
	if selection == WindowsSandboxSelectionMXC {
		// Rust #45524: MXC launches accept TTY and managed networking, and the
		// only request shape it still rejects is a custom argv0. That rejection
		// happens before the availability check, so it wins over the fail-closed
		// diagnostic.
		if params.Arg0 != nil {
			return nil, true, requestError(-32602, "MXC custom argv0 is not supported")
		}
		// Availability is the launcher's own PSEC probe, so this check and
		// sandbox.WindowsMXCAvailable() cannot disagree.
		if !sandbox.WindowsMXCAvailable() {
			return nil, true, requestError(-32602, "native MXC is unavailable on this executor")
		}
		session, err := startExecServerMXCProcess(params, &sandboxContext)
		if err != nil {
			return nil, true, err
		}
		return session, true, nil
	}
	if !hasJSONValue(sandboxContext.Permissions) {
		return nil, true, requestError(-32602, "invalid sandbox context: permissions are required")
	}
	if params.EnforceManagedNetwork && selection != WindowsSandboxSelectionElevated {
		return nil, true, fmt.Errorf("managed networking requires the elevated Windows sandbox backend")
	}
	if err := sandboxContext.WindowsSandboxProxySettingsMode.Validate(); err != nil {
		return nil, true, requestError(-32602, "invalid sandbox context: "+err.Error())
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
	if selection == WindowsSandboxSelectionElevated {
		level = windowsunified.WindowsSandboxLevelElevated
	}
	// Rust #49098: a remote controller cannot resolve a sandbox-compatible
	// PowerShell on this host, so request preparation applies the existing
	// fallback for the elevated/MXC Windows sandbox backends.
	command := append([]string(nil), params.Argv...)
	if len(command) > 0 {
		command[0] = windowsSandboxPowerShellProgram(command[0], string(selection))
	}
	if selection == WindowsSandboxSelectionMXC {
		command = prefixMXCPowerShellCWD(command)
	}
	session, err := windowsunified.SpawnWindowsSandboxLiveSessionForLevel(&windowsunified.WindowsSandboxSessionRequest{
		Capture: windowssandbox.CaptureRequest{
			PermissionProfileID:              "exec-server",
			PermissionProfile:                profile,
			WorkspaceRoots:                   workspaceRoots,
			CodexHome:                        fsHelperCodexHome(),
			Command:                          command,
			CWD:                              cwd,
			Env:                              childEnv(params),
			TTY:                              params.TTY,
			StdinOpen:                        params.TTY || params.PipeStdin,
			ProxyEnforced:                    params.EnforceManagedNetwork,
			ProxySettingsMode:                windowsSandboxProxySettingsMode(sandboxContext.WindowsSandboxProxySettingsMode),
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

func windowsSandboxProxySettingsMode(mode WindowsSandboxProxySettingsMode) windowssandbox.ProxySettingsMode {
	if mode == WindowsSandboxProxySettingsPreserve {
		return windowssandbox.ProxySettingsPreserve
	}
	return windowssandbox.ProxySettingsReconcile
}

// startExecServerMXCProcess launches the request through sandbox/mxc's native
// PSEC runner and adapts it to the executor's process API (Rust
// `prepare_exec_request` MXC branch).
func startExecServerMXCProcess(params *ExecParams, sandboxContext *FileSystemSandboxContext) (*startedExecServerSandboxProcess, error) {
	if sandboxContext == nil || !hasJSONValue(sandboxContext.Permissions) {
		return nil, requestError(-32602, "invalid sandbox context: permissions are required")
	}
	if params.EnforceManagedNetwork && params.ManagedNetwork == nil {
		return nil, requestError(-32602, "managed network enforcement requires managedNetwork context")
	}
	if err := sandboxContext.WindowsSandboxProxySettingsMode.Validate(); err != nil {
		return nil, requestError(-32602, "invalid sandbox context: "+err.Error())
	}
	cwd, err := nativeExecServerPath(params.CWD, "cwd")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cwd) == "" {
		return nil, requestError(-32602, "cwd is required for Windows sandbox process launch")
	}
	profileJSON, err := nativePermissionProfileJSON(sandboxContext.Permissions)
	if err != nil {
		return nil, requestError(-32602, fmt.Sprintf("invalid sandbox permission path URI: %v", err))
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(profileJSON)
	if err != nil {
		return nil, requestError(-32602, fmt.Sprintf("invalid sandbox permission profile: %v", err))
	}
	if profile.Disabled {
		return nil, requestError(-32602, "sandbox intent cannot be enforced on this executor")
	}
	env := childEnv(params)
	// A managed-network launch is "proxied" whenever the request carries the
	// context: Rust gates on `command.managed_network.is_some()` alone, without
	// looking at enforce_managed_network, and then denies direct egress while
	// allowing host loopback so the child can reach the managed proxy. That
	// shape is what lets a proxy launch work here too; MXC's own
	// `network_policy.proxy` form is not emitted by codex, so this branch needs
	// no executor-local proxy address.
	var managed *mxc.ManagedNetworkContext
	if params.ManagedNetwork != nil {
		managed = &mxc.ManagedNetworkContext{
			LoopbackPorts:     append([]uint16(nil), params.ManagedNetwork.LoopbackPorts...),
			AllowLocalBinding: params.ManagedNetwork.AllowLocalBinding,
		}
	}
	// Rust #60647: PowerShell under MXC can start with $PWD unset when a
	// workspace ancestor is unreadable; prefix the guarded workaround before
	// either the native or the two-stage launcher path.
	argv := prefixMXCPowerShellCWD(params.Argv)
	// A TTY request goes through the two-stage launcher instead: see
	// startExecServerMXCTTYProcess and plan section 18.
	if params.TTY {
		ttyParams := *params
		ttyParams.Argv = argv
		return startExecServerMXCTTYProcess(&ttyParams, profile, cwd, managed, env)
	}
	policy, err := mxc.PrepareFileSystemPolicy(profile, cwd, env, cwd)
	if err != nil {
		return nil, err
	}
	egress, ingress, err := mxc.TranslateNetworkPolicy(profile.AllowsNetwork(), managed)
	if err != nil {
		return nil, err
	}
	request := &mxc.NativeRequest{
		ScriptCode:       windowssandbox.ArgvToCommandLine(argv),
		WorkingDirectory: cwd,
		Env:              envListForMXC(env),
		Policy: mxc.NativeContainerPolicy{
			Capabilities:         []string{"registryRead"},
			ReadWritePaths:       policy.ReadWritePaths,
			ReadOnlyPaths:        policy.ReadOnlyPaths,
			DeniedPaths:          policy.DeniedPaths,
			NetworkEgress:        egress,
			NetworkIngress:       ingress,
			NetworkSpecified:     true,
			NetworkModeSpecified: true,
			UI:                   mxc.UIPolicy{Disable: false},
			BaseProcessUI:        mxc.BaseProcessUI{Isolation: "desktop"},
		},
	}
	session, err := mxc.SpawnLiveSession(request)
	if err != nil {
		return nil, err
	}
	return &startedExecServerSandboxProcess{
		stdin:     session.Stdin,
		readers:   append([]io.ReadCloser(nil), session.Readers...),
		wait:      session.Wait,
		terminate: session.Terminate,
		close:     session.Close,
	}, nil
}

// startExecServerMXCTTYProcess launches a TTY request through the two-stage MXC
// path (Rust #45524).
//
// A PSEC child cannot receive a pseudoconsole, so the executor starts its own
// executable as a launcher helper under a ConPTY, and that helper enters the
// sandbox for the real command. Rust reaches the same shape by rewriting the
// prepared command to `[sandbox_exe, LauncherArg1]` and handing it to its
// generic PTY spawner (codex-rs/sandboxing/src/manager.rs, `WindowsMxc` branch);
// sandbox/mxc exposes the two steps as CreateCommandArgs + SpawnLauncherSession.
func startExecServerMXCTTYProcess(
	params *ExecParams,
	profile *sandbox.PermissionProfile,
	cwd string,
	managed *mxc.ManagedNetworkContext,
	env map[string]string,
) (*startedExecServerSandboxProcess, error) {
	// CreateCommandArgs stamps the host-owned launcher variables into the map it
	// is handed, so it gets this launch's copy instead of the shared child env.
	launcherEnv := make(map[string]string, len(env)+4)
	for key, value := range env {
		launcherEnv[key] = value
	}
	args, err := mxc.CreateCommandArgs(params.Argv, *profile, cwd, managed, launcherEnv)
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve the Codex executable for the MXC launcher: %w", err)
	}
	session, err := mxc.SpawnLauncherSession(executable, args, launcherEnv, cwd)
	if err != nil {
		return nil, err
	}
	return &startedExecServerSandboxProcess{
		stdin:     session.Stdin,
		readers:   append([]io.ReadCloser(nil), session.Readers...),
		wait:      session.Wait,
		terminate: session.Terminate,
		close:     session.Close,
	}, nil
}

// envListForMXC renders the filtered child environment as sorted KEY=VALUE
// entries; nil keeps the SDK's inherited-environment behavior.
func envListForMXC(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, key+"="+env[key])
	}
	return entries
}
