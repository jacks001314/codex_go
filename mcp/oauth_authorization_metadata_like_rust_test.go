package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDiscoverAuthorizationMetadataPublishedOnlyLikeRust mirrors Rust #60578:
// conventional endpoints are not treated as published until the server serves
// an authorization metadata document, and published extensions are retained.
func TestDiscoverAuthorizationMetadataPublishedOnlyLikeRust(t *testing.T) {
	published := false
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !published || r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"issuer":"` + issuer + `",
			"authorization_endpoint":"` + issuer + `/authorize",
			"token_endpoint":"` + issuer + `/token",
			"device_authorization_endpoint":"` + issuer + `/device"
		}`))
	}))
	defer server.Close()
	issuer = server.URL

	metadata, err := DiscoverAuthorizationMetadata(context.Background(), issuer, server.Client())
	if err != nil || metadata != nil {
		t.Fatalf("unpublished metadata = %#v, %v; want nil, nil", metadata, err)
	}
	published = true
	metadata, err = DiscoverAuthorizationMetadata(context.Background(), issuer, server.Client())
	if err != nil {
		t.Fatalf("published DiscoverAuthorizationMetadata() error = %v", err)
	}
	if metadata == nil || metadata.AuthorizationEndpoint != issuer+"/authorize" || metadata.TokenEndpoint != issuer+"/token" {
		t.Fatalf("published metadata = %#v", metadata)
	}
	var device string
	if raw := metadata.AdditionalFields["device_authorization_endpoint"]; json.Unmarshal(raw, &device) != nil || device != issuer+"/device" {
		t.Fatalf("device_authorization_endpoint = %s", raw)
	}
}

// TestDiscoverAuthorizationMetadataPropagatesFailuresLikeRust covers an
// unavailable issuer and a same-host issuer mismatch.
func TestDiscoverAuthorizationMetadataPropagatesFailuresLikeRust(t *testing.T) {
	t.Run("unavailable issuer", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		defer server.Close()
		if _, err := DiscoverAuthorizationMetadata(context.Background(), server.URL, server.Client()); err == nil {
			t.Fatal("503 discovery did not return an error")
		}
	})
	t.Run("issuer mismatch", func(t *testing.T) {
		var origin string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/.well-known/oauth-authorization-server" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`{` +
				`"issuer":"` + origin + `/tenant-b",` +
				`"authorization_endpoint":"` + origin + `/authorize",` +
				`"token_endpoint":"` + origin + `/token"}`))
		}))
		defer server.Close()
		origin = server.URL
		_, err := DiscoverAuthorizationMetadata(context.Background(), origin+"/tenant-a", server.Client())
		if err == nil || !strings.Contains(err.Error(), "issuer mismatch") {
			t.Fatalf("issuer mismatch error = %v", err)
		}
	})
}

// TestDiscoverAuthorizationMetadataThroughProtectedResourceLikeRust follows a
// separate authorization server without attaching application credentials.
func TestDiscoverAuthorizationMetadataThroughProtectedResourceLikeRust(t *testing.T) {
	var requests []*http.Request
	var authorizationServer string
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Clone(context.Background()))
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			_, _ = w.Write([]byte(`{"resource":"` + "https://resource.example.test" + `","authorization_servers":["` + authorizationServer + `"]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer resource.Close()

	var authorizationRequests []*http.Request
	authorization := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizationRequests = append(authorizationRequests, r.Clone(context.Background()))
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{` +
			`"issuer":"` + authorizationServer + `",` +
			`"authorization_endpoint":"` + authorizationServer + `/login",` +
			`"token_endpoint":"` + authorizationServer + `/exchange"}`))
	}))
	defer authorization.Close()
	authorizationServer = authorization.URL

	metadata, err := DiscoverAuthorizationMetadata(context.Background(), resource.URL, resource.Client())
	if err != nil {
		t.Fatalf("DiscoverAuthorizationMetadata() error = %v", err)
	}
	if metadata == nil || metadata.Issuer != authorizationServer || metadata.TokenEndpoint != authorizationServer+"/exchange" {
		t.Fatalf("metadata through protected resource = %#v", metadata)
	}
	for _, request := range append(requests, authorizationRequests...) {
		if request.Body != nil && request.ContentLength > 0 {
			t.Fatalf("discovery request carried a body: %s", request.URL)
		}
		for _, header := range []string{"Authorization", "Cookie", "X-Gateway-Authorization"} {
			if value := request.Header.Get(header); value != "" {
				t.Fatalf("discovery request %s carried %s=%q", request.URL, header, value)
			}
		}
	}
}
