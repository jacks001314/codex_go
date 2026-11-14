package appserver

import (
	"encoding/json"
	"testing"
)

// Rust #61257: startup notifications and elicitation requests carry the
// registration scope so same-named servers stay distinguishable.
func TestMCPRegistrationScopeSurvivesNotificationsLikeRust(t *testing.T) {
	environmentID := "env-a"

	notification, err := json.Marshal(&MCPServerStatusUpdatedNotification{
		Name: "docs", Status: "ready", EnvironmentID: &environmentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decodedNotification map[string]any
	if err := json.Unmarshal(notification, &decodedNotification); err != nil {
		t.Fatal(err)
	}
	if decodedNotification["environmentId"] != "env-a" {
		t.Fatalf("notification environmentId = %v", decodedNotification["environmentId"])
	}

	withoutScope, err := json.Marshal(&MCPServerStatusUpdatedNotification{Name: "docs", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	var decodedWithoutScope map[string]any
	if err := json.Unmarshal(withoutScope, &decodedWithoutScope); err != nil {
		t.Fatal(err)
	}
	if _, present := decodedWithoutScope["environmentId"]; present {
		t.Fatalf("thread-wide notification leaked environmentId: %s", withoutScope)
	}

	elicitation, err := json.Marshal(&MCPElicitationRequestParams{
		ThreadID: "thread", ServerName: "docs", Mode: "form", Message: "confirm",
		EnvironmentID: &environmentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decodedElicitation map[string]any
	if err := json.Unmarshal(elicitation, &decodedElicitation); err != nil {
		t.Fatal(err)
	}
	if decodedElicitation["environmentId"] != "env-a" {
		t.Fatalf("elicitation environmentId = %v", decodedElicitation["environmentId"])
	}
}
