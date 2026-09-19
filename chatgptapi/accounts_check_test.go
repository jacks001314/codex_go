package chatgptapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAccountsCheckResponseDecodesListAndMapLikeRust mirrors Rust's
// RawAccountsCheckResponse: accounts arrive either as a list of entries or as a
// map keyed by account id, ordered by account_ordering.
func TestAccountsCheckResponseDecodesListAndMapLikeRust(t *testing.T) {
	var list AccountsCheckResponse
	if err := json.Unmarshal([]byte(`{"accounts":[{"id":"a","workspace_backend_origin":"https://chatgpt.com","account_routing_override":"us"}]}`), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Accounts) != 1 || list.Accounts[0].ID != "a" ||
		list.Accounts[0].WorkspaceBackendOrigin == nil || *list.Accounts[0].WorkspaceBackendOrigin != "https://chatgpt.com" ||
		list.Accounts[0].AccountRoutingOverride == nil || *list.Accounts[0].AccountRoutingOverride != "us" {
		t.Fatalf("list accounts = %#v", list.Accounts)
	}

	var mapped AccountsCheckResponse
	body := `{"accounts":{"second":{"account":{"account_id":"b"}},"first":{"account":{"account_id":"a"}}},"account_ordering":["first","second"],"default_account_id":"first"}`
	if err := json.Unmarshal([]byte(body), &mapped); err != nil {
		t.Fatalf("decode map: %v", err)
	}
	if len(mapped.Accounts) != 2 || mapped.Accounts[0].ID != "a" || mapped.Accounts[1].ID != "b" {
		t.Fatalf("mapped accounts = %#v", mapped.Accounts)
	}
	if mapped.DefaultAccountID == nil || *mapped.DefaultAccountID != "first" {
		t.Fatalf("default account = %#v", mapped.DefaultAccountID)
	}
	if mapped.Accounts[0].WorkspaceBackendOrigin != nil || mapped.Accounts[0].AccountRoutingOverride != nil {
		t.Fatalf("map form must not invent routing: %#v", mapped.Accounts[0])
	}
}

func TestCloudClientGetAccountsCheckPathAndHeaders(t *testing.T) {
	var path string
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		authHeader = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"accounts":[{"id":"workspace","workspace_backend_origin":"https://chatgpt.com","account_routing_override":"NO_CONSTRAINT"}]}`))
	}))
	defer server.Close()

	client := NewCloudClient(&CloudClientOptions{
		BaseURL: server.URL,
		Headers: http.Header{"Authorization": []string{"Bearer chatgpt-token"}},
	})
	response, err := client.GetAccountsCheck(context.Background())
	if err != nil {
		t.Fatalf("GetAccountsCheck error = %v", err)
	}
	if path != "/api/codex/accounts/check" {
		t.Fatalf("path = %q", path)
	}
	if authHeader != "Bearer chatgpt-token" {
		t.Fatalf("Authorization = %q", authHeader)
	}
	if len(response.Accounts) != 1 || response.Accounts[0].ID != "workspace" {
		t.Fatalf("accounts = %#v", response.Accounts)
	}

	chatgptClient := NewCloudClient(&CloudClientOptions{BaseURL: server.URL + "/backend-api"})
	if _, err := chatgptClient.GetAccountsCheck(context.Background()); err != nil {
		t.Fatalf("GetAccountsCheck (chatgpt path) error = %v", err)
	}
	// Rust builds `{base_url}/wham/accounts/check` for the ChatGPT API style, so
	// a base URL that already carries /backend-api keeps it.
	if path != "/backend-api/wham/accounts/check" {
		t.Fatalf("chatgpt path = %q", path)
	}
}
