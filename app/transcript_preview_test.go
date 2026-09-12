package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	codextui "codex_go/tui"
)

func TestTranscriptPreviewFromTurnsIsChronologicalAndBounded(t *testing.T) {
	turns := []appserver.Turn{
		{ID: "t1", Items: []appserver.ThreadItem{{ID: "u1", Type: "userMessage", Role: "user", Text: "first"}}},
		{ID: "t2", Items: []appserver.ThreadItem{
			{ID: "u2", Type: "userMessage", Role: "user", Text: "second"},
			{ID: "a1", Type: "agentMessage", Text: "answer"},
		}},
	}
	got := transcriptPreviewFromTurns(turns, "")
	want := []codextui.TranscriptPreviewLine{
		{Speaker: codextui.TranscriptPreviewUser, Text: "first"},
		{Speaker: codextui.TranscriptPreviewUser, Text: "second"},
		{Speaker: codextui.TranscriptPreviewAssistant, Text: "answer"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
}

func TestTranscriptPreviewFromTurnsCapsAtSixLines(t *testing.T) {
	var items []appserver.ThreadItem
	for _, text := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight"} {
		items = append(items, appserver.ThreadItem{ID: text, Type: "userMessage", Role: "user", Text: text})
	}
	got := transcriptPreviewFromTurns([]appserver.Turn{{ID: "t1", Items: items}}, "")
	if len(got) != transcriptPreviewMaxLines {
		t.Fatalf("lines = %d, want %d", len(got), transcriptPreviewMaxLines)
	}
	// The newest six are kept, in chronological order.
	wantTexts := []string{"three", "four", "five", "six", "seven", "eight"}
	for index, line := range got {
		if line.Text != wantTexts[index] {
			t.Fatalf("line %d = %q, want %q (%#v)", index, line.Text, wantTexts[index], got)
		}
	}
}

func TestTranscriptPreviewFromTurnsKeepsMessageLineOrder(t *testing.T) {
	turns := []appserver.Turn{{ID: "t1", Items: []appserver.ThreadItem{
		{ID: "a1", Type: "agentMessage", Text: "line one\nline two\nline three"},
	}}}
	got := transcriptPreviewFromTurns(turns, "")
	want := []codextui.TranscriptPreviewLine{
		{Speaker: codextui.TranscriptPreviewAssistant, Text: "line one"},
		{Speaker: codextui.TranscriptPreviewAssistant, Text: "line two"},
		{Speaker: codextui.TranscriptPreviewAssistant, Text: "line three"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
}

func TestTranscriptPreviewSkipsNonMessageItems(t *testing.T) {
	turns := []appserver.Turn{{ID: "t1", Items: []appserver.ThreadItem{
		{ID: "c1", Type: "commandExecution", Text: "ls"},
		{ID: "r1", Type: "reasoning", Text: "thinking"},
		{ID: "u1", Type: "userMessage", Role: "user", Text: "prompt"},
	}}}
	got := transcriptPreviewFromTurns(turns, "")
	if len(got) != 1 || got[0].Text != "prompt" || got[0].Speaker != codextui.TranscriptPreviewUser {
		t.Fatalf("lines = %#v", got)
	}
}

// TestInteractiveRemoteTranscriptPreviewHandlerPagesItems covers the paginated
// path: the picker's preview is fetched from thread/items/list (newest first).
func TestInteractiveRemoteTranscriptPreviewHandlerPagesItems(t *testing.T) {
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
			case string(appserver.MethodThreadItemsList):
				var params appserver.ThreadItemsListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-1" || params.SortDirection != appserver.SortDesc ||
					params.Limit == nil || *params.Limit != transcriptPreviewPageSize || params.Cursor != nil {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/items/list params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"data": []any{
							map[string]any{"turnId": "t2", "item": map[string]any{"id": "a1", "type": "agentMessage", "text": "answer"}},
							map[string]any{"turnId": "t2", "item": map[string]any{"id": "u2", "type": "userMessage", "text": "second"}},
						},
						"nextCursor": nil,
					},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	lines, err := interactiveRemoteTranscriptPreviewHandler(ctx, endpoint)("thread-1", "/repo")
	if err != nil {
		t.Fatalf("preview error = %v", err)
	}
	want := []codextui.TranscriptPreviewLine{
		{Speaker: codextui.TranscriptPreviewUser, Text: "second"},
		{Speaker: codextui.TranscriptPreviewAssistant, Text: "answer"},
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %#v, want %#v", lines, want)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}
