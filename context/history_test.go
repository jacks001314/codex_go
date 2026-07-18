package context

import (
	"reflect"
	"testing"

	"codex_go/eventmap"
)

func TestManagerRecordNormalizeAndPrompt(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(
		HistoryItem{Kind: eventmap.ResponseMessage, Role: "system", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "skip"}}},
		HistoryItem{Kind: eventmap.ResponseMessage, Role: "user", ID: "u1", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "hello"}}},
		HistoryItem{Kind: eventmap.ResponseOther, ID: "call-a", WebSearchAction: "function_call"},
	)
	prompt := manager.ForPrompt(false)
	if len(prompt) != 3 {
		t.Fatalf("prompt len = %d, prompt=%#v", len(prompt), prompt)
	}
	if prompt[2].WebSearchAction != "function_call_output" || prompt[2].ImageResult != "aborted" {
		t.Fatalf("synthetic output = %#v", prompt[2])
	}
}

func TestDropLastUserTurns(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(
		HistoryItem{Kind: eventmap.ResponseMessage, Role: "developer", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "prefix"}}},
		HistoryItem{Kind: eventmap.ResponseMessage, Role: "user", ID: "u1", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "one"}}},
		HistoryItem{Kind: eventmap.ResponseMessage, Role: "assistant", ID: "a1", Content: []eventmap.ContentItem{{Kind: eventmap.ContentOutputText, Text: "two"}}},
		HistoryItem{Kind: eventmap.ResponseMessage, Role: "user", ID: "u2", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "three"}}},
	)
	manager.DropLastUserTurns(1)
	items := manager.RawItems()
	if len(items) != 3 || items[len(items)-1].ID != "a1" {
		t.Fatalf("items = %#v", items)
	}
	manager.DropLastUserTurns(5)
	items = manager.RawItems()
	if len(items) != 1 || items[0].Role != "developer" {
		t.Fatalf("items after full rollback = %#v", items)
	}
}

func TestStripImagesAndBuildTextMessage(t *testing.T) {
	items := []HistoryItem{{
		Kind: eventmap.ResponseMessage,
		Role: "user",
		Content: []eventmap.ContentItem{
			{Kind: eventmap.ContentInputImage, ImageURL: "data:image/png;base64,abc"},
			{Kind: eventmap.ContentInputText, Text: "text"},
		},
	}}
	StripImages(&items, "no image")
	if items[0].Content[0].Kind != eventmap.ContentInputText || items[0].Content[0].Text != "no image" {
		t.Fatalf("items = %#v", items)
	}
	message := BuildTextMessage("developer", []string{"", "a", "b"})
	if message == nil || len(message.Content) != 2 {
		t.Fatalf("message = %#v", message)
	}
}

func TestMergeContextualMessagesAndTokenInfo(t *testing.T) {
	items := MergeContextualMessages([]HistoryItem{
		{Kind: eventmap.ResponseMessage, Role: "developer", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "a"}}},
		{Kind: eventmap.ResponseMessage, Role: "developer", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "b"}}},
		{Kind: eventmap.ResponseMessage, Role: "user", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "c"}}},
	})
	if len(items) != 2 || len(items[0].Content) != 2 {
		t.Fatalf("items = %#v", items)
	}
	manager := NewHistoryManager()
	window := int64(4096)
	manager.UpdateTokenInfo(TokenUsage{InputTokens: 1, TotalTokens: 3}, &window)
	manager.UpdateTokenInfo(TokenUsage{OutputTokens: 2, TotalTokens: 2}, nil)
	info := manager.TokenInfo()
	if info.TotalTokenUsage.TotalTokens != 5 || info.LastTokenUsage.OutputTokens != 2 || *info.ModelContextWindow != 4096 {
		t.Fatalf("info = %#v", info)
	}
}

func TestRemoveFirstItemRemovesCounterpart(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(
		HistoryItem{Kind: eventmap.ResponseOther, ID: "call-a", WebSearchAction: "function_call"},
		HistoryItem{Kind: eventmap.ResponseOther, ID: "call-a", WebSearchAction: "function_call_output"},
		HistoryItem{Kind: eventmap.ResponseMessage, Role: "user", ID: "u1"},
	)
	manager.RemoveFirstItem()
	got := manager.RawItems()
	want := []HistoryItem{{Kind: eventmap.ResponseMessage, Role: "user", ID: "u1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RawItems() = %#v, want %#v", got, want)
	}
}
