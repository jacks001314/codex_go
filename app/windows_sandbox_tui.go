package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

func interactiveWindowsSandboxStartupPrompt(root *cli.RootOptions, requirements *chatwidget.PermissionRequirements) *codextea.WindowsSandboxStartupPrompt {
	if runtime.GOOS != "windows" {
		return nil
	}
	codexHome := auth.DefaultCodexHome()
	loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(root))
	if err != nil || loaded == nil {
		return nil
	}
	setupComplete, _ := windowssandbox.SandboxSetupIsComplete(codexHome)
	requirementsSourcePresent := loaded.Requirements != nil && loaded.Requirements.AllowedWindowsSandboxImplementations != nil
	return interactiveWindowsSandboxPromptFromValues(root, loaded.Values, requirements, requirementsSourcePresent, setupComplete)
}

// interactiveWindowsSandboxPromptFromValues derives the startup prompt from the
// authoritative configuration values and requirements. For a connected app
// server those values come from config/read and configRequirements/read.
func interactiveWindowsSandboxPromptFromValues(
	root *cli.RootOptions,
	values map[string]any,
	requirements *chatwidget.PermissionRequirements,
	requirementsSourcePresent bool,
	setupComplete bool,
) *codextea.WindowsSandboxStartupPrompt {
	level := interactiveWindowsSandboxLevel(values)
	elevatedSetupRequired := chatwidget.ElevatedWindowsSandboxSetupRequired(level, requirementsSourcePresent, setupComplete)
	// Rust tui/src/lib.rs: the startup NUX fires only when the session made a
	// directory trust decision (a fresh/untrusted cwd) while the sandbox
	// backend is disabled, or when elevated sandbox setup is required. A cwd
	// that was already trusted in the effective config produced no trust
	// decision, so the NUX is skipped - mirroring Rust
	// onboarding_result.directory_trust_persisted. Go has no in-session
	// directory-trust onboarding, so the pre-session trust state is the
	// available signal: trusted now means no trust decision was made.
	cwd := ""
	if root != nil {
		cwd = strings.TrimSpace(root.Shared.CWD)
	}
	if cwd == "" {
		if resolved, err := os.Getwd(); err == nil {
			cwd = strings.TrimSpace(resolved)
		}
	}
	trustDecisionContext := !config.ProjectConfigEnabled(values, cwd)
	showNow := (trustDecisionContext && level == chatwidget.WindowsSandboxLevelDisabled) || elevatedSetupRequired
	decision := chatwidget.MaybePromptWindowsSandboxEnable(showNow, level, elevatedSetupRequired, true)
	if !decision.OpenEnablePrompt {
		return nil
	}
	allowUnelevated := true
	allowElevated := true
	if requirements != nil {
		allowUnelevated = chatwidget.WindowsSandboxModeAllowed(*requirements, chatwidget.WindowsSandboxModeUnelevated)
		allowElevated = chatwidget.WindowsSandboxModeAllowed(*requirements, chatwidget.WindowsSandboxModeElevated)
	}
	return &codextea.WindowsSandboxStartupPrompt{
		AllowUnelevated:     allowUnelevated,
		AllowElevated:       allowElevated,
		SetupChoiceRequired: !allowUnelevated || elevatedSetupRequired,
	}
}

func interactiveRemoteWindowsSandboxStartupPrompt(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, requirements *chatwidget.PermissionRequirements) *codextea.WindowsSandboxStartupPrompt {
	if !interactiveRemoteEndpointIsLocal(endpoint) {
		return nil
	}
	// Rust #44939: a local app server owns sandbox readiness. When it reports
	// ready, elevated setup is already in place and the TUI must not prompt;
	// the TUI's own setup files are not authoritative for a daemon connection.
	if interactiveRemoteWindowsSandboxReady(ctx, endpoint) {
		return nil
	}
	values, requirementsSourcePresent, remoteRequirements, err := interactiveRemoteWindowsSandboxState(ctx, root, endpoint)
	if err != nil {
		return nil
	}
	if requirements != nil {
		remoteRequirements = requirements
	}
	return interactiveWindowsSandboxPromptFromValues(root, values, remoteRequirements, requirementsSourcePresent, false)
}

// interactiveRemoteWindowsSandboxState reads the connected app server's
// effective config and requirements for the current working directory
// (Rust #45821 refresh_windows_sandbox_config).
func interactiveRemoteWindowsSandboxState(
	ctx context.Context,
	root *cli.RootOptions,
	endpoint *appserverdaemon.RemoteAppServerEndpoint,
) (map[string]any, bool, *chatwidget.PermissionRequirements, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := openRemoteSessionClient(callCtx, endpoint)
	if err != nil {
		return nil, false, nil, err
	}
	defer client.close()
	params := config.ConfigReadParams{}
	if root != nil {
		if cwd := strings.TrimSpace(root.Shared.CWD); cwd != "" {
			params.CWD = &cwd
		}
	}
	var read config.ConfigReadResponse
	if err := remoteSessionRequest(callCtx, client, appserver.MethodConfigRead, params, &read); err != nil {
		return nil, false, nil, err
	}
	var requirements config.ConfigRequirementsReadResponse
	sourcePresent := false
	var permissionRequirements *chatwidget.PermissionRequirements
	if err := remoteSessionRequest(callCtx, client, appserver.MethodConfigRequirementsRead, map[string]any{}, &requirements); err == nil {
		sourcePresent = requirements.Requirements != nil && requirements.Requirements.AllowedWindowsSandboxImplementations != nil
		permissionRequirements = interactivePermissionRequirementsFromConfigRequirements(requirements.Requirements)
	}
	return read.Config, sourcePresent, permissionRequirements, nil
}

// windowsSandboxReadyViaClient queries the connected app server's
// windowsSandbox/readiness status (Rust #44939 windows_sandbox_ready).
func windowsSandboxReadyViaClient(ctx context.Context, client *remoteAppServerTUIClient) bool {
	if client == nil {
		return false
	}
	id, err := client.sendRequest(ctx, appserver.MethodWindowsSandboxReadiness, nil)
	if err != nil {
		return false
	}
	var response sandbox.WindowsReadinessResponse
	if err := client.waitResponse(ctx, id, &response); err != nil {
		return false
	}
	return response.Status == sandbox.WindowsReadinessReady
}

func remoteAppServerRunsOnWindows(client *remoteAppServerTUIClient) bool {
	return client != nil && client.platformOS == "windows"
}

// interactiveRemoteWindowsSandboxReady queries readiness from a local app
// server, bounded like Rust's 5-second readiness timeout. An unavailable or
// slow server reports not-ready so the configured behavior is preserved.
func interactiveRemoteWindowsSandboxReady(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) bool {
	if runtime.GOOS != "windows" || !interactiveRemoteEndpointIsLocal(endpoint) {
		return false
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := openRemoteSessionClient(callCtx, endpoint)
	if err != nil {
		return false
	}
	defer client.close()
	// Rust #42202: the connected server reports its own platform metadata.
	// Older servers that omit platformOs are treated like any other non-Windows
	// server, even when the TUI host itself is Windows.
	if !remoteAppServerRunsOnWindows(client) {
		return false
	}
	return windowsSandboxReadyViaClient(callCtx, client)
}

func interactiveWindowsSandboxLevel(values map[string]any) chatwidget.WindowsSandboxLevel {
	// Rust tui/src/windows_sandbox.rs: a configured `windows.sandbox = "mxc"`
	// selects the native backend, which counts as enabled and never falls back to
	// the legacy setup modes.
	if mode, ok := config.WindowsSandboxModeFromValues(values); ok && mode == config.WindowsSandboxModeMxc {
		return chatwidget.WindowsSandboxLevelMxc
	}
	var mode *codextui.WindowsSandboxModeConfig
	if windows, ok := values["windows"].(map[string]any); ok {
		if parsed, valid := codextui.ParseWindowsSandboxModeConfig(stringValue(windows["sandbox"])); valid {
			mode = parsed
		}
	}
	if mode == nil {
		if parsed, valid := codextui.ParseWindowsSandboxModeConfig(stringValue(values["windows_sandbox"])); valid {
			mode = parsed
		}
	}
	features := (&config.Config{Values: values}).FeatureSettings()
	level := codextui.WindowsSandboxLevelFromConfig(mode, codextui.WindowsSandboxFeatureFlags{
		WindowsSandbox:         features["experimental_windows_sandbox"],
		WindowsSandboxElevated: features["elevated_windows_sandbox"],
	})
	switch level {
	case codextui.WindowsSandboxLevelElevated:
		return chatwidget.WindowsSandboxLevelElevated
	case codextui.WindowsSandboxLevelRestrictedToken:
		return chatwidget.WindowsSandboxLevelUnelevated
	default:
		return chatwidget.WindowsSandboxLevelDisabled
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func interactiveWindowsSandboxSetupHandler(root *cli.RootOptions) codextea.WindowsSandboxSetupFunc {
	if runtime.GOOS != "windows" {
		return nil
	}
	return func(mode chatwidget.WindowsSandboxMode, cwd string) (codextea.WindowsSandboxSetupOutcome, error) {
		service := interactiveConfigService(root)
		router := appserver.NewRuntimeRouter(appserver.RuntimeServices{
			Config:  service,
			Windows: sandbox.NewWindowsManager(sandbox.WindowsReadinessNotConfigured),
		})
		defer router.Close()
		return interactiveRunWindowsSandboxSetup(router, mode, cwd)
	}
}

// interactiveLocalWindowsSandboxStateHandler reads the embedded app-server
// config for the requested directory (Rust #45821).
func interactiveLocalWindowsSandboxStateHandler(root *cli.RootOptions) codextea.WindowsSandboxStateReaderFunc {
	if runtime.GOOS != "windows" {
		return nil
	}
	return func(cwd string) (codextea.WindowsSandboxState, error) {
		service := interactiveConfigService(root)
		params := &config.ConfigReadParams{}
		if cwd = strings.TrimSpace(cwd); cwd != "" {
			params.CWD = &cwd
		}
		read, err := service.Read(params)
		if err != nil {
			return codextea.WindowsSandboxState{}, err
		}
		requirements := chatwidget.PermissionRequirements{}
		if response := service.Requirements(); response != nil {
			if parsed := interactivePermissionRequirementsFromConfigRequirements(response.Requirements); parsed != nil {
				requirements = *parsed
			}
		}
		return codextea.WindowsSandboxState{
			Level:        interactiveWindowsSandboxLevel(read.Config),
			Requirements: requirements,
		}, nil
	}
}

// interactiveRemoteWindowsSandboxStateHandler reads config/read and
// configRequirements/read from a local app server (Rust #45821).
func interactiveRemoteWindowsSandboxStateHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.WindowsSandboxStateReaderFunc {
	if runtime.GOOS != "windows" || !interactiveRemoteEndpointIsLocal(endpoint) {
		return nil
	}
	return func(cwd string) (codextea.WindowsSandboxState, error) {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		client, err := openRemoteSessionClient(callCtx, endpoint)
		if err != nil {
			return codextea.WindowsSandboxState{}, err
		}
		defer client.close()
		params := config.ConfigReadParams{}
		if cwd = strings.TrimSpace(cwd); cwd != "" {
			params.CWD = &cwd
		}
		var read config.ConfigReadResponse
		if err := remoteSessionRequest(callCtx, client, appserver.MethodConfigRead, params, &read); err != nil {
			return codextea.WindowsSandboxState{}, err
		}
		requirements := chatwidget.PermissionRequirements{}
		var response config.ConfigRequirementsReadResponse
		if err := remoteSessionRequest(callCtx, client, appserver.MethodConfigRequirementsRead, map[string]any{}, &response); err == nil {
			if parsed := interactivePermissionRequirementsFromConfigRequirements(response.Requirements); parsed != nil {
				requirements = *parsed
			}
		}
		return codextea.WindowsSandboxState{
			Level:        interactiveWindowsSandboxLevel(read.Config),
			Requirements: requirements,
		}, nil
	}
}

func interactiveRunWindowsSandboxSetup(router *appserver.RuntimeRouter, mode chatwidget.WindowsSandboxMode, cwd string) (codextea.WindowsSandboxSetupOutcome, error) {
	if router == nil {
		return codextea.WindowsSandboxSetupOutcome{}, errors.New("Windows sandbox setup runtime is unavailable")
	}
	completion := make(chan sandbox.WindowsSetupCompletedNotification, 1)
	router.SetNotificationSink(appserver.NotificationSinkFunc(func(notification *appserver.Notification) {
		if notification == nil || notification.Method != appserver.NotificationWindowsSandboxSetupCompleted {
			return
		}
		if payload, ok := notification.Params.(*sandbox.WindowsSetupCompletedNotification); ok && payload != nil {
			completion <- *payload
		}
	}))

	params := sandbox.WindowsSetupStartParams{Mode: remoteWindowsSandboxSetupMode(mode)}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		params.CWD = &cwd
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return codextea.WindowsSandboxSetupOutcome{}, err
	}
	response := router.Handle(&appserver.Request{
		ID:     appserver.IntID(1),
		Method: appserver.MethodWindowsSandboxSetupStart,
		Params: raw,
	})
	if response == nil {
		return codextea.WindowsSandboxSetupOutcome{}, errors.New("Windows sandbox setup returned no response")
	}
	if response.Error != nil {
		return codextea.WindowsSandboxSetupOutcome{}, errors.New(response.Error.Message)
	}
	started, ok := response.Result.(*sandbox.WindowsSetupStartResponse)
	if !ok || started == nil {
		return codextea.WindowsSandboxSetupOutcome{}, fmt.Errorf("unexpected Windows sandbox setup response %T", response.Result)
	}
	if !started.Started {
		return codextea.WindowsSandboxSetupOutcome{Started: false}, nil
	}
	result := <-completion
	return codextea.WindowsSandboxSetupOutcome{
		Started: true,
		Completion: &codextea.WindowsSandboxSetupCompletion{
			Mode:    remoteWindowsSandboxModeFromSandbox(result.Mode),
			Success: result.Success,
			Error:   strings.TrimSpace(stringPtrValue(result.Error)),
		},
	}, nil
}

func interactiveRemoteEndpointIsLocal(endpoint *appserverdaemon.RemoteAppServerEndpoint) bool {
	if endpoint == nil {
		return false
	}
	if endpoint.Kind == appserverdaemon.RemoteEndpointUnixSocket {
		// Rust #54421: a bound socket at the advertised path may be an SSH forward,
		// so the path alone cannot establish that the daemon is local. Only the
		// verified default daemon endpoint keeps local workspace behavior.
		_, verified := verifiedLocalDaemonEndpoint(endpoint)
		return verified
	}
	if endpoint.Kind != appserverdaemon.RemoteEndpointWebSocket {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(endpoint.WebSocketURL))
	if err != nil {
		return false
	}
	host := strings.TrimSpace(parsed.Hostname())
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
