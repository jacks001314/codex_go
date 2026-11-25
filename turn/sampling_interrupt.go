package turn

import (
	"context"
	"sync"
	"sync/atomic"

	"codex_go/model"
)

// SamplingInterrupt belongs to one active turn until its terminal event has
// been published. Tool cancellation never cancels the item being sampled.
type SamplingInterrupt struct {
	mu           sync.Mutex
	turnID       string
	cancel       context.CancelFunc
	boundary     func()
	requested    atomic.Bool
	aborted      bool
	finished     chan struct{}
	once         sync.Once
	followupTail chan struct{}
}

func NewSamplingInterrupt(turnID string) *SamplingInterrupt {
	return &SamplingInterrupt{turnID: turnID, finished: make(chan struct{})}
}

// Register admits an attempt only while it still owns this active turn.
func (s *SamplingInterrupt) Register(ctx context.Context, turnID string, boundary func()) (context.Context, error) {
	if s == nil {
		return ctx, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnID != turnID || s.aborted || s.requested.Load() || ctx.Err() != nil {
		return nil, context.Canceled
	}
	tools, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.boundary = boundary
	return tools, nil
}

// Unregister releases an uninterrupted attempt. A pending stop stays attached
// to the turn, keeping new input behind Finished.
func (s *SamplingInterrupt) Unregister() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requested.Load() {
		return true
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel, s.boundary = nil, nil
	return false
}

// Request returns false when the caller must hard-abort instead. Claiming that
// fallback under the registration lock prevents a new attempt racing the abort.
func (s *SamplingInterrupt) Request() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	if s.requested.Load() {
		s.mu.Unlock()
		return true
	}
	if s.cancel == nil || s.aborted {
		s.aborted = true
		s.mu.Unlock()
		return false
	}
	s.requested.Store(true)
	cancel, boundary := s.cancel, s.boundary
	s.mu.Unlock()
	cancel()
	if boundary != nil {
		boundary()
	}
	return true
}

func (s *SamplingInterrupt) Requested() bool           { return s != nil && s.requested.Load() }
func (s *SamplingInterrupt) Finished() <-chan struct{} { return s.finished }

// Finish must run after publication of the terminal event, including hard-abort
// paths. It invalidates registration before releasing waiting follow-ups.
func (s *SamplingInterrupt) Finish() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.aborted = true
		if s.cancel != nil {
			s.cancel()
		}
		s.mu.Unlock()
		close(s.finished)
	})
}

// sampledItemBoundary serializes stream callbacks with the interrupt request.
// Opening the request starts the first item even before an added event arrives.
type sampledItemBoundary struct {
	mu       sync.Mutex
	stop     *SamplingInterrupt
	cancel   context.CancelFunc
	between  bool
	stopped  bool
	items    []model.AgentItem
	response model.AgentResponse
}

func (b *sampledItemBoundary) interrupt() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.between {
		b.stopped = true
		b.cancel()
	}
}

func (b *sampledItemBoundary) handler(next model.ResponsesStreamHandler) model.ResponsesStreamHandler {
	return func(event *model.ResponsesStreamEvent) {
		b.mu.Lock()
		if event == nil || b.stopped {
			b.mu.Unlock()
			return
		}
		if b.between && b.stop.Requested() {
			b.stopped = true
			b.cancel()
			b.mu.Unlock()
			return
		}
		switch event.Kind {
		case model.ResponsesStreamEventOutputAdded:
			b.between = false
		case model.ResponsesStreamEventOutputDone:
			if event.Item != nil {
				b.items = append(b.items, *event.Item)
			}
			b.between = true
		}
		if event.ResponseID != "" {
			b.response.ResponseID = event.ResponseID
		}
		if event.Usage != nil {
			b.response.Usage = *event.Usage
		}
		between := b.between
		b.mu.Unlock()
		// Observers may themselves request a stop. Never call them under our lock.
		if next != nil {
			next(event)
		}
		if between && b.stop.Requested() {
			b.interrupt()
		}
	}
}

// retain reconstructs the accepted prefix even when a transport returns only a
// cancellation error. Complete-response runners with no callbacks retain their
// first item; subsequent items were never active when the stop was requested.
func (b *sampledItemBoundary) retain(response *model.AgentResponse) *model.AgentResponse {
	b.mu.Lock()
	defer b.mu.Unlock()
	if response == nil {
		response = &b.response
	}
	kept := *response
	kept.Items = append([]model.AgentItem(nil), b.items...)
	if len(kept.Items) == 0 && len(response.Items) > 0 {
		kept.Items = append(kept.Items, response.Items[0])
	} else if len(kept.Items) == 0 && response.Message != "" {
		kept.Items = []model.AgentItem{{Type: "message", Text: response.Message}}
	}
	kept.Message = ""
	for _, item := range kept.Items {
		if item.Type == "message" || item.Type == "agent_message" {
			kept.Message += item.Text
		}
	}
	kept.Canceled, kept.Preempted = false, false
	// The upstream response continues beyond the retained prefix. Never reuse its
	// stored response ID, which could reintroduce discarded items on resume.
	kept.ResponseID = ""
	return &kept
}

// EnqueueFollowup serializes explicit follow-ups behind terminal publication.
// Each callback starts a fresh turn and waits for its terminal event before the
// next callback runs. Accepted input outlives its submitting tool invocation.
func (s *SamplingInterrupt) EnqueueFollowup(run func()) {
	s.mu.Lock()
	previous := s.followupTail
	if previous == nil {
		previous = s.finished
	}
	done := make(chan struct{})
	s.followupTail = done
	s.mu.Unlock()
	go func() { <-previous; defer close(done); run() }()
}

// BindTurn binds an exec reservation to the admitted turn before sampling starts.
func (s *SamplingInterrupt) BindTurn(turnID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnID == "" && !s.aborted {
		s.turnID = turnID
	}
}

// SamplingInterruptEnabled mirrors the Rust TurnContext child-only gate.
func SamplingInterruptEnabled(configured bool, version, parentThreadID string) bool {
	return configured && version == "v2" && parentThreadID != ""
}

type itemBoundaryToolsKey struct{}

// A retained call must have an output even if it does not resolve to a local
// handler (for example an unrecognized buffered tool). Keep the wire shape of
// custom and search calls as well as ordinary function calls.
func (l *AgentLoop) abortInterruptedToolItems(result *AgentLoopResult, items []model.AgentItem) {
	for i := range items {
		result.InputItems = append(result.InputItems, &items[i])
	}
	var executions []ToolExecutionResult
	if l.dispatcher != nil {
		executions = l.dispatcher.AbortToolItems(items, "aborted before handler start: agent interrupted")
	}
	result.ToolExecutions = append(result.ToolExecutions, executions...)
	answered := make(map[string]bool)
	for _, execution := range executions {
		if execution.Response != nil {
			answered[execution.Response.CallID] = true
			result.InputItems = append(result.InputItems, execution.Response)
		}
		result.InputItems = append(result.InputItems, execution.InputItems...)
	}
	for _, item := range items {
		id := firstNonEmpty(item.CallID, item.ID)
		if answered[id] {
			continue
		}
		kind := "function_call_output"
		if item.Type == "custom_tool_call" {
			kind = "custom_tool_call_output"
		}
		output := map[string]any{"type": kind, "call_id": id, "output": "aborted before handler start: agent interrupted"}
		if item.Type == "tool_search_call" {
			output = map[string]any{"type": "tool_search_output", "call_id": id, "status": "completed", "execution": item.Execution, "tools": []any{}}
		}
		result.InputItems = append(result.InputItems, output)
	}
}
