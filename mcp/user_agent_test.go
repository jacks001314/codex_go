package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPHTTPClientSetsDefaultAndPreservesConfiguredUserAgent(t *testing.T) {
	for _, test := range []struct {
		name       string
		config     *ServerConfig
		wantHeader string
	}{
		{name: "default", wantHeader: mcpUserAgent()},
		{
			name:       "configured",
			config:     &ServerConfig{HTTPHeaders: map[string]string{"user-agent": "custom-agent/9.9"}},
			wantHeader: "custom-agent/9.9",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if got := request.Header.Get("User-Agent"); got != test.wantHeader {
					t.Fatalf("User-Agent = %q, want %q", got, test.wantHeader)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			client := mcpHTTPClientWithDefaultHeaders(server.Client(), test.config)
			response, err := client.Get(server.URL)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			_ = response.Body.Close()
		})
	}
}

func TestMCPOAuthRequestsUseDefaultUserAgent(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("User-Agent"); got != mcpUserAgent() {
			t.Fatalf("User-Agent = %q, want %q", got, mcpUserAgent())
		}
		paths = append(paths, request.URL.Path)
		switch {
		case strings.Contains(request.URL.Path, ".well-known/oauth-authorization-server"):
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": serverURL(request) + "/authorize",
				"token_endpoint":         serverURL(request) + "/token",
			})
		case request.URL.Path == "/token":
			writeJSON(t, w, map[string]any{
				"access_token": "access-new",
				"token_type":   "Bearer",
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	discovery, err := DiscoverStreamableHTTPOAuth(context.Background(), server.URL+"/mcp", server.Client())
	if err != nil || discovery == nil {
		t.Fatalf("DiscoverStreamableHTTPOAuth() = %#v, %v", discovery, err)
	}
	_, err = NewOAuthTokenClient(server.Client()).RefreshToken(context.Background(), &OAuthRefreshOptions{
		ServerName:    "docs",
		ServerURL:     server.URL + "/mcp",
		ClientID:      "client-1",
		TokenEndpoint: server.URL + "/token",
		RefreshToken:  "refresh-old",
	})
	if err != nil {
		t.Fatalf("RefreshToken() error = %v", err)
	}
	if len(paths) < 2 {
		t.Fatalf("OAuth request paths = %#v", paths)
	}
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}
