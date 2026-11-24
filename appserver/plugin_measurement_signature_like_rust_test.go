package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/plugin"
)

// Mirrors Rust #63489: a measurement reference stages the same signed archive
// the install path accepts, so a tampered bundle never becomes a trusted root.
func TestMeasurementBundleFetchVerifiesArchiveSignatureLikeRust(t *testing.T) {
	t.Setenv("CODEX_TEST_ALLOW_HTTP_REMOTE_PLUGIN_BUNDLE_DOWNLOADS", "1")
	bundle := measurementTestBundleArchive(t)
	signer := newPluginSignatureTestSigner(t)

	newServer := func(signing json.RawMessage) *httptest.Server {
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/ps/plugins/installed":
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
							"signing":             signing,
						},
					}},
					"pagination": map[string]any{},
				})
			case "/bundle.zip":
				_, _ = writer.Write(bundle)
			case "/ps/plugins/signing-keys":
				_ = json.NewEncoder(writer).Encode(map[string]any{
					"keys": []map[string]any{{
						"key_id":                     signer.keyID,
						"purpose":                    "PLUGIN_RELEASE_ARCHIVE_SIGNING",
						"algorithm":                  "RSA_PKCS1_SHA256",
						"public_key_spki_der_base64": signer.public,
					}},
					"cache_ttl_seconds": 600,
				})
			default:
				http.NotFound(writer, request)
			}
		}))
		t.Cleanup(server.Close)
		return server
	}

	verify := func(signing json.RawMessage) error {
		server := newServer(signing)
		fetcher := &authenticatedMeasurementBundleFetcher{
			client: server.Client(), baseURL: server.URL, token: "token", accountID: "account", productSKU: "tpp",
		}
		reference, err := fetcher.FetchMeasurementBundle(context.Background(), plugin.PluginMeasurementTarget{PluginName: "demo"})
		if err != nil || reference == nil {
			t.Fatalf("FetchMeasurementBundle() = (%#v, %v)", reference, err)
		}
		_, err = reference.Materialize(context.Background(), t.TempDir())
		return err
	}

	if err := verify(signer.signingJSON(t, bundle)); err != nil {
		t.Fatalf("signed measurement bundle error = %v", err)
	}
	tampered := append([]byte(nil), bundle...)
	tampered[0] ^= 1
	if err := verify(signer.signingJSON(t, tampered)); err == nil || !strings.Contains(err.Error(), "digest_mismatch") {
		t.Fatalf("tampered measurement bundle error = %v, want digest_mismatch", err)
	}
	if err := verify(nil); err != nil {
		t.Fatalf("unsigned measurement bundle error = %v", err)
	}
}
