package appserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/plugin"
)

func measurementTestBundleArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gzipWriter)
	entries := map[string]string{
		".codex-plugin/plugin.json": `{"name":"demo","version":"1.0.0"}`,
		"scripts/run.sh":            "#!/bin/sh\n",
	}
	for name, contents := range entries {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestAuthenticatedMeasurementBundleFetcherLikeRust(t *testing.T) {
	t.Setenv("CODEX_TEST_ALLOW_HTTP_REMOTE_PLUGIN_BUNDLE_DOWNLOADS", "1")
	bundle := measurementTestBundleArchive(t)
	var sawAuth, sawAccount, sawProductSKU bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ps/plugins/installed":
			sawAuth = request.Header.Get("Authorization") == "Bearer token"
			sawAccount = request.Header.Get("ChatGPT-Account-ID") == "account"
			sawProductSKU = request.Header.Get("OAI-Product-Sku") == "tpp"
			if request.URL.Query().Get("scope") != "GLOBAL" || request.URL.Query().Get("includeDownloadUrls") != "true" {
				t.Errorf("unexpected query = %q", request.URL.RawQuery)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"plugins": []map[string]any{{
					"id":      "remote-demo",
					"name":    "demo",
					"scope":   "GLOBAL",
					"enabled": true,
					"status":  "AVAILABLE",
					"release": map[string]any{
						"version":             "1.0.0",
						"bundle_download_url": serverURL(request) + "/bundle.zip",
					},
				}},
				"pagination": map[string]any{},
			})
		case "/bundle.zip":
			writer.Header().Set("Content-Type", "application/zip")
			_, _ = writer.Write(bundle)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	fetcher := &authenticatedMeasurementBundleFetcher{
		client:     server.Client(),
		baseURL:    server.URL,
		token:      "token",
		accountID:  "account",
		productSKU: "tpp",
	}
	resolved, err := fetcher.FetchMeasurementBundle(context.Background(), plugin.PluginMeasurementTarget{PluginName: "demo", Version: "1.0.0"})
	if err != nil || resolved == nil {
		t.Fatalf("FetchMeasurementBundle() = %#v, %v", resolved, err)
	}
	if !sawAuth || !sawAccount || !sawProductSKU {
		t.Fatalf("auth headers: authorization=%v account=%v", sawAuth, sawAccount)
	}
	if resolved.RemotePluginID != "remote-demo" || resolved.Version != "1.0.0" {
		t.Fatalf("bundle = %#v", resolved)
	}
	home := t.TempDir()
	roots, err := resolved.Materialize(context.Background(), home)
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if roots.RootCount() != 1 {
		t.Fatalf("roots = %#v", roots)
	}
	script := filepath.Join(home, "plugins", "cache", remoteInstalledGlobalMarketplace, "demo", "1.0.0", "scripts", "run.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("materialized script: %v", err)
	}
}

func TestAuthenticatedMeasurementBundleFetcherRejectsAmbiguousAndIneligibleLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		plugins []map[string]any
	}{
		{
			name: "ambiguous",
			plugins: []map[string]any{
				measurementPlugin("remote-1", "demo", "GLOBAL", true, "AVAILABLE"),
				measurementPlugin("remote-2", "demo", "GLOBAL", true, "AVAILABLE"),
			},
		},
		{
			name:    "user scope",
			plugins: []map[string]any{measurementPlugin("remote-1", "demo", "USER", true, "AVAILABLE")},
		},
		{
			name:    "disabled",
			plugins: []map[string]any{measurementPlugin("remote-1", "demo", "GLOBAL", false, "AVAILABLE")},
		},
		{
			name:    "unavailable",
			plugins: []map[string]any{measurementPlugin("remote-1", "demo", "GLOBAL", true, "DISABLED_BY_ADMIN")},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_ = json.NewEncoder(writer).Encode(map[string]any{
					"plugins":    testCase.plugins,
					"pagination": map[string]any{},
				})
			}))
			defer server.Close()
			fetcher := &authenticatedMeasurementBundleFetcher{client: server.Client(), baseURL: server.URL, token: "token"}
			resolved, err := fetcher.FetchMeasurementBundle(context.Background(), plugin.PluginMeasurementTarget{PluginName: "demo", Version: "1.0.0"})
			if err != nil || resolved != nil {
				t.Fatalf("FetchMeasurementBundle() = %#v, %v", resolved, err)
			}
		})
	}
}

func measurementPlugin(id string, name string, scope string, enabled bool, availability string) map[string]any {
	return map[string]any{
		"id":      id,
		"name":    name,
		"scope":   scope,
		"enabled": enabled,
		"status":  availability,
		"release": map[string]any{
			"version":             "1.0.0",
			"bundle_download_url": "https://example.com/bundle.zip",
		},
	}
}

func serverURL(request *http.Request) string {
	return "http://" + strings.TrimSpace(request.Host)
}
