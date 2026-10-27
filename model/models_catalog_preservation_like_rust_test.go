package model

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"codex_go/auth"
)

type scriptedModelsEndpointResult struct {
	response *ModelsEndpointResponse
	err      error
}

type scriptedModelsEndpoint struct {
	calls   int
	etags   []string
	results []scriptedModelsEndpointResult
}

func (e *scriptedModelsEndpoint) ListModels(_ context.Context, etag string) (*ModelsEndpointResponse, error) {
	e.calls++
	e.etags = append(e.etags, etag)
	if len(e.results) == 0 {
		return nil, errors.New("unexpected catalog fetch")
	}
	result := e.results[0]
	e.results = e.results[1:]
	return result.response, result.err
}

// Mirrors Rust #55958: a transient provider-catalog failure preserves the last
// successful models, ETag, freshness, and cached entry.
func TestRemoteModelsManagerPreservesExplicitCatalogOnTransientFailureLikeRust(t *testing.T) {
	failures := map[string]error{
		"network":         errors.New("network unavailable"),
		"request timeout": context.DeadlineExceeded,
		"rate limited":    &modelsEndpointHTTPError{StatusCode: 429},
		"unavailable":     &modelsEndpointHTTPError{StatusCode: 503},
		"not found":       &modelsEndpointHTTPError{StatusCode: 404},
		"malformed":       errors.New("malformed catalog"),
		// A sign-in already in flight is a transient condition, not rejected access
		// (Rust maps GatewayAuthError::LoginInProgress to an invalid request).
		"login in progress": auth.ErrGatewayLoginInProgress,
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
			initial := &ModelsEndpointResponse{
				Models: []ModelInfo{{
					Slug:               "provider-model",
					DisplayName:        "Provider Model",
					Visibility:         VisibilityList,
					SupportedInAPI:     true,
					SupportsSearchTool: true,
					ContextWindow:      1_000_000,
				}},
				ETag: "last-successful-etag",
			}
			endpoint := &scriptedModelsEndpoint{results: []scriptedModelsEndpointResult{
				{response: initial},
				{err: failure},
			}}
			manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
				Endpoint:                endpoint,
				Identity:                "identity-a",
				ExplicitProviderCatalog: true,
			})
			manager.ConfigureCache(home)
			manager.now = func() time.Time { return now }

			first := manager.RawModelCatalog(RefreshOnline)
			if !reflect.DeepEqual(first.Models, initial.Models) {
				t.Fatalf("initial catalog = %#v, want %#v", first.Models, initial.Models)
			}
			info := manager.GetModelInfo("provider-model", nil)
			cachePath := filepath.Join(home, modelsCacheFilename)
			cacheBefore, err := readModelsCache(cachePath)
			if err != nil {
				t.Fatalf("readModelsCache(initial) error = %v", err)
			}

			manager.RefreshIfNewETag("next-etag")
			if endpoint.calls != 2 || endpoint.etags[1] != "last-successful-etag" {
				t.Fatalf("calls/etags after failed refresh = %d/%#v", endpoint.calls, endpoint.etags)
			}
			if got := manager.GetRemoteModels(); !reflect.DeepEqual(got, initial.Models) {
				t.Fatalf("catalog after failed refresh = %#v, want %#v", got, initial.Models)
			}
			if got := manager.GetModelInfo("provider-model", nil); !reflect.DeepEqual(got, info) {
				t.Fatalf("model info after failed refresh = %#v, want %#v", got, info)
			}
			cacheAfter, err := readModelsCache(cachePath)
			if err != nil {
				t.Fatalf("readModelsCache(after failure) error = %v", err)
			}
			if !reflect.DeepEqual(cacheAfter, cacheBefore) {
				t.Fatalf("cache changed on transient failure = %#v, want %#v", cacheAfter, cacheBefore)
			}

			restarted := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
				Endpoint:                endpoint,
				Identity:                "identity-a",
				ExplicitProviderCatalog: true,
			})
			restarted.ConfigureCache(home)
			restarted.now = func() time.Time { return now }
			restartedModels := restarted.RawModelCatalog(RefreshOnlineIfUncached).Models
			if len(restartedModels) != 1 || restartedModels[0].Slug != "provider-model" ||
				!restartedModels[0].SupportsSearchTool || restartedModels[0].ContextWindow != 1_000_000 {
				t.Fatalf("restarted catalog = %#v, want the cached provider metadata", restartedModels)
			}
			if endpoint.calls != 2 {
				t.Fatalf("restart fetched despite fresh cache: calls = %d", endpoint.calls)
			}
		})
	}
}

// Mirrors Rust #55958: access rejection still invalidates the provider catalog
// and persisted cache until a refresh succeeds.
func TestRemoteModelsManagerInvalidatesExplicitCatalogOnAccessRejectionLikeRust(t *testing.T) {
	failures := map[string]error{
		"unauthorized":  &modelsEndpointHTTPError{StatusCode: 401},
		"forbidden":     &modelsEndpointHTTPError{StatusCode: 403},
		"refresh-token": &auth.RefreshTokenFailedError{Reason: auth.RefreshTokenFailedRevoked, Message: "credentials revoked"},
		// Rust #55958: only a required sign-in invalidates the catalog.
		"login required": auth.ErrGatewayLoginRequired,
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			models := []ModelInfo{{Slug: "provider-model", DisplayName: "Provider Model", Visibility: VisibilityList, SupportedInAPI: true}}
			endpoint := &scriptedModelsEndpoint{results: []scriptedModelsEndpointResult{
				{response: &ModelsEndpointResponse{Models: models, ETag: "etag-1"}},
				{err: failure},
				{response: &ModelsEndpointResponse{Models: models, ETag: "etag-2"}},
			}}
			manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
				Endpoint:                endpoint,
				Identity:                "identity-a",
				ExplicitProviderCatalog: true,
			})
			manager.ConfigureCache(home)
			if got := manager.RawModelCatalog(RefreshOnline); !reflect.DeepEqual(got.Models, models) {
				t.Fatalf("initial catalog = %#v", got.Models)
			}
			manager.RefreshIfNewETag("next-etag")
			if got := manager.GetRemoteModels(); len(got) != 0 {
				t.Fatalf("catalog after access rejection = %#v, want empty", got)
			}
			cache, err := readModelsCache(filepath.Join(home, modelsCacheFilename))
			if err != nil {
				t.Fatalf("readModelsCache() error = %v", err)
			}
			if len(cache.Models) != 0 || cache.ETag != "" {
				t.Fatalf("cache after access rejection = %#v, want empty authoritative entry", cache)
			}
			if got := manager.RawModelCatalog(RefreshOnlineIfUncached); !reflect.DeepEqual(got.Models, models) {
				t.Fatalf("catalog after successful refresh = %#v, want %#v", got.Models, models)
			}
		})
	}
}

// A successful empty catalog remains authoritative and is persisted.
func TestRemoteModelsManagerPersistsEmptyExplicitCatalogLikeRust(t *testing.T) {
	home := t.TempDir()
	endpoint := &scriptedModelsEndpoint{results: []scriptedModelsEndpointResult{
		{response: &ModelsEndpointResponse{Models: []ModelInfo{{Slug: "provider-model", DisplayName: "Provider Model", Visibility: VisibilityList, SupportedInAPI: true}}, ETag: "etag-1"}},
		{response: &ModelsEndpointResponse{Models: nil, ETag: "etag-empty"}},
	}}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		Endpoint:                endpoint,
		Identity:                "identity-a",
		ExplicitProviderCatalog: true,
	})
	manager.ConfigureCache(home)
	_ = manager.RawModelCatalog(RefreshOnline)
	if got := manager.RawModelCatalog(RefreshOnline); len(got.Models) != 0 {
		t.Fatalf("empty authoritative catalog = %#v, want empty", got.Models)
	}
	cache, err := readModelsCache(filepath.Join(home, modelsCacheFilename))
	if err != nil {
		t.Fatalf("readModelsCache() error = %v", err)
	}
	if len(cache.Models) != 0 || cache.ETag != "etag-empty" {
		t.Fatalf("persisted empty catalog = %#v", cache)
	}
}
