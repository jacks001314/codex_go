package turn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/tool"
)

func TestBuildToolRegistryRegistersStandaloneWebSearchRunTool(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.EnableToolSearch = false
	options.WebSearch = &WebSearchOptions{}

	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	executor, ok := registry.Lookup(tool.NamespacedName(WebSearchNamespace, WebSearchRunTool))
	if !ok {
		t.Fatal("web.run missing")
	}
	spec := executor.Spec()
	if !spec.Parallel || spec.Exposure != tool.ExposureModelVisible {
		t.Fatalf("spec = %#v", spec)
	}
	properties := spec.InputSchema["properties"].(map[string]any)
	timeSchema := properties["time"].(map[string]any)
	if timeSchema["description"] != "Get time for the given UTC offsets." {
		t.Fatalf("time schema = %#v", timeSchema)
	}
}

func TestWebSearchHandlerPostsAlphaSearchAndReturnsRustOutputShape(t *testing.T) {
	var searchBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/alpha/search" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("X-Provider-Test"); got != "provider" {
			t.Fatalf("provider header = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&searchBody); err != nil {
			t.Fatalf("Decode(body) error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codexapi.SearchResponse{Output: "Search result"})
	}))
	defer server.Close()

	handler := NewWebSearchHandler(&WebSearchOptions{
		SessionID: "thread-1",
		Model:     "mock-model",
		Provider: model.APIProvider{
			BaseURL: server.URL + "/api/codex",
			Headers: http.Header{
				"X-Provider-Test": []string{"provider"},
			},
		},
		Auth:       model.BearerAuthHeaders("test-token", "", false),
		HTTPClient: server.Client(),
		InputItems: []any{
			model.UserMessageInputItem("Search the web"),
		},
		Settings: &codexapi.SearchSettings{
			AllowedCallers: []codexapi.AllowedCaller{codexapi.AllowedCallerDirect},
		},
	})
	invocation := &tool.Invocation{
		CallID:   "call-web-search",
		ToolName: tool.NamespacedName(WebSearchNamespace, WebSearchRunTool),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"search_query":[{"q":"standalone web search"}]}`,
		},
	}
	output, err := handler.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || !output.Success || output.Body != "Search result" {
		t.Fatalf("output = %#v", output)
	}
	if searchBody["model"] != "mock-model" || searchBody["id"] != "thread-1" {
		t.Fatalf("search body model/id = %#v", searchBody)
	}
	commands := searchBody["commands"].(map[string]any)
	searchQuery := commands["search_query"].([]any)[0].(map[string]any)
	if searchQuery["q"] != "standalone web search" {
		t.Fatalf("commands = %#v", commands)
	}
	settings := searchBody["settings"].(map[string]any)
	allowedCallers := settings["allowed_callers"].([]any)
	if len(allowedCallers) != 1 || allowedCallers[0] != "direct" {
		t.Fatalf("settings = %#v", settings)
	}
	input := searchBody["input"].([]any)
	last := input[len(input)-1].(map[string]any)
	content := last["content"].([]any)[0].(map[string]any)
	if last["type"] != "message" || last["role"] != "user" || content["type"] != "input_text" || content["text"] != "Search the web" {
		t.Fatalf("input last = %#v", last)
	}
	contentItems, ok := output.Data["content_items"].([]FunctionCallOutputContentItem)
	if !ok || len(contentItems) != 1 || contentItems[0].Type != "input_text" || contentItems[0].Text != "Search result" {
		t.Fatalf("content_items = %#v", output.Data["content_items"])
	}
	action := output.Data["web_search_action"].(map[string]any)
	if action["type"] != "search" || action["query"] != "standalone web search" {
		t.Fatalf("action = %#v", action)
	}
	response := ToolResponseFromOutput(invocation, output)
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal(response) error = %v", err)
	}
	if !strings.Contains(string(data), `"type":"function_call_output"`) ||
		!strings.Contains(string(data), `"call_id":"call-web-search"`) ||
		!strings.Contains(string(data), `"type":"input_text"`) ||
		!strings.Contains(string(data), `"text":"Search result"`) {
		t.Fatalf("response json = %s", data)
	}
}

func TestWebSearchRecentInputKeepsPreviousVisibleTurnLikeRust(t *testing.T) {
	items := []any{
		searchTestMessage("system", "system"),
		searchTestMessage("user", "old user"),
		searchTestMessage("assistant", "old assistant"),
		searchTestMessageWithID("user", "previous user", "msg_previous_user"),
		map[string]any{"type": "function_call", "name": "tool", "call_id": "call-1", "arguments": "{}"},
		searchTestMessageWithID("assistant", "previous assistant", "msg_previous_assistant"),
		searchTestMessage("developer", "developer"),
		searchTestMessage("user", "<environment_context>\n<cwd>/tmp</cwd>\n</environment_context>"),
		searchTestMessage("user", "current user"),
		searchTestMessage("assistant", "current commentary"),
	}
	got, ok := cloneSearchInputItems(items).([]any)
	if !ok {
		t.Fatalf("recent input = %#v", cloneSearchInputItems(items))
	}
	if len(got) != 3 {
		t.Fatalf("recent input len = %d, items = %#v", len(got), got)
	}
	if searchInputMessageText(got[0]) != "previous user" ||
		searchInputMessageText(got[1]) != "previous assistant" ||
		searchInputMessageText(got[2]) != "current user" {
		t.Fatalf("recent input = %#v", got)
	}
	for i := range got {
		message := got[i].(map[string]any)
		if _, ok := message["id"]; ok {
			t.Fatalf("recent input kept id: %#v", message)
		}
	}
}

func TestWebSearchRecentInputKeepsOnlyTextFromUserMessagesLikeRust(t *testing.T) {
	items := []any{
		map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": "previous user",
			}, {
				"type":      "input_image",
				"image_url": "data:image/png;base64,image",
			}},
		},
		searchTestMessage("assistant", "previous assistant"),
		searchTestMessage("user", "current user"),
	}
	got, ok := cloneSearchInputItems(items).([]any)
	if !ok || len(got) != 3 {
		t.Fatalf("recent input = %#v", cloneSearchInputItems(items))
	}
	firstContent := got[0].(map[string]any)["content"].([]any)
	if len(firstContent) != 1 || firstContent[0].(map[string]any)["type"] != "input_text" {
		t.Fatalf("first content = %#v", firstContent)
	}
}

func TestWebSearchCommandActionReportsQueriesAndNavigationLikeRust(t *testing.T) {
	cases := []struct {
		arguments string
		expected  map[string]any
	}{{
		arguments: `{"image_query":[{"q":"waterfalls"},{"q":"mountains"}]}`,
		expected:  map[string]any{"type": "search", "query": nil, "queries": []string{"waterfalls", "mountains"}},
	}, {
		arguments: `{"open":[{"ref_id":"https://example.com/docs"}]}`,
		expected:  map[string]any{"type": "openPage", "url": "https://example.com/docs"},
	}, {
		arguments: `{"find":[{"ref_id":"https://example.com/docs","pattern":"install"}]}`,
		expected:  map[string]any{"type": "findInPage", "url": "https://example.com/docs", "pattern": "install"},
	}, {
		arguments: `{"find":[{"ref_id":"turn0search0","pattern":"install"}]}`,
		expected:  map[string]any{"type": "findInPage", "url": nil, "pattern": "install"},
	}, {
		arguments: `{"open":[{"ref_id":"turn0search0"}]}`,
		expected:  map[string]any{"type": "other"},
	}}
	for _, tc := range cases {
		var commands codexapi.SearchCommands
		if err := json.Unmarshal([]byte(tc.arguments), &commands); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", tc.arguments, err)
		}
		if got := webSearchCommandAction(&commands); !reflect.DeepEqual(got, tc.expected) {
			t.Fatalf("action for %s = %#v, want %#v", tc.arguments, got, tc.expected)
		}
	}
}

func searchTestMessage(role string, text string) map[string]any {
	return searchTestMessageWithID(role, text, "")
}

func searchTestMessageWithID(role string, text string, id string) map[string]any {
	contentType := "input_text"
	if role == "assistant" {
		contentType = "output_text"
	}
	message := map[string]any{
		"type": "message",
		"role": role,
		"content": []map[string]any{{
			"type": contentType,
			"text": text,
		}},
	}
	if id != "" {
		message["id"] = id
	}
	return message
}

func searchInputMessageText(value any) string {
	message := value.(map[string]any)
	content := message["content"].([]any)
	block := content[0].(map[string]any)
	text, _ := block["text"].(string)
	return text
}
