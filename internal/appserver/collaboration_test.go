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

func TestCollaborationModeListDefaultPresetsMatchRust(t *testing.T) {
	response := NewCollaborationModeService(nil).List(&CollaborationModeListParams{})
	medium := "medium"
	want := []CollaborationModeMask{
		{Name: "Plan", Mode: "plan", ReasoningEffort: &medium},
		{Name: "Default", Mode: "default"},
	}
	if len(response.Data) != len(want) {
		t.Fatalf("modes = %#v, want %#v", response.Data, want)
	}
	for i := range want {
		if response.Data[i].Name != want[i].Name ||
			response.Data[i].Mode != want[i].Mode ||
			ptrStringValue(response.Data[i].Model) != ptrStringValue(want[i].Model) ||
			ptrStringValue(response.Data[i].ReasoningEffort) != ptrStringValue(want[i].ReasoningEffort) {
			t.Fatalf("mode[%d] = %#v, want %#v", i, response.Data[i], want[i])
		}
	}
}

func TestRuntimeRouterCollaborationModeListReturnsRustPresets(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	response := router.Handle(requestWithParams(t, IntID(1), MethodCollaborationModeList, CollaborationModeListParams{}))
	if response.Error != nil {
		t.Fatalf("collaborationMode/list = %+v", response)
	}
	list := response.Result.(*CollaborationModeListResponse)
	if len(list.Data) != 2 || list.Data[0].Mode != "plan" || ptrStringValue(list.Data[0].ReasoningEffort) != "medium" || list.Data[1].Mode != "default" {
		t.Fatalf("collaboration modes = %#v", list.Data)
	}
}
