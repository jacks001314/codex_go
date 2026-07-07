package appserver

import (
	"encoding/json"
	"testing"
)

func TestCollaborationModeListResponseMarshalRustShape(t *testing.T) {
	empty, err := json.Marshal(&CollaborationModeListResponse{})
	if err != nil {
		t.Fatalf("MarshalJSON(empty) error = %v", err)
	}
	if string(empty) != `{"data":[]}` {
		t.Fatalf("empty payload = %s", empty)
	}

	data, err := json.Marshal(&CollaborationModeListResponse{Data: []CollaborationModeMask{{Name: "Custom"}}})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	var payload map[string][]map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	item := payload["data"][0]
	if item["name"] != "Custom" || item["mode"] != nil || item["model"] != nil || item["reasoning_effort"] != nil {
		t.Fatalf("payload = %#v", item)
	}
}
