package appserver

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	"codex_go/realtime"
)

// Rust #46011: managed enforce_residency overrides static and environment
// provider residency headers for both WebSocket and WebRTC/sideband realtime
// connections, while unrelated provider headers stay intact.
func TestRealtimeStartOptionsApplyManagedResidencyLikeRust(t *testing.T) {
	const envVar = "CODEX_TEST_REALTIME_RESIDENCY_HEADER"
	home := t.TempDir()
	configBody := `model_provider = "mock_provider"

[features]
realtime_conversation = true

[model_providers.mock_provider]
name = "Mock realtime provider"
base_url = "https://responses.example.test/v1"
wire_api = "responses"

[model_providers.mock_provider.http_headers]
"X-OpenAI-Internal-Codex-Residency" = "eu-static"
"x-provider-header" = "preserved"

[model_providers.mock_provider.env_http_headers]
"x-openai-internal-codex-residency" = "` + envVar + `"
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("enforce_residency = \"us\"\n"), 0o600); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
	t.Setenv(envVar, "eu-environment")
	t.Setenv(auth.OpenAIAPIKeyEnv, "sk-realtime-residency")

	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	options, err := router.realtimeStartOptions(&realtime.StartParams{
		ThreadID:       "thread-residency",
		OutputModality: realtime.OutputAudio,
	})
	if err != nil {
		t.Fatalf("realtimeStartOptions: %v", err)
	}
	if options == nil || options.Backend == nil {
		t.Fatal("realtimeStartOptions returned no backend")
	}
	for _, testCase := range []struct {
		name    string
		headers http.Header
	}{
		{name: "websocket", headers: options.Backend.Headers},
		{name: "webrtc", headers: options.Backend.CallHeaders},
	} {
		if got := testCase.headers.Get(model.ResidencyHeaderName); got != "us" {
			t.Fatalf("%s residency header = %q, want us", testCase.name, got)
		}
		if values := testCase.headers.Values(model.ResidencyHeaderName); len(values) != 1 {
			t.Fatalf("%s residency values = %#v, want one managed value", testCase.name, values)
		}
		if got := testCase.headers.Get("x-provider-header"); got != "preserved" {
			t.Fatalf("%s unrelated provider header = %q, want preserved", testCase.name, got)
		}
	}
}
