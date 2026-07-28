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
	chatwidget "codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

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
