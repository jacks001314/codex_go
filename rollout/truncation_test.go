package rollout

import (
	"testing"

	"codex_go/eventmap"
)

func TestUserMessagePositionsApplyRollback(t *testing.T) {
	items := []TruncationItem{
		user("one"),
		user("two"),
		{Kind: TruncationItemThreadRolledBack, NumTurns: 1},
		user("three"),
	}
	got := UserMessagePositions(items)
	if len(got) != 2 || got[0] != 0 || got[1] != 3 {
		t.Fatalf("positions = %#v", got)
	}
}

func TestTruncationHelpers(t *testing.T) {
	items := []TruncationItem{{Kind: TruncationItemResponse}, user("one"), user("two"), {Kind: TruncationItemInterAgentCommunication, TriggerTurn: true}}
	if got := TruncateBeforeNthUserMessageFromStart(items, 1); len(got) != 2 {
		t.Fatalf("truncate before = %#v", got)
	}
	if got := TruncateToLastNForkTurns(items, 1); len(got) != 1 || got[0].Kind != TruncationItemInterAgentCommunication {
		t.Fatalf("truncate last fork = %#v", got)
	}
}

func TestTruncateAfterTurnID(t *testing.T) {
	items := []TruncationItem{{Kind: TruncationItemTurnStarted, TurnID: "a"}, user("one"), {Kind: TruncationItemTurnStarted, TurnID: "b"}, user("two")}
	got, err := TruncateAfterTurnID(items, "a")
	if err != nil {
		t.Fatalf("TruncateAfterTurnID() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got = %#v", got)
	}
	if _, err := TruncateAfterTurnID(items, "missing"); err == nil {
		t.Fatalf("expected missing turn error")
	}
}

func user(text string) TruncationItem {
	return TruncationItem{Kind: TruncationItemResponse, Response: &eventmap.ResponseItem{
		Kind:    eventmap.ResponseMessage,
		Role:    "user",
		Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: text}},
	}}
}
