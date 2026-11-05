package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
)

func execServerTestChatGPTToken(t *testing.T, accountID string, plan string) string {
	t.Helper()
	authClaims := map[string]any{}
	if accountID != "" {
		authClaims["chatgpt_account_id"] = accountID
	}
	if plan != "" {
		authClaims["chatgpt_plan_type"] = plan
	}
	payload, err := json.Marshal(map[string]any{"https://api.openai.com/auth": authClaims})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".c2ln"
}

func TestExecServerChatGPTTokenClaimsLikeRust(t *testing.T) {
	token := execServerTestChatGPTToken(t, "alderaan", "enterprise")
	snapshot, err := execServerChatGPTAuthFromToken(token, "", false)
	if err != nil {
		t.Fatalf("execServerChatGPTAuthFromToken() error = %v", err)
	}
	if got := auth.AccountIDFromAuthForRestrictions(snapshot); got != "alderaan" {
		t.Fatalf("account id = %q", got)
	}
	if got := auth.ChatGPTPlanTypeRawFromAuth(snapshot); got != string(auth.PlanEnterprise) {
		t.Fatalf("plan = %q", got)
	}
	if _, err := execServerChatGPTAuthFromToken(token, "endor", true); err == nil || err.Error() != "--chatgpt-account-id does not match the token" {
		t.Fatalf("mismatched account error = %v", err)
	}
	if _, err := execServerChatGPTAuthFromToken(token, "", true); !errors.Is(err, execServerInvalidChatGPTAccountID) {
		t.Fatalf("explicit empty account error = %v, want execServerInvalidChatGPTAccountID", err)
	}

	accountless := execServerTestChatGPTToken(t, "", "pro")
	if _, err := execServerChatGPTAuthFromToken(accountless, "", false); !errors.Is(err, execServerChatGPTAccountMissing) {
		t.Fatalf("accountless token error = %v, want execServerChatGPTAccountMissing", err)
	}
	snapshot, err = execServerChatGPTAuthFromToken(accountless, "endor", true)
	if err != nil {
		t.Fatalf("accountless token with override error = %v", err)
	}
	if got := auth.ChatGPTPlanTypeRawFromAuth(snapshot); got != string(auth.PlanUnknown) {
		t.Fatalf("accountless override plan = %q, want unknown", got)
	}
}

func TestExecServerChatGPTPolicySelectionLikeRust(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		accountID string
		wantFetch int
	}{
		{name: "personal skips policy", token: execServerTestChatGPTToken(t, "endor", "pro"), wantFetch: 0},
		{name: "enterprise loads policy", token: execServerTestChatGPTToken(t, "endor", "enterprise"), wantFetch: 1},
		{name: "unknown plan loads policy", token: execServerTestChatGPTToken(t, "endor", "future-plan"), wantFetch: 1},
		{name: "unbound account loads policy", token: execServerTestChatGPTToken(t, "", "pro"), accountID: "endor", wantFetch: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/backend-api/wham/config/bundle" {
					t.Errorf("policy path = %q", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer "+tc.token {
					t.Errorf("policy Authorization = %q", r.Header.Get("Authorization"))
				}
				if r.Header.Get("ChatGPT-Account-ID") != "endor" {
					t.Errorf("policy ChatGPT-Account-ID = %q", r.Header.Get("ChatGPT-Account-ID"))
				}
				_, _ = w.Write([]byte(`{"config_toml":{},"requirements_toml":{}}`))
			}))
			defer server.Close()
			snapshot, err := execServerChatGPTAuthFromToken(tc.token, tc.accountID, tc.accountID != "")
			if err != nil {
				t.Fatalf("execServerChatGPTAuthFromToken() error = %v", err)
			}
			home := t.TempDir()
			bootstrap := &config.Config{Values: map[string]any{"chatgpt_base_url": server.URL + "/backend-api"}}
			loader, err := managedConfigBundleLoaderForSuppliedAuthWithClient(context.Background(), home, bootstrap, snapshot, server.Client())
			if err != nil {
				t.Fatalf("managedConfigBundleLoaderForSuppliedAuthWithClient() error = %v", err)
			}
			if tc.wantFetch == 0 {
				if loader != nil {
					t.Fatal("personal plan loader = non-nil, want no policy lookup")
				}
				return
			}
			if loader == nil {
				t.Fatal("policy loader = nil")
			}
			if _, err := loader.Get(); err != nil {
				t.Fatalf("loader.Get() error = %v", err)
			}
			if requests != tc.wantFetch {
				t.Fatalf("policy requests = %d, want %d", requests, tc.wantFetch)
			}
			if _, err := os.Stat(filepath.Join(home, "cloud-config-bundle-cache.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("credential policy load wrote a disk cache: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, "auth.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("credential policy load wrote auth.json: %v", err)
			}
		})
	}
}

func TestExecServerSuppliedTokenKeepsConfigServerAuthLikeRust(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("supplied ChatGPT token replaced the configured policy-server authentication")
	}))
	defer server.Close()
	snapshot, err := execServerChatGPTAuthFromToken(execServerTestChatGPTToken(t, "endor", "pro"), "", false)
	if err != nil {
		t.Fatalf("execServerChatGPTAuthFromToken() error = %v", err)
	}
	configServer := &config.ConfigServer{
		BaseURL: server.URL + "/settings",
		OAuth: &config.ConfigServerOAuth{
			AuthorizationURL: server.URL + "/authorize",
			TokenURL:         server.URL + "/token",
			ClientID:         "client-id",
		},
	}
	bootstrap := &config.Config{
		Values:       map[string]any{"chatgpt_base_url": server.URL + "/backend-api"},
		Requirements: &config.ConfigRequirements{ConfigServer: configServer},
	}
	loader, err := managedConfigBundleLoaderForSuppliedAuthWithClient(context.Background(), t.TempDir(), bootstrap, snapshot, server.Client())
	if err != nil {
		t.Fatalf("managedConfigBundleLoaderForSuppliedAuthWithClient() error = %v", err)
	}
	if loader == nil || loader.ConfigServer() == nil || loader.ConfigServer().BaseURL != configServer.BaseURL {
		t.Fatalf("managed loader config server = %#v", loader)
	}
	if _, err := loader.Get(); err == nil || !strings.Contains(err.Error(), configServerSignInMessage) {
		t.Fatalf("loader.Get() error = %v, want the config-server sign-in requirement", err)
	}
}

func TestExecServerStdinCloseCancelsPendingPolicyLikeRust(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestStarted <- struct{}{}
		time.Sleep(5 * time.Second)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	snapshot, err := execServerChatGPTAuthFromToken(execServerTestChatGPTToken(t, "endor", "enterprise"), "", false)
	if err != nil {
		t.Fatalf("execServerChatGPTAuthFromToken() error = %v", err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer reader.Close()
	closed := monitorExecServerStdinClose(reader)
	ctx, cancel := execServerContextWithStdinClose(context.Background(), closed)
	defer cancel()
	bootstrap := &config.Config{Values: map[string]any{"chatgpt_base_url": server.URL + "/backend-api"}}
	loader, err := managedConfigBundleLoaderForSuppliedAuthWithClient(ctx, t.TempDir(), bootstrap, snapshot, server.Client())
	if err != nil {
		t.Fatalf("managedConfigBundleLoaderForSuppliedAuthWithClient() error = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := loader.Get()
		done <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("policy request did not start")
	}
	_ = writer.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("policy load after stdin close = nil error, want cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("policy load was not cancelled by stdin close")
	}
	if !execServerStdinClosed(closed) {
		t.Fatal("stdin close was not observed")
	}
}
