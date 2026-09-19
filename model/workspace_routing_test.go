package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/chatgptapi"
)

// TestApplyWorkspaceRoutingMatchesRust mirrors Rust apply_workspace_routing:
// only the origin changes, and the routing header follows the override.
func TestApplyWorkspaceRoutingMatchesRust(t *testing.T) {
	cases := []struct {
		name        string
		baseURL     string
		headers     http.Header
		override    auth.AccountRoutingOverride
		wantBaseURL string
		wantHeader  []string
	}{
		{
			name:        "us override keeps the path and sets the header",
			baseURL:     "https://chatgpt.com/backend-api/codex",
			override:    auth.AccountRoutingOverrideUS,
			wantBaseURL: "https://gov.chatgpt.com/backend-api/codex",
			wantHeader:  []string{"us"},
		},
		{
			name:        "us_cr override replaces an existing header",
			baseURL:     "https://chatgpt.com/backend-api/codex",
			headers:     http.Header{AccountRoutingHeader: []string{"us"}},
			override:    auth.AccountRoutingOverrideUSCR,
			wantBaseURL: "https://gov.chatgpt.com/backend-api/codex",
			wantHeader:  []string{"us_cr"},
		},
		{
			name:        "no constraint clears the header",
			baseURL:     "https://chatgpt.com/backend-api/codex",
			headers:     http.Header{AccountRoutingHeader: []string{"us"}},
			override:    auth.AccountRoutingOverrideNoConstraint,
			wantBaseURL: "https://gov.chatgpt.com/backend-api/codex",
			wantHeader:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &APIProvider{BaseURL: tc.baseURL, Headers: tc.headers}
			err := ApplyWorkspaceRouting(provider, &auth.WorkspaceRouting{
				ChatGPTAccountID:       "workspace",
				BackendOrigin:          "https://gov.chatgpt.com",
				AccountRoutingOverride: tc.override,
			})
			if err != nil {
				t.Fatalf("ApplyWorkspaceRouting error = %v", err)
			}
			if provider.BaseURL != tc.wantBaseURL {
				t.Fatalf("BaseURL = %q, want %q", provider.BaseURL, tc.wantBaseURL)
			}
			gotHeader := []string(nil)
			if provider.Headers != nil {
				gotHeader = provider.Headers.Values(AccountRoutingHeader)
			}
			if len(gotHeader) != len(tc.wantHeader) {
				t.Fatalf("header = %#v, want %#v", gotHeader, tc.wantHeader)
			}
			for i := range gotHeader {
				if gotHeader[i] != tc.wantHeader[i] {
					t.Fatalf("header = %#v, want %#v", gotHeader, tc.wantHeader)
				}
			}
		})
	}
}

func TestApplyWorkspaceRoutingRejectsInvalidInputsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name    string
		origin  string
		wantErr string
	}{
		{name: "insecure origin", origin: "http://chatgpt.com", wantErr: "invalid workspace backend origin"},
		{name: "origin with path", origin: "https://chatgpt.com/backend-api", wantErr: "invalid workspace backend origin"},
		{name: "origin with credentials", origin: "https://user@chatgpt.com", wantErr: "invalid workspace backend origin"},
		{name: "origin with query", origin: "https://chatgpt.com?a=1", wantErr: "invalid workspace backend origin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &APIProvider{BaseURL: "https://chatgpt.com/backend-api/codex"}
			err := ApplyWorkspaceRouting(provider, &auth.WorkspaceRouting{
				BackendOrigin:          tc.origin,
				AccountRoutingOverride: auth.AccountRoutingOverrideUS,
			})
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			if provider.BaseURL != "https://chatgpt.com/backend-api/codex" {
				t.Fatalf("BaseURL mutated on error: %q", provider.BaseURL)
			}
		})
	}

	provider := &APIProvider{BaseURL: "https://chatgpt.com/backend-api/codex"}
	err := ApplyWorkspaceRouting(provider, &auth.WorkspaceRouting{
		BackendOrigin:          "https://gov.chatgpt.com",
		AccountRoutingOverride: auth.AccountRoutingOverride("unknown"),
	})
	if err == nil || err.Error() != "invalid workspace routing override" {
		t.Fatalf("override error = %v", err)
	}
}

// TestResponsesAgentRunnerRejectsRedirectsWhenRouted proves the routed provider
// never follows a redirect, so the account-routing credential cannot be
// forwarded to another origin (Rust ClientRedirectPolicy::Reject).
func TestResponsesAgentRunnerRejectsRedirectsWhenRouted(t *testing.T) {
	targetHit := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetHit = true
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:        &APIProvider{BaseURL: redirector.URL},
		RejectRedirects: true,
	})
	httpRequest, err := http.NewRequest(http.MethodPost, redirector.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	response, err := runner.doResponsesHTTPRequest(httpRequest)
	if err != nil {
		t.Fatalf("doResponsesHTTPRequest error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want the unfollowed redirect", response.StatusCode)
	}
	if targetHit {
		t.Fatal("routed provider followed a redirect")
	}
}

// TestDiscoverWorkspaceRoutingMatchesRust covers the accounts/check selection
// and error mapping Rust read_account performs before resolve_routing.
func TestDiscoverWorkspaceRoutingMatchesRust(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr error
	}{
		{
			name: "selects the requested workspace",
			body: `{"accounts":[{"id":"other","workspace_backend_origin":"https://other.example","account_routing_override":"us"},{"id":"selected","workspace_backend_origin":"https://gov.chatgpt.com","account_routing_override":"NO_CONSTRAINT"}]}`,
			want: "https://gov.chatgpt.com",
		},
		{
			name:    "missing workspace",
			body:    `{"accounts":[{"id":"other"}]}`,
			wantErr: ErrWorkspaceRoutingMissingWorkspace,
		},
		{
			name:    "duplicate workspace",
			body:    `{"accounts":[{"id":"selected"},{"id":"selected"}]}`,
			wantErr: ErrWorkspaceRoutingDuplicateWorkspace,
		},
		{
			name:    "unauthorized",
			status:  http.StatusUnauthorized,
			wantErr: ErrWorkspaceRoutingUnauthorized,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := chatgptapi.NewCloudClient(&chatgptapi.CloudClientOptions{BaseURL: server.URL})
			got, err := DiscoverWorkspaceRouting(context.Background(), client, "selected", "", "https://chatgpt.com/backend-api/codex")
			if tc.wantErr != nil {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr.Error()) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DiscoverWorkspaceRouting error = %v", err)
			}
			if got.BackendOrigin != tc.want || got.ChatGPTAccountID != "selected" {
				t.Fatalf("routing = %+v, want origin %q", got, tc.want)
			}
		})
	}
}
