package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"codex_go/auth"
	"codex_go/config"
)

const (
	execServerCloudBundlePath   = "/backend-api/wham/config/bundle"
	execServerCloudRegisterPath = "/cloud/environment/test/register"
)

type execServerCloudRequestLog struct {
	mu    sync.Mutex
	paths []string
}

func (l *execServerCloudRequestLog) add(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paths = append(l.paths, path)
}

func (l *execServerCloudRequestLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.paths...)
}

func TestRemoteExecServerLoadsCloudProviderWithChatGPTAuthLikeRust(t *testing.T) {
	log := &execServerCloudRequestLog{}
	bundle := map[string]any{
		"config_toml": map[string]any{
			"enterprise_managed": []map[string]any{{
				"id":       "cloud-provider",
				"name":     "Cloud provider",
				"contents": "[model_providers.cloud-provider]\nname = \"Cloud provider\"\nwire_api = \"responses\"\n",
			}},
		},
	}
	home, serverURL := startExecServerCloudBackend(t, log, bundle, "Bearer chatgpt-token")
	writeExecServerCloudConfig(t, home, serverURL, "model_provider = \"cloud-provider\"\n")
	saveExecServerCloudAuth(t, home)

	err := Run(context.Background(), []string{
		"exec-server", "--remote", serverURL, "--environment-id", "test",
	}, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "registration reached") {
		t.Fatalf("remote exec-server error = %v, want registration rejection", err)
	}
	want := []string{execServerCloudBundlePath, execServerCloudRegisterPath}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("request paths = %v, want %v", got, want)
	}
}

func TestRemoteExecServerAPIKeySkipsChatGPTCloudConfigLikeRust(t *testing.T) {
	log := &execServerCloudRequestLog{}
	home, serverURL := startExecServerCloudBackend(t, log, nil, "Bearer test-api-key")
	t.Setenv(auth.CodexAPIKeyEnv, "test-api-key")
	writeExecServerCloudConfig(t, home, serverURL, "")
	_ = os.Remove(filepath.Join(home, "auth.json"))

	err := Run(context.Background(), []string{
		"exec-server", "--remote", serverURL, "--environment-id", "test",
	}, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "registration reached") {
		t.Fatalf("remote exec-server error = %v, want registration rejection", err)
	}
	want := []string{execServerCloudRegisterPath}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("request paths = %v, want %v", got, want)
	}
}

func TestRemoteExecServerCloudPolicyDeniesRegistrationLikeRust(t *testing.T) {
	log := &execServerCloudRequestLog{}
	bundle := map[string]any{
		"requirements_toml": map[string]any{
			"enterprise_managed": []map[string]any{{
				"id":   "cloud-policy",
				"name": "Cloud policy",
				"contents": "[application.network]\n" +
					"enabled = true\n" +
					"[application.network.domains]\n" +
					"\"gateway.example.com\" = \"allow\"\n",
			}},
		},
	}
	home, serverURL := startExecServerCloudBackend(t, log, bundle, "Bearer chatgpt-token")
	writeExecServerCloudConfig(t, home, serverURL, "")
	saveExecServerCloudAuth(t, home)

	err := Run(context.Background(), []string{
		"exec-server", "--remote", serverURL, "--environment-id", "test",
	}, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "destination denied by application network policy") {
		t.Fatalf("remote exec-server error = %v, want the cloud policy denial", err)
	}
	want := []string{execServerCloudBundlePath}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("request paths = %v, want %v", got, want)
	}
}

func TestRemoteExecServerAgentIdentitySkipsChatGPTCloudConfigLikeRust(t *testing.T) {
	log := &execServerCloudRequestLog{}
	home, serverURL := startExecServerCloudBackend(t, log, nil, "Bearer agent-token")
	t.Setenv(auth.CodexAccessTokenEnv, "agent-token")
	writeExecServerCloudConfig(t, home, serverURL, "")

	err := Run(context.Background(), []string{
		"exec-server", "--remote", serverURL, "--environment-id", "test", "--use-agent-identity-auth",
	}, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "registration reached") {
		t.Fatalf("remote exec-server error = %v, want registration rejection", err)
	}
	want := []string{execServerCloudRegisterPath}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("request paths = %v, want %v", got, want)
	}
}

func startExecServerCloudBackend(t *testing.T, log *execServerCloudRequestLog, bundle map[string]any, registrationAuth string) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r.URL.Path)
		switch r.URL.Path {
		case execServerCloudBundlePath:
			if bundle == nil {
				t.Errorf("unexpected cloud config bootstrap")
				http.NotFound(w, r)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer chatgpt-token" {
				t.Errorf("cloud config Authorization = %q", got)
			}
			if got := r.Header.Get("ChatGPT-Account-ID"); got != "workspace-123" {
				t.Errorf("cloud config ChatGPT-Account-ID = %q", got)
			}
			_ = json.NewEncoder(w).Encode(bundle)
		case execServerCloudRegisterPath:
			if got := r.Header.Get("Authorization"); got != registrationAuth {
				t.Errorf("registration Authorization = %q, want %q", got, registrationAuth)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, "registration reached")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return home, server.URL
}

func writeExecServerCloudConfig(t *testing.T, home string, serverURL string, extra string) {
	t.Helper()
	body := fmt.Sprintf("cli_auth_credentials_store = \"file\"\nchatgpt_base_url = %q\n%s", serverURL+"/backend-api", extra)
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath(home)), 0o700); err != nil {
		t.Fatalf("MkdirAll(config) error = %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
}

func saveExecServerCloudAuth(t *testing.T, home string) {
	t.Helper()
	snapshot := auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: map[string]any{
		"access_token": "chatgpt-token", "account_id": "workspace-123", "chatgpt_account_id": "workspace-123",
		"chatgpt_user_id": "user-123", "plan_type": "enterprise",
	}}
	if err := auth.NewStoreWithOptions(home, auth.StoreOptionsFromConfig("file", false)).Save(snapshot); err != nil {
		t.Fatalf("Save(auth) error = %v", err)
	}
}
