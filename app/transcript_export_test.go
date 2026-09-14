package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
)

func TestTranscriptExportMarkdownMatchesRustSections(t *testing.T) {
	turns := []appserver.Turn{
		{ID: "turn-1", Items: []appserver.ThreadItem{
			{ID: "u1", Type: "userMessage", Role: "user", Text: "hello"},
			{ID: "a1", Type: "agentMessage", Text: "hi **there**"},
			{ID: "r1", Type: "reasoning", Text: "thinking"},
			{ID: "c1", Type: "commandExecution", Text: "ls", Status: "completed"},
			{ID: "p1", Type: "plan", Text: "- step"},
		}},
		{ID: "turn-review", Items: []appserver.ThreadItem{
			{ID: "rev1", Type: "enteredReviewMode", Text: "changes against main"},
			{ID: "u2", Type: "userMessage", Role: "user", Text: "review prompt"},
			{ID: "rev2", Type: "exitedReviewMode", Text: "review complete"},
		}},
		{ID: "turn-2", Items: []appserver.ThreadItem{
			{ID: "u3", Type: "userMessage", Role: "user", Text: "second"},
		}},
	}

	markdown := transcriptExportMarkdown(turns, false)
	if !strings.HasPrefix(markdown, "# Codex conversation\n") {
		t.Fatalf("missing header:\n%s", markdown)
	}
	for _, want := range []string{
		"## User\n\nhello\n",
		"## Assistant\n\nhi **there**\n",
		"## Reasoning\n\nthinking\n",
		"## Activity\n\n    ls\n",
		"## Plan\n\n- step\n",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}
	if strings.Contains(markdown, "review prompt") || strings.Contains(markdown, "changes against main") {
		t.Fatalf("hidden review content leaked into the export:\n%s", markdown)
	}
	// The visible prompt after the review turn is still exported.
	if !strings.Contains(markdown, "## User\n\nsecond\n") {
		t.Fatalf("visible prompt after review missing:\n%s", markdown)
	}
	if strings.Index(markdown, "## User\n\nhello") > strings.Index(markdown, "## User\n\nsecond") {
		t.Fatalf("sections are out of order:\n%s", markdown)
	}
}

func TestTranscriptExportMarkdownEmptyWithoutContent(t *testing.T) {
	if markdown := transcriptExportMarkdown(nil, false); markdown != "" {
		t.Fatalf("empty turns = %q, want no export", markdown)
	}
	onlyMarkers := []appserver.Turn{{ID: "turn-1", Items: []appserver.ThreadItem{
		{ID: "rev1", Type: "enteredReviewMode", Text: "changes"},
		{ID: "rev2", Type: "exitedReviewMode", Text: "done"},
	}}}
	if markdown := transcriptExportMarkdown(onlyMarkers, false); markdown != "" {
		t.Fatalf("marker-only turns = %q, want no export", markdown)
	}
}

func TestTranscriptExportSectionFallsBackToToolText(t *testing.T) {
	item := appserver.ThreadItem{ID: "c1", Type: "commandExecution", Name: "shell", Status: "completed"}
	heading, text, indent, ok := transcriptExportSection(item, false)
	if !ok || heading != "Activity" || !indent || !strings.Contains(text, "shell") {
		t.Fatalf("section = %q %q indent=%v ok=%v", heading, text, indent, ok)
	}
}

func TestInteractiveRemoteTranscriptExportHandlerReadsThread(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-source" || !params.IncludeTurns {
					remoteTUITestSendErr(serverErrs, errThreadReadParams(params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{"thread": map[string]any{
						"id":  "thread-source",
						"cwd": "/repo",
						"turns": []any{map[string]any{
							"id": "turn-1", "status": "completed",
							"items": []any{
								map[string]any{"id": "u1", "type": "userMessage", "text": "hello"},
								map[string]any{"id": "a1", "type": "agentMessage", "text": "hi"},
							},
						}},
					}},
				})
			default:
				remoteTUITestSendErr(serverErrs, errUnexpectedMethod(req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	markdown, err := interactiveRemoteTranscriptExportHandler(ctx, endpoint, false)("thread-source")
	if err != nil {
		t.Fatalf("export error = %v", err)
	}
	for _, want := range []string{"# Codex conversation", "## User\n\nhello", "## Assistant\n\nhi"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func errThreadReadParams(params appserver.ThreadReadParams) error {
	return errors.New("unexpected thread/read params: " + params.ThreadID)
}

func errUnexpectedMethod(method string) error {
	return errors.New("unexpected method " + method)
}

// TestReasoningRawVisibilityGatesRawContent pins Rust RawReasoningVisibility:
// the reasoning projections carry the raw chain-of-thought variant, and it is
// selected only when show_raw_agent_reasoning is enabled.
func TestReasoningRawVisibilityGatesRawContent(t *testing.T) {
	item := appserver.ThreadItem{
		ID:   "r1",
		Type: "reasoning",
		Data: map[string]any{
			"summary": []any{"**Step one**\n\nSummary body."},
			"content": []any{"Raw chain of thought."},
		},
	}

	message, ok := remoteTUIMessageFromThreadItem(item)
	if !ok || !message.TranscriptOnly || message.ItemID != "r1" {
		t.Fatalf("reasoning message = %#v ok=%v", message, ok)
	}
	if message.Text != "Summary body." {
		t.Fatalf("summary variant = %q", message.Text)
	}
	if message.ReasoningRawText != "Summary body.\n\nRaw chain of thought." {
		t.Fatalf("raw variant = %q", message.ReasoningRawText)
	}

	// Export follows thread_transcript.rs: the raw content replaces the
	// summary projection when raw reasoning is visible.
	if got := transcriptExportReasoningText(item, false); got != "Summary body." {
		t.Fatalf("raw-off export text = %q", got)
	}
	if got := transcriptExportReasoningText(item, true); got != "Raw chain of thought." {
		t.Fatalf("raw-on export text = %q", got)
	}
}
