package appserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

func TestPlanHTTPRetryPreservesCompletedPlans(t *testing.T) {
	for _, name := range []string{"unfinished", "completed", "tool_continuation"} {
		completedFirst := name != "unfinished"
		toolContinuation := name == "tool_continuation"
		t.Run(name, func(t *testing.T) {
			store := session.NewStore(t.TempDir())
			sink := NewNotificationBuffer()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if toolContinuation && n > 1 {
					body, err := io.ReadAll(r.Body)
					if err != nil || !strings.Contains(string(body), "call-plan") || !strings.Contains(string(body), "function_call_output") {
						t.Errorf("missing tool result in continuation request: %s, %v", body, err)
					}
				}
				responseID, itemID, text := "resp-first", "msg-first", "<proposed_plan>\nfirst\n</proposed_plan>"
				if n > 1 {
					responseID, itemID, text = "resp-second", "msg-second", "<proposed_plan>\nsecond\n</proposed_plan>"
				}
				w.Header().Set("Content-Type", "text/event-stream")
				events := []string{
					`{"type":"response.created","response":{"id":` + strconv.Quote(responseID) + `}}`,
					`{"type":"response.output_item.added","item":{"id":` + strconv.Quote(itemID) + `,"type":"message","role":"assistant","content":[]}}`,
					`{"type":"response.output_text.delta","item_id":` + strconv.Quote(itemID) + `,"delta":` + strconv.Quote(text) + `}`,
				}
				if n > 1 || completedFirst {
					events = append(events, `{"type":"response.output_item.done","item":{"id":`+strconv.Quote(itemID)+`,"type":"message","role":"assistant","content":[{"type":"output_text","text":`+strconv.Quote(text)+`}]}}`)
				}
				if toolContinuation && n == 1 {
					events = append(events, `{"type":"response.output_item.added","item":{"id":"tool-plan","type":"function_call","name":"update_plan","call_id":"call-plan","arguments":""}}`, `{"type":"response.output_item.done","item":{"id":"tool-plan","type":"function_call","name":"update_plan","call_id":"call-plan","arguments":"{\"plan\":[{\"step\":\"Review\",\"status\":\"completed\"}]}"}}`)
				}
				if n > 1 || toolContinuation {
					events = append(events, `{"type":"response.completed","response":{"id":`+strconv.Quote(responseID)+`}}`)
				}
				_, _ = w.Write([]byte(modelResponsesSSE(events...)))
			}))
			defer server.Close()
			runner := model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{Provider: &model.APIProvider{BaseURL: server.URL + "/v1", StreamMaxRetries: 1}, Stream: true})
			router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Turns: turn.NewTurnService(), Agent: runner, ThreadStatus: NewThreadStatusManager()})
			if toolContinuation {
				home := t.TempDir()
				if err := os.WriteFile(config.ConfigPath(home), []byte("[tools.update_plan]\nenabled = true\n"), 0600); err != nil {
					t.Fatal(err)
				}
				router.services.Config = config.NewConfigService(home)
			}
			router.SetNotificationSink(sink)
			started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "plan"}))
			if started.Error != nil {
				t.Fatal(started.Error)
			}
			threadID := started.Result.(*ThreadStartResponse).Thread.ID
			reply := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID, Prompt: "plan", Model: "gpt-test", CollaborationMode: map[string]any{"mode": "plan"}}))
			if reply.Error != nil {
				t.Fatal(reply.Error)
			}
			turnID := reply.Result.(*turn.TurnStartResponse).Turn.ID
			first := waitForPlanDelta(t, sink, turnID, "first\n")
			second := waitForPlanDelta(t, sink, turnID, "second\n")
			if completedFirst == (first.ItemID == second.ItemID) {
				t.Fatalf("completedFirst=%v IDs=%q/%q", completedFirst, first.ItemID, second.ItemID)
			}
			waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
			if toolContinuation {
				waitForTurnPlanUpdated(t, sink, turnID)
			}
			waitForItemCompleted(t, sink, second.ItemID)
			if completedFirst {
				waitForItemCompleted(t, sink, first.ItemID)
			}
			record, err := store.Load(session.ThreadID(threadID))
			if err != nil {
				t.Fatal(err)
			}
			var plans []session.Item
			for _, item := range record.Items {
				if item.Type == "plan" {
					plans = append(plans, item)
				}
			}
			want := 1
			if completedFirst {
				want = 2
			}
			if len(plans) != want {
				t.Fatalf("persisted plans=%#v, want %d", plans, want)
			}
			if plans[len(plans)-1].ID != second.ItemID {
				t.Fatal("last persisted plan disagrees with stream")
			}
			if completedFirst && (plans[0].ID != first.ItemID || plans[0].Text != "first\n") {
				t.Fatalf("lost first completion: %#v", plans[0])
			}
			if calls.Load() != 2 {
				t.Fatalf("requests=%d", calls.Load())
			}
		})
	}
}
