package appserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"codex_go/auth"
	"codex_go/config"
)

func TestAccountVoiceSettingsReadWriteLikeRust(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token": "voice-token",
			"account_id":   "workspace-1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	requests := make(chan *http.Request, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests <- request.Clone(request.Context())
		if request.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"selected": "juniper",
				"voices":   []map[string]any{{"voice": "juniper", "name": "Juniper"}, {"voice": "maple"}},
			})
		}
	}))
	defer backend.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte("chatgpt_base_url = \""+backend.URL+"/backend-api\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Account: auth.NewAccountManager(), Config: config.NewConfigService(home)})
	initializeExperimentalConnection(t, router, "voice-settings")

	read := requestWithParams(t, IntID(2), MethodAccountVoiceSettingsRead, nil)
	read.ConnectionID = "voice-settings"
	readResponse := router.Handle(read)
	if readResponse.Error != nil {
		t.Fatalf("read error = %+v", readResponse.Error)
	}
	settings := readResponse.Result.(*auth.AccountVoiceSettingsReadResponse)
	if settings.Selected != "juniper" || len(settings.Voices) != 2 || settings.Voices[1] != (auth.AccountVoiceOption{Voice: "maple", Name: "maple"}) {
		t.Fatalf("read response = %#v", settings)
	}

	write := requestWithParams(t, IntID(3), MethodAccountVoiceSettingsWrite, auth.AccountVoiceSettingsWriteParams{Voice: "maple"})
	write.ConnectionID = "voice-settings"
	writeResponse := router.Handle(write)
	if writeResponse.Error != nil {
		t.Fatalf("write error = %+v", writeResponse.Error)
	}
	if _, ok := writeResponse.Result.(*auth.AccountVoiceSettingsWriteResponse); !ok {
		t.Fatalf("write response = %#v", writeResponse.Result)
	}

	readRequest := <-requests
	if readRequest.Header.Get("Authorization") != "Bearer voice-token" || readRequest.URL.Path != "/backend-api/settings/voices" {
		t.Fatalf("read request = %#v", readRequest)
	}
	writeRequest := <-requests
	if writeRequest.Method != http.MethodPatch || writeRequest.URL.Query().Get("value") != "maple" {
		t.Fatalf("write request = %s %s", writeRequest.Method, writeRequest.URL.String())
	}
}

func TestAccountVoiceSettingsRejectsNonChatGPTAuthLikeRust(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "test-key"}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Account: auth.NewAccountManager(), Config: config.NewConfigService(home)})
	initializeExperimentalConnection(t, router, "voice-auth")
	request := requestWithParams(t, IntID(2), MethodAccountVoiceSettingsRead, nil)
	request.ConnectionID = "voice-auth"
	response := router.Handle(request)
	if response.Error == nil || response.Error.Message != "ChatGPT authentication required for voice settings" {
		t.Fatalf("response = %#v", response)
	}
}
