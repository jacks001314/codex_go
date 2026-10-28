package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/turn"
)

func TestAdmittedUserInputResponseItemIDsLikeRust(t *testing.T) {
	params := &turn.TurnStartParams{
		ThreadID:     "thread-1",
		Prompt:       "Repeat this input.",
		UserItemUUID: "11111111-1111-7111-8111-111111111111",
	}
	item, notice := (&RuntimeRouter{}).preparedUserMessageInputItem("turn-1", params)
	if notice != nil {
		t.Fatalf("unexpected notice: %#v", notice)
	}
	message, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("item = %#v", item)
	}
	if got := message["id"]; got != "msg_input_11111111-1111-7111-8111-111111111111" {
		t.Fatalf("input item id = %#v", got)
	}

	sessionItem, ok := runtimeUserPromptSessionItem("turn-1", params, time.Now().UTC())
	if !ok {
		t.Fatal("runtimeUserPromptSessionItem() did not build an item")
	}
	if sessionItem.ID == "msg_input_11111111-1111-7111-8111-111111111111" {
		t.Fatalf("UI item id must stay distinct from the Responses id: %q", sessionItem.ID)
	}
	if sessionItem.ResponseItemID != "msg_input_11111111-1111-7111-8111-111111111111" {
		t.Fatalf("session ResponseItemID = %q", sessionItem.ResponseItemID)
	}

	// Without a host uuid a fresh prefixed id is minted.
	plain := &turn.TurnStartParams{ThreadID: "thread-1", Prompt: "Repeat this input."}
	firstItem, _ := (&RuntimeRouter{}).preparedUserMessageInputItem("turn-2", plain)
	firstID, _ := firstItem.(map[string]any)["id"].(string)
	if firstID == "" || !strings.HasPrefix(firstID, "msg_") || strings.HasPrefix(firstID, "msg_input_") {
		t.Fatalf("generated id = %q", firstID)
	}
}

func TestSteerUserInputIDsStayDistinctLikeRust(t *testing.T) {
	first := &turn.TurnSteerParams{
		ThreadID:            "thread-1",
		ExpectedTurnID:      "turn-1",
		Prompt:              "same",
		ClientUserMessageID: "same-client-id",
		UserItemUUID:        "22222222-2222-7222-8222-222222222222",
	}
	second := &turn.TurnSteerParams{
		ThreadID:            "thread-1",
		ExpectedTurnID:      "turn-1",
		Prompt:              "same",
		ClientUserMessageID: "same-client-id",
		UserItemUUID:        "33333333-3333-7333-8333-333333333333",
	}
	firstItem, ok := sessionItemFromTurnSteer(first)
	if !ok {
		t.Fatal("first steer item missing")
	}
	secondItem, ok := sessionItemFromTurnSteer(second)
	if !ok {
		t.Fatal("second steer item missing")
	}
	if firstItem.ResponseItemID != "msg_input_22222222-2222-7222-8222-222222222222" {
		t.Fatalf("first response item id = %q", firstItem.ResponseItemID)
	}
	if secondItem.ResponseItemID != "msg_input_33333333-3333-7333-8333-333333333333" {
		t.Fatalf("second response item id = %q", secondItem.ResponseItemID)
	}
	if firstItem.ResponseItemID == secondItem.ResponseItemID {
		t.Fatal("steer response item ids collided")
	}
}
