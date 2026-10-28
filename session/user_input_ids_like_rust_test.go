package session

import "testing"

func TestMessageInputItemKeepsResponseItemIDLikeRust(t *testing.T) {
	item := Item{
		ID:             "user-turn-1",
		ResponseItemID: "msg_input_abc",
		Type:           "message",
		Role:           "user",
		Text:           "hello",
		Content:        []ContentPart{{Type: "input_text", Text: "hello"}},
	}
	input, ok := InputItemFromItem(&item, &HistoryBuildOptions{}).(map[string]any)
	if !ok {
		t.Fatal("input item is not a map")
	}
	if input["id"] != "msg_input_abc" {
		t.Fatalf("id = %#v", input["id"])
	}

	plain := item
	plain.ResponseItemID = ""
	input, ok = InputItemFromItem(&plain, &HistoryBuildOptions{}).(map[string]any)
	if !ok {
		t.Fatal("plain input item is not a map")
	}
	if _, present := input["id"]; present {
		t.Fatalf("unexpected id on plain message: %#v", input["id"])
	}
}
