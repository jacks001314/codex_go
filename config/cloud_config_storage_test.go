package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

const cloudManagedPermissionProfileRequirements = `
default_permissions = "managed-cloud"

[allowed_permission_profiles]
managed-cloud = true

[permissions.managed-cloud]
extends = ":workspace"

[permissions.managed-cloud.network]
enabled = true
`

func TestLoadCloudConfigBundleFetchesAndUsesIdentityScopedCache(t *testing.T) {
	home := t.TempDir()
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
		_ = json.NewEncoder(w).Encode(CloudConfigBundle{
			RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
				ID: "req-managed-cloud", Name: "Managed permissions", Contents: cloudManagedPermissionProfileRequirements,
			}}},
		})
	}))
	defer server.Close()

	opts := CloudConfigFetchOptions{
		CodexHome:     home,
		BaseURL:       server.URL + "/backend-api",
		ChatGPTUserID: "user-123",
		AccountID:     "workspace-123",
		HTTPClient:    server.Client(),
		Headers: http.Header{
			"Authorization":      []string{"Bearer chatgpt-token"},
			"ChatGPT-Account-ID": []string{"workspace-123"},
		},
	}
	first, err := LoadCloudConfigBundle(context.Background(), opts)
	if err != nil {
		t.Fatalf("LoadCloudConfigBundle(fetch) error = %v", err)
	}
	if first == nil || len(first.RequirementsTOML.EnterpriseManaged) != 1 {
		t.Fatalf("bundle = %#v", first)
	}
	second, err := LoadCloudConfigBundle(context.Background(), opts)
	if err != nil {
		t.Fatalf("LoadCloudConfigBundle(cache) error = %v", err)
	}
	if second == nil || requests.Load() != 1 {
		t.Fatalf("cache result = %#v, requests = %d", second, requests.Load())
	}

	data, err := os.ReadFile(filepath.Join(home, cloudConfigBundleCacheFilename))
	if err != nil {
		t.Fatalf("ReadFile(cache) error = %v", err)
	}
	var cache cloudConfigBundleCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatalf("Unmarshal(cache) error = %v", err)
	}
	if cache.SignedPayload.ChatGPTUserID == nil || *cache.SignedPayload.ChatGPTUserID != "user-123" ||
		cache.SignedPayload.AccountID == nil || *cache.SignedPayload.AccountID != "workspace-123" || cache.Signature == "" {
		t.Fatalf("cache identity/signature = %#v", cache)
	}
}

func TestLoadEffectiveCloudManagedPermissionProfileIsExplicitlyGated(t *testing.T) {
	home := t.TempDir()
	calls := 0
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) {
		calls++
		return &CloudConfigBundle{RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
			ID: "req-managed-cloud", Name: "Managed permissions", Contents: cloudManagedPermissionProfileRequirements,
		}}}}, nil
	})

	withoutManaged, err := LoadEffectiveWithOptions(home, &EffectiveOptions{CloudConfigBundle: loader})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions(default) error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("cloud loader called on default path: %d", calls)
	}
	if _, err := withoutManaged.ResolveSandboxPermissionProfile("managed-cloud", home); err == nil {
		t.Fatal("default path unexpectedly loaded cloud-managed permission profile")
	}

	withManaged, err := LoadEffectiveWithOptions(home, &EffectiveOptions{
		IncludeManagedConfig: true,
		CloudConfigBundle:    loader,
	})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions(managed) error = %v", err)
	}
	resolved, err := withManaged.ResolveSandboxPermissionProfile("managed-cloud", home)
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile() error = %v", err)
	}
	if resolved == nil || resolved.ID != "managed-cloud" || resolved.Profile == nil || !resolved.Profile.AllowsNetwork() {
		t.Fatalf("resolved = %#v", resolved)
	}
	if _, err := withManaged.ResolveSandboxPermissionProfile(":danger-full-access", home); err == nil {
		t.Fatal("managed allowed_permission_profiles did not reject disallowed profile")
	}
}
