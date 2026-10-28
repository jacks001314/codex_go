package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stubResponsesProxySubjectSource struct {
	mu       sync.Mutex
	subjects []string
	calls    int
}

func (s *stubResponsesProxySubjectSource) SubjectToken(context.Context, *responsesProxyTokenClient) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.calls
	s.calls++
	if index >= len(s.subjects) {
		index = len(s.subjects) - 1
	}
	return s.subjects[index], nil
}

func (s *stubResponsesProxySubjectSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func tokenExchangeResponse(accessToken string, expiresIn, expiresAt int64) map[string]any {
	return map[string]any{
		"access_token":      accessToken,
		"token_type":        "Bearer",
		"issued_token_type": responsesProxyAccessTokenType,
		"expires_in":        expiresIn,
		"expires_at":        expiresAt,
	}
}

type tokenExchangeTestServer struct {
	server   *httptest.Server
	calls    atomic.Int64
	mu       sync.Mutex
	lastBody string
}

func (s *tokenExchangeTestServer) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastBody
}

func newTokenExchangeTestServer(t *testing.T, responses ...map[string]any) *tokenExchangeTestServer {
	t.Helper()
	fixture := &tokenExchangeTestServer{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(fixture.calls.Add(1)) - 1
		body, _ := io.ReadAll(r.Body)
		fixture.mu.Lock()
		fixture.lastBody = string(body)
		fixture.mu.Unlock()
		if index >= len(responses) {
			index = len(responses) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(responses[index])
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func TestResponsesProxyFederatedAuthCachesAndRenewsLikeRust(t *testing.T) {
	now := time.Now().Unix()
	exchangeServer := newTokenExchangeTestServer(t,
		tokenExchangeResponse("at-1", 3600, now+3600),
		tokenExchangeResponse("at-2", 3600, now+3600),
	)
	source := &stubResponsesProxySubjectSource{subjects: []string{"subject-1", "subject-2"}}
	auth, err := newResponsesProxyFederatedAuthWith(source, "idp-1", "sa-1", exchangeServer.server.URL, newResponsesProxyTokenClient())
	if err != nil {
		t.Fatalf("newResponsesProxyFederatedAuthWith returned error: %v", err)
	}
	for turn := 0; turn < 2; turn++ {
		header, err := auth.Header(context.Background())
		if err != nil {
			t.Fatalf("Header returned error: %v", err)
		}
		if header != "Bearer at-1" {
			t.Fatalf("header = %q", header)
		}
	}
	if exchangeServer.calls.Load() != 1 || source.callCount() != 1 {
		t.Fatalf("cached header reused %d exchanges / %d subjects", exchangeServer.calls.Load(), source.callCount())
	}
	var exchange map[string]any
	if err := json.Unmarshal([]byte(exchangeServer.body()), &exchange); err != nil {
		t.Fatalf("exchange body = %q: %v", exchangeServer.body(), err)
	}
	if exchange["grant_type"] != responsesProxyExchangeGrantType ||
		exchange["subject_token_type"] != responsesProxyJWTTokenType ||
		exchange["subject_token"] != "subject-1" ||
		exchange["identity_provider_id"] != "idp-1" ||
		exchange["service_account_id"] != "sa-1" {
		t.Fatalf("exchange body = %#v", exchange)
	}

	// A renewal uses a fresh subject token and replaces the cached credential.
	auth.cached.renewAt = time.Now().Add(-time.Second)
	header, err := auth.Header(context.Background())
	if err != nil {
		t.Fatalf("renewal Header returned error: %v", err)
	}
	if header != "Bearer at-2" || exchangeServer.calls.Load() != 2 || source.callCount() != 2 {
		t.Fatalf("renewal header = %q, exchanges %d, subjects %d", header, exchangeServer.calls.Load(), source.callCount())
	}
	if err := json.Unmarshal([]byte(exchangeServer.body()), &exchange); err != nil || exchange["subject_token"] != "subject-2" {
		t.Fatalf("renewal exchange body = %#v (%v)", exchange, err)
	}
}

func TestResponsesProxyFederatedAuthRejectsInvalidResponsesLikeRust(t *testing.T) {
	now := time.Now().Unix()
	invalid := map[string]map[string]any{
		"unsupported token type": func() map[string]any {
			body := tokenExchangeResponse("at-1", 3600, now+3600)
			body["token_type"] = "mac"
			return body
		}(),
		"unsupported issued token type": func() map[string]any {
			body := tokenExchangeResponse("at-1", 3600, now+3600)
			body["issued_token_type"] = "urn:ietf:params:oauth:token-type:jwt"
			return body
		}(),
		"lifetime too long": tokenExchangeResponse("at-1", 3601, now+3601),
		"expired":           tokenExchangeResponse("at-1", 0, now-1),
		"invalid bearer":    tokenExchangeResponse("at 1", 3600, now+3600),
	}
	for name, body := range invalid {
		t.Run(name, func(t *testing.T) {
			exchangeServer := newTokenExchangeTestServer(t, body)
			source := &stubResponsesProxySubjectSource{subjects: []string{"subject-1"}}
			auth, err := newResponsesProxyFederatedAuthWith(source, "idp", "sa", exchangeServer.server.URL, newResponsesProxyTokenClient())
			if err != nil {
				t.Fatalf("constructor error: %v", err)
			}
			_, err = auth.Header(context.Background())
			if err == nil {
				t.Fatal("invalid exchange must fail")
			}
			if strings.Contains(err.Error(), "at-1") || strings.Contains(err.Error(), exchangeServer.server.URL) {
				t.Fatalf("error leaks credentials: %v", err)
			}
		})
	}
}

func TestResponsesProxyFederatedAuthDropsCredentialOnFailedRenewalLikeRust(t *testing.T) {
	now := time.Now().Unix()
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(tokenExchangeResponse("at-1", 3600, now+3600))
	}))
	defer server.Close()
	source := &stubResponsesProxySubjectSource{subjects: []string{"subject-1"}}
	auth, err := newResponsesProxyFederatedAuthWith(source, "idp", "sa", server.URL, newResponsesProxyTokenClient())
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}
	if _, err := auth.Header(context.Background()); err != nil {
		t.Fatalf("initial Header returned error: %v", err)
	}
	fail.Store(true)
	auth.cached.renewAt = time.Now().Add(-time.Second)
	if _, err := auth.Header(context.Background()); err == nil {
		t.Fatal("failed renewal must surface an error")
	}
	if auth.cached != nil {
		t.Fatal("failed renewal must discard the cached credential")
	}
	fail.Store(false)
	if header, err := auth.Header(context.Background()); err != nil || header != "Bearer at-1" {
		t.Fatalf("recovered header = %q, %v", header, err)
	}
}

func TestResponsesProxyTokenClientDoesNotFollowRedirectsLikeRust(t *testing.T) {
	var redirected atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"value": "jwt"})
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := newResponsesProxyTokenClient()
	response, err := client.client.Get(redirector.URL)
	if err != nil {
		t.Fatalf("token client GET returned error: %v", err)
	}
	_, err = readResponsesProxyTokenResponse(response, "GitHub OIDC")
	if err == nil || !strings.Contains(err.Error(), "rejected (HTTP 302)") {
		t.Fatalf("redirect error = %v", err)
	}
	if redirected.Load() != 0 {
		t.Fatal("token client must not follow redirects")
	}
}

func TestResponsesProxyGithubOIDCLikeRust(t *testing.T) {
	var gotAuth string
	var gotQuery url.Values
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(map[string]string{"value": "jwt.subject.value"})
	}))
	defer server.Close()

	source, err := newResponsesProxyGithubOIDC(server.URL+"/oidc?audience=old&x=1", "new-audience", strings.NewReader("request-token\n"))
	if err != nil {
		t.Fatalf("newResponsesProxyGithubOIDC returned error: %v", err)
	}
	client := newResponsesProxyTokenClient()
	client.client = server.Client()
	token, err := source.SubjectToken(context.Background(), client)
	if err != nil {
		t.Fatalf("SubjectToken returned error: %v", err)
	}
	if token != "jwt.subject.value" {
		t.Fatalf("token = %q", token)
	}
	if gotAuth != "Bearer request-token" {
		t.Fatalf("request auth = %q", gotAuth)
	}
	if gotQuery.Get("audience") != "new-audience" || gotQuery.Get("x") != "1" {
		t.Fatalf("query = %#v", gotQuery)
	}
}

func TestResponsesProxyGithubOIDCValidatesRequestURLLikeRust(t *testing.T) {
	for name, requestURL := range map[string]string{
		"http":     "http://example.test/oidc",
		"userinfo": "https://user:pass@example.test/oidc",
		"fragment": "https://example.test/oidc#frag",
		"relative": "/oidc",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newResponsesProxyGithubOIDC(requestURL, "aud", strings.NewReader("token\n"))
			if err == nil || !strings.Contains(err.Error(), "must be HTTPS without user info or fragment") {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "example.test") {
				t.Fatalf("error echoes the URL: %v", err)
			}
		})
	}
	if _, err := newResponsesProxyGithubOIDC("https://example.test/oidc", "aud", strings.NewReader("")); err == nil ||
		!strings.Contains(err.Error(), "invalid workload identity bearer credential") {
		t.Fatalf("empty credential error = %v", err)
	}
	oversized := strings.Repeat("a", responsesProxyMaxTokenBytes+3)
	if _, err := newResponsesProxyGithubOIDC("https://example.test/oidc", "aud", strings.NewReader(oversized)); err == nil ||
		!strings.Contains(err.Error(), "request credential too large") {
		t.Fatalf("oversized credential error = %v", err)
	}
}

func TestResponsesProxyUpstreamClientRedirectPolicyLikeRust(t *testing.T) {
	if client := responsesProxyUpstreamClient(responsesProxyAPIKeyAuth{header: "Bearer key"}); client.CheckRedirect != nil {
		t.Fatal("API key auth keeps the default redirect policy")
	}
	source := &stubResponsesProxySubjectSource{subjects: []string{"subject"}}
	federated, err := newResponsesProxyFederatedAuth(source, "idp", "sa")
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}
	if client := responsesProxyUpstreamClient(federated); client.CheckRedirect == nil {
		t.Fatal("federated auth must disable redirects")
	}
}

type staticResponsesProxyAuth struct{ header string }

func (a staticResponsesProxyAuth) Header(context.Context) (string, error) { return a.header, nil }
func (staticResponsesProxyAuth) FollowsRedirects() bool                   { return true }

func TestResponsesProxyForwardsDynamicAuthLikeRust(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL + "/v1/responses")
	proxy := &responsesAPIProxyServer{
		auth:        staticResponsesProxyAuth{header: "Bearer dynamic"},
		upstreamURL: upstreamURL,
		httpClient:  &http.Client{},
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hi"}`))
	recorder := httptest.NewRecorder()
	proxy.handle(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("proxy status = %d body=%q", recorder.Code, recorder.Body.String())
	}
	if gotAuth != "Bearer dynamic" {
		t.Fatalf("upstream authorization = %q", gotAuth)
	}
}

type chunkedReader struct {
	chunks []string
	index  int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.index]
	r.index++
	n := copy(p, chunk)
	if n < len(chunk) {
		r.chunks[r.index-1] = chunk[n:]
		r.index--
	}
	return n, nil
}

func TestReadResponsesAPIProxyAuthHeaderShortReadsLikeRust(t *testing.T) {
	reader := &chunkedReader{chunks: []string{"sk-", "abc", "123\r\nignored"}}
	header, err := readResponsesAPIProxyAuthHeader(reader)
	if err != nil {
		t.Fatalf("readResponsesAPIProxyAuthHeader returned error: %v", err)
	}
	if header != "Bearer sk-abc123" {
		t.Fatalf("header = %q", header)
	}

	for _, token := range []string{"sk.abc", "sk+abc", "sk=abc", "sk abc"} {
		if _, err := readResponsesAPIProxyAuthHeader(strings.NewReader(token)); err == nil {
			t.Fatalf("API key %q must be rejected", token)
		}
	}
	for _, token := range []string{"jwt.token.value", "a+b/c=="} {
		if _, err := validateResponsesProxyBearerToken([]byte(token)); err != nil {
			t.Fatalf("bearer token %q rejected: %v", token, err)
		}
	}
}
