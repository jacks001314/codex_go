package apps

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatGPTDirectoryProviderListsPagesWorkspaceAndNormalizes(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization = %q", got)
		}
		requests = append(requests, r.URL.String())
		switch r.URL.String() {
		case "/connectors/directory/list?external_logos=true":
			writeDirectoryJSON(t, w, map[string]any{
				"apps": []map[string]any{
					{"id": "alpha", "name": " Alpha "},
					{"id": "hidden", "name": "Hidden", "visibility": "HIDDEN"},
				},
				"nextToken": "page 2",
			})
		case "/connectors/directory/list?external_logos=true&token=page+2":
			writeDirectoryJSON(t, w, map[string]any{
				"apps": []map[string]any{
					{
						"id":          "alpha",
						"name":        "",
						"description": " Merged description ",
						"iconAssets": map[string]string{
							"256_square": "https://example.com/alpha.png",
						},
						"labels": map[string]string{"tier": "alpha"},
					},
					{"id": "beta", "name": "Beta"},
				},
			})
		case "/connectors/directory/list_workspace?external_logos=true":
			writeDirectoryJSON(t, w, map[string]any{
				"apps": []map[string]any{{"id": "workspace", "name": "Workspace App"}},
			})
		default:
			t.Fatalf("unexpected request %s", r.URL.String())
		}
	}))
	defer server.Close()

	provider := NewChatGPTDirectoryProvider(&ChatGPTDirectoryProviderOptions{
		BaseURL: server.URL,
		Headers: http.Header{
			"Authorization": []string{"Bearer token"},
		},
		IsWorkspaceAccount: true,
	})
	response, err := provider.ListDirectoryApps(&AppDirectoryListParams{})
	if err != nil {
		t.Fatalf("ListDirectoryApps() error = %v", err)
	}
	if len(response.Apps) != 3 {
		t.Fatalf("apps = %#v, want alpha, beta, workspace", response.Apps)
	}
	alpha := response.Apps[0]
	if alpha.ID != "alpha" || alpha.Name != "Alpha" || alpha.Description == nil || *alpha.Description != "Merged description" {
		t.Fatalf("alpha = %#v", alpha)
	}
	if alpha.InstallURL == nil || *alpha.InstallURL != "https://chatgpt.com/apps/alpha/alpha" {
		t.Fatalf("alpha install URL = %+v", alpha.InstallURL)
	}
	if alpha.LabelMap["tier"] != "alpha" || alpha.IconAssets["256_square"] == "" || alpha.IsAccessible || !alpha.IsEnabled {
		t.Fatalf("alpha metadata = %#v", alpha)
	}
	if requests[0] != "/connectors/directory/list?external_logos=true" ||
		requests[1] != "/connectors/directory/list?external_logos=true&token=page+2" ||
		requests[2] != "/connectors/directory/list_workspace?external_logos=true" {
		t.Fatalf("requests = %#v", requests)
	}
}

func writeDirectoryJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}
