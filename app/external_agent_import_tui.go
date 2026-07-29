package app

import (
	"encoding/json"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/config"
	codextea "codex_go/tui/tea"
)

const (
	interactiveExternalAgentImportRemoteUnavailable = "Import from other apps is unavailable in remote sessions. Start Codex locally and run /import."
	interactiveExternalAgentImportDaemonUnavailable = "Import from other apps is unavailable while Codex is connected to the local app-server daemon. Stop the daemon, restart Codex, and run /import."
)

func interactiveExternalAgentDetectHandler(root *cli.RootOptions) codextea.ExternalAgentDetectFunc {
	return func(cwd string, migrationSource string) (config.ExternalAgentConfigDetectResponse, error) {
		migrationSource = strings.TrimSpace(migrationSource)
		params := &config.ExternalAgentConfigDetectParams{IncludeHome: true, MigrationSource: &migrationSource}
		if cwd = strings.TrimSpace(cwd); cwd != "" {
			params.CWDs = []string{cwd}
		}
		response := interactiveConfigService(root).DetectExternalAgentConfig(params)
		if response == nil {
			return config.ExternalAgentConfigDetectResponse{}, errors.New("external agent returned no response")
		}
		return *response, nil
	}
}

func interactiveExternalAgentImportHandler(root *cli.RootOptions) codextea.ExternalAgentImportFunc {
	return func(items []config.ExternalAgentConfigMigrationItem, migrationSource string) (config.ExternalAgentConfigImportResponse, <-chan codextea.ExternalAgentImportCompletion, error) {
		router := appserver.NewRuntimeRouter(appserver.RuntimeServices{
			Config:       interactiveConfigService(root),
			ThreadRouter: appserver.NewRouter(newSessionStore()),
		})
		notifications := make(chan config.ExternalAgentConfigImportCompletedNotification, 1)
		router.SetNotificationSink(appserver.NotificationSinkFunc(func(notification *appserver.Notification) {
			if notification == nil || notification.Method != appserver.NotificationExternalAgentConfigImportCompleted {
				return
			}
			if payload, ok := notification.Params.(*config.ExternalAgentConfigImportCompletedNotification); ok && payload != nil {
				notifications <- *payload
			}
		}))
		migrationSource = strings.TrimSpace(migrationSource)
		params, err := json.Marshal(config.ExternalAgentConfigImportParams{
			MigrationItems:  append([]config.ExternalAgentConfigMigrationItem(nil), items...),
			MigrationSource: &migrationSource,
		})
		if err != nil {
			_ = router.Close()
			return config.ExternalAgentConfigImportResponse{}, nil, err
		}
		response := router.Handle(&appserver.Request{
			JSONRPC: "2.0",
			ID:      appserver.IntID(1),
			Method:  appserver.MethodExternalAgentConfigImport,
			Params:  params,
		})
		if response == nil {
			_ = router.Close()
			return config.ExternalAgentConfigImportResponse{}, nil, errors.New("externalAgentConfig/import returned no response")
		}
		if response.Error != nil {
			_ = router.Close()
			return config.ExternalAgentConfigImportResponse{}, nil, errors.New(response.Error.Message)
		}
		imported, ok := response.Result.(*config.ExternalAgentConfigImportResponse)
		if !ok || imported == nil {
			_ = router.Close()
			return config.ExternalAgentConfigImportResponse{}, nil, errors.New("externalAgentConfig/import returned an unexpected response")
		}
		completion := make(chan codextea.ExternalAgentImportCompletion, 1)
		go func() {
			completed := <-notifications
			completion <- codextea.ExternalAgentImportCompletion{Completed: completed}
			close(completion)
			_ = router.Close()
		}()
		return *imported, completion, nil
	}
}

func interactiveRemoteExternalAgentDetectHandler(endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.ExternalAgentDetectFunc {
	message := interactiveExternalAgentImportRemoteUnavailable
	if interactiveRemoteEndpointIsLocal(endpoint) {
		message = interactiveExternalAgentImportDaemonUnavailable
	}
	return func(string, string) (config.ExternalAgentConfigDetectResponse, error) {
		return config.ExternalAgentConfigDetectResponse{}, errors.New(message)
	}
}
