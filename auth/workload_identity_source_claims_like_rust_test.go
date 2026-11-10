package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// apiWorkloadAccessToken builds the inert JWT fixture Rust #60433 requires:
// parseable OAuth source claims with a non-empty iss and session_id plus the
// platform WIF identity block.
func apiWorkloadAccessToken(marker string, mutate func(claims map[string]any)) string {
	identity := map[string]any{
		"user_id":                           "service-account-one",
		"wif_product":                       "api",
		"wif_identity_provider_id":          "provider-one",
		"wif_federation_rule_id":            "rule-one",
		"wif_runtime_authorization_version": 1,
		"wif_provider_revision":             "provider-revision-one",
		"wif_mapping_revision":              "mapping-revision-one",
	}
	claims := map[string]any{
		"iss":                         "https://auth.api.openai.org",
		"sub":                         "service-account-one",
		"session_id":                  "family-one",
		"jti":                         marker,
		"https://api.openai.com/auth": identity,
	}
	if mutate != nil {
		mutate(claims)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func apiWorkloadAuthSnapshot(token string) *AuthDotJSON {
	return &AuthDotJSON{
		OpenAIAPIKey: token,
		WorkloadIdentity: &WorkloadIdentityAPIMetadata{
			APIOrganizationID: "org-one",
			APIProjectID:      "project-one",
			UserID:            "service-account-one",
		},
	}
}

// TestWorkloadIdentityAPISourceClaimsBindOwnershipLikeRust mirrors Rust #60433:
// an ordinary token refresh keeps the source, while a new session family or a
// provider or mapping revision change retires it.
func TestWorkloadIdentityAPISourceClaimsBindOwnershipLikeRust(t *testing.T) {
	base := apiWorkloadAccessToken("token-a", nil)
	for _, testCase := range []struct {
		name string
		next string
		same bool
	}{
		{
			name: "plain refresh",
			next: apiWorkloadAccessToken("token-b", func(claims map[string]any) {
				claims["iat"] = 200
				claims["exp"] = 300
			}),
			same: true,
		},
		{
			name: "new session family",
			next: apiWorkloadAccessToken("token-b", func(claims map[string]any) {
				claims["session_id"] = "family-b"
			}),
		},
		{
			name: "new provider revision",
			next: apiWorkloadAccessToken("token-b", func(claims map[string]any) {
				claims["https://api.openai.com/auth"].(map[string]any)["wif_provider_revision"] = "provider-revision-b"
			}),
		},
		{
			name: "new mapping revision",
			next: apiWorkloadAccessToken("token-b", func(claims map[string]any) {
				claims["https://api.openai.com/auth"].(map[string]any)["wif_mapping_revision"] = "mapping-revision-b"
			}),
		},
		{
			name: "missing session family",
			next: apiWorkloadAccessToken("token-b", func(claims map[string]any) {
				delete(claims, "session_id")
			}),
		},
		{
			name: "empty session family",
			next: apiWorkloadAccessToken("token-b", func(claims map[string]any) {
				claims["session_id"] = ""
			}),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			previous := apiWorkloadAuthSnapshot(base)
			current := apiWorkloadAuthSnapshot(testCase.next)
			if got := SameAuthOwner(previous, current); got != testCase.same {
				t.Fatalf("SameAuthOwner() = %v, want %v", got, testCase.same)
			}
			if !SameAuthOwner(previous, apiWorkloadAuthSnapshot(base)) {
				t.Fatal("unchanged credentials must keep ownership")
			}
		})
	}
}

// TestWorkloadIdentityAPIExchangeRequiresSessionClaimLikeRust mirrors Rust
// #60433: an API exchange token without iss or session_id is rejected, the
// rejected credential is evicted, and the next resolve exchanges again.
func TestWorkloadIdentityAPIExchangeRequiresSessionClaimLikeRust(t *testing.T) {
	for _, mutation := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing session family", mutate: func(claims map[string]any) { delete(claims, "session_id") }},
		{name: "empty session family", mutate: func(claims map[string]any) { claims["session_id"] = "" }},
		{name: "whitespace session family", mutate: func(claims map[string]any) { claims["session_id"] = "   " }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			var exchanges atomic.Int64
			supported := apiWorkloadAccessToken("supported", nil)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				token := supported
				if exchanges.Add(1) == 1 {
					token = apiWorkloadAccessToken("unsupported", mutation.mutate)
				}
				fmt.Fprint(w, workloadAPITestResponse(token, "org-one", "project-one", "service-account-one"))
			}))
			defer server.Close()
			adapter, err := workloadTestAuth(t, &workloadIdentitySessionRegistry{}, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.ResolveAuth(context.Background()); err == nil {
				t.Fatal("exchange token without a runtime family must be rejected")
			} else if !workloadIdentityErrorIsPermanent(err) {
				t.Fatalf("rejection must be permanent: %v", err)
			}
			resolved, err := adapter.ResolveAuth(context.Background())
			if err != nil {
				t.Fatalf("evicted credential must be exchanged again: %v", err)
			}
			if resolved.OpenAIAPIKey != supported {
				t.Fatalf("resolved token = %q, want the supported exchange", resolved.OpenAIAPIKey)
			}
			if exchanges.Load() != 2 {
				t.Fatalf("exchange count = %d, want 2", exchanges.Load())
			}
			if strings.TrimSpace(resolved.OpenAIAPIKey) == "" {
				t.Fatal("resolved token is empty")
			}
		})
	}
}
