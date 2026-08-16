package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
	level := interactiveWindowsSandboxLevel(loaded.Values)
	setupComplete, _ := windowssandbox.SandboxSetupIsComplete(codexHome)
	requirementsSourcePresent := loaded.Requirements != nil && loaded.Requirements.AllowedWindowsSandboxImplementations != nil
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
	trustDecisionContext := !config.ProjectConfigEnabled(loaded.Values, cwd)
	showNow := (trustDecisionContext && level == chatwidget.WindowsSandboxLevelDisabled) || elevatedSetupRequired
	decision := chatwidget.MaybePromptWindowsSandboxEnable(showNow, level, elevatedSetupRequired, true)
	if !decision.OpenEnablePrompt {
		return nil
	}
	allowUnelevated := true
	if requirements != nil {
		allowUnelevated = chatwidget.WindowsSandboxModeAllowed(*requirements, chatwidget.WindowsSandboxModeUnelevated)
	}
	return &codextea.WindowsSandboxStartupPrompt{
		AllowUnelevated:     allowUnelevated,
		SetupChoiceRequired: !allowUnelevated || elevatedSetupRequired,
	}
}

func interactiveRemoteWindowsSandboxStartupPrompt(root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, requirements *chatwidget.PermissionRequirements) *codextea.WindowsSandboxStartupPrompt {
	if !interactiveRemoteEndpointIsLocal(endpoint) {
		return nil
	}
	return interactiveWindowsSandboxStartupPrompt(root, requirements)
}

func interactiveWindowsSandboxLevel(values map[string]any) chatwidget.WindowsSandboxLevel {
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

func interactiveSandboxReadDirHandler(root *cli.RootOptions) codextea.SandboxReadDirFunc {
	if runtime.GOOS != "windows" {
		return nil
	}
	return func(path string) (string, error) {
		cwd := interactiveSessionPickerCWD(root)
		if strings.TrimSpace(cwd) == "" {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
		absoluteCWD, err := filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
		absoluteCWD = filepath.Clean(absoluteCWD)
		codexHome := auth.DefaultCodexHome()
		loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(root))
		if err != nil {
			return "", err
		}
		resolved, err := loaded.ResolveSandboxPermissionProfile("", absoluteCWD)
		if err != nil {
			return "", err
		}
		if resolved == nil || resolved.Profile == nil {
			profile := sandbox.WorkspaceWritePermissionProfile()
			resolved = &config.SandboxPermissionProfileResolution{Profile: &profile}
		}
		workspaceRoots := append([]string(nil), resolved.WorkspaceRoots...)
		if len(workspaceRoots) == 0 {
			workspaceRoots = []string{absoluteCWD}
		}
		return windowssandbox.GrantReadRootNonElevated(&windowssandbox.ReadRootGrantRequest{
			PermissionProfile: resolved.Profile,
			WorkspaceRoots:    workspaceRoots,
			CommandCWD:        absoluteCWD,
			Env:               environmentMapFromEnviron(os.Environ()),
			CodexHome:         codexHome,
		}, path)
	}
}

func interactiveRemoteSandboxReadDirHandler(root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.SandboxReadDirFunc {
	if !interactiveRemoteEndpointIsLocal(endpoint) {
		return func(string) (string, error) {
			return "", errors.New("sandbox read access cannot be changed through a remote app-server connection")
		}
	}
	return interactiveSandboxReadDirHandler(root)
}

func interactiveRemoteEndpointIsLocal(endpoint *appserverdaemon.RemoteAppServerEndpoint) bool {
	if endpoint == nil {
		return false
	}
	if endpoint.Kind == appserverdaemon.RemoteEndpointUnixSocket {
		return true
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
