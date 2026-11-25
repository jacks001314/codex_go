package exec

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
	"codex_go/cli"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

type execSamplingInterruptAgent struct {
	mu       sync.Mutex
	requests []*model.AgentRequest
	entered  chan string
	release  chan struct{}
}

func (a *execSamplingInterruptAgent) Run(ctx context.Context, req *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	index := len(a.requests)
	a.requests = append(a.requests, req)
	a.mu.Unlock()
	a.entered <- req.TurnID
	item := model.AgentItem{ID: "message", Type: "message", Text: "retained answer"}
	if index == 0 {
		req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputAdded, Item: &item})
		<-a.release
		req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &item})
		drop := model.AgentItem{ID: "drop", Type: "message", Text: "discard this"}
		req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &drop})
		return nil, ctx.Err()
	}
	return &model.AgentResponse{Items: []model.AgentItem{item}, Message: item.Text}, nil
}

func TestSamplingInterruptExecControllerAndFreshFollowups(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features.multi_agent_v2]\nenabled = true\ninterrupt_at_item_boundary = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner(home)
	a := &execSamplingInterruptAgent{entered: make(chan string, 8), release: make(chan struct{})}
	runner.Agent = a
	controller := newExecAgentController(runner, context.Background(), &Request{Exec: cli.ExecOptions{Prompt: "root", Shared: cli.SharedOptions{CWD: t.TempDir()}}}, "thread-root", 4).(*execAgentController)
	controller.interruptAtItemBoundary = true
	t.Cleanup(controller.shutdown)
	message := "work"
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker", Message: &message, ForkTurns: execStringPointer("none")})
	if err != nil {
		t.Fatal(err)
	}
	receive := func() string {
		t.Helper()
		select {
		case id := <-a.entered:
			return id
		case <-time.After(10 * time.Second):
			t.Fatal("child did not sample")
			return ""
		}
	}
	old := receive()
	controller.mu.Lock()
	slot := controller.taskLocked(child.TaskName).samplingInterrupt
	controller.mu.Unlock()
	if slot == nil {
		t.Fatal("missing exec task interrupt slot")
	}
	if _, err := controller.InterruptAgent(context.Background(), &agent.InterruptAgentArgs{Target: child.TaskName}); err != nil {
		t.Fatal(err)
	}
	if !slot.Requested() {
		t.Fatal("real exec controller hard-aborted")
	}
	for _, message := range []string{"Resume one.", "Resume two."} {
		if err := controller.FollowupTask(context.Background(), &agent.FollowupTaskArgs{Target: child.TaskName, Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-a.entered:
		t.Fatal("followup before terminal")
	default:
	}
	close(a.release)
	select {
	case <-slot.Finished():
	case <-time.After(10 * time.Second):
		t.Fatal("terminal not published")
	}
	first := receive()
	second := receive()
	if old == first || first == second || old == second {
		t.Fatalf("reused turn IDs: %s %s %s", old, first, second)
	}
	// Wait until accepted followups have persisted their terminal events.
	controller.mu.Lock()
	lastSlot := controller.taskLocked(child.TaskName).samplingInterrupt
	controller.mu.Unlock()
	select {
	case <-lastSlot.Finished():
	case <-time.After(10 * time.Second):
		t.Fatal("followup not finished")
	}
	a.mu.Lock()
	requests := append([]*model.AgentRequest(nil), a.requests...)
	a.mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("requests=%d", len(requests))
	}
	for index, message := range []string{"Resume one.", "Resume two."} {
		data, _ := json.Marshal(requests[index+1].InputItems)
		if !strings.Contains(string(data), message) || !strings.Contains(string(data), "retained answer") {
			t.Fatalf("resume context: %s", data)
		}
	}
	record, err := runner.sessionStore().Read(session.ThreadID(child.AgentID), true, true)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(record.Items)
	if strings.Contains(string(data), "discard this") || !strings.Contains(string(data), "retained answer") {
		t.Fatalf("history: %s", data)
	}
}

func TestSamplingInterruptExecIdleMailDoesNotRestart(t *testing.T) {
	runner := NewLocalRunner(t.TempDir())
	controller := newExecAgentController(runner, context.Background(), &Request{}, "root", 4).(*execAgentController)
	task := &execAgentTask{id: "child", taskName: "worker", path: "/root/worker", status: agent.AgentMessageStatus{Kind: agent.AgentMessageStatusInterrupted}}
	controller.tasks[task.id] = task
	if err := controller.SendMessage(context.Background(), &agent.SendMessageArgs{Target: "/root/worker", Message: "idle contribution"}); err != nil {
		t.Fatal(err)
	}
	if task.status.Kind != agent.AgentMessageStatusInterrupted || task.cancel != nil || len(task.pendingMessages) != 1 {
		t.Fatalf("idle mail restarted interrupted child: %+v", task)
	}
}

type execInterruptCompactingAgent struct {
	base    execMidTurnRolloverAgent
	preTurn bool
	entered chan string
	release chan struct{}
}

func (a *execInterruptCompactingAgent) Run(ctx context.Context, req *model.AgentRequest) (*model.AgentResponse, error) {
	if strings.Contains(req.Prompt, "Summarize the conversation") {
		a.entered <- req.TurnID
		<-a.release
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if a.preTurn && !strings.Contains(req.Prompt, "Summarize the conversation") {
		a.base.mu.Lock()
		a.base.calls++
		a.base.mu.Unlock()
		return &model.AgentResponse{Message: "completed before resume", Items: []model.AgentItem{{Type: "message", Text: "completed before resume"}}, Usage: model.AgentUsage{InputTokens: 300000, OutputTokens: 100}}, nil
	}
	return a.base.Run(ctx, req)
}

func TestSamplingInterruptExecRemoteCompactionKeepsCheckpoint(t *testing.T) {
	for _, preTurn := range []bool{false, true} {
		t.Run(fmt.Sprintf("preTurn=%v", preTurn), func(t *testing.T) {
			testSamplingInterruptExecRemoteCompaction(t, preTurn)
		})
	}
}

func testSamplingInterruptExecRemoteCompaction(t *testing.T, preTurn bool) {
	home := t.TempDir()
	cfg := "model = \"gpt-5.4\"\nmodel_context_window = 272000\nmodel_auto_compact_token_limit = 100000\n[features.multi_agent_v2]\nenabled = true\ninterrupt_at_item_boundary = true\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner(home)
	a := &execInterruptCompactingAgent{preTurn: preTurn, entered: make(chan string, 1), release: make(chan struct{})}
	runner.Agent = a
	controller := newExecAgentController(runner, context.Background(), &Request{Exec: cli.ExecOptions{Prompt: "root", Shared: cli.SharedOptions{CWD: t.TempDir()}}}, "thread-root", 4).(*execAgentController)
	controller.interruptAtItemBoundary = true
	t.Cleanup(controller.shutdown)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(a.release) }) }
	t.Cleanup(release)
	message := "work until compaction"
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker", Message: &message, ForkTurns: execStringPointer("none")})
	if err != nil {
		t.Fatal(err)
	}
	if preTurn {
		controller.mu.Lock()
		first := controller.taskLocked(child.TaskName).samplingInterrupt
		controller.mu.Unlock()
		select {
		case <-first.Finished():
		case <-time.After(10 * time.Second):
			t.Fatal("initial answer did not finish")
		}
		if err := controller.FollowupTask(context.Background(), &agent.FollowupTaskArgs{Target: child.TaskName, Message: "Resume into pre-turn compaction."}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-a.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("remote compaction not entered")
	}
	controller.mu.Lock()
	slot := controller.taskLocked(child.TaskName).samplingInterrupt
	controller.mu.Unlock()
	if _, err := controller.InterruptAgent(context.Background(), &agent.InterruptAgentArgs{Target: child.TaskName}); err != nil || !slot.Requested() {
		t.Fatalf("compaction registration missing: %v", err)
	}
	select {
	case <-slot.Finished():
		t.Fatal("terminal published before summary installation")
	default:
	}
	release()
	select {
	case <-slot.Finished():
	case <-time.After(10 * time.Second):
		t.Fatal("interrupted compaction did not finish")
	}
	record, err := runner.sessionStore().Read(session.ThreadID(child.AgentID), true, true)
	if err != nil {
		t.Fatal(err)
	}
	if record.Metadata.Extra["compaction_summary"] != "compacted summary" {
		t.Fatalf("checkpoint missing: %+v", record.Metadata.Extra)
	}
	a.base.mu.Lock()
	calls := a.base.calls
	a.base.mu.Unlock()
	if calls != 1 {
		t.Fatalf("sampled after interrupted compaction: %d", calls)
	}
	controller.mu.Lock()
	status := controller.taskLocked(child.TaskName).status
	controller.mu.Unlock()
	if status.Kind != agent.AgentMessageStatusInterrupted {
		t.Fatalf("interrupted compaction status: %+v", status)
	}
}
