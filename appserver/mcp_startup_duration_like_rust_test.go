package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/mcp"
)

// Rust #63613: mcpServer/startupStatus/updated serializes durationMs for ready
// and failed transitions and serializes it as null while starting.
func TestMCPStartupStatusNotificationCarriesDurationLikeRust(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{})
	defer router.Close()
	router.SetNotificationSink(sink)

	service := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		"docs": {Name: "docs", Config: mcp.ServerConfig{
			Command:        "definitely-not-a-real-mcp-binary-xyz",
			Enabled:        true,
			StartupTimeout: 2 * time.Second,
		}},
	}})
	router.observeMCPStartupStatus(service, "")

	type payload struct {
		Name       string  `json:"name"`
		Status     string  `json:"status"`
		DurationMs *uint64 `json:"durationMs"`
	}
	// Decode every notification so a missing field and an explicit null stay
	// distinguishable.
	decode := func() []payload {
		var out []payload
		for _, notification := range sink.List() {
			if notification.Method != NotificationMCPServerStatusUpdated {
				continue
			}
			raw, err := json.Marshal(notification.Params)
			if err != nil {
				t.Fatalf("marshal notification: %v", err)
			}
			var decoded payload
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("decode notification: %v", err)
			}
			if decoded.Name != "docs" {
				continue
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatalf("decode notification fields: %v", err)
			}
			if _, ok := fields["durationMs"]; !ok {
				t.Fatalf("notification omits durationMs: %s", raw)
			}
			out = append(out, decoded)
		}
		return out
	}

	// Drive one real startup attempt through the service so the listener sees
	// the starting and failed transitions.
	name := "docs"
	_, _ = service.ListStatusChecked(&mcp.MCPListServerStatusParams{ServerName: &name, Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailFull}})

	got := decode()
	if len(got) != 2 {
		t.Fatalf("startup notifications = %+v, want starting and failed", got)
	}
	if got[0].Status != string(mcp.MCPServerStarting) {
		t.Fatalf("first status = %q, want starting", got[0].Status)
	}
	if got[0].DurationMs != nil {
		t.Fatalf("starting durationMs = %d, want null", *got[0].DurationMs)
	}
	if got[1].Status != string(mcp.MCPServerFailed) {
		t.Fatalf("second status = %q, want failed", got[1].Status)
	}
	if got[1].DurationMs == nil {
		t.Fatal("failed durationMs = null, want a measured value")
	}
	if *got[1].DurationMs > 10000 {
		t.Fatalf("failed durationMs = %d, want a plausible measurement", *got[1].DurationMs)
	}
}
