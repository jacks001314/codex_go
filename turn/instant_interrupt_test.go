package turn

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
)

// heldCodeModeTool keeps a code-mode cell running until the test releases it, so
// a queued user message can be observed while the cell is still running (the Go
// counterpart of Rust's held tool in core/tests/suite/pending_input.rs).
type heldCodeModeTool struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newHeldCodeModeTool() *heldCodeModeTool {
	return &heldCodeModeTool{started: make(chan struct{}), release: make(chan struct{})}
}

func (h *heldCodeModeTool) Spec() tool.Spec {
	return tool.Spec{Name: tool.PlainName("held_tool"), Description: "blocks until the test releases it"}
}

func (h *heldCodeModeTool) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	h.once.Do(func() { close(h.started) })
	select {
	case <-h.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &tool.Output{CallID: invocation.CallID, ToolName: invocation.ToolName, Success: true, Body: "held released"}, nil
}

func newInstantInterruptDispatcher(t *testing.T, enabled bool) (*ToolDispatcher, *heldCodeModeTool) {
	t.Helper()
	registry := tool.NewRegistry()
	held := newHeldCodeModeTool()
	if err := registry.Register(held); err != nil {
		t.Fatalf("register held tool: %v", err)
	}
	exec, wait := tool.NewCodeModeExecutors(registry)
	if err := registry.Register(exec); err != nil {
		t.Fatalf("register exec: %v", err)
	}
	if err := registry.Register(wait); err != nil {
		t.Fatalf("register wait: %v", err)
	}
	return NewToolDispatcher(&ToolDispatcherOptions{
		Router:           tool.NewRouter(registry),
		ThreadID:         "t1",
		TurnID:           "turn-1",
		InstantInterrupt: enabled,
	}), held
}

var codeModeCellIDPattern = regexp.MustCompile(`cell ID ([A-Za-z0-9:._-]+)`)

const heldExecSource = "// @exec: {\"yield_time_ms\": 60000}\nawait tools.held_tool({}); text('finished');"

func execToolCallItem(callID string, source string) model.AgentItem {
	return model.AgentItem{Type: "custom_tool_call", Name: tool.CodeModeExecToolName, CallID: callID, Input: source}
}

func waitToolCallItem(callID string, cellID string) model.AgentItem {
	return model.AgentItem{
		Type:      "function_call",
		Name:      tool.CodeModeWaitToolName,
		CallID:    callID,
		Arguments: `{"cell_id":"` + cellID + `","yield_time_ms":60000}`,
	}
}

// heldExecLoopAgent scripts the sampling requests of the loop-level test: the
// first request queues a user message (a steer arriving while the code-mode cell
// runs) and issues the held exec call; later requests answer with text.
type heldExecLoopAgent struct {
	mailbox     *SteerMailbox
	threadID    string
	turnID      string
	steerQueued bool
	requests    []model.AgentRequest
}

func (a *heldExecLoopAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		if !a.steerQueued {
			a.steerQueued = true
			_ = a.mailbox.Enqueue(&SteerEnqueueParams{
				ThreadID:   a.threadID,
				TurnID:     a.turnID,
				InputItems: []any{queuedUserMessage("answer this instead")},
			})
		}
		return &model.AgentResponse{
			ResponseID: "resp-exec",
			Usage:      model.AgentUsage{TotalTokens: 100},
			Items:      []model.AgentItem{execToolCallItem("exec-1", heldExecSource)},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-done",
		Message:    "done",
		Usage:      model.AgentUsage{TotalTokens: 20},
		Items:      []model.AgentItem{{Type: "agent_message", Text: "done"}},
	}, nil
}

// Mirrors Rust's integration coverage at the loop level: the sampling request
// watches queued user input, yields the code-mode observation when a steer
// arrives while the cell runs, and the queued message is delivered to the next
// request.
func TestAgentLoopInstantInterruptYieldsCellAndDeliversQueuedInput(t *testing.T) {
	registry := tool.NewRegistry()
	held := newHeldCodeModeTool()
	if err := registry.Register(held); err != nil {
		t.Fatalf("register held tool: %v", err)
	}
	exec, wait := tool.NewCodeModeExecutors(registry)
	if err := registry.Register(exec); err != nil {
		t.Fatalf("register exec: %v", err)
	}
	if err := registry.Register(wait); err != nil {
		t.Fatalf("register wait: %v", err)
	}
	mailbox := NewSteerMailbox()
	agent := &heldExecLoopAgent{mailbox: mailbox, threadID: "t1", turnID: "turn-1"}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:            agent,
		Router:           tool.NewRouter(registry),
		SteerMailbox:     mailbox,
		InstantInterrupt: true,
	})
	result, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt: "run the cell", ThreadID: "t1", TurnID: "turn-1", ToolMode: model.ToolModeCodeMode,
	})
	close(held.release)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(result.ToolExecutions) == 0 || result.ToolExecutions[0].Response == nil {
		t.Fatalf("tool executions = %#v", result.ToolExecutions)
	}
	execText := result.ToolExecutions[0].Response.Output.Text()
	if !strings.Contains(execText, "running") || codeModeCellIDPattern.FindStringSubmatch(execText) == nil {
		t.Fatalf("exec output = %q, want a yielded observation with a cell id", execText)
	}
	if len(agent.requests) < 2 {
		t.Fatalf("agent requests = %d, want the follow-up sampling request", len(agent.requests))
	}
	if !inputItemsContainText(agent.requests[1].InputItems, "answer this instead") {
		t.Fatalf("the queued user message did not reach the next request: %#v", agent.requests[1].InputItems)
	}
	if !inputItemsContainText(result.SteerInputItems, "answer this instead") {
		t.Fatalf("result steer items = %#v", result.SteerInputItems)
	}
}

// inputItemsContainText searches either shape the loop appends: typed model items
// and the raw maps a drained steer carries.
func inputItemsContainText(items []any, text string) bool {
	encoded, err := json.Marshal(items)
	if err != nil {
		return false
	}
	return strings.Contains(string(encoded), text)
}

func toolResultText(t *testing.T, result ToolExecutionResult) string {
	t.Helper()
	if result.Response == nil {
		t.Fatal("tool result has no response item")
	}
	return result.Response.Output.Text()
}

// Mirrors Rust #48135's `steers_yield_exec_and_wait_without_stopping_the_cell`:
// with instant_interrupt on, a user message queued while a code-mode cell runs
// yields that observation, and a later wait from the next sampling request still
// collects the completed result.
func TestInstantInterruptYieldsCodeModeExecOnQueuedUserInput(t *testing.T) {
	dispatcher, held := newInstantInterruptDispatcher(t, true)
	mailbox := NewSteerMailbox()
	preempt, stop := dispatcher.BeginStepPreempt(mailbox)
	if preempt == nil {
		t.Fatal("an instant-interrupt step must own a preemption signal")
	}
	defer stop()
	if preempt.Cancelled() {
		t.Fatal("the request's signal must start uncancelled")
	}
	stepCtx := tool.WithCodeModePreempt(context.Background(), preempt)

	type outcome struct {
		results []ToolExecutionResult
		err     error
	}
	execDone := make(chan outcome, 1)
	go func() {
		results, err := dispatcher.ExecuteToolItems(stepCtx, []model.AgentItem{execToolCallItem("exec-1", heldExecSource)})
		execDone <- outcome{results: results, err: err}
	}()
	select {
	case <-held.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the code-mode cell never reached the held tool")
	}

	// The message arrives while the cell is still running.
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t1", TurnID: "turn-1", InputItems: []any{queuedUserMessage("answer this instead")}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var execResult outcome
	select {
	case execResult = <-execDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the exec observation did not yield after queued user input")
	}
	if execResult.err != nil {
		t.Fatalf("exec error = %v", execResult.err)
	}
	execText := toolResultText(t, execResult.results[0])
	if !strings.Contains(execText, "running") {
		t.Fatalf("exec output = %q, want a yielded observation", execText)
	}
	match := codeModeCellIDPattern.FindStringSubmatch(execText)
	if match == nil {
		t.Fatalf("exec output carries no cell id: %q", execText)
	}
	cellID := match[1]

	// The request's token stays latched, so a call received later in the same
	// response yields immediately without cancelling the cell.
	latched, err := dispatcher.ExecuteToolItems(stepCtx, []model.AgentItem{waitToolCallItem("wait-latched", cellID)})
	if err != nil {
		t.Fatalf("latched wait error = %v", err)
	}
	if text := toolResultText(t, latched[0]); !strings.Contains(text, "running") {
		t.Fatalf("later call in the same request = %q, want a yielded observation", text)
	}

	// The next sampling request consumes the queued message (draining the steer
	// like the agent loop does) and waits with a fresh signal: the cell keeps
	// running, so a wait collects its completed result.
	close(held.release)
	mailbox.Drain(&SteerDrainParams{ThreadID: "t1", TurnID: "turn-1"})
	nextPreempt, nextStop := dispatcher.BeginStepPreempt(mailbox)
	if nextPreempt == nil || nextPreempt.Cancelled() {
		t.Fatalf("the next request's signal = %#v", nextPreempt)
	}
	defer nextStop()
	waited, err := dispatcher.ExecuteToolItems(tool.WithCodeModePreempt(context.Background(), nextPreempt), []model.AgentItem{waitToolCallItem("wait-1", cellID)})
	if err != nil {
		t.Fatalf("wait error = %v", err)
	}
	if text := toolResultText(t, waited[0]); !strings.Contains(text, "finished") {
		t.Fatalf("wait output = %q, want the completed cell result", text)
	}
}

// The feature is off by default: without it the code-mode call has no
// preemption signal, so a queued user message does not yield its observation.
func TestInstantInterruptDisabledKeepsCodeModeCallBlocking(t *testing.T) {
	dispatcher, held := newInstantInterruptDispatcher(t, false)
	mailbox := NewSteerMailbox()
	preempt, stop := dispatcher.BeginStepPreempt(mailbox)
	if preempt != nil || stop != nil {
		t.Fatalf("BeginStepPreempt() with the feature off = %#v, %v", preempt, stop != nil)
	}
	stepCtx := tool.WithCodeModePreempt(context.Background(), preempt)

	type outcome struct {
		results []ToolExecutionResult
		err     error
	}
	execDone := make(chan outcome, 1)
	go func() {
		results, err := dispatcher.ExecuteToolItems(stepCtx, []model.AgentItem{execToolCallItem("exec-1", heldExecSource)})
		execDone <- outcome{results: results, err: err}
	}()
	select {
	case <-held.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the code-mode cell never reached the held tool")
	}
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "t1", TurnID: "turn-1", InputItems: []any{queuedUserMessage("not an interrupt")}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case result := <-execDone:
		t.Fatalf("the code-mode call yielded with the feature off: %#v (%v)", result.results, result.err)
	case <-time.After(150 * time.Millisecond):
	}

	close(held.release)
	select {
	case result := <-execDone:
		if result.err != nil {
			t.Fatalf("exec error = %v", result.err)
		}
		if text := toolResultText(t, result.results[0]); !strings.Contains(text, "finished") {
			t.Fatalf("exec output = %q, want the completed cell result", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the code-mode call never completed after the held tool was released")
	}
}
