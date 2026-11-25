package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/compact"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

type samplingInterruptAgent struct {
	mu        sync.Mutex
	requests  []*model.AgentRequest
	entered   chan string
	release   chan struct{}
	stream    bool
	toolFirst bool
}

func (a *samplingInterruptAgent) Run(ctx context.Context, req *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	index := len(a.requests)
	a.requests = append(a.requests, req)
	a.mu.Unlock()
	if index == 0 && a.stream {
		kept := model.AgentItem{ID: "retained", Type: "message", Text: "retained completed item"}
		req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputAdded, Item: &kept})
		a.entered <- req.TurnID
		<-a.release
		req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &kept})
		drop := model.AgentItem{ID: "discarded", Type: "message", Text: "must never be persisted"}
		req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputAdded, Item: &drop})
		req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &drop})
		return nil, ctx.Err()
	}
	if index == 0 && a.toolFirst {
		return &model.AgentResponse{Items: []model.AgentItem{{ID: "call", Type: "function_call", Name: "echo", CallID: "call", Arguments: "{}"}}, Usage: model.AgentUsage{InputTokens: 300000, OutputTokens: 100}}, nil
	}
	text := fmt.Sprintf("completed answer %d", index)
	a.entered <- req.TurnID
	return &model.AgentResponse{Items: []model.AgentItem{{ID: fmt.Sprintf("answer-%d", index), Type: "message", Text: text}}, Message: text, Usage: model.AgentUsage{InputTokens: 300000, OutputTokens: 100}}, nil
}
func (a *samplingInterruptAgent) snapshot() []*model.AgentRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*model.AgentRequest(nil), a.requests...)
}

func newSamplingInterruptChild(t *testing.T, a *samplingInterruptAgent, runner compact.RemoteRunner, extra string) (*RuntimeRouter, *runtimeAgentController, *session.Store, *NotificationBuffer, string) {
	t.Helper()
	home := t.TempDir()
	cfg := "model_context_window = 1000000\n" + extra + "\n[features.multi_agent_v2]\nenabled = true\ninterrupt_at_item_boundary = true\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Turns: turn.NewTurnService(), Agent: a, Config: config.NewConfigService(home), ThreadStatus: NewThreadStatusManager(), CompactRunner: runner})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	controller := newRuntimeAgentControllerWithVersion(router, "parent", parent.Metadata.CWD, 4, agent.VersionV2).(*runtimeAgentController)
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	return router, controller, store, sink, child.AgentID
}
func receiveSamplingTurn(t *testing.T, c <-chan string) string {
	t.Helper()
	select {
	case id := <-c:
		return id
	case <-time.After(10 * time.Second):
		t.Fatal("sampling did not start")
		return ""
	}
}

func TestSamplingInterruptControllerRetainsHistoryAndSerialFollowups(t *testing.T) {
	a := &samplingInterruptAgent{entered: make(chan string, 8), release: make(chan struct{}), stream: true}
	router, c, store, sink, child := newSamplingInterruptChild(t, a, nil, "")
	response, startErr := router.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{ThreadID: child, Prompt: "initial work"}))
	if startErr != nil {
		t.Fatal(startErr)
	}
	_ = response
	old := receiveSamplingTurn(t, a.entered)
	active := router.threads.ActiveTurn(child)
	if active == nil || active.SamplingInterrupt == nil {
		t.Fatal("missing child interrupt slot")
	}
	if _, err := c.InterruptAgent(context.Background(), &agent.InterruptAgentArgs{Target: child}); err != nil {
		t.Fatal(err)
	}
	if !active.SamplingInterrupt.Requested() {
		t.Fatal("real controller hard-aborted enabled child")
	}
	for _, message := range []string{"Resume one.", "Resume two."} {
		if err := c.FollowupTask(context.Background(), &agent.FollowupTaskArgs{Target: child, Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-a.entered:
		t.Fatal("followup sampled before old item/terminal")
	default:
	}
	close(a.release)
	waitForTurnCompletedStatus(t, sink, old, TurnStatusInterrupted)
	first := receiveSamplingTurn(t, a.entered)
	waitForTurnCompletedStatus(t, sink, first, TurnStatusCompleted)
	second := receiveSamplingTurn(t, a.entered)
	waitForTurnCompletedStatus(t, sink, second, TurnStatusCompleted)
	if first == old || second == first || second == old {
		t.Fatalf("followup turn IDs: %s %s %s", old, first, second)
	}
	record, err := store.Read(session.ThreadID(child), true, true)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(record.Items)
	if !strings.Contains(string(data), "retained completed item") || strings.Contains(string(data), "must never be persisted") {
		t.Fatalf("history: %s", data)
	}
	requests := a.snapshot()
	if len(requests) != 3 {
		t.Fatalf("requests=%d", len(requests))
	}
	for index, message := range []string{"Resume one.", "Resume two."} {
		data, _ := json.Marshal(requests[index+1].InputItems)
		if !strings.Contains(string(data), message) {
			t.Fatalf("followup missing: %s", data)
		}
	}
}

type samplingInterruptCompactor struct {
	mu      sync.Mutex
	entered chan compact.Phase
	release chan struct{}
	count   int
}

func (c *samplingInterruptCompactor) Compact(ctx context.Context, req *compact.Request) (*compact.Result, error) {
	c.mu.Lock()
	c.count++
	index := c.count
	c.mu.Unlock()
	c.entered <- req.Phase
	<-c.release
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	summary := fmt.Sprintf("installed checkpoint %d", index)
	return &compact.Result{Request: *req, Status: compact.StatusCompleted, Summary: summary, NewHistory: compact.BuildCompactedHistory(nil, nil, summary), CompletedAt: time.Now().UTC(), Source: compact.SourceRemote}, nil
}

func TestSamplingInterruptRemoteCompactionPreservesSummaryAndAnswer(t *testing.T) {
	for _, post := range []bool{false, true} {
		t.Run(fmt.Sprintf("post=%v", post), func(t *testing.T) {
			a := &samplingInterruptAgent{entered: make(chan string, 8), toolFirst: !post}
			compactor := &samplingInterruptCompactor{entered: make(chan compact.Phase, 8), release: make(chan struct{}, 8)}
			extra := "model_auto_compact_token_limit = 100000\n"
			if post {
				extra = "model_post_turn_compact_threshold_percent = 1\n"
			}
			router, c, store, sink, child := newSamplingInterruptChild(t, a, compactor, extra)
			response, startErr := router.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{ThreadID: child, Prompt: "work"}))
			if startErr != nil {
				t.Fatal(startErr)
			}
			old := response.Turn.ID
			var phase compact.Phase
			select {
			case phase = <-compactor.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("remote compaction not entered")
			}
			want := compact.PhaseMidTurn
			if post {
				want = compact.PhasePostTurn
			}
			if phase != want {
				t.Fatalf("phase=%s want=%s", phase, want)
			}
			if _, err := c.InterruptAgent(context.Background(), &agent.InterruptAgentArgs{Target: child}); err != nil {
				t.Fatal(err)
			}
			active := router.threads.ActiveTurn(child)
			if active == nil || !active.SamplingInterrupt.Requested() {
				t.Fatal("compaction interrupt registration missing")
			}
			if err := c.FollowupTask(context.Background(), &agent.FollowupTaskArgs{Target: child, Message: "Resume after summary."}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-active.SamplingInterrupt.Finished():
				t.Fatal("terminal before checkpoint installation")
			default:
			}
			compactor.release <- struct{}{}
			status := TurnStatusInterrupted
			if post {
				status = TurnStatusCompleted
			}
			completed := waitForTurnCompletedStatus(t, sink, old, status)
			if post {
				data, _ := json.Marshal(completed)
				if !strings.Contains(string(data), "completed answer 0") {
					t.Fatalf("answer not delivered: %s", data)
				}
				// first regular answer was reported before post-turn compaction
				if id := receiveSamplingTurn(t, a.entered); id != old {
					t.Fatal(id)
				}
			}
			var resumed string
			for resumed == "" {
				select {
				case resumed = <-a.entered:
				case phase := <-compactor.entered:
					t.Logf("additional compaction before resume sampling: %s", phase)
					compactor.release <- struct{}{}
				case <-time.After(5 * time.Second):
					for _, n := range sink.List() {
						if n.Method == NotificationWarning || n.Method == NotificationError {
							t.Logf("notification: %+v", n.Params)
						}
					}
					t.Fatalf("resume not sampled; active=%+v", router.threads.ActiveTurn(child))
				}
			}
			// Post-turn compaction runs again for the resumed answer.
			select {
			case <-compactor.entered:
				compactor.release <- struct{}{}
			case <-time.After(10 * time.Second):
				t.Fatal("resumed compaction not entered")
			}
			waitForTurnCompletedStatus(t, sink, resumed, TurnStatusCompleted)
			requests := a.snapshot()
			if len(requests) < 2 || requests[1].TurnID == old {
				t.Fatal("resume reused interrupted turn")
			}
			data, _ := json.Marshal(requests[1].InputItems)
			if !strings.Contains(string(data), "installed checkpoint 1") || !strings.Contains(string(data), "Resume after summary.") {
				t.Fatalf("resumed context: %s", data)
			}
			record, err := store.Read(session.ThreadID(child), true, true)
			if err != nil {
				t.Fatal(err)
			}
			if record.Metadata.Extra["compaction_summary"] != "installed checkpoint 2" {
				t.Fatalf("checkpoint lost: %+v", record.Metadata.Extra)
			}
		})
	}
}

func TestSamplingInterruptControllerIdleMailDoesNotRestart(t *testing.T) {
	a := &samplingInterruptAgent{entered: make(chan string, 8), release: make(chan struct{}), stream: true}
	router, c, _, sink, child := newSamplingInterruptChild(t, a, nil, "")
	if _, err := router.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{ThreadID: child, Prompt: "work"})); err != nil {
		t.Fatal(err)
	}
	old := receiveSamplingTurn(t, a.entered)
	active := router.threads.ActiveTurn(child)
	if _, err := c.InterruptAgent(context.Background(), &agent.InterruptAgentArgs{Target: child}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendMessage(context.Background(), &agent.SendMessageArgs{Target: child, Message: "while stopping"}); err != nil {
		t.Fatal(err)
	}
	if router.requireSteerMailbox().HasPendingMailboxItems(child, old) {
		t.Fatal("mail was admitted to the interrupted sampling attempt")
	}
	close(a.release)
	waitForTurnCompletedStatus(t, sink, old, TurnStatusInterrupted)
	select {
	case <-active.SamplingInterrupt.Finished():
	case <-time.After(10 * time.Second):
		t.Fatal("terminal not finished")
	}
	if err := c.SendMessage(context.Background(), &agent.SendMessageArgs{Target: child, Message: "idle contribution"}); err != nil {
		t.Fatal(err)
	}
	if router.threads.ActiveTurn(child) != nil || len(a.snapshot()) != 1 {
		t.Fatal("idle contributor restarted interrupted child")
	}
	if queued := router.drainRuntimeAgentMessages(child); len(queued) != 2 {
		t.Fatalf("deferred mail count: %d", len(queued))
	}
}
