package doctor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/model"
)

func TestWebsocketProbePrintsAuthEndpointAndDNSWithoutCredentialsLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	provider := &model.ProviderInfo{
		Name:               "OpenAI",
		BaseURL:            server.URL + "/v1",
		WireAPI:            model.WireAPIResponses,
		SupportsWebsockets: true,
		RequiresOpenAIAuth: true,
	}
	check := websocketReachabilityCheckFromProvider("openai", provider, nil)
	var details strings.Builder
	for _, detail := range check.Details {
		details.WriteString(detail + "\n")
	}
	joined := details.String()
	for _, want := range []string{
		"auth mode: none\n",
		"endpoint: ws",
		"DNS: ",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "/v1/responses") {
		t.Fatalf("endpoint does not target responses:\n%s", joined)
	}
	for _, detail := range check.Details {
		if strings.HasPrefix(detail, "provider setup failed") {
			t.Fatalf("signed-out probe reported a setup failure: %q", detail)
		}
	}
}

func TestRenderHumanRendersLabelLessDetailAsBulletLikeRust(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusWarning,
		Checks: []*DoctorCheck{
			NewCheck("network.websocket_reachability", "websocket", CheckStatusWarning, "Responses WebSocket timed out; HTTPS fallback may still work").
				Detail("endpoint: wss://api.openai.com/v1/responses").
				Detail("handshake timed out"),
		},
	}
	if rendered := RenderHuman(report, &Options{}); !strings.Contains(rendered, "    \u00b7 handshake timed out") {
		t.Fatalf("unicode report missing bullet:\n%s", rendered)
	}
	if rendered := RenderHuman(report, &Options{ASCII: true}); !strings.Contains(rendered, "    - handshake timed out") {
		t.Fatalf("ascii report missing bullet:\n%s", rendered)
	}
}
