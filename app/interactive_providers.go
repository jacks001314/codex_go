package app

import (
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/config"
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

// interactiveLocalModelProviderWriter applies a provider patch through the
// embedded app server's config queue.
func interactiveLocalModelProviderWriter(root *cli.RootOptions) codextea.ModelProviderConfigWriteFunc {
	return func(params model.ProviderConfigWriteParams) (model.ProviderConfigWriteResponse, error) {
		router := appserver.NewRuntimeRouter(appserver.RuntimeServices{
			Config:     interactiveConfigService(root),
			DefaultCWD: interactiveSessionPickerCWD(root),
		})
		defer router.Close()
		response, err := router.ModelProviderConfigWrite(params)
		if err != nil {
			return model.ProviderConfigWriteResponse{}, err
		}
		if response == nil {
			return model.ProviderConfigWriteResponse{}, errors.New("modelProvider/config/write returned no response")
		}
		return *response, nil
	}
}

// interactiveLocalModelProviderClientOverride mirrors Rust
// AppServerSession::client_provider_override_for_new_chats for embedded
// sessions: only an invocation-level selection pins the provider, because the
// embedded server reloads the same user profile before every /new.
func interactiveLocalModelProviderClientOverride(root *cli.RootOptions) string {
	if !launchFlagSelectsModelProvider(root) {
		return ""
	}
	return interactiveConfiguredModelProvider(root)
}

// interactiveRemoteModelProviderClientOverride mirrors Rust's Remote thread
// parameter mode: a named profile or an invocation flag that selects a provider
// pins the provider for new chats.
func interactiveRemoteModelProviderClientOverride(root *cli.RootOptions) string {
	if !launchFlagSelectsModelProvider(root) && !profileSelectsModelProvider(root) {
		return ""
	}
	return interactiveConfiguredModelProvider(root)
}

// launchFlagSelectsModelProvider reports whether this invocation pinned
// model_provider through SessionFlags ("--oss"/"--local-provider" or a
// "-c model_provider=..." override).
func launchFlagSelectsModelProvider(root *cli.RootOptions) bool {
	if root != nil && strings.TrimSpace(root.Shared.OSSProvider) != "" {
		return true
	}
	for _, key := range remoteCLIConfigOverrideKeys(root) {
		if strings.TrimSpace(key) == "model_provider" {
			return true
		}
	}
	return false
}

// profileSelectsModelProvider reports whether a named user profile layer
// contributes model_provider (Rust ConfigLayerSource::User { profile: Some }).
func profileSelectsModelProvider(root *cli.RootOptions) bool {
	read := interactiveEffectiveConfigRead(root)
	if read == nil {
		return false
	}
	return config.HasLaunchSetting(read.Layers, nil, "model_provider")
}

func interactiveConfiguredModelProvider(root *cli.RootOptions) string {
	read := interactiveEffectiveConfigRead(root)
	if read == nil {
		return ""
	}
	return interactiveStringFromConfig(read.Config, "model_provider")
}

func interactiveEffectiveConfigRead(root *cli.RootOptions) *config.ConfigReadResponse {
	params := &config.ConfigReadParams{IncludeLayers: true}
	if cwd := interactiveSessionPickerCWD(root); cwd != "" {
		params.CWD = &cwd
	}
	read, err := interactiveConfigService(root).Read(params)
	if err != nil {
		return nil
	}
	return read
}
