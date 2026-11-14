//go:build unix

package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func TestSPIFFEUnixExchangeRefreshAfterAgentRestart(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent.sock")
	var acquisitions, exchanges atomic.Int32
	assertion := func(label string) string {
		return "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"spiffe://example.org/test","aud":"codex","label":"`+label+`"}`)) + ".synthetic"
	}
	startAgent := func(label string) func() {
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		server := grpc.NewServer()
		server.RegisterService(&grpc.ServiceDesc{ServiceName: "SpiffeWorkloadAPI", HandlerType: (*interface{})(nil), Methods: []grpc.MethodDesc{{MethodName: "FetchJWTSVID", Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			var request spiffeJWTRequest
			if err := decode(&request); err != nil {
				return nil, err
			}
			acquisitions.Add(1)
			return &spiffeJWTResponse{SVIDs: []*spiffeJWTSVID{{Token: assertion(label)}}}, nil
		}}}}, struct{}{})
		done := make(chan struct{})
		go func() { defer close(done); _ = server.Serve(listener) }()
		var once sync.Once
		stop := func() { once.Do(func() { server.Stop(); _ = listener.Close(); <-done }) }
		t.Cleanup(stop)
		return stop
	}
	stop := startAgent("one")
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", 400)
			return
		}
		count := exchanges.Add(1)
		label := "one"
		if count > 1 {
			label = "two"
		}
		if r.Form.Get("assertion") != assertion(label) || r.Form.Get("federation_rule_id") != "rule" {
			http.Error(w, "unexpected assertion", 400)
			return
		}
		fmt.Fprintf(w, `{"access_token":"token-%d","chatgpt_account_id":"acct","chatgpt_account_user_id":"u","expires_in":300,"issued_token_type":"urn:ietf:params:oauth:token-type:access_token","scope":"codex","token_type":"Bearer","user_id":"u"}`, count)
	}))
	defer tokenServer.Close()
	cfg, err := resolveWorkloadIdentityConfig(defaultWorkloadChatGPTBaseURL, workloadIdentityProcessEnv{hasFederationRule: true, federationRuleID: "rule", hasIdentityTokenSource: true, identityTokenSource: "spiffe", hasSPIFFEEndpoint: true, spiffeEndpoint: "unix://" + socket, hasSPIFFEAudience: true, spiffeAudience: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	cfg.tokenURL = tokenServer.URL
	exchange, err := cfg.exchange()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, err := exchange.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := exchange.Resolve(ctx)
	if err != nil || cached.Version != first.Version || acquisitions.Load() != 1 || exchanges.Load() != 1 {
		t.Fatalf("cache counts=%d/%d err=%v", acquisitions.Load(), exchanges.Load(), err)
	}
	stop()
	startAgent("two")
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := exchange.Refresh(ctx, first.Version)
			if err == nil && (token.Version != 2 || token.AccessToken != "token-2") {
				err = fmt.Errorf("unexpected refreshed token version %d", token.Version)
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
	if acquisitions.Load() != 2 || exchanges.Load() != 2 {
		t.Fatalf("refresh not coalesced: %d/%d", acquisitions.Load(), exchanges.Load())
	}
}

func TestSPIFFEUnixRejectsExplicitEmptyID(t *testing.T) {
	env := workloadIdentityProcessEnv{hasFederationRule: true, federationRuleID: "rule", hasIdentityTokenSource: true, identityTokenSource: "spiffe", hasSPIFFEEndpoint: true, spiffeEndpoint: "unix:///test/agent.sock", hasSPIFFEAudience: true, spiffeAudience: "codex", hasSPIFFEID: true}
	if _, err := resolveWorkloadIdentityConfig(defaultWorkloadChatGPTBaseURL, env); err == nil {
		t.Fatal("explicit empty ID accepted")
	}
	env.hasSPIFFEID = false
	if _, err := resolveWorkloadIdentityConfig(defaultWorkloadChatGPTBaseURL, env); err != nil {
		t.Fatalf("omitted ID rejected: %v", err)
	}
}
