package apps

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatGPTMetadataProviderRequestAndResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ps/apps/batch" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("ChatGPT-Account-ID") != "account-1" || r.Header.Get("OAI-Product-SKU") != "tpp" {
			t.Fatalf("headers = %#v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["include_tools"] != true {
			t.Fatalf("body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"apps":[{"id":"alpha","name":"Alpha","description":"Alpha description","icon_url":"https://example.test/icon","tools":[{"name":"search","title":"Search","description":"Use search","is_enabled":false,"disabled_reason":"disabled_by_admin","is_read_only":true}]}]}`))
	}))
	defer server.Close()
	provider := NewChatGPTMetadataProvider(&ChatGPTMetadataProviderOptions{
		BaseURL:    server.URL,
		Headers:    http.Header{"Authorization": []string{"Bearer token"}, "ChatGPT-Account-ID": []string{"account-1"}},
		ProductSKU: "tpp",
	})
	response, err := provider.ReadAppMetadata(&AppMetadataReadParams{AppIDs: []string{"alpha", "missing"}, IncludeTools: true})
	if err != nil {
		t.Fatalf("ReadAppMetadata() error = %v", err)
	}
	if len(response.Apps) != 1 || response.Apps[0].ID != "alpha" || len(response.Apps[0].ToolSummaries) != 1 || len(response.MissingAppIDs) != 1 || response.MissingAppIDs[0] != "missing" {
		t.Fatalf("response = %#v", response)
	}
	tool := response.Apps[0].ToolSummaries[0]
	if tool.IsEnabled || tool.DisabledReason == nil || *tool.DisabledReason != "disabled_by_admin" || !tool.IsReadOnly {
		t.Fatalf("tool summary = %#v", tool)
	}
}

func TestChatGPTMetadataProviderDefaultsLegacyToolsEnabled(t *testing.T) {
	legacy := metadataAppTool{Name: "search", Description: "Use search"}
	metadata := (metadataApp{ID: "alpha", Name: "Alpha", Tools: []metadataAppTool{legacy}}).connectorMetadata(true)
	if len(metadata.ToolSummaries) != 1 || !metadata.ToolSummaries[0].IsEnabled || metadata.ToolSummaries[0].IsReadOnly {
		t.Fatalf("legacy tool summary = %#v", metadata.ToolSummaries)
	}
}

func TestChatGPTMetadataProviderDefaultsProductSKUAndRejectsBackendFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OAI-Product-SKU") != "codex" {
			t.Fatalf("product SKU = %q", r.Header.Get("OAI-Product-SKU"))
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	provider := NewChatGPTMetadataProvider(&ChatGPTMetadataProviderOptions{BaseURL: server.URL})
	if _, err := provider.ReadAppMetadata(&AppMetadataReadParams{AppIDs: []string{"alpha"}}); err == nil {
		t.Fatal("ReadAppMetadata() error = nil")
	}
}
