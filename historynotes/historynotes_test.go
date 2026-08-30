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
	var gotLimit json.Number
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		if raw, ok := body["limit"]; ok {
			_ = json.Unmarshal(raw, &gotLimit)
		}
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

	// #40775: client-side argument limits were removed; an oversized limit is
	// forwarded to the backend instead of rejected locally.
	oversized, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-2",
		ToolName: tool.NamespacedName("history", "list_items"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"limit":999}`},
	})
	if err != nil {
		t.Fatalf("oversized limit Execute() error = %v", err)
	}
	if !oversized.Success {
		t.Fatalf("oversized limit not forwarded: %#v", oversized)
	}
	if gotLimit.String() != "999" {
		t.Fatalf("forwarded limit = %v, want 999", gotLimit)
	}
}

func TestHistoryNotesSearchQueryLengthValidation(t *testing.T) {
	action := HistorySearchContents
	if err := action.ValidateArguments(map[string]any{"query": strings.Repeat("x", maxSearchQueryChars)}); err != nil {
		t.Fatalf("boundary query rejected: %v", err)
	}
	// #40775: the client-side query length limit was removed; oversized queries
	// are forwarded to the backend.
	if err := action.ValidateArguments(map[string]any{"query": strings.Repeat("x", maxSearchQueryChars+1)}); err != nil {
		t.Fatalf("oversized query rejected: %v", err)
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

func TestHistoryNotesEncryptedArgumentsHeaderLikeRust(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"alpha/history/v2/search_contents", true},
		{"alpha/notes/v2/search_contents", true},
		{"alpha/notes/v2/append_to_file", true},
		{"alpha/notes/v2/write_file", true},
		{"alpha/history/v2/list_windows", false},
		{"alpha/notes/v2/thread_hint", false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get(encryptedToolArgumentsHeader); (got == "true") != tc.want {
					t.Fatalf("header = %q, want present=%v", got, tc.want)
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			backend := &Backend{BaseURL: server.URL, HTTPDoer: server.Client().Do}
			if _, err := backend.Call(context.Background(), tc.path, "session-1", "root", map[string]any{}); err != nil {
				t.Fatalf("Call(%s) error = %v", tc.path, err)
			}
		})
	}
}

func TestHistoryNotesMarkSensitiveFieldsEncryptedLikeRust(t *testing.T) {
	encryptedFields := map[string]bool{
		"history.search_contents.query": true,
		"notes.search_contents.query":   true,
		"notes.append_to_file.text":     true,
		"notes.write_file.text":         true,
	}
	for _, action := range allActions {
		schema := action.Parameters()
		props, _ := schema["properties"].(map[string]any)
		for name, prop := range props {
			propMap, _ := prop.(map[string]any)
			if propMap == nil {
				continue
			}
			key := action.Namespace() + "." + action.Name() + "." + name
			if encryptedFields[key] {
				if propMap["encrypted"] != true {
					t.Fatalf("field %s not marked encrypted: %#v", key, propMap)
				}
			} else if encrypted, _ := propMap["encrypted"].(bool); encrypted {
				t.Fatalf("field %s unexpectedly encrypted", key)
			}
		}
	}
}

func TestHistoryNotesForwardsTruncationPolicyLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(toolOutputTruncationPolicyHeader); got != `{"mode":"tokens","limit":10000}` {
			t.Fatalf("truncation policy header = %q, want tokens/10000", got)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	backend := &Backend{BaseURL: server.URL, HTTPDoer: server.Client().Do, ToolTruncationPolicy: &ToolTruncationPolicy{Mode: "tokens", Limit: 10000}}
	if _, err := backend.Call(context.Background(), "alpha/history/v2/list_windows", "session-1", "root", map[string]any{}); err != nil {
		t.Fatalf("Call() error = %v", err)
	}

	// No policy set -> no header.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(toolOutputTruncationPolicyHeader); got != "" {
			t.Fatalf("unexpected truncation policy header = %q", got)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server2.Close()
	plain := &Backend{BaseURL: server2.URL, HTTPDoer: server2.Client().Do}
	if _, err := plain.Call(context.Background(), "alpha/history/v2/list_windows", "session-1", "root", map[string]any{}); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
}

func TestHistoryNotesToolOutputPreservesUnboundedResultLikeRust(t *testing.T) {
	// The backend enforces the output budget before encryption, so a result
	// larger than the old client-side token limit must be returned unchanged.
	bigPayload := strings.Repeat("x", 40_001)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":["` + bigPayload + `"]}`))
	}))
	defer server.Close()
	executor := NewToolExecutor(HistoryReadItem, &Backend{BaseURL: server.URL, HTTPDoer: server.Client().Do}, "session-1", "root")
	output, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-1",
		ToolName: tool.NamespacedName("history", "read_item"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"item_id":"item-1","window_id":"window-1"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !output.Success || !strings.Contains(output.Body, bigPayload) {
		t.Fatalf("output body truncated or missing payload: len=%d", len(output.Body))
	}
}

func TestHistoryNotesBackendSanitizesErrorsLikeRust(t *testing.T) {
	// A non-2xx response surfaces only a consistent message with the status,
	// without leaking the underlying detail body.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`secret internal detail`))
	}))
	defer server.Close()
	backend := &Backend{BaseURL: server.URL, HTTPDoer: server.Client().Do}
	_, err := backend.Call(context.Background(), "alpha/history/v2/list_windows", "session-1", "root", map[string]any{})
	if err == nil || !strings.HasPrefix(err.Error(), operationErrorPrefix) || strings.Contains(err.Error(), "secret internal detail") {
		t.Fatalf("status error = %v, want sanitized operation error", err)
	}

	// Invalid JSON returns the sanitized message.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server2.Close()
	backend2 := &Backend{BaseURL: server2.URL, HTTPDoer: server2.Client().Do}
	_, err = backend2.Call(context.Background(), "alpha/history/v2/list_windows", "session-1", "root", map[string]any{})
	if err == nil || err.Error() != operationErrorPrefix+" The backend returned invalid JSON." {
		t.Fatalf("invalid-JSON error = %v, want sanitized operation error", err)
	}

	// An auth failure is sanitized without the underlying error.
	backend3 := &Backend{
		BaseURL: server.URL,
		ApplyAuth: func(*http.Request, []byte) error {
			return &customAuthError{message: "token refresh failed"}
		},
	}
	_, err = backend3.Call(context.Background(), "alpha/history/v2/list_windows", "session-1", "root", map[string]any{})
	if err == nil || err.Error() != operationErrorPrefix+" Could not apply backend authentication." || strings.Contains(err.Error(), "token refresh failed") {
		t.Fatalf("auth error = %v, want sanitized operation error", err)
	}
}

type customAuthError struct{ message string }

func (e *customAuthError) Error() string { return e.message }
