package appserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"codex_go/apps"
	"codex_go/auth"
	"codex_go/config"
)

type appReadProviderFunc func(*apps.AppMetadataReadParams) (*apps.AppMetadataReadResponse, error)

func (f appReadProviderFunc) ReadAppMetadata(params *apps.AppMetadataReadParams) (*apps.AppMetadataReadResponse, error) {
	return f(params)
}

func TestRuntimeRouterAppReadDeduplicatesAndReturnsMetadata(t *testing.T) {
	service := apps.NewAppService(nil)
	service.SetMetadataProvider(appReadProviderFunc(func(params *apps.AppMetadataReadParams) (*apps.AppMetadataReadResponse, error) {
		return &apps.AppMetadataReadResponse{Apps: []apps.ConnectorMetadata{{ID: "alpha", Name: "Alpha"}}}, nil
	}))
	router := NewRuntimeRouter(RuntimeServices{Apps: service})
	response := router.Handle(requestWithParams(t, IntID(1), MethodAppRead, apps.AppsReadParams{AppIDs: []string{"alpha", "missing", "alpha"}}))
	if response.Error != nil {
		t.Fatalf("app/read error = %+v", response.Error)
	}
	result := response.Result.(*apps.AppsReadResponse)
	if len(result.Apps) != 1 || result.Apps[0].ID != "alpha" || len(result.MissingAppIDs) != 1 || result.MissingAppIDs[0] != "missing" {
		t.Fatalf("app/read result = %#v", result)
	}
}

func TestRuntimeRouterAppReadDisabledReturnsAllIDsMissing(t *testing.T) {
	home := t.TempDir()
	configService := config.NewConfigService(home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\napps = false\n"), 0o600); err != nil {
		t.Fatalf("disable apps feature: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Apps: apps.NewAppService(nil), Config: configService})
	response := router.Handle(requestWithParams(t, IntID(1), MethodAppRead, apps.AppsReadParams{AppIDs: []string{"beta", "alpha", "beta"}}))
	if response.Error != nil {
		t.Fatalf("app/read error = %+v", response.Error)
	}
	result := response.Result.(*apps.AppsReadResponse)
	if len(result.Apps) != 0 || len(result.MissingAppIDs) != 2 || result.MissingAppIDs[0] != "beta" || result.MissingAppIDs[1] != "alpha" {
		t.Fatalf("disabled app/read result = %#v", result)
	}
}

func TestRuntimeRouterAppReadRejectsMoreThanOneHundredIDs(t *testing.T) {
	ids := make([]string, 101)
	for index := range ids {
		ids[index] = "app"
	}
	router := NewRuntimeRouter(RuntimeServices{Apps: apps.NewAppService(nil)})
	response := router.Handle(requestWithParams(t, IntID(1), MethodAppRead, apps.AppsReadParams{AppIDs: ids}))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode || response.Error.Message != "app/read accepts at most 100 appIds" {
		t.Fatalf("app/read error = %+v", response.Error)
	}
}

func TestAppReadProtocolJSONShape(t *testing.T) {
	params, err := json.Marshal(apps.AppsReadParams{AppIDs: []string{"app"}})
	// Rust 7f928f6ddc: app/read accepts an optional threadId that evaluates the
	// thread's effective configuration; the wire shape includes it as null.
	if err != nil || string(params) != `{"appIds":["app"],"threadId":null}` {
		t.Fatalf("params JSON = %s err=%v", params, err)
	}
}

func TestRuntimeRouterAppReadUsesChatGPTBatchProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("ChatGPT-Account-ID") != "account-1" || r.Header.Get("OAI-Product-SKU") != "tpp" {
			t.Fatalf("headers = %#v", r.Header)
		}
		_, _ = w.Write([]byte(`{"apps":[{"id":"alpha","name":"Alpha","tools":[]}]}`))
	}))
	defer server.Close()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("chatgpt_base_url = \""+server.URL+"\"\napps_mcp_product_sku = \"tpp\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("token", "account-1", nil)); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Apps: apps.NewAppService(nil), Config: config.NewConfigService(home)})
	response := router.Handle(requestWithParams(t, IntID(1), MethodAppRead, apps.AppsReadParams{AppIDs: []string{"alpha"}, IncludeTools: true}))
	if response.Error != nil {
		t.Fatalf("app/read error = %+v", response.Error)
	}
	result := response.Result.(*apps.AppsReadResponse)
	if len(result.Apps) != 1 || result.Apps[0].ID != "alpha" {
		t.Fatalf("result = %#v", result)
	}
}
