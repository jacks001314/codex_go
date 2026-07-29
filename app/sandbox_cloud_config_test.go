package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
)

func TestSandboxFetchesCachesAndEnforcesCloudManagedPermissionProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/backend-api/wham/config/bundle" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer chatgpt-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "workspace-123" {
			t.Fatalf("ChatGPT-Account-ID = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"requirements_toml": map[string]any{
				"enterprise_managed": []map[string]any{{
					"id": "req-managed-cloud", "name": "Managed permissions", "contents": cloudSandboxRequirements,
				}},
			},
		})
	}))
	defer server.Close()

	configBody := fmt.Sprintf("cli_auth_credentials_store = \"file\"\nchatgpt_base_url = %q\n", server.URL+"/backend-api")
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	snapshot := auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: map[string]any{
		"access_token": "chatgpt-token", "account_id": "workspace-123", "chatgpt_account_id": "workspace-123",
		"chatgpt_user_id": "user-123", "plan_type": "enterprise",
	}}
	if err := auth.NewStoreWithOptions(home, auth.StoreOptionsFromConfig("file", false)).Save(snapshot); err != nil {
		t.Fatalf("Save(auth) error = %v", err)
	}

	if _, err := loadSandboxRunConfigForRunContext(context.Background(), &cli.SandboxOptions{IncludeManagedConfig: true}); err != nil {
		t.Fatalf("default sandbox load error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("default sandbox requested cloud config %d times", requests.Load())
	}

	opts := &cli.SandboxOptions{PermissionProfile: "managed-cloud", IncludeManagedConfig: true, CWD: home}
	for attempt := 0; attempt < 2; attempt++ {
		runConfig, err := loadSandboxRunConfigForRunContext(context.Background(), opts)
		if err != nil {
			t.Fatalf("loadSandboxRunConfigForRunContext(%d) error = %v", attempt, err)
		}
		resolved := runConfig.PermissionProfile
		if resolved == nil || resolved.ID != "managed-cloud" || resolved.Profile == nil || !resolved.Profile.AllowsNetwork() {
			t.Fatalf("resolved(%d) = %#v", attempt, resolved)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("cloud config requests = %d, want fetch then cache hit", requests.Load())
	}
	if _, err := os.Stat(filepath.Join(home, "cloud-config-bundle-cache.json")); err != nil {
		t.Fatalf("cloud config cache missing: %v", err)
	}
}

const cloudSandboxRequirements = `
default_permissions = "managed-cloud"

[allowed_permission_profiles]
managed-cloud = true

[permissions.managed-cloud]
extends = ":workspace"

[permissions.managed-cloud.network]
enabled = true
`
