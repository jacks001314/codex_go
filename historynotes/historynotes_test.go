package historynotes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/tool"
)

func TestToolsExposeNineNamespacedExecutorsLikeRust(t *testing.T) {
	backend := &Backend{BaseURL: "https://chatgpt.com/backend-api"}
	executors := Tools(backend, "session-1", "root")
	if len(executors) != 9 {
		t.Fatalf("tools = %d, want 9", len(executors))
	}
	seen := map[string]bool{}
	for _, executor := range executors {
		spec := executor.Spec()
		key := spec.Name.Key()
		if seen[key] {
			t.Fatalf("duplicate tool %q", key)
		}
		seen[key] = true
		if spec.NamespaceDescription == "" {
			t.Fatalf("tool %s missing namespace description", key)
		}
		if spec.InputSchema == nil {
			t.Fatalf("tool %s missing input schema", key)
		}
	}
	for _, want := range []string{
		"history.list_windows", "history.list_items", "history.read_item", "history.search_contents",
		"notes.list_files_by_prefix", "notes.read_file", "notes.search_contents", "notes.append_to_file", "notes.write_file",
	} {
		if !seen[want] {
			t.Fatalf("missing tool %q", want)
		}
	}
}

func TestHistoryNotesBackendPostsContextAndAuthLikeRust(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_, _ = w.Write([]byte(`{"windows":[{"id":"w1"}]}`))
	}))
	defer server.Close()
	backend := &Backend{
		BaseURL: server.URL,
		ApplyAuth: func(request *http.Request, _ []byte) error {
			request.Header.Set("Authorization", "Bearer token")
			return nil
		},
		HTTPDoer: server.Client().Do,
	}
	result, err := backend.Call(context.Background(), "alpha/history/v2/list_windows", "session-1", "root", map[string]any{"limit": 10})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if gotPath != "/alpha/history/v2/list_windows" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer token" {
		t.Fatalf("auth = %q", gotAuth)
	}
	contextValue, ok := gotBody["context"].(map[string]any)
	if !ok || contextValue["session_id"] != "session-1" || contextValue["current_agent_name"] != "root" {
		t.Fatalf("context = %#v", gotBody["context"])
	}
	if string(result) != `{"windows":[{"id":"w1"}]}` {
		t.Fatalf("result = %s", result)
	}
}

func TestHistoryNotesToolValidationAndExecutionLikeRust(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	executor := NewToolExecutor(HistoryListItems, &Backend{BaseURL: server.URL, HTTPDoer: server.Client().Do}, "session-1", "root")
	output, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-1",
		ToolName: tool.NamespacedName("history", "list_items"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"limit":5}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !output.Success || gotPath != "/alpha/history/v2/list_items" || !strings.Contains(output.Body, `"items"`) {
		t.Fatalf("output = %#v path=%q", output, gotPath)
	}

	oversized, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-2",
		ToolName: tool.NamespacedName("history", "list_items"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"limit":999}`},
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds the maximum of 20") {
		t.Fatalf("oversized limit error = %v", err)
	}
	_ = oversized
}

func TestHistoryNotesSearchQueryLengthValidation(t *testing.T) {
	action := HistorySearchContents
	if err := action.ValidateArguments(map[string]any{"query": strings.Repeat("x", maxSearchQueryChars)}); err != nil {
		t.Fatalf("boundary query rejected: %v", err)
	}
	if err := action.ValidateArguments(map[string]any{"query": strings.Repeat("x", maxSearchQueryChars+1)}); err == nil {
		t.Fatal("oversized query accepted")
	}
}

func TestThreadHintFetchesBoundedText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"  remember to run gofmt  "}`))
	}))
	defer server.Close()
	backend := &Backend{BaseURL: server.URL}
	got, ok := backend.ThreadHint(context.Background(), "session-1", "root")
	if !ok || got != "remember to run gofmt" {
		t.Fatalf("ThreadHint = %q ok=%v, want trimmed text", got, ok)
	}

	// An oversized hint is omitted (Rust #40539 MAX_THREAD_HINT_BYTES).
	big := strings.Repeat("x", MaxThreadHintBytes+1)
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"` + big + `"}`))
	}))
	defer server2.Close()
	backend2 := &Backend{BaseURL: server2.URL}
	if _, ok := backend2.ThreadHint(context.Background(), "session-1", "root"); ok {
		t.Fatal("oversized thread hint should be omitted")
	}
}
