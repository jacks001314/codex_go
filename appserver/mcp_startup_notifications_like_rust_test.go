package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/mcp"
)

// Rust #61257: the startup notification must carry the thread and the
// registration scope of the server that transitioned.
func TestMCPStartupStatusNotificationCarriesEnvironmentLikeRust(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{})
	defer router.Close()
	router.SetNotificationSink(sink)

	service := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		"docs": {
			Name: "docs",
			Config: mcp.ServerConfig{
				Command:        "definitely-not-a-real-mcp-binary-xyz",
				EnvironmentID:  "env-a",
				Enabled:        true,
				StartupTimeout: 2 * time.Second,
			},
		},
	}})
	router.observeMCPStartupStatus(service, "thread-startup")
	name := "docs"
	_, _ = service.ListStatusChecked(&mcp.MCPListServerStatusParams{ServerName: &name, Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailFull}})

	var found bool
	for _, notification := range sink.List() {
		if notification.Method != NotificationMCPServerStatusUpdated {
			continue
		}
		var payload MCPServerStatusUpdatedNotification
		raw, _ := json.Marshal(notification.Params)
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode notification: %v", err)
		}
		if payload.Name != "docs" {
			continue
		}
		if payload.ThreadID == nil || *payload.ThreadID != "thread-startup" {
			t.Fatalf("threadId = %v", payload.ThreadID)
		}
		if payload.EnvironmentID == nil || *payload.EnvironmentID != "env-a" {
			t.Fatalf("environmentId = %v", payload.EnvironmentID)
		}
		if payload.Status != string(mcp.MCPServerFailed) {
			continue
		}
		found = true
	}
	if !found {
		t.Fatalf("no failed startup notification: %+v", sink.List())
	}
}
