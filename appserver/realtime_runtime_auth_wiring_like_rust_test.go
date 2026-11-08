package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/realtime"
	"codex_go/session"
)

func realtimeAuthRouter(t *testing.T, configBody string) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"sk-test"}`), 0o600); err != nil {
		t.Fatalf("WriteFile auth error = %v", err)
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(t.TempDir()), home, &RuntimeRouterOptions{})
	t.Cleanup(func() { _ = router.Close() })
	return router
}

// TestRealtimeStartWiresRuntimeAuthPerHandshakeLikeRust pins Rust #60003's
// wiring seam: a realtime start hands the transport a resolver that re-selects
// the runtime credential for every handshake, while a disabled feature or a
// provider that supplies its own credential keeps the static snapshot.
func TestRealtimeStartWiresRuntimeAuthPerHandshakeLikeRust(t *testing.T) {
	enabled := realtimeAuthRouter(t, "model_provider = \"openai\"\nfeatures = { runtime_agent_instances = true }\n")
	options, err := enabled.realtimeStartOptions(&realtime.StartParams{ThreadID: ""})
	if err != nil {
		t.Fatalf("realtimeStartOptions() error = %v", err)
	}
	if options == nil || options.Backend == nil {
		t.Fatal("realtime start produced no transport backend")
	}
	if options.Backend.RuntimeAuthHeaders == nil {
		t.Fatal("the realtime backend did not receive the per-handshake runtime auth resolver")
	}

	disabled := realtimeAuthRouter(t, "model_provider = \"openai\"\n")
	plain, err := disabled.realtimeStartOptions(&realtime.StartParams{ThreadID: ""})
	if err != nil {
		t.Fatalf("realtimeStartOptions() with the feature off error = %v", err)
	}
	if plain != nil && plain.Backend != nil && plain.Backend.RuntimeAuthHeaders != nil {
		t.Fatal("the feature being off still installed a runtime auth resolver")
	}

	// A provider that supplies its own credential must not be shadowed by the
	// saved-login runtime (Rust #59823 bind_api_key).
	explicit := realtimeAuthRouter(t, "model_provider = \"openai\"\nexperimental_bearer_token = \"sk-explicit\"\nfeatures = { runtime_agent_instances = true }\n")
	explicitOptions, err := explicit.realtimeStartOptions(&realtime.StartParams{ThreadID: ""})
	if err != nil {
		t.Fatalf("realtimeStartOptions() with an explicit credential error = %v", err)
	}
	if explicitOptions != nil && explicitOptions.Backend != nil && explicitOptions.Backend.RuntimeAuthHeaders != nil {
		t.Fatal("runtime auth shadowed a provider that supplies its own credential")
	}
}

// TestRealtimeRuntimeAuthResolverSkipsForeignDestinationsLikeRust pins the
// destination gate: a non-OpenAI backend keeps its own credential, so the
// resolver reports nil and the handshake leaves the static headers untouched.
func TestRealtimeRuntimeAuthResolverSkipsForeignDestinationsLikeRust(t *testing.T) {
	router := realtimeAuthRouter(t, "model_provider = \"openai\"\nfeatures = { runtime_agent_instances = true }\n")
	cfg := &config.Config{Values: map[string]any{
		"model_provider": "openai",
		"features":       map[string]any{"runtime_agent_instances": true},
	}}
	hook := router.realtimeRuntimeAuthHeaders(cfg)
	if hook == nil {
		t.Fatal("runtime auth resolver was not built for an OpenAI provider")
	}
	headers, err := hook(context.Background(), "https://example.com/v1/realtime")
	if err != nil {
		t.Fatalf("non-OpenAI destination error = %v", err)
	}
	if headers != nil {
		t.Fatalf("non-OpenAI destination headers = %#v, want nil", headers)
	}
}
