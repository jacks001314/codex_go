package chatgptapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCloudClientVoiceSettingsRequestsLikeRust(t *testing.T) {
	requests := make(chan *http.Request, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests <- request.Clone(request.Context())
		if request.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"selected": "juniper",
				"voices":   []map[string]any{{"voice": "juniper", "name": "Juniper"}, {"voice": "maple"}},
			})
		}
	}))
	defer server.Close()

	client := NewCloudClientWithoutRedirects(&CloudClientOptions{BaseURL: server.URL + "/backend-api"})
	settings, err := client.GetVoiceSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings.Selected != "juniper" || len(settings.Voices) != 2 || settings.Voices[1].Name != nil {
		t.Fatalf("settings = %#v", settings)
	}
	if err := client.SetVoiceSettings(context.Background(), "maple"); err != nil {
		t.Fatal(err)
	}

	read := <-requests
	if read.Method != http.MethodGet || read.URL.Path != "/backend-api/settings/voices" || read.URL.Query().Get("voice_mode") != "wingman" {
		t.Fatalf("read request = %s %s", read.Method, read.URL.String())
	}
	write := <-requests
	if write.Method != http.MethodPatch || write.URL.Path != "/backend-api/settings/account_user_setting" || write.URL.Query().Get("feature") != "voice_name" || write.URL.Query().Get("value") != "maple" {
		t.Fatalf("write request = %s %s", write.Method, write.URL.String())
	}
}
