package app

import (
	"encoding/json"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/config"
	codextea "codex_go/tui/tea"
)

func interactiveHooksReader(root *cli.RootOptions) codextea.HooksListReaderFunc {
	return func(cwd string) (appserver.HookListResponse, error) {
		cwd = strings.TrimSpace(cwd)
		if cwd == "" {
			cwd = interactiveSessionPickerCWD(root)
		}
		router := appserver.NewRuntimeRouter(appserver.RuntimeServices{
			Config:     interactiveConfigService(root),
			DefaultCWD: cwd,
		})
		defer router.Close()
		params := appserver.HookListParams{}
		if cwd != "" {
			params.CWDs = []string{cwd}
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return appserver.HookListResponse{}, err
		}
		response := router.Handle(&appserver.Request{
			JSONRPC: "2.0",
			ID:      appserver.IntID(1),
			Method:  appserver.MethodHooksList,
			Params:  raw,
		})
		if response == nil {
			return appserver.HookListResponse{}, errors.New("hooks/list returned no response")
		}
		if response.Error != nil {
			return appserver.HookListResponse{}, errors.New(response.Error.Message)
		}
		listed, ok := response.Result.(*appserver.HookListResponse)
		if !ok || listed == nil {
			return appserver.HookListResponse{}, errors.New("hooks/list returned an unexpected response")
		}
		return *listed, nil
	}
}

func interactiveHookConfigWriter(root *cli.RootOptions) codextea.HookConfigWriteFunc {
	return func(params config.ConfigBatchWriteParams) error {
		_, err := interactiveConfigService(root).BatchWrite(&params)
		return err
	}
}
