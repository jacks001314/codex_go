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

func textMessage(role string, id string, text string) HistoryItem {
	return HistoryItem{
		Kind:    eventmap.ResponseMessage,
		Role:    role,
		ID:      id,
		Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: text}},
	}
}

func outputMessage(role string, id string, text string) HistoryItem {
	return HistoryItem{
		Kind:    eventmap.ResponseMessage,
		Role:    role,
		ID:      id,
		Content: []eventmap.ContentItem{{Kind: eventmap.ContentOutputText, Text: text}},
	}
}

// TestDropLastUserTurnsTrimsContextUpdatesAboveRolledBackTurn mirrors Rust
// history_tests::drop_last_n_user_turns_trims_context_updates_above_rolled_back_turn:
// the contiguous contextual developer/user items above the rolled-back turn are
// trimmed, while a purely contextual bundle leaves the reference baseline intact.
func TestDropLastUserTurnsTrimsContextUpdatesAboveRolledBackTurn(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(
		outputMessage("assistant", "prefix", "session prefix item"),
		textMessage("user", "u1", "turn 1 user"),
		outputMessage("assistant", "a1", "turn 1 assistant"),
		textMessage("developer", "d1", "<managed_developer_instructions>\nROLLED_BACK_MANAGED_INSTRUCTIONS\n</managed_developer_instructions>"),
		textMessage("developer", "d2", "<apps_instructions>\nROLLED_BACK_APPS_INSTRUCTIONS"),
		textMessage("developer", "d3", "<plugins_instructions>\nROLLED_BACK_PLUGIN_INSTRUCTIONS"),
		textMessage("developer", "d4", "<environments_instructions>\nROLLED_BACK_ENVIRONMENT_INSTRUCTIONS"),
		textMessage("developer", "d5", "<collaboration_mode>ROLLED_BACK_DEV_INSTRUCTIONS</collaboration_mode>"),
		textMessage("developer", "d6", "<multi_agent_role>ROLLED_BACK_MULTI_AGENT_ROLE</multi_agent_role>"),
		textMessage("developer", "d7", "<multi_agent_mode>ROLLED_BACK_MULTI_AGENT_MODE</multi_agent_mode>"),
		textMessage("user", "ctx", "<environment_context><cwd>PRETURN_CONTEXT_DIFF_CWD</cwd></environment_context>"),
		textMessage("user", "u2", "turn 2 user"),
		outputMessage("assistant", "a2", "turn 2 assistant"),
	)
	baseline := []byte(`{"turn_id":"reference-turn","model":"gpt-test"}`)
	manager.SetReferenceContextItem(baseline)

	manager.DropLastUserTurns(1)

	items := manager.RawItems()
	wantIDs := []string{"prefix", "u1", "a1"}
	if len(items) != len(wantIDs) {
		t.Fatalf("items = %#v, want %d items", items, len(wantIDs))
	}
	for i, id := range wantIDs {
		if items[i].ID != id {
			t.Fatalf("items[%d].ID = %q, want %q (items=%#v)", i, items[i].ID, id, items)
		}
	}
	if got := manager.ReferenceContextItem(); string(got) != string(baseline) {
		t.Fatalf("reference context item = %s, want %s", got, baseline)
	}
}

// TestDropLastUserTurnsClearsReferenceContextForMixedDeveloperContextBundles
// mirrors Rust
// history_tests::drop_last_n_user_turns_clears_reference_context_for_mixed_developer_context_bundles:
// a trimmed developer message mixing contextual fragments with persistent
// developer text is not reconstructible, so the baseline is cleared.
func TestDropLastUserTurnsClearsReferenceContextForMixedDeveloperContextBundles(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(
		textMessage("user", "u1", "turn 1 user"),
		outputMessage("assistant", "a1", "turn 1 assistant"),
		HistoryItem{
			Kind: eventmap.ResponseMessage,
			Role: "developer",
			ID:   "d1",
			Content: []eventmap.ContentItem{
				{Kind: eventmap.ContentInputText, Text: "<permissions instructions>contextual permissions</permissions instructions>"},
				{Kind: eventmap.ContentInputText, Text: "persistent plugin instructions"},
			},
		},
		textMessage("user", "ctx", "<environment_context><cwd>PRETURN_CONTEXT_DIFF_CWD</cwd></environment_context>"),
		textMessage("user", "u2", "turn 2 user"),
		outputMessage("assistant", "a2", "turn 2 assistant"),
	)
	manager.SetReferenceContextItem([]byte(`{"turn_id":"reference-turn"}`))

	manager.DropLastUserTurns(1)

	items := manager.RawItems()
	if len(items) != 2 || items[0].ID != "u1" || items[1].ID != "a1" {
		t.Fatalf("items = %#v, want [u1 a1]", items)
	}
	if got := manager.ReferenceContextItem(); len(got) != 0 {
		t.Fatalf("reference context item = %s, want cleared", got)
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

func TestForPromptTagsOmittedImageUnsupported(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(
		HistoryItem{Kind: eventmap.ResponseMessage, Role: "user", ID: "u1",
			Content:          []eventmap.ContentItem{{Kind: eventmap.ContentInputImage, ImageURL: "data:image/png;base64,AAA"}},
			ContentItemKinds: []string{"user.image"}},
	)
	prompt := manager.ForPrompt(false)
	if len(prompt) != 1 || len(prompt[0].ContentItemKinds) != 1 || prompt[0].ContentItemKinds[0] != "images.unsupported" {
		t.Fatalf("omitted image kind = %#v, want images.unsupported (Rust #40277)", prompt[0].ContentItemKinds)
	}
	if prompt[0].Content[0].Kind != eventmap.ContentInputText || prompt[0].Content[0].Text != ImageOmittedPlaceholder {
		t.Fatalf("omitted image content = %#v", prompt[0].Content)
	}
}

func TestMergeContextualMessagesPreservesContentItemKinds(t *testing.T) {
	merged := MergeContextualMessages([]HistoryItem{
		{Kind: eventmap.ResponseMessage, Role: "user", ID: "u1", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "a"}}, ContentItemKinds: []string{"user.text"}},
		{Kind: eventmap.ResponseMessage, Role: "user", ID: "u2", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputImage, ImageURL: "data:"}}, ContentItemKinds: []string{"user.image"}},
	})
	if len(merged) != 1 || len(merged[0].ContentItemKinds) != 2 || merged[0].ContentItemKinds[0] != "user.text" || merged[0].ContentItemKinds[1] != "user.image" {
		t.Fatalf("merged kinds = %#v", merged[0].ContentItemKinds)
	}
}
