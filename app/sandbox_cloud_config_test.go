package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

	// Rust #57270: a sandbox invocation without an explicit permission profile
	// still includes managed requirements and bootstraps cloud config.
	opts := &cli.SandboxOptions{CWD: home}
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

func TestSandboxCloudBootstrapSkipsSandboxStateJSONLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	writeFileForSandboxCloudTest(t, config.ConfigPath(home), fmt.Sprintf("chatgpt_base_url = %q\n", server.URL+"/backend-api"))
	saveSandboxCloudAuth(t, home)

	loader, err := sandboxCloudConfigBundleForRun(context.Background(), home, &cli.SandboxOptions{SandboxStateJSON: "{}"})
	if err != nil {
		t.Fatalf("sandboxCloudConfigBundleForRun() error = %v", err)
	}
	if loader != nil {
		t.Fatal("sandbox-state-json still produced a cloud config loader")
	}
	if requests.Load() != 0 {
		t.Fatalf("sandbox-state-json requested cloud config %d times", requests.Load())
	}
}

func TestSandboxCloudBootstrapHonorsLocalNetworkPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	writeFileForSandboxCloudTest(t, config.ConfigPath(home), fmt.Sprintf("chatgpt_base_url = %q\n", server.URL+"/backend-api"))
	writeFileForSandboxCloudTest(t, filepath.Join(home, "requirements.toml"), "[application.network]\nenabled = true\n")
	saveSandboxCloudAuth(t, home)

	loader, err := sandboxCloudConfigBundleForRun(context.Background(), home, &cli.SandboxOptions{})
	if err != nil {
		t.Fatalf("sandboxCloudConfigBundleForRun() error = %v", err)
	}
	if loader == nil {
		t.Fatal("eligible sandbox auth produced no cloud config loader")
	}
	if _, err := loader.Get(); err == nil || !strings.Contains(err.Error(), "destination denied by application network policy") {
		t.Fatalf("cloud bootstrap error = %v, want the local policy denial", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("local policy still sent %d cloud config requests", requests.Load())
	}
}

func TestSandboxCloudConfigAppliesProviderWithoutExplicitProfileLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"config_toml": map[string]any{
				"enterprise_managed": []map[string]any{{
					"id":       "cloud-provider",
					"name":     "Cloud provider",
					"contents": "[model_providers.cloud-provider]\nname = \"Cloud provider\"\nwire_api = \"responses\"\n",
				}},
			},
		})
	}))
	defer server.Close()
	writeFileForSandboxCloudTest(t, config.ConfigPath(home), fmt.Sprintf("chatgpt_base_url = %q\n", server.URL+"/backend-api"))
	saveSandboxCloudAuth(t, home)

	loader, err := sandboxCloudConfigBundleForRun(context.Background(), home, &cli.SandboxOptions{})
	if err != nil {
		t.Fatalf("sandboxCloudConfigBundleForRun() error = %v", err)
	}
	if loader == nil {
		t.Fatal("eligible sandbox auth produced no cloud config loader")
	}
	loaded, err := config.LoadEffectiveWithOptions(home, &config.EffectiveOptions{
		RawOverrides:         []string{`model_provider="cloud-provider"`},
		IncludeManagedConfig: true,
		ManagedConfigPath:    filepath.Join(home, "managed_config.toml"),
		CloudConfigBundle:    loader,
	})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	if got := loaded.Values["model_provider"]; got != "cloud-provider" {
		t.Fatalf("model_provider = %#v", got)
	}
	providers, _ := loaded.Values["model_providers"].(map[string]any)
	provider, _ := providers["cloud-provider"].(map[string]any)
	if provider["name"] != "Cloud provider" {
		t.Fatalf("cloud provider = %#v", provider)
	}
	if requests.Load() != 1 {
		t.Fatalf("cloud config requests = %d", requests.Load())
	}
}

func writeFileForSandboxCloudTest(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func saveSandboxCloudAuth(t *testing.T, home string) {
	t.Helper()
	snapshot := auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: map[string]any{
		"access_token": "chatgpt-token", "account_id": "workspace-123", "chatgpt_account_id": "workspace-123",
		"chatgpt_user_id": "user-123", "plan_type": "enterprise",
	}}
	if err := auth.NewStoreWithOptions(home, auth.StoreOptionsFromConfig("file", false)).Save(snapshot); err != nil {
		t.Fatalf("Save(auth) error = %v", err)
	}
}
