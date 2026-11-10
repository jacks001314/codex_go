package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func workloadAPITestResponse(accessToken string, organizationID string, projectID string, userID string) string {
	// Rust #60433: an API exchange token must carry parseable OAuth source
	// claims, so test markers become inert JWTs.
	if !strings.HasPrefix(accessToken, "e30.") {
		accessToken = apiWorkloadAccessToken(accessToken, nil)
	}
	return fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","expires_in":600,"wif_product":"api","api_organization_id":%q,"api_project_id":%q,"user_id":%q}`, accessToken, organizationID, projectID, userID)
}

// Rust #59275 accepts an API product exchange with organization, project and
// service account metadata and rejects malformed or unknown products.
func TestWorkloadIdentityExchangeParsesAPIProductLikeRust(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		ok   bool
	}{
		{name: "api", body: workloadAPITestResponse("sk-api-one", "org-one", "project-one", "service-account-one"), ok: true},
		{name: "missing organization", body: workloadAPITestResponse("sk-api-one", "", "project-one", "service-account-one")},
		{name: "missing project", body: workloadAPITestResponse("sk-api-one", "org-one", "", "service-account-one")},
		{name: "missing service account", body: workloadAPITestResponse("sk-api-one", "org-one", "project-one", "")},
		{name: "unknown product", body: `{"access_token":"sk-api-one","token_type":"Bearer","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","expires_in":600,"wif_product":"beta","user_id":"user-one"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			exchange, err := NewWorkloadIdentityExchange(WorkloadIdentityConfig{
				AssertionFile:    workloadTestSessionConfig(t, server.URL).assertionFile,
				FederationRuleID: "rule-one",
			}, server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			token, err := exchange.Resolve(context.Background())
			if !test.ok {
				if err == nil || err.Error() != workloadErrInvalidExchangeResponse.Error() {
					t.Fatalf("err = %v, want invalid exchange response", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if token.Product != WorkloadIdentityProductAPI || token.API == nil {
				t.Fatalf("token = %+v, want API product metadata", token)
			}
			if token.API.APIOrganizationID != "org-one" || token.API.APIProjectID != "project-one" || token.API.UserID != "service-account-one" {
				t.Fatalf("API metadata = %+v", token.API)
			}
		})
	}
}

// Rust #59275 resolves an API product exchange as API key authentication.
func TestWorkloadIdentityResolvesAPIProductAsAPIKeyLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, workloadAPITestResponse("sk-api-one", "org-one", "project-one", "service-account-one"))
	}))
	defer server.Close()
	adapter, err := workloadTestAuth(t, &workloadIdentitySessionRegistry{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := adapter.ResolveAuth(context.Background())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Mode() != "api-key" || resolved.OpenAIAPIKey != apiWorkloadAccessToken("sk-api-one", nil) {
		t.Fatalf("resolved = %+v", resolved)
	}
	if resolved.WorkloadIdentity == nil || resolved.WorkloadIdentity.APIOrganizationID != "org-one" || resolved.WorkloadIdentity.APIProjectID != "project-one" || resolved.WorkloadIdentity.UserID != "service-account-one" {
		t.Fatalf("workload identity metadata = %+v", resolved.WorkloadIdentity)
	}
	if strings.Contains(fmt.Sprintf("%+v", resolved), "sk-api-one") {
		t.Fatal("API key leaked through debug formatting")
	}
}

// Rust #59275 applies login restrictions to the product the exchange resolved.
func TestWorkloadIdentityAppliesLoginPolicyToResolvedProductLikeRust(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, workloadAPITestResponse("sk-api-one", "org-one", "project-one", "service-account-one"))
	}))
	defer apiServer.Close()
	chatgptServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, workloadTestSuccessResponse(t, workloadTestJWT(t, "user-one"), "account-user-one", "user-one"))
	}))
	defer chatgptServer.Close()

	apiAdapter, err := workloadTestAuth(t, &workloadIdentitySessionRegistry{}, apiServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	apiAdapter.chatgptLoginAllowed = false
	apiAdapter.apiLoginAllowed = true
	if _, err := apiAdapter.ResolveAuth(context.Background()); err != nil {
		t.Fatalf("API product must not require the ChatGPT login policy: %v", err)
	}

	blockedAPI, err := workloadTestAuth(t, &workloadIdentitySessionRegistry{}, apiServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	blockedAPI.apiLoginAllowed = false
	if _, err := blockedAPI.ResolveAuth(context.Background()); err == nil || !strings.Contains(err.Error(), "API key authentication") {
		t.Fatalf("API product policy error = %v", err)
	}

	chatgptAdapter, err := workloadTestAuth(t, &workloadIdentitySessionRegistry{}, chatgptServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	chatgptAdapter.chatgptLoginAllowed = false
	chatgptAdapter.apiLoginAllowed = true
	if _, err := chatgptAdapter.ResolveAuth(context.Background()); err == nil || !strings.Contains(err.Error(), "ChatGPT authentication") {
		t.Fatalf("ChatGPT product policy error = %v", err)
	}
}

// Rust #59275 applies forced ChatGPT workspaces to the resolved ChatGPT
// product; API products carry no ChatGPT workspace.
func TestWorkloadIdentityForcedWorkspaceAppliesToChatGPTProductLikeRust(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, workloadAPITestResponse("sk-api-one", "org-one", "project-one", "service-account-one"))
	}))
	defer apiServer.Close()
	chatgptServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, workloadTestSuccessResponse(t, workloadTestJWT(t, "user-one"), "account-user-one", "user-one"))
	}))
	defer chatgptServer.Close()

	blocked, err := workloadTestAuth(t, &workloadIdentitySessionRegistry{}, chatgptServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	blocked.forcedChatGPTWorkspaces = []string{"another-account"}
	if _, err := blocked.ResolveAuth(context.Background()); err == nil || !strings.Contains(err.Error(), "restricted to workspace") {
		t.Fatalf("workspace restriction error = %v", err)
	}

	allowed, err := workloadTestAuth(t, &workloadIdentitySessionRegistry{}, chatgptServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	allowed.forcedChatGPTWorkspaces = []string{"account-one"}
	if _, err := allowed.ResolveAuth(context.Background()); err != nil {
		t.Fatalf("matching workspace must resolve: %v", err)
	}

	api, err := workloadTestAuth(t, &workloadIdentitySessionRegistry{}, apiServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	api.forcedChatGPTWorkspaces = []string{"another-account"}
	if _, err := api.ResolveAuth(context.Background()); err != nil {
		t.Fatalf("API product must ignore ChatGPT workspace restrictions: %v", err)
	}
}

// Rust #59275 keeps ownership for the same API identity across renewal while
// rejecting identity changes.
func TestWorkloadIdentityAPIIdentityOwnershipLikeRust(t *testing.T) {
	var exchanges atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch exchanges.Add(1) {
		case 1:
			fmt.Fprint(w, workloadAPITestResponse("sk-api-one", "org-one", "project-one", "service-account-one"))
		case 2:
			fmt.Fprint(w, workloadAPITestResponse("sk-api-two", "org-one", "project-one", "service-account-one"))
		default:
			fmt.Fprint(w, workloadAPITestResponse("sk-api-three", "org-one", "project-two", "service-account-one"))
		}
	}))
	defer server.Close()
	adapter, err := workloadTestAuth(t, &workloadIdentitySessionRegistry{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := adapter.ResolveAuth(context.Background())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	renewed, err := adapter.RefreshAuth(context.Background(), "")
	if err != nil {
		t.Fatalf("renewal for the same identity must succeed: %v", err)
	}
	if !SameAuthOwner(initial, renewed) {
		t.Fatalf("same API identity lost ownership: %+v vs %+v", initial.WorkloadIdentity, renewed.WorkloadIdentity)
	}
	if _, err := adapter.RefreshAuth(context.Background(), ""); err == nil {
		t.Fatal("API identity change must be rejected")
	} else if !workloadIdentityErrorIsPermanent(err) {
		t.Fatalf("identity-change error must be permanent: %v", err)
	}
}
