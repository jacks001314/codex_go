package app

import (
	"context"
	"io"
	"strings"
	"testing"

	"codex_go/cli"
)

// These tests mirror Rust responses-api-proxy Args::auth and run_main (#55320):
// --github-oidc validates the upstream before reading stdin, requires the four
// workload identity settings without accepting blanks, and completes the
// initial token exchange before the proxy publishes server info.

type proxyOIDCRecordingAuth struct {
	headers int
}

func (a *proxyOIDCRecordingAuth) Header(context.Context) (string, error) {
	a.headers++
	return "Bearer exchanged", nil
}

func (*proxyOIDCRecordingAuth) FollowsRedirects() bool { return false }

type proxyUnreadStdin struct {
	reads int
}

func (r *proxyUnreadStdin) Read([]byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

func githubOIDCProxyOptions() *cli.ResponsesAPIProxyOptions {
	return &cli.ResponsesAPIProxyOptions{
		GithubOIDC:           true,
		OIDCAudience:         "aud-1",
		IdentityProviderID:   "idp-1",
		ServiceAccountID:     "sa-1",
		GithubOIDCRequestURL: "https://example.test/oidc",
		UpstreamURL:          responsesProxyOpenAIResponsesURL,
	}
}

func stubProxyFederatedAuth(t *testing.T, auth responsesProxyAuth) (*string, *string) {
	t.Helper()
	provider := new(string)
	serviceAccount := new(string)
	original := responsesProxyNewFederatedAuth
	responsesProxyNewFederatedAuth = func(source responsesProxySubjectTokenSource, providerValue, serviceAccountValue string) (responsesProxyAuth, error) {
		if source == nil {
			t.Fatalf("subject token source = nil")
		}
		*provider = providerValue
		*serviceAccount = serviceAccountValue
		return auth, nil
	}
	t.Cleanup(func() { responsesProxyNewFederatedAuth = original })
	return provider, serviceAccount
}

func TestResponsesAPIProxyGithubOIDCCompletesInitialExchangeLikeRust(t *testing.T) {
	auth := &proxyOIDCRecordingAuth{}
	provider, serviceAccount := stubProxyFederatedAuth(t, auth)
	stdin := strings.NewReader("github-request-token\n")

	got, err := newResponsesAPIProxyAuth(context.Background(), githubOIDCProxyOptions(), responsesProxyOpenAIResponsesURL, stdin)
	if err != nil {
		t.Fatalf("newResponsesAPIProxyAuth error = %v", err)
	}
	if got != auth {
		t.Fatalf("auth = %#v, want the federated auth", got)
	}
	if *provider != "idp-1" || *serviceAccount != "sa-1" {
		t.Fatalf("federated auth provider=%q serviceAccount=%q", *provider, *serviceAccount)
	}
	if auth.headers != 1 {
		t.Fatalf("initial exchange header calls = %d, want 1", auth.headers)
	}
}

func TestResponsesAPIProxyGithubOIDCRequiresDefaultUpstreamLikeRust(t *testing.T) {
	original := responsesProxyNewFederatedAuth
	called := false
	responsesProxyNewFederatedAuth = func(responsesProxySubjectTokenSource, string, string) (responsesProxyAuth, error) {
		called = true
		return &proxyOIDCRecordingAuth{}, nil
	}
	t.Cleanup(func() { responsesProxyNewFederatedAuth = original })

	opts := githubOIDCProxyOptions()
	opts.UpstreamURL = "http://127.0.0.1:1234/responses"
	stdin := &proxyUnreadStdin{}
	_, err := newResponsesAPIProxyAuth(context.Background(), opts, opts.UpstreamURL, stdin)
	if err == nil || !strings.Contains(err.Error(), "workload identity requires the default OpenAI Responses endpoint") {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatalf("federated auth was built for a non-default upstream")
	}
	if stdin.reads != 0 {
		t.Fatalf("stdin reads = %d, want 0 before the upstream check", stdin.reads)
	}
}

func TestResponsesAPIProxyGithubOIDCRejectsBlankSettingsLikeRust(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*cli.ResponsesAPIProxyOptions)
		want string
	}{
		{"identity provider", func(o *cli.ResponsesAPIProxyOptions) { o.IdentityProviderID = "  " }, "--identity-provider-id"},
		{"service account", func(o *cli.ResponsesAPIProxyOptions) { o.ServiceAccountID = "" }, "--service-account-id"},
		{"audience", func(o *cli.ResponsesAPIProxyOptions) { o.OIDCAudience = "" }, "--oidc-audience"},
		{"request url", func(o *cli.ResponsesAPIProxyOptions) { o.GithubOIDCRequestURL = "" }, "--github-oidc-request-url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := responsesProxyNewFederatedAuth
			responsesProxyNewFederatedAuth = func(responsesProxySubjectTokenSource, string, string) (responsesProxyAuth, error) {
				t.Fatalf("federated auth was built from blank settings")
				return nil, nil
			}
			t.Cleanup(func() { responsesProxyNewFederatedAuth = original })

			opts := githubOIDCProxyOptions()
			test.mut(opts)
			stdin := &proxyUnreadStdin{}
			_, err := newResponsesAPIProxyAuth(context.Background(), opts, opts.UpstreamURL, stdin)
			if err == nil || err.Error() != "workload identity requires a nonempty "+test.want {
				t.Fatalf("error = %v, want the %s diagnostic", err, test.want)
			}
			if stdin.reads != 0 {
				t.Fatalf("stdin reads = %d, want 0 before the settings check", stdin.reads)
			}
		})
	}
}

func TestResponsesAPIProxyGithubOIDCRejectsUnsafeRequestURLLikeRust(t *testing.T) {
	for _, requestURL := range []string{"http://example.test/oidc", "https://user:pass@example.test/oidc", "https://example.test/oidc#frag", ""} {
		opts := githubOIDCProxyOptions()
		opts.GithubOIDCRequestURL = requestURL
		if _, err := newResponsesAPIProxyAuth(context.Background(), opts, opts.UpstreamURL, strings.NewReader("token")); err == nil {
			t.Fatalf("request URL %q was accepted", requestURL)
		}
	}
}

func TestResponsesAPIProxyAPIKeyModeLeavesExchangeAloneLikeRust(t *testing.T) {
	original := responsesProxyNewFederatedAuth
	responsesProxyNewFederatedAuth = func(responsesProxySubjectTokenSource, string, string) (responsesProxyAuth, error) {
		t.Fatalf("API-key mode built a federated auth")
		return nil, nil
	}
	t.Cleanup(func() { responsesProxyNewFederatedAuth = original })

	// A 64-byte sk- style key satisfies the API-key reader without a token
	// exchange, and no header() call happens before server info is published.
	key := "sk-" + strings.Repeat("a", 61)
	auth, err := newResponsesAPIProxyAuth(context.Background(), &cli.ResponsesAPIProxyOptions{}, responsesProxyOpenAIResponsesURL, strings.NewReader(key))
	if err != nil {
		t.Fatalf("newResponsesAPIProxyAuth error = %v", err)
	}
	if _, ok := auth.(responsesProxyAPIKeyAuth); !ok {
		t.Fatalf("auth = %#v, want the API-key auth", auth)
	}
}
