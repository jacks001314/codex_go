package turn

import (
	"context"
	"sync"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
)

// reorderingDynamicToolCaller mirrors the client in Rust #54654's
// code_mode_dynamic_tools_tests: both calls of the batch must be waiting for the
// client before either response is submitted, and the client answers the second
// call first.
type reorderingDynamicToolCaller struct {
	pending      int
	respondFirst string
	timeout      time.Duration

	mu                     sync.Mutex
	arrived                []string
	responded              []string
	inFlight               int
	maxInFlight            int
	inFlightWhenResponding []int

	allPending    chan struct{}
	allPendingOne sync.Once
	firstAnswered chan struct{}
	firstAnswerOn sync.Once
}

func newReorderingDynamicToolCaller(pending int, respondFirst string) *reorderingDynamicToolCaller {
	return &reorderingDynamicToolCaller{
		pending:       pending,
		respondFirst:  respondFirst,
		timeout:       5 * time.Second,
		allPending:    make(chan struct{}),
		firstAnswered: make(chan struct{}),
	}
}

func (c *reorderingDynamicToolCaller) Request(ctx context.Context, _ string, params any, target any) error {
	call, _ := params.(*DynamicToolCallParams)
	callID := ""
	key := ""
	if call != nil {
		callID = call.CallID
		if arguments, ok := call.Arguments.(map[string]any); ok {
			key, _ = arguments["key"].(string)
		}
	}
	c.mu.Lock()
	c.arrived = append(c.arrived, callID)
	c.inFlight++
	if c.inFlight > c.maxInFlight {
		c.maxInFlight = c.inFlight
	}
	allPending := len(c.arrived) >= c.pending
	c.mu.Unlock()
	if allPending {
		c.allPendingOne.Do(func() { close(c.allPending) })
	}
	// No call answers before every call of the batch is pending, so an early
	// response cannot be an artifact of serial execution.
	if err := c.wait(ctx, c.allPending); err != nil {
		c.answered("")
		return err
	}
	if callID != c.respondFirst {
		if err := c.wait(ctx, c.firstAnswered); err != nil {
			c.answered("")
			return err
		}
	}
	c.answered(key)
	if callID == c.respondFirst {
		c.firstAnswerOn.Do(func() { close(c.firstAnswered) })
	}
	if response, ok := target.(*DynamicToolCallResponse); ok {
		*response = DynamicToolCallResponse{
			Success:      true,
			ContentItems: []DynamicToolCallOutputContentItem{{Type: "inputText", Text: "value-" + key}},
		}
	}
	return nil
}

func (c *reorderingDynamicToolCaller) answered(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inFlight--
	c.inFlightWhenResponding = append(c.inFlightWhenResponding, c.inFlight+1)
	c.responded = append(c.responded, key)
}

func (c *reorderingDynamicToolCaller) wait(ctx context.Context, signal chan struct{}) error {
	select {
	case <-signal:
		return nil
	case <-time.After(c.timeout):
		return context.DeadlineExceeded
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestDynamicToolCallsRunInParallelLikeRust mirrors Rust #54654
// (code_mode_dynamic_tools_tests::code_mode_dynamic_tool_calls_pending_together_resolve_in_reverse_order):
// dynamic tools opt into `supports_parallel_tool_calls`, so two calls of one
// batch both reach the client before either is answered, and each result keeps
// its own invocation even though the client answers in reverse order.
func TestDynamicToolCallsRunInParallelLikeRust(t *testing.T) {
	caller := newReorderingDynamicToolCaller(2, "call-2")
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.DynamicToolCaller = caller
	options.DynamicTools = []DynamicToolSpec{{Type: "function", Function: &DynamicToolFunctionSpec{
		Name: "lookup",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"key": map[string]any{"type": "string"}},
			"required":   []string{"key"},
		},
	}}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	router := tool.NewRouter(registry)
	if !router.SupportsParallel(tool.PlainName("lookup")) {
		t.Fatal("dynamic tool did not opt into parallel execution (#54654)")
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: router})
	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{
		{Type: "function_call", CallID: "call-1", Name: "lookup", Arguments: `{"key":"A"}`},
		{Type: "function_call", CallID: "call-2", Name: "lookup", Arguments: `{"key":"B"}`},
	})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v (a serial dynamic tool cannot answer one call while another is pending)", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v, want two results", results)
	}
	caller.mu.Lock()
	arrived := append([]string(nil), caller.arrived...)
	responded := append([]string(nil), caller.responded...)
	maxInFlight := caller.maxInFlight
	inFlightWhenResponding := append([]int(nil), caller.inFlightWhenResponding...)
	caller.mu.Unlock()
	if len(arrived) != 2 {
		t.Fatalf("client saw %d calls, want 2", len(arrived))
	}
	if maxInFlight != 2 {
		t.Fatalf("max concurrent requests = %d, want 2 (one pending dynamic call must not block another)", maxInFlight)
	}
	if len(inFlightWhenResponding) != 2 || inFlightWhenResponding[0] != 2 {
		t.Fatalf("in-flight counts while answering = %#v, want the first answer with both calls pending", inFlightWhenResponding)
	}
	if len(responded) != 2 || responded[0] != "B" || responded[1] != "A" {
		t.Fatalf("responses submitted in order %#v, want the reverse arrival order [B A]", responded)
	}
	for index, key := range []string{"A", "B"} {
		if body := results[index].Output.Body; body != "value-"+key {
			t.Fatalf("results[%d].Body = %#v, want %q", index, results[index].Output.Body, "value-"+key)
		}
		if want := []string{"call-1", "call-2"}[index]; results[index].Invocation.CallID != want {
			t.Fatalf("results[%d] belongs to %q, want %q", index, results[index].Invocation.CallID, want)
		}
	}
}
