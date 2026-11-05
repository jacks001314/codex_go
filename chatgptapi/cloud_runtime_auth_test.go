package chatgptapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type recordedRuntimeAuth struct {
	destinations []string
	calls        int
}

func (r *recordedRuntimeAuth) HeadersForDestination(_ context.Context, destination string) (http.Header, error) {
	r.calls++
	r.destinations = append(r.destinations, destination)
	return http.Header{
		"Authorization":            []string{"AgentAssertion 1." + destination[len(destination)-1:] + ".b.c"},
		"x-openai-agent-assertion": []string{"1.a.b.c"},
	}, nil
}

// Mirrors Rust #59572: the cloud client resolves runtime credentials for each
// request attempt instead of baking them into the client.
func TestCloudClientResolvesRuntimeAuthPerRequestLikeRust(t *testing.T) {
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seen = append(seen, request.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"threads":[]}`))
	}))
	defer server.Close()

	provider := &recordedRuntimeAuth{}
	client := NewCloudClient(&CloudClientOptions{
		BaseURL:     server.URL,
		Headers:     http.Header{"Authorization": []string{"Bearer static"}},
		HTTPClient:  server.Client(),
		RuntimeAuth: provider,
	})
	for i := 0; i < 2; i++ {
		if _, err := client.GetTurnCosts(context.Background(), map[string][]string{"thread": {"turn"}}); err != nil {
			t.Fatalf("GetTurnCosts() error = %v", err)
		}
	}
	if provider.calls != 2 {
		t.Fatalf("runtime auth calls = %d, want one per request", provider.calls)
	}
	for _, authorization := range seen {
		if authorization == "Bearer static" {
			t.Fatalf("static credential reached the server: %#v", seen)
		}
		if len(authorization) == 0 || authorization[:14] != "AgentAssertion" {
			t.Fatalf("authorization = %q, want the runtime assertion", authorization)
		}
	}
}
