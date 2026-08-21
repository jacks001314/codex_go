package appserver

import (
	"context"
	"testing"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/plugin"
	"codex_go/tool"
)

func TestPluginInstallRuntimeRequestsElicitationAndInstallsOnAccept(t *testing.T) {
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{
		Summary: plugin.PluginSummary{
			ID:              "docs@market",
			Name:            "docs",
			DisplayName:     "Docs",
			MarketplaceName: "market",
			InstallPolicy:   plugin.InstallAllowed,
		},
	})
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestMCPElicitation {
			t.Errorf("method = %s", request.Method)
		}
		params, ok := request.Params.(*MCPElicitationRequestParams)
		if !ok {
			t.Errorf("params type = %T", request.Params)
		} else {
			if params.ServerName != codexAppsMCPServerName || params.ElicitationID != "request_plugin_install_call-1" {
				t.Errorf("params = %#v", params)
			}
			meta, ok := params.Meta.(map[string]any)
			if !ok || meta["tool_id"] != "docs@market" || meta["codex_approval_kind"] != "tool_suggestion" {
				t.Errorf("meta = %#v", params.Meta)
			}
		}
		_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: MCPElicitationActionAccept}))
	}))
	runtime := &pluginInstallRuntime{
		broker:   broker,
		plugins:  plugins,
		threadID: "thread-1",
		turnID:   "turn-1",
	}
	result, err := runtime.RequestPluginInstall(context.Background(), &tool.PluginInstallRequest{
		CallID:        "call-1",
		SuggestionID:  "request_plugin_install_call-1",
		Tool:          plugin.DiscoverableInfo{ID: "docs@market", Name: "Docs"},
		SuggestReason: "Use docs",
		Elicitation: &tool.PluginInstallElicitation{
			Message:         "Use docs",
			Meta:            map[string]any{"tool_id": "docs@market", "codex_approval_kind": "tool_suggestion"},
			RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	})
	if err != nil {
		t.Fatalf("RequestPluginInstall() error = %v", err)
	}
	if result == nil || !result.Sent || !result.UserConfirmed || !result.Completed {
		t.Fatalf("result = %#v", result)
	}
	installed := plugins.Installed(&plugin.PluginInstalledParams{})
	if len(installed.Plugins) != 1 || !installed.Plugins[0].Installed || !installed.Plugins[0].Enabled {
		t.Fatalf("installed = %#v", installed.Plugins)
	}
}

func TestPluginInstallRuntimeHydratesRecommendedMetadataBeforeElicitation(t *testing.T) {
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{
		Summary: plugin.PluginSummary{
			ID:              "docs@market",
			Name:            "docs",
			DisplayName:     "Docs",
			MarketplaceName: "market",
			RemotePluginID:  "remote-docs",
			InstallPolicy:   plugin.InstallAllowed,
		},
	})
	broker := NewServerRequestBroker()
	var elicited bool
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		elicited = true
		_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: MCPElicitationActionAccept}))
	}))
	runtime := &pluginInstallRuntime{
		broker:   broker,
		plugins:  plugins,
		threadID: "thread-1",
		turnID:   "turn-1",
	}
	// Rust #39143: a recommended plugin with a remote id hydrates metadata
	// before the install elicitation is presented.
	result, err := runtime.RequestPluginInstall(context.Background(), &tool.PluginInstallRequest{
		CallID:        "call-1",
		SuggestionID:  "request_plugin_install_call-1",
		ToolType:      "plugin",
		Tool:          plugin.DiscoverableInfo{ID: "docs@market", RemotePluginID: "remote-docs", Name: "Docs"},
		SuggestReason: "Use docs",
		Elicitation: &tool.PluginInstallElicitation{
			Message:         "Use docs",
			Meta:            map[string]any{"tool_id": "docs@market"},
			RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	})
	if err != nil {
		t.Fatalf("RequestPluginInstall() error = %v", err)
	}
	if result == nil || !elicited || !result.Completed {
		t.Fatalf("result = %#v elicited=%v", result, elicited)
	}
}

func TestPluginInstallRuntimeSkipsElicitationWhenRecommendedUnavailable(t *testing.T) {
	plugins := plugin.NewPluginService()
	broker := NewServerRequestBroker()
	var elicited bool
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		elicited = true
	}))
	runtime := &pluginInstallRuntime{
		broker:   broker,
		plugins:  plugins,
		threadID: "thread-1",
		turnID:   "turn-1",
	}
	// The recommendation is no longer available, so elicitation is skipped.
	result, err := runtime.RequestPluginInstall(context.Background(), &tool.PluginInstallRequest{
		CallID:        "call-1",
		SuggestionID:  "request_plugin_install_call-1",
		ToolType:      "plugin",
		Tool:          plugin.DiscoverableInfo{ID: "docs@market", RemotePluginID: "missing-remote", Name: "Docs"},
		SuggestReason: "Use docs",
		Elicitation: &tool.PluginInstallElicitation{
			Message:         "Use docs",
			Meta:            map[string]any{"tool_id": "docs@market"},
			RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	})
	if err != nil {
		t.Fatalf("RequestPluginInstall() error = %v", err)
	}
	if result == nil || elicited || result.ResponseAction != "unavailable" {
		t.Fatalf("result = %#v elicited=%v, want unavailable", result, elicited)
	}
}

func TestPluginInstallRuntimeDeclineDoesNotInstall(t *testing.T) {
	configService := config.NewConfigService(t.TempDir())
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "docs@market", Name: "docs", DisplayName: "Docs", MarketplaceName: "market"}})
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{
			Action: MCPElicitationActionDecline,
			Meta:   map[string]any{"persist": "always"},
		}))
	}))
	runtime := &pluginInstallRuntime{broker: broker, plugins: plugins, config: configService}
	result, err := runtime.RequestPluginInstall(context.Background(), &tool.PluginInstallRequest{
		Tool:          plugin.DiscoverableInfo{ID: "docs@market", Name: "Docs"},
		SuggestReason: "Use docs",
	})
	if err != nil {
		t.Fatalf("RequestPluginInstall() error = %v", err)
	}
	if result == nil || !result.Sent || result.UserConfirmed || result.Completed || !result.PersistDisable {
		t.Fatalf("result = %#v", result)
	}
	if installed := plugins.Installed(&plugin.PluginInstalledParams{}); len(installed.Plugins) != 0 {
		t.Fatalf("installed = %#v", installed.Plugins)
	}
	read, err := configService.Read(&config.ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	disabled := disabledToolSuggestEntries(read.Config)
	if len(disabled) != 1 {
		t.Fatalf("disabled = %#v", disabled)
	}
	entry, _ := disabled[0].(map[string]any)
	if entry["type"] != "plugin" || entry["id"] != "docs@market" {
		t.Fatalf("disabled entry = %#v", entry)
	}
}

func TestPluginInstallRuntimeConnectorAcceptDoesNotInstallPlugin(t *testing.T) {
	plugins := plugin.NewPluginService()
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		params, ok := request.Params.(*MCPElicitationRequestParams)
		if !ok {
			t.Fatalf("params type = %T", request.Params)
		}
		if meta, ok := params.Meta.(map[string]any); !ok || meta["suggestion_id"] != nil {
			t.Fatalf("connector install meta must omit suggestion_id: %#v", params.Meta)
		}
		_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: MCPElicitationActionAccept}))
	}))
	runtime := &pluginInstallRuntime{broker: broker, plugins: plugins}
	result, err := runtime.RequestPluginInstall(context.Background(), &tool.PluginInstallRequest{
		ToolType:      "connector",
		Tool:          plugin.DiscoverableInfo{ID: "connector_docs", Name: "Docs Connector", ToolType: "connector"},
		SuggestReason: "Connect docs",
	})
	if err != nil {
		t.Fatalf("RequestPluginInstall() error = %v", err)
	}
	if result == nil || !result.Sent || !result.UserConfirmed || result.Completed {
		t.Fatalf("result = %#v", result)
	}
	if installed := plugins.Installed(&plugin.PluginInstalledParams{}); len(installed.Plugins) != 0 {
		t.Fatalf("installed = %#v", installed.Plugins)
	}
}

func TestPluginInstallRuntimeConnectorAcceptVerifiesAccessibleApp(t *testing.T) {
	appService := apps.NewAppService(nil)
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		appService.Add(&apps.AppEntry{ID: "connector_docs", Name: "Docs Connector", IsAccessible: true, IsEnabled: true})
		_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: MCPElicitationActionAccept}))
	}))
	runtime := &pluginInstallRuntime{broker: broker, apps: appService}
	result, err := runtime.RequestPluginInstall(context.Background(), &tool.PluginInstallRequest{
		ToolType:      "connector",
		Tool:          plugin.DiscoverableInfo{ID: "connector_docs", Name: "Docs Connector", ToolType: "connector"},
		SuggestReason: "Connect docs",
	})
	if err != nil {
		t.Fatalf("RequestPluginInstall() error = %v", err)
	}
	if result == nil || !result.Sent || !result.UserConfirmed || !result.Completed {
		t.Fatalf("result = %#v", result)
	}
}

func TestPluginInstallRuntimeConnectorAcceptForceRefetchesAccessibleApps(t *testing.T) {
	provider := &connectorAccessibleProviderForInstallTest{}
	appService := apps.NewAppService(nil)
	appService.SetAccessibleProvider(provider)
	if _, err := appService.List(&apps.AppListParams{}); err != nil {
		t.Fatalf("List(prime cache) error = %v", err)
	}
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		provider.accessible = true
		_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: MCPElicitationActionAccept}))
	}))
	runtime := &pluginInstallRuntime{broker: broker, apps: appService}
	result, err := runtime.RequestPluginInstall(context.Background(), &tool.PluginInstallRequest{
		ToolType:      "connector",
		Tool:          plugin.DiscoverableInfo{ID: "connector_docs", Name: "Docs Connector", ToolType: "connector"},
		SuggestReason: "Connect docs",
	})
	if err != nil {
		t.Fatalf("RequestPluginInstall() error = %v", err)
	}
	if result == nil || !result.Completed {
		t.Fatalf("result = %#v, want completed after force refetch", result)
	}
	if !provider.sawForceRefetch {
		t.Fatalf("accessible provider was not force-refetched")
	}
}

type connectorAccessibleProviderForInstallTest struct {
	accessible      bool
	sawForceRefetch bool
}

func (p *connectorAccessibleProviderForInstallTest) ListAccessibleApps(params *apps.AppAccessibleListParams) (*apps.AppAccessibleListResponse, error) {
	if params != nil && params.ForceRefetch {
		p.sawForceRefetch = true
	}
	if p == nil || !p.accessible {
		return &apps.AppAccessibleListResponse{}, nil
	}
	return &apps.AppAccessibleListResponse{Apps: []apps.AppEntry{{
		ID:           "connector_docs",
		Name:         "Docs Connector",
		IsAccessible: true,
		IsEnabled:    true,
	}}}, nil
}

func TestPluginInstallCandidatesForTurnApplyDisabledAndLoadedConnectorConfig(t *testing.T) {
	configService := config.NewConfigService(t.TempDir())
	if err := persistDisabledInstallSuggestion(configService, "plugin", "docs@market"); err != nil {
		t.Fatalf("persistDisabledInstallSuggestion() error = %v", err)
	}
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		ID:              "docs@market",
		Name:            "docs",
		DisplayName:     "Docs",
		MarketplaceName: "market",
		InstallPolicy:   plugin.InstallAllowed,
	}})
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		ID:              "keep@market",
		Name:            "keep",
		DisplayName:     "Keep",
		MarketplaceName: "market",
		InstallPolicy:   plugin.InstallAllowed,
	}})
	plugins.AddPlugin(plugin.PluginDetail{
		Summary: plugin.PluginSummary{
			ID:              "installed@market",
			Name:            "installed",
			DisplayName:     "Installed",
			MarketplaceName: "market",
			Installed:       true,
			Enabled:         true,
		},
		Apps: []plugin.AppSummary{{ID: "connector_docs", DisplayName: "Docs Connector"}},
	})
	appService := apps.NewAppService([]apps.AppEntry{{
		ID:           "connector_docs",
		Name:         "Docs Connector",
		IsAccessible: true,
		IsEnabled:    true,
	}})
	router := NewRuntimeRouter(RuntimeServices{
		Apps:    appService,
		Config:  configService,
		Plugins: plugins,
	})

	ids := map[string]bool{}
	for _, candidate := range router.pluginInstallCandidatesForTurn(nil) {
		ids[candidate.ID] = true
	}
	if ids["docs@market"] || ids["connector_docs"] || !ids["keep@market"] {
		t.Fatalf("candidate ids = %#v, want only keep@market from filtered set", ids)
	}
}
