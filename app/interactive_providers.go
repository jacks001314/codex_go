package app

import (
	"errors"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/model"
	codextea "codex_go/tui/tea"
)

// interactiveLocalModelProviderList browses the embedded app server's provider
// catalog without opening a connection (Rust #59541 app::provider).
func interactiveLocalModelProviderList(root *cli.RootOptions) codextea.ModelProviderListFunc {
	return func() (model.ProviderListResponse, error) {
		router := appserver.NewRuntimeRouter(appserver.RuntimeServices{
			Config:     interactiveConfigService(root),
			DefaultCWD: interactiveSessionPickerCWD(root),
		})
		defer router.Close()
		response, err := router.ModelProviderList()
		if err != nil {
			return model.ProviderListResponse{}, err
		}
		if response == nil {
			return model.ProviderListResponse{}, errors.New("modelProvider/list returned no response")
		}
		return *response, nil
	}
}
