package appserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// Rust CurrentTimeReminderState::take_reminder_due (core/src/session/time_reminder.rs):
// a new context window forces delivery, after_user_or_tool_output needs a
// boundary unless the window changed, and the boundary is consumed even when
// the interval suppresses the reminder.
func TestCurrentTimeReminderStateMatchesRustTakeReminderDue(t *testing.T) {
	base := time.Date(2026, 6, 17, 17, 30, 0, 0, time.UTC)
	anyInference := config.CurrentTimeReminderAnyInference
	afterOutput := config.CurrentTimeReminderAfterUserOrToolOutput

	t.Run("any_inference_new_window_then_interval", func(t *testing.T) {
		state := &currentTimeReminderTurnState{}
		if !state.takeReminderDue("w1", base, 60, anyInference) {
			t.Fatal("first inference in a new window must deliver")
		}
		if state.takeReminderDue("w1", base.Add(10*time.Second), 60, anyInference) {
			t.Fatal("a repeat inference before the interval must not deliver")
		}
		if !state.takeReminderDue("w1", base.Add(70*time.Second), 60, anyInference) {
			t.Fatal("an inference after the interval must deliver")
		}
		if !state.takeReminderDue("w2", base.Add(80*time.Second), 60, anyInference) {
			t.Fatal("a new window must force delivery")
		}
	})

	t.Run("after_user_or_tool_output_requires_a_boundary", func(t *testing.T) {
		state := &currentTimeReminderTurnState{}
		if !state.takeReminderDue("w1", base, 60, afterOutput) {
			t.Fatal("the first window must deliver even without a boundary")
		}
		if state.takeReminderDue("w1", base.Add(120*time.Second), 60, afterOutput) {
			t.Fatal("a plain inference must not deliver without a user/tool-output boundary")
		}
		state.noteUserOrToolOutputBoundary()
		if !state.takeReminderDue("w1", base.Add(130*time.Second), 60, afterOutput) {
			t.Fatal("a boundary after the interval must deliver")
		}
	})

	t.Run("boundary_is_consumed_when_the_interval_suppresses", func(t *testing.T) {
		state := &currentTimeReminderTurnState{}
		if !state.takeReminderDue("w1", base, 60, afterOutput) {
			t.Fatal("the first window must deliver")
		}
		state.noteUserOrToolOutputBoundary()
		if state.takeReminderDue("w1", base.Add(10*time.Second), 60, afterOutput) {
			t.Fatal("the interval must suppress delivery")
		}
		if state.takeReminderDue("w1", base.Add(120*time.Second), 60, afterOutput) {
			t.Fatal("the consumed boundary must not deliver on a later boundary-less inference")
		}
		state.noteUserOrToolOutputBoundary()
		if !state.takeReminderDue("w1", base.Add(130*time.Second), 60, afterOutput) {
			t.Fatal("a fresh boundary after the interval must deliver")
		}
	})

	t.Run("new_window_ignores_the_boundary_gate", func(t *testing.T) {
		state := &currentTimeReminderTurnState{}
		if !state.takeReminderDue("w1", base, 60, afterOutput) {
			t.Fatal("the first window must deliver")
		}
		if !state.takeReminderDue("w2", base.Add(time.Second), 60, afterOutput) {
			t.Fatal("a new window must deliver without a boundary even inside the interval")
		}
	})
}

func TestSessionItemIsUserOrToolOutputBoundaryLikeRust(t *testing.T) {
	cases := []struct {
		name string
		item session.Item
		want bool
	}{
		{name: "user message", item: session.Item{Type: "message", Role: "user", Text: "hello"}, want: true},
		{name: "user message content", item: session.Item{Type: "message", Role: "user", Content: []session.ContentPart{{Type: "text", Text: "hello"}}}, want: true},
		{name: "contextual user fragment", item: session.Item{Type: "message", Role: "user", Text: "<environment_context>\n<shell>x</shell>\n</environment_context>"}, want: false},
		{name: "compaction summary", item: session.Item{Type: "message", Role: "user", Text: "Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:\nsummary"}, want: false},
		{name: "agent message", item: session.Item{Type: "agent_message", Role: "assistant", Text: "task"}, want: true},
		{name: "function call output", item: session.Item{Type: "function_call_output", CallID: "call-1", Text: "out"}, want: true},
		{name: "custom tool call output", item: session.Item{Type: "custom_tool_call_output", CallID: "call-1", Text: "out"}, want: true},
		{name: "tool search output", item: session.Item{Type: "tool_search_output", CallID: "call-1"}, want: true},
		{name: "plain assistant message", item: session.Item{Type: "message", Role: "assistant", Text: "done"}, want: false},
		{name: "trigger turn assistant message", item: session.Item{Type: "message", Role: "assistant", Text: `<inter_agent trigger_turn="true"/>`}, want: true},
		{name: "reasoning", item: session.Item{Type: "reasoning", Text: "think"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionItemIsUserOrToolOutputBoundary(&tc.item); got != tc.want {
				t.Fatalf("sessionItemIsUserOrToolOutputBoundary(%#v) = %v, want %v", tc.item, got, tc.want)
			}
		})
	}
}

// Rust notes the boundary when history records the item
// (Session::record_conversation_items and record_inter_agent_communication).
func TestRuntimeRouterNotesRecordedReminderBoundaryLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	router.noteCurrentTimeReminderRecordedItems("thread", []session.Item{{Type: "function_call_output", CallID: "call-1"}})
	state := router.newCurrentTimeReminderTurnState("thread")
	state.mu.Lock()
	pending := state.pendingUserOrToolOutputBoundary
	state.mu.Unlock()
	if !pending {
		t.Fatal("a recorded tool output must set the pending user/tool-output boundary")
	}
	router.noteCurrentTimeReminderRecordedItems("thread", []session.Item{{Type: "message", Role: "assistant", Text: "plain"}})
	state.mu.Lock()
	pending = state.pendingUserOrToolOutputBoundary
	state.mu.Unlock()
	if !pending {
		t.Fatal("a non-boundary recording must not clear the pending boundary")
	}
}

// A new context window forces a reminder even when the interval and delivery
// mode would otherwise suppress it (Rust take_reminder_due is_new_window).
func TestRuntimeRouterCurrentTimeReminderNewWindowForcesDeliveryLikeRust(t *testing.T) {
	home := t.TempDir()
	var bodiesMu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		bodiesMu.Lock()
		call := len(bodies) + 1
		bodies = append(bodies, body)
		bodiesMu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(modelResponsesSSE(
			fmt.Sprintf(`{"type":"response.created","response":{"id":"resp-current-time-window-%d"}}`, call),
			fmt.Sprintf(`{"type":"response.output_item.done","item":{"id":"msg-current-time-window-%d","type":"message","role":"assistant","content":[{"type":"output_text","text":"Done"}]}}`, call),
			fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-current-time-window-%d","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`, call),
		)))
	}))
	defer server.Close()
	configBody := fmt.Sprintf(`
model = "mock-model"
approval_policy = "never"
sandbox_mode = "read-only"
model_provider = "mock_provider"
include_environment_context = false

[features.current_time_reminder]
enabled = true
reminder_interval_seconds = 3600
clock_source = "external"
delivery_mode = "after_user_or_tool_output"

[model_providers.mock_provider]
name = "Mock provider for test"
base_url = "%s/v1"
wire_api = "responses"
request_max_retries = 0
stream_max_retries = 0
`, server.URL)
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	first := int64(1781717655)
	router.SetTimeProvider(&scriptedTimeProvider{values: []int64{first, first + 10, first + 20}})

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	startTurn := func(id int64, prompt string) {
		response := router.Handle(requestWithParams(t, IntID(id), MethodTurnStart, turn.TurnStartParams{
			ThreadID: threadID,
			Input:    []turn.TurnUserInput{{Text: prompt}},
		}))
		if response.Error != nil {
			t.Fatalf("turn start error: %+v", response.Error)
		}
		waitForTurnCompletedStatus(t, sink, response.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
	}
	startTurn(2, "first")
	startTurn(3, "second")
	router.advanceContextWindowID(threadID)
	startTurn(4, "third")

	bodiesMu.Lock()
	defer bodiesMu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("recorded bodies = %d, want 3", len(bodies))
	}
	got := [][]string{
		currentTimeReminderTextsFromBody(bodies[0]),
		currentTimeReminderTextsFromBody(bodies[1]),
		currentTimeReminderTextsFromBody(bodies[2]),
	}
	want := [][]string{
		{"It is 2026-06-17 17:34:15 UTC."},
		{"It is 2026-06-17 17:34:15 UTC."},
		{"It is 2026-06-17 17:34:15 UTC.", "It is 2026-06-17 17:34:35 UTC."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("current time reminders = %#v, want %#v", got, want)
	}
}
