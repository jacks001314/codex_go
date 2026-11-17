package cloudclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newEnvironmentCatalogClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	client, err := New(server.URL, Credentials{BearerToken: "token", AccountID: "account"})
	if err != nil {
		server.Close()
		t.Fatalf("New() error = %v", err)
	}
	client.httpClient = server.Client()
	return client, server
}

// Rust #61956: the saved environment catalog follows each scope's cursors,
// drops the default environment, and marks workspace-shared entries.
func TestSavedEnvironmentCatalogFollowsEachScopeCursorLikeRust(t *testing.T) {
	var requests []*http.Request
	responses := []string{
		`{"data":[{"id":"asenvcfg_base","name":"Default"}],"next_cursor":"a+b"}`,
		`{"data":[{"id":"asenvcfg_personal","name":"Personal Rust"},{"id":"","name":"Broken"}],"next_cursor":null}`,
		`{"data":[{"id":"ws~asenvcfg_saved","name":"Team Rust"},{"id":"ws~blank","name":"   "}],"next_cursor":null}`,
	}
	client, server := newEnvironmentCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Clone(context.Background()))
		if len(requests) > len(responses) {
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responses[len(requests)-1]))
	})
	defer server.Close()
	defer client.Close()

	environments, err := client.ListSavedEnvironments(context.Background())
	if err != nil {
		t.Fatalf("ListSavedEnvironments() error = %v", err)
	}
	want := []SavedEnvironment{
		{ID: "asenvcfg_personal", Name: "Personal Rust"},
		{ID: "ws~asenvcfg_saved", Name: "Team Rust", WorkspaceShared: true},
	}
	if len(environments) != len(want) {
		t.Fatalf("environments = %#v", environments)
	}
	for index, environment := range want {
		if environments[index] != environment {
			t.Fatalf("environments[%d] = %#v, want %#v", index, environments[index], environment)
		}
	}
	wantQueries := []string{
		"scope=user&limit=100&omitDraft=true&view=picker",
		"scope=user&limit=100&omitDraft=true&view=picker&cursor=a%2Bb",
		"scope=workspace&limit=100&omitDraft=true&view=picker",
	}
	if len(requests) != len(wantQueries) {
		t.Fatalf("requests = %d, want %d", len(requests), len(wantQueries))
	}
	for index, request := range requests {
		if request.URL.Path != "/v1/environment-configs" || request.URL.RawQuery != wantQueries[index] {
			t.Fatalf("request[%d] = %s?%s", index, request.URL.Path, request.URL.RawQuery)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := request.Header.Get("chatgpt-account-id"); got != "account" {
			t.Fatalf("account header = %q", got)
		}
		if got := request.Header.Get("x-openai-product-sku"); got != "codex" {
			t.Fatalf("product sku = %q", got)
		}
	}
}

// Rust #61956: any failed, oversized, or malformed page fails the read
// instead of returning a partial catalog.
func TestSavedEnvironmentCatalogRejectsBadPagesLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			},
		},
		{
			name: "malformed",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("{"))
			},
		},
		{
			name: "oversized",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":[{"id":"a","name":"` + strings.Repeat("x", environmentCatalogMaxBytes) + `"}]}`))
			},
		},
		{
			name: "repeated cursor",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":[],"next_cursor":"same"}`))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, server := newEnvironmentCatalogClient(t, tc.handler)
			defer server.Close()
			defer client.Close()
			environments, err := client.ListSavedEnvironments(context.Background())
			if !errors.Is(err, ErrEnvironmentCatalogUnavailable) || environments != nil {
				t.Fatalf("ListSavedEnvironments() = %#v, %v", environments, err)
			}
		})
	}
}
