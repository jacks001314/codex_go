package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/config"
	codextea "codex_go/tui/tea"
)

const (
	interactiveExternalAgentImportRemoteUnavailable = "Import from other apps is unavailable in remote sessions. Start Codex locally and run /import."
	// Rust #46117: recommend restarting with the explicit embedded-server flag.
	interactiveExternalAgentImportDaemonUnavailable = "Import from other apps is unavailable while Codex is connected to the local app-server daemon. Restart with `codex --no-daemon` and run /import."
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
	return func(cwd string, migrationSource string) (config.ExternalAgentConfigDetectResponse, error) {
		migrationSource = strings.TrimSpace(migrationSource)
		params := &config.ExternalAgentConfigDetectParams{IncludeHome: true}
		if migrationSource != "" {
			params.MigrationSource = &migrationSource
		}
		if cwd = strings.TrimSpace(cwd); cwd != "" {
			params.CWDs = []string{cwd}
		}
		return interactiveRemotePluginCall[config.ExternalAgentConfigDetectResponse](
			context.Background(),
			endpoint,
			appserver.MethodExternalAgentConfigDetect,
			params,
		)
	}
}

func interactiveRemoteExternalAgentImportHandler(endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.ExternalAgentImportFunc {
	return func(items []config.ExternalAgentConfigMigrationItem, migrationSource string) (config.ExternalAgentConfigImportResponse, <-chan codextea.ExternalAgentImportCompletion, error) {
		ctx := context.Background()
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return config.ExternalAgentConfigImportResponse{}, nil, err
		}
		migrationSource = strings.TrimSpace(migrationSource)
		source := "cli"
		params := config.ExternalAgentConfigImportParams{
			MigrationItems: append([]config.ExternalAgentConfigMigrationItem(nil), items...),
			Source:         &source,
		}
		if migrationSource != "" {
			params.ProviderID = &migrationSource
			params.MigrationSource = &migrationSource
		}
		requestID, err := client.sendRequest(ctx, appserver.MethodExternalAgentConfigImport, params)
		if err != nil {
			client.close()
			return config.ExternalAgentConfigImportResponse{}, nil, err
		}
		var response config.ExternalAgentConfigImportResponse
		earlyCompletion := make(chan config.ExternalAgentConfigImportCompletedNotification, 1)
		if err := waitRemoteExternalAgentImportResponse(ctx, client, requestID, &response, earlyCompletion); err != nil {
			client.close()
			return config.ExternalAgentConfigImportResponse{}, nil, err
		}
		completion := make(chan codextea.ExternalAgentImportCompletion, 1)
		go func() {
			defer close(completion)
			defer client.close()
			select {
			case completed := <-earlyCompletion:
				if strings.TrimSpace(completed.ImportID) == strings.TrimSpace(response.ImportID) {
					completion <- codextea.ExternalAgentImportCompletion{Completed: completed}
					return
				}
			default:
			}
			for {
				message, readErr := client.readRemoteMessage(ctx)
				if readErr != nil {
					completion <- codextea.ExternalAgentImportCompletion{Err: readErr}
					return
				}
				if appserver.NotificationMethod(strings.TrimSpace(message.Method)) != appserver.NotificationExternalAgentConfigImportCompleted {
					continue
				}
				var completed config.ExternalAgentConfigImportCompletedNotification
				if err := json.Unmarshal(message.Params, &completed); err != nil {
					completion <- codextea.ExternalAgentImportCompletion{Err: err}
					return
				}
				if strings.TrimSpace(completed.ImportID) != strings.TrimSpace(response.ImportID) {
					continue
				}
				completion <- codextea.ExternalAgentImportCompletion{Completed: completed}
				return
			}
		}()
		return response, completion, nil
	}
}

func waitRemoteExternalAgentImportResponse(ctx context.Context, client *remoteAppServerTUIClient, requestID int64, response *config.ExternalAgentConfigImportResponse, completion chan<- config.ExternalAgentConfigImportCompletedNotification) error {
	want := fmt.Sprint(requestID)
	for {
		message, err := client.readRemoteMessage(ctx)
		if err != nil {
			return err
		}
		if len(message.ID) > 0 && strings.TrimSpace(message.Method) == "" {
			got, err := remoteRequestIDString(message.ID)
			if err != nil {
				return err
			}
			if got != want {
				continue
			}
			if message.Error != nil {
				return &remoteRPCError{Code: message.Error.Code, Message: strings.TrimSpace(message.Error.Message)}
			}
			if len(message.Result) > 0 {
				return json.Unmarshal(message.Result, response)
			}
			return nil
		}
		if appserver.NotificationMethod(strings.TrimSpace(message.Method)) != appserver.NotificationExternalAgentConfigImportCompleted {
			continue
		}
		var completed config.ExternalAgentConfigImportCompletedNotification
		if err := json.Unmarshal(message.Params, &completed); err == nil {
			select {
			case completion <- completed:
			default:
			}
		}
	}
}
