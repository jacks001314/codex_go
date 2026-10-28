package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/keyring"
)

type configServerTestStore struct {
	mu     sync.Mutex
	values map[string]string
}

func newConfigServerTestStore() *configServerTestStore {
	return &configServerTestStore{values: map[string]string{}}
}

func (s *configServerTestStore) key(service string, account string) string {
	return service + "\x00" + account
}

func (s *configServerTestStore) Load(service string, account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[s.key(service, account)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (s *configServerTestStore) Save(service string, account string, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[s.key(service, account)] = secret
	return nil
}

func (s *configServerTestStore) Delete(service string, account string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.key(service, account)
	if _, ok := s.values[key]; !ok {
		return false, nil
	}
	delete(s.values, key)
	return true, nil
}

func configServerTestBundle(id string) config.CloudConfigBundle {
	return config.CloudConfigBundle{
		ConfigTOML: config.CloudConfigTOMLBundle{EnterpriseManaged: []config.CloudConfigFragment{{
			ID: id, Name: id, Contents: "model = \"gpt-5\"",
		}}},
	}
}

func configServerTestBootstrap() *config.Config {
	return &config.Config{Values: map[string]any{}}
}

// Mirrors Rust policy_fetch_requires_config_server_sign_in: a configured OAuth
// server surfaces a sign-in error without touching the network.
func TestConfigServerFetchRequiresSignInLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	home := t.TempDir()
	configServer := &config.ConfigServer{
		BaseURL: server.URL + "/settings",
		OAuth: &config.ConfigServerOAuth{
			AuthorizationURL: server.URL + "/authorize",
			TokenURL:         server.URL + "/token",
			ClientID:         "company-client",
		},
	}
	loader, err := configServerCloudConfigBundleLoaderForRunWithDeps(context.Background(), home, configServerTestBootstrap(), configServer, newConfigServerTestStore(), server.Client())
	if err != nil {
		t.Fatalf("loader error = %v", err)
	}
	bundle, err := loader.Get()
	if bundle != nil {
		t.Fatalf("bundle = %#v", bundle)
	}
	loadErr, ok := err.(*config.CloudConfigLoadError)
	if !ok || loadErr.Code != config.CloudConfigLoadAuth {
		t.Fatalf("error = %#v, want auth", err)
	}
	if loadErr.Message != "Sign in to the configuration server to load your organization's settings" {
		t.Fatalf("message = %q", loadErr.Message)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want none", requests.Load())
	}
}

// Mirrors Rust cached_policy_is_reused_only_for_the_same_endpoint.
func TestConfigServerCacheIsScopedToEndpointLikeRust(t *testing.T) {
	var primaryRequests, otherEndpointRequests atomic.Int32
	bundle := configServerTestBundle("first")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/settings/codex/config/bundle":
			if primaryRequests.Add(1) == 1 {
				_ = json.NewEncoder(w).Encode(bundle)
				return
			}
			http.Error(w, "cache miss", http.StatusForbidden)
		case "/different/codex/config/bundle":
			otherEndpointRequests.Add(1)
			http.Error(w, "different endpoint", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	home := t.TempDir()
	load := func(basePath string) (*config.CloudConfigLoader, error) {
		return configServerCloudConfigBundleLoaderForRunWithDeps(context.Background(), home, configServerTestBootstrap(), &config.ConfigServer{BaseURL: server.URL + basePath}, nil, server.Client())
	}
	first, err := load("/settings")
	if err != nil {
		t.Fatal(err)
	}
	got, err := first.Get()
	if err != nil || got == nil || got.ConfigTOML.EnterpriseManaged[0].ID != "first" {
		t.Fatalf("first load = %#v, %v", got, err)
	}
	again, err := load("/settings")
	if err != nil {
		t.Fatal(err)
	}
	cached, err := again.Get()
	if err != nil || cached == nil || cached.ConfigTOML.EnterpriseManaged[0].ID != "first" {
		t.Fatalf("cached load = %#v, %v", cached, err)
	}
	if primaryRequests.Load() != 1 {
		t.Fatalf("primary requests = %d, want the cache to avoid a refetch", primaryRequests.Load())
	}
	different, err := load("/different")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := different.Get(); err == nil {
		t.Fatal("a different endpoint must not reuse the cached bundle")
	}
	if otherEndpointRequests.Load() != 1 {
		t.Fatalf("different endpoint requests = %d", otherEndpointRequests.Load())
	}
}

// Mirrors Rust for_config_endpoint: the bundle request never follows redirects.
func TestConfigServerBundleFetchDoesNotFollowRedirectsLikeRust(t *testing.T) {
	var redirected atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/settings/codex/config/bundle":
			http.Redirect(w, r, "/other", http.StatusFound)
		case "/other":
			redirected.Add(1)
			_ = json.NewEncoder(w).Encode(configServerTestBundle("redirected"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	loader, err := configServerCloudConfigBundleLoaderForRunWithDeps(context.Background(), t.TempDir(), configServerTestBootstrap(), &config.ConfigServer{BaseURL: server.URL + "/settings"}, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Get(); err == nil {
		t.Fatal("a redirect response must fail the bundle fetch")
	} else if loadErr, ok := err.(*config.CloudConfigLoadError); !ok || loadErr.StatusCode == nil || *loadErr.StatusCode != http.StatusFound {
		t.Fatalf("error = %#v", err)
	}
	if redirected.Load() != 0 {
		t.Fatalf("redirect target requests = %d", redirected.Load())
	}
}

// Mirrors Rust external_auth_recovers_once_per_bundle_fetch: a rejected
// access token is replaced once and the bundle request retried.
func TestConfigServerRecoversRejectedCredentialsOnceLikeRust(t *testing.T) {
	var bundleRequests atomic.Int32
	var refreshes atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshes.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "account-1", "refresh_token": "refresh-1", "expires_in": 3600, "token_type": "Bearer",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "account-0", "refresh_token": "refresh-0", "expires_in": 3600, "token_type": "Bearer",
			})
		case "/settings/codex/config/bundle":
			bundleRequests.Add(1)
			if strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") == "account-1" {
				_ = json.NewEncoder(w).Encode(configServerTestBundle("replacement"))
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	home := t.TempDir()
	store := newConfigServerTestStore()
	oauth := &config.ConfigServerOAuth{
		AuthorizationURL: server.URL + "/authorize",
		TokenURL:         server.URL + "/token",
		ClientID:         "company-client",
	}
	configServer := &config.ConfigServer{BaseURL: server.URL + "/settings", OAuth: oauth}
	manager := auth.NewOAuthSessionManager(configServerOAuthSessionConfig(oauth), home, server.Client(), store)
	loginWithTestBrowser(t, manager)
	loader, err := configServerCloudConfigBundleLoaderForRunWithDeps(context.Background(), home, configServerTestBootstrap(), configServer, store, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := loader.Get()
	if err != nil || bundle == nil || bundle.ConfigTOML.EnterpriseManaged[0].ID != "replacement" {
		t.Fatalf("bundle = %#v, %v", bundle, err)
	}
	if bundleRequests.Load() != 2 {
		t.Fatalf("bundle requests = %d, want 2", bundleRequests.Load())
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshes.Load())
	}
}

func loginWithTestBrowser(t *testing.T, manager *auth.OAuthSessionManager) {
	t.Helper()
	started := make(chan string, 1)
	loginErr := make(chan error, 1)
	go func() {
		loginErr <- manager.LoginWithBrowser(context.Background(), func(value string) { started <- value })
	}()
	authorizationURL := <-started
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	go func() {
		response, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=browser-authorization-code&state=" + url.QueryEscape(parsed.Query().Get("state")))
		if err == nil {
			_, _ = io.ReadAll(response.Body)
			_ = response.Body.Close()
		}
	}()
	if err := <-loginErr; err != nil {
		t.Fatalf("LoginWithBrowser() error = %v", err)
	}
}

// Mirrors Rust's required HTTPS policy through app-server startup: an insecure
// locally managed config server is rejected before any fetch.
func TestAppServerStartupRejectsInsecureConfigServerLikeRust(t *testing.T) {
	home := t.TempDir()
	requirements := "[config_server]\nbase_url = \"http://config.example.com/settings\"\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAppServerConfig(home, cli.AppServerOptions{}, nil); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("loadAppServerConfig() error = %v, want an HTTPS validation error", err)
	}
}
