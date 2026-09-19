package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// cloudConfigStubDoer records the bootstrap GET attempts and returns either a
// transport failure or a canned response.
type cloudConfigStubDoer struct {
	calls    int
	err      error
	response func() *http.Response
}

func (d *cloudConfigStubDoer) Do(*http.Request) (*http.Response, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return d.response(), nil
}

func cloudConfigBundleResponse(t *testing.T) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
			ID: "req-managed-cloud", Name: "Managed permissions", Contents: cloudManagedPermissionProfileRequirements,
		}}},
	})
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(encoded)),
	}
}

// Mirrors Rust #46562: a bootstrap GET that fails to connect is retried through
// the system-proxy client, while an HTTP error is final.
func TestLoadCloudConfigBundleRetriesThroughSystemProxyLikeRust(t *testing.T) {
	connectionFailure := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	primary := &cloudConfigStubDoer{err: connectionFailure}
	fallback := &cloudConfigStubDoer{response: func() *http.Response { return cloudConfigBundleResponse(t) }}
	bundle, err := LoadCloudConfigBundle(context.Background(), CloudConfigFetchOptions{
		CodexHome:          t.TempDir(),
		BaseURL:            "https://example.test/backend-api",
		HTTPClient:         primary,
		FallbackHTTPClient: fallback,
	})
	if err != nil {
		t.Fatalf("LoadCloudConfigBundle error = %v", err)
	}
	if bundle == nil || primary.calls != 1 || fallback.calls != 1 {
		t.Fatalf("bundle=%#v primary=%d fallback=%d", bundle, primary.calls, fallback.calls)
	}

	httpError := &cloudConfigStubDoer{response: func() *http.Response {
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("nope"))}
	}}
	unusedFallback := &cloudConfigStubDoer{response: func() *http.Response { return cloudConfigBundleResponse(t) }}
	if _, err := LoadCloudConfigBundle(context.Background(), CloudConfigFetchOptions{
		CodexHome:          t.TempDir(),
		BaseURL:            "https://example.test/backend-api",
		HTTPClient:         httpError,
		FallbackHTTPClient: unusedFallback,
	}); err == nil {
		t.Fatal("an HTTP error must fail the load")
	}
	if unusedFallback.calls != 0 {
		t.Fatalf("fallback ran %d times after an HTTP error", unusedFallback.calls)
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
	// Rust resolve_default_permissions falls back to the required default when
	// the selected profile is disallowed.
	fallback, err := withManaged.ResolveSandboxPermissionProfile(":danger-full-access", home)
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile(disallowed) error = %v", err)
	}
	if fallback == nil || fallback.ID != "managed-cloud" {
		t.Fatalf("disallowed profile did not fall back to the required default: %#v", fallback)
	}
}
