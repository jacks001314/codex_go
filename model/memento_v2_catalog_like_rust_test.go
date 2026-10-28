package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMementoV2CatalogMessagesDecodeLikeRust(t *testing.T) {
	raw := `{"tools":{"history":{"namespace_description":"history ns","list_windows":{"description":""},"search_contents":{"parameters":"{\"type\":\"object\"}"}},"notes":{"read_file":{"description":"read file"}},"context_management":{"new_context":{"description":"new context"}}}}`
	var messages ModelMessages
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		t.Fatalf("decode: %v", err)
	}
	history := messages.HistoryTools()
	if history == nil || history.NamespaceDescription == nil || *history.NamespaceDescription != "history ns" {
		t.Fatalf("history namespace = %#v", history)
	}
	listWindows := history.Tool("list_windows")
	if listWindows == nil || listWindows.Description == nil || *listWindows.Description != "" {
		t.Fatalf("explicit empty description lost: %#v", listWindows)
	}
	if history.Tool("list_items") != nil {
		t.Fatal("sparse history entry decoded as present")
	}
	if history.Tool("search_contents").Parameters == nil {
		t.Fatal("history parameter override lost")
	}
	notes := messages.NotesTools()
	if notes == nil || notes.Tool("read_file").Description == nil || *notes.Tool("read_file").Description != "read file" {
		t.Fatalf("notes entry = %#v", notes)
	}
	context := messages.ContextManagementTools()
	if context == nil || context.Tool("new_context").Description == nil || *context.Tool("new_context").Description != "new context" {
		t.Fatalf("context management entry = %#v", context)
	}
	if context.Tool("get_context_remaining") != nil {
		t.Fatal("sparse context-management entry decoded as present")
	}
	encoded, err := json.Marshal(&messages)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(string(encoded), "list_items") || strings.Contains(string(encoded), "append_to_file") {
		t.Fatalf("sparse entries were serialized: %s", encoded)
	}
}

func TestCloneModelInfoDeepCopiesMementoV2MessagesLikeRust(t *testing.T) {
	description := "original"
	parameters := `{"type":"object"}`
	info := ModelInfo{ModelMessages: &ModelMessages{Tools: &ToolMessages{
		History: &HistoryToolMessages{ListWindows: &ToolMessage{Description: &description}},
		Notes:   &NotesToolMessages{ReadFile: &ToolMessage{Parameters: &parameters}},
		ContextManagement: &ContextManagementToolMessages{
			NewContext: &ToolMessage{Description: &description},
		},
	}}}
	clone := cloneModelInfo(info)
	*clone.ModelMessages.Tools.History.ListWindows.Description = "changed"
	*clone.ModelMessages.Tools.Notes.ReadFile.Parameters = "{}"
	*clone.ModelMessages.Tools.ContextManagement.NewContext.Description = "changed"
	if *info.ModelMessages.Tools.History.ListWindows.Description != "original" {
		t.Fatal("history description was shared with the clone")
	}
	if *info.ModelMessages.Tools.Notes.ReadFile.Parameters != parameters {
		t.Fatal("notes parameters were shared with the clone")
	}
	if *info.ModelMessages.Tools.ContextManagement.NewContext.Description != "original" {
		t.Fatal("context-management description was shared with the clone")
	}
}
