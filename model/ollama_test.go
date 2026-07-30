package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type ollamaTestRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ollamaTestRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestOllamaReadyUsesSharedHTTPClientForAllChecks(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Codex-Shared-Client"); got != "configured" {
			t.Errorf("shared client header = %q", got)
		}
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.14.1"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"gpt-oss:20b"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	baseTransport := server.Client().Transport
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	shared := &http.Client{Transport: ollamaTestRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		cloned := request.Clone(request.Context())
		cloned.Header = request.Header.Clone()
		cloned.Header.Set("X-Codex-Shared-Client", "configured")
		cloned.URL.Scheme = target.Scheme
		cloned.URL.Host = target.Host
		return baseTransport.RoundTrip(cloned)
	})}
	if err := EnsureProviderReady(context.Background(), OllamaOSSProviderID, ReadyConfig{HTTPClient: shared}); err != nil {
		t.Fatalf("EnsureProviderReady() error = %v", err)
	}
	if len(paths) != 2 || paths[0] != "/api/version" || paths[1] != "/api/tags" {
		t.Fatalf("Ollama request paths = %#v", paths)
	}
}

func TestSupportsResponses(t *testing.T) {
	if !OllamaSupportsResponses(OllamaVersion{}) {
		t.Fatalf("dev zero version should be supported")
	}
	if OllamaSupportsResponses(OllamaVersion{Major: 0, Minor: 13, Patch: 3}) {
		t.Fatalf("0.13.3 should not support responses")
	}
	if !OllamaSupportsResponses(OllamaVersion{Major: 0, Minor: 13, Patch: 4}) {
		t.Fatalf("0.13.4 should support responses")
	}
}

func TestParseVersion(t *testing.T) {
	got, ok := ParseOllamaVersion("0.14.1-rc1")
	if !ok || got != (OllamaVersion{Major: 0, Minor: 14, Patch: 1}) {
		t.Fatalf("ParseOllamaVersion() = %+v, %v", got, ok)
	}
}
